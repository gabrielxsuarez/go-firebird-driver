// Ejecución y fetch de statements: op_execute/op_execute2/op_fetch (spec cap. 8).

package wire

import "fmt"

// writeExecuteOp writes the op_execute message to the buffer WITHOUT flushing.
func (wc *WireConnection) writeExecuteOp(stmtHandle, txHandle int32, blr, params []byte) {
	wc.writer.WriteInt32(opExecute)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteBuffer(blr)
	wc.writer.WriteInt32(0) // p_sqldata_message_number

	if len(params) > 0 {
		wc.writer.WriteInt32(1) // message count: has parameters
		wc.writer.WriteRaw(params)
	} else {
		wc.writer.WriteInt32(0) // message count: no parameters
	}

	if wc.protocolVersion >= 16 {
		wc.writer.WriteUInt32(0) // p_sqldata_timeout (0 = default)
	}
	if wc.protocolVersion >= 18 {
		wc.writer.WriteUInt32(0) // p_sqldata_cursor_flags (0 = not scrollable)
	}
}

// Execute sends op_execute for a non-returning statement (INSERT, UPDATE, DELETE, DDL).
func (wc *WireConnection) Execute(stmtHandle, txHandle int32, blr, params []byte) error {
	wc.writeExecuteOp(stmtHandle, txHandle, blr, params)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_execute: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_execute: %w", err)
	}
	return nil
}

// ExecuteAndCommitRetaining batches op_execute + op_commit_retaining into a
// single flush. The transaction handle remains valid for reuse.
func (wc *WireConnection) ExecuteAndCommitRetaining(stmtHandle, txHandle int32, blr, params []byte) error {
	wc.writeExecuteOp(stmtHandle, txHandle, blr, params)

	wc.writer.WriteInt32(opCommitRetaining)
	wc.writer.WriteInt32(txHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("execute+commit_retaining: flush: %w", err)
	}

	_, execErr := wc.readResponse()
	_, commitErr := wc.reader.ReadResponse()

	if execErr != nil {
		return fmt.Errorf("execute+commit_retaining: execute: %w", execErr)
	}
	if commitErr != nil {
		return fmt.Errorf("execute+commit_retaining: commit_retaining: %w", commitErr)
	}
	return nil
}

// Execute2 sends op_execute2 for a singleton returning statement
// (EXECUTE PROCEDURE). It returns the row carried by op_sql_response.
func (wc *WireConnection) Execute2(stmtHandle, txHandle int32, inBLR, params, outBLR []byte, outputs []ColumnDescriptor) (int32, []any, error) {
	wc.writer.WriteInt32(opExecute2)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteBuffer(inBLR)
	wc.writer.WriteInt32(0) // p_sqldata_message_number

	if len(params) > 0 {
		wc.writer.WriteInt32(1)
		wc.writer.WriteRaw(params)
	} else {
		wc.writer.WriteInt32(0)
	}

	// Output BLR for return values
	wc.writer.WriteBuffer(outBLR)
	wc.writer.WriteInt32(0) // p_sqldata_out_message_number

	if wc.protocolVersion >= 16 {
		wc.writer.WriteUInt32(0)
	}
	if wc.protocolVersion >= 18 {
		wc.writer.WriteUInt32(0)
	}

	if err := wc.flush(); err != nil {
		return 0, nil, fmt.Errorf("op_execute2: flush: %w", err)
	}

	// Drain deferred responses
	if err := wc.consumeDeferred(); err != nil {
		return 0, nil, fmt.Errorf("op_execute2: consume deferred: %w", err)
	}

	op := wc.reader.ReadOpcode()
	if wc.reader.Err() != nil {
		return 0, nil, fmt.Errorf("op_execute2: read opcode: %w", wc.reader.Err())
	}

	var msgs int32
	var row []any
	switch op {
	case opSQLResponse:
		sqlResp := wc.reader.readSQLResponse()
		msgs = sqlResp.Messages
		if msgs > 0 {
			row = make([]any, len(outputs))
			if err := DecodeRow(wc.reader, outputs, wc.nullBuf[:], row); err != nil {
				return msgs, nil, fmt.Errorf("op_execute2: decode row: %w", err)
			}
		}
		if wc.reader.Err() != nil {
			return 0, nil, fmt.Errorf("op_execute2: read sql_response: %w", wc.reader.Err())
		}

		op = wc.reader.ReadOpcode()
		if wc.reader.Err() != nil {
			return msgs, row, fmt.Errorf("op_execute2: read response opcode: %w", wc.reader.Err())
		}
		if op != opResponse {
			return msgs, row, fmt.Errorf("op_execute2: unexpected opcode %d, expected op_response (%d)", op, opResponse)
		}
		resp := wc.reader.readGenericResponse()
		if wc.reader.Err() != nil {
			return msgs, row, fmt.Errorf("op_execute2: read response: %w", wc.reader.Err())
		}
		if resp.Status.HasError() {
			return msgs, row, fmt.Errorf("op_execute2: %w", &StatusError{SV: resp.Status})
		}
		return msgs, row, nil

	case opResponse:
		resp := wc.reader.readGenericResponse()
		if wc.reader.Err() != nil {
			return 0, nil, fmt.Errorf("op_execute2: read response: %w", wc.reader.Err())
		}
		if resp.Status.HasError() {
			return 0, nil, fmt.Errorf("op_execute2: %w", &StatusError{SV: resp.Status})
		}
		return 0, nil, nil

	default:
		return 0, nil, fmt.Errorf("op_execute2: unexpected opcode %d, expected %d or %d", op, opSQLResponse, opResponse)
	}
}

