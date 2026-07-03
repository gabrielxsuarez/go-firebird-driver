// Ciclo de vida de statements: allocate/prepare/free, pool de handles y describe (spec cap. 8).

package wire

import "fmt"

// PrepareStatementWithItems sends op_prepare_statement using a caller-provided
// info item set. This allows lighter describe requests for exec-only paths.
func (wc *WireConnection) PrepareStatementWithItems(txHandle, stmtHandle int32, sql string, bufferLength int32, items []byte) ([]byte, error) {
	wc.writer.WriteInt32(opPrepareStatement)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteUInt32(SQLDialectCurrent)
	wc.writer.WriteString(sql)
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_prepare_statement: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_prepare_statement: %w", err)
	}
	return resp.Data, nil
}

// AllocateAndPrepare performs allocate+prepare in a single round-trip (lazy send).
// Returns the allocated handle and the prepare info data.
func (wc *WireConnection) AllocateAndPrepare(txHandle int32, sql string, bufferLength int32) (int32, []byte, error) {
	return wc.AllocateAndPrepareWithItems(txHandle, sql, bufferLength, PrepareInfoItems())
}

// AllocateAndPrepareWithItems performs allocate+prepare using a caller-provided
// info item set. If a pooled statement handle is available, skips the allocate
// and sends only prepare (saving 1 message + 1 response per operation).
func (wc *WireConnection) AllocateAndPrepareWithItems(txHandle int32, sql string, bufferLength int32, items []byte) (int32, []byte, error) {

	// Fast path: reuse a pooled handle (prepare only, no allocate needed)
	if wc.freeCount > 0 {
		wc.freeCount--
		stmtHandle := wc.freeHandles[wc.freeCount]
		data, err := wc.PrepareStatementWithItems(txHandle, stmtHandle, sql, bufferLength, items)
		if err != nil {
			return 0, nil, fmt.Errorf("prepare (reuse handle): %w", err)
		}
		return stmtHandle, data, nil
	}

	// Write allocate (no flush)
	wc.writer.WriteInt32(opAllocateStatement)
	wc.writer.WriteInt32(wc.dbHandle)

	// Write prepare with placeholder handle (0xFFFF)
	wc.writer.WriteInt32(opPrepareStatement)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt32(int32(InvalidObject)) // placeholder
	wc.writer.WriteUInt32(SQLDialectCurrent)
	wc.writer.WriteString(sql)
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	// Single flush for both operations
	if err := wc.flush(); err != nil {
		return 0, nil, fmt.Errorf("allocate+prepare: flush: %w", err)
	}

	// Read allocate response (gets the real handle), draining any deferred first
	allocResp, err := wc.readResponse()
	if err != nil {
		return 0, nil, fmt.Errorf("allocate+prepare: allocate: %w", err)
	}
	stmtHandle := allocResp.Handle

	// Read prepare response (descriptor info)
	prepResp, err := wc.reader.ReadResponse() // no deferred to drain at this point
	if err != nil {
		return stmtHandle, nil, fmt.Errorf("allocate+prepare: prepare: %w", err)
	}

	return stmtHandle, prepResp.Data, nil
}

// RecycleStatement returns a statement handle to the pool for reuse,
// avoiding future op_allocate round-trips. If hasCursor is true,
// sends DSQLClose first to close the open cursor.
// If the pool is full, drops the handle instead.
func (wc *WireConnection) RecycleStatement(stmtHandle int32, hasCursor bool) error {
	if hasCursor {
		if err := wc.FreeStatement(stmtHandle, DSQLClose); err != nil {
			return err
		}
	}
	if wc.freeCount < maxFreeHandles {
		wc.freeHandles[wc.freeCount] = stmtHandle
		wc.freeCount++
		return nil
	}
	// Pool full — drop the handle
	return wc.FreeStatement(stmtHandle, DSQLDrop)
}

// DrainStatementPool drops all pooled statement handles.
// Called during connection close to release server resources.
func (wc *WireConnection) DrainStatementPool() {
	for wc.freeCount > 0 {
		wc.freeCount--
		_ = wc.FreeStatement(wc.freeHandles[wc.freeCount], DSQLDrop)
	}
}

// FreeStatement sends op_free_statement with the given option.
func (wc *WireConnection) FreeStatement(stmtHandle int32, option uint32) error {
	wc.writer.WriteInt32(opFreeStatement)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteUInt32(option)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_free_statement: flush: %w", err)
	}

	if wc.lazySend {
		wc.deferredCount++
		return nil
	}

	_, err := wc.reader.ReadResponse()
	if err != nil {
		return fmt.Errorf("op_free_statement: %w", err)
	}
	return nil
}

// InfoSQL sends op_info_sql and returns the raw info buffer.
func (wc *WireConnection) InfoSQL(stmtHandle int32, items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoSQL)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_sql: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_sql: %w", err)
	}
	return resp.Data, nil
}

// CompleteSQLDescribe parses statement describe data from a prepare (or
// op_info_sql) response and, if the buffer was truncated, keeps requesting
// continuations with isc_info_sql_sqlda_start until every output column and
// input parameter descriptor has been received. items must be the same info
// item list used for the original request.
func (wc *WireConnection) CompleteSQLDescribe(stmtHandle int32, buf []byte, items []byte, bufferLength int32) (int32, []ColumnDescriptor, []ColumnDescriptor, error) {
	var st describeState
	truncated := parseSQLDescribeChunk(buf, &st)
	for truncated {
		prevDone := st.doneOutputs + st.doneInputs
		contItems := itemsWithSqldaStart(items, st.doneOutputs+1, st.doneInputs+1)
		data, err := wc.InfoSQL(stmtHandle, contItems, bufferLength)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("describe continuation: %w", err)
		}
		truncated = parseSQLDescribeChunk(data, &st)
		if truncated && st.doneOutputs+st.doneInputs == prevDone {
			return 0, nil, nil, fmt.Errorf("describe continuation made no progress (outputs %d/%d, inputs %d/%d)",
				st.doneOutputs, st.numOutputs, st.doneInputs, st.numInputs)
		}
	}
	return st.stmtType, st.outputs, st.inputs, nil
}