// FetchRowsReuse sends op_fetch and decodes all returned rows, reusing the
// provided row/value buffers when they are large enough.
func (wc *WireConnection) FetchRowsReuse(
	stmtHandle int32,
	blr []byte,
	descs []ColumnDescriptor,
	fetchSize int32,
	rowsBuf [][]any,
	valuesBuf []any,
) ([][]any, []any, bool, error) {
	wc.writer.WriteInt32(opFetch)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteBuffer(blr)
	wc.writer.WriteInt32(0)         // p_sqldata_message_number
	wc.writer.WriteInt32(fetchSize) // p_sqldata_messages

	if err := wc.flush(); err != nil {
		return nil, nil, false, fmt.Errorf("op_fetch: flush: %w", err)
	}

	if err := wc.consumeDeferred(); err != nil {
		return nil, nil, false, fmt.Errorf("op_fetch: consume deferred: %w", err)
	}

	numCols := len(descs)
	maxRows := int(fetchSize)
	if maxRows < 0 {
		maxRows = 0
	}

	initialRows := maxRows
	if initialRows > 8 {
		initialRows = 8
	}
	if maxRows > 0 && initialRows == 0 {
		initialRows = 1
	}

	var allValues []any
	rowCapacity := 0
	if numCols == 0 {
		allValues = valuesBuf[:0]
		rowCapacity = maxRows
	} else {
		rowCapacity = cap(valuesBuf) / numCols
		if rowCapacity >= initialRows {
			allValues = valuesBuf[: initialRows*numCols : cap(valuesBuf)]
		} else {
			rowCapacity = initialRows
			allValues = make([]any, rowCapacity*numCols)
		}
	}

	var rows [][]any
	if cap(rowsBuf) >= initialRows {
		rows = rowsBuf[:0]
	} else {
		rows = make([][]any, 0, initialRows)
	}
	eof := false
	rowIdx := 0

	for {
		op := wc.reader.ReadOpcode()
		if wc.reader.Err() != nil {
			return nil, allValues, false, fmt.Errorf("op_fetch: read opcode: %w", wc.reader.Err())
		}

		if op != opFetchResponse {
			if op == opResponse {
				// Error del statement en medio del lote (ver Fetch).
				return nil, allValues, false, fetchServerError(wc)
			}
			return nil, allValues, false, fmt.Errorf("op_fetch: unexpected opcode %d, expected %d", op, opFetchResponse)
		}

		resp := wc.reader.readFetchResponse()
		if wc.reader.Err() != nil {
			return nil, allValues, false, fmt.Errorf("op_fetch: read response: %w", wc.reader.Err())
		}

		if resp.Messages == 0 {
			// status=0,messages=0 → batch complete, more rows available
			// status=100,messages=0 → cursor exhausted
			eof = (resp.Status == 100)
			break
		}

		if numCols > 0 && rowIdx >= rowCapacity {
			newCapacity := rowCapacity * 2
			if newCapacity == 0 {
				newCapacity = 1
			}
			if maxRows > 0 && newCapacity > maxRows {
				newCapacity = maxRows
			}
			if newCapacity <= rowIdx {
				newCapacity = rowIdx + 1
			}

			newValues := make([]any, newCapacity*numCols)
			copy(newValues, allValues[:rowIdx*numCols])
			allValues = newValues
			rowCapacity = newCapacity

			for i := range rows {
				rows[i] = allValues[i*numCols : (i+1)*numCols]
			}
		}

		var row []any
		if numCols > 0 {
			if len(allValues) < (rowIdx+1)*numCols {
				allValues = allValues[:(rowIdx+1)*numCols]
			}
			row = allValues[rowIdx*numCols : (rowIdx+1)*numCols]
		}
		rowIdx++

		if err := DecodeRow(wc.reader, descs, wc.nullBuf[:], row); err != nil {
			return nil, allValues, false, fmt.Errorf("op_fetch: decode row: %w", err)
		}

		rows = append(rows, row)
	}

	return rows, allValues, eof, nil
}

// fetchServerError consumes an op_response body that arrived where an
// op_fetch_response was expected and returns the server's real error.
// Reading the full response keeps the wire stream synchronized so the
// connection remains usable.
func fetchServerError(wc *WireConnection) error {
	resp := wc.reader.readGenericResponse()
	if wc.reader.Err() != nil {
		return fmt.Errorf("op_fetch: read error response: %w", wc.reader.Err())
	}
	if resp.Status.HasError() {
		return fmt.Errorf("op_fetch: %w", &StatusError{SV: resp.Status})
	}
	return fmt.Errorf("op_fetch: server returned op_response without error status")
}
