package wire

import (
	"fmt"
	"sync"
	"time"
)

// WireConnection manages a single Firebird database connection at the wire
// protocol level. It owns the transport layers (conn), XDR reader/writer,
// and tracks protocol state (version, handles, deferred responses).
type WireConnection struct {
	conn   *Conn
	reader *Reader
	writer *Writer

	dbHandle        int32
	protocolVersion uint32
	charset         string
	lazySend        bool // true when ptype_lazy_send is negotiated

	// Lazy send: count of responses not yet consumed.
	deferredCount int

	// nullBuf is a reusable buffer for null bitset reads (avoids per-row alloc).
	// Sized for up to 256 columns; enlarged if needed.
	nullBuf [32]byte

	// Statement handle pool: reuse freed handles to avoid op_allocate round-trips.
	// Handles are closed with DSQLClose (cursor closed, handle stays allocated)
	// and re-prepared when needed.
	freeHandles [maxFreeHandles]int32
	freeCount   int

	// cancelMu protects async cancel writes to the socket.
	cancelMu sync.Mutex

	// writeMu serializes all writes to the transport. This matters when
	// cancellation is sent from a goroutine while another operation is flushing
	// through the encrypted connection layers.
	writeMu sync.Mutex
}

const maxFreeHandles = 8

// ProtocolVersion returns the negotiated protocol version.
func (wc *WireConnection) ProtocolVersion() uint32 {
	return wc.protocolVersion
}

// SetDeadline sets the read/write deadline on the underlying connection.
func (wc *WireConnection) SetDeadline(t time.Time) {
	_ = wc.conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline on the underlying connection. Setting
// a past time forces a blocked read to return, used to honor a cancelled
// context when the server can't interrupt the current operation (e.g. a lock
// wait). The connection must be treated as broken afterwards.
func (wc *WireConnection) SetReadDeadline(t time.Time) {
	_ = wc.conn.SetReadDeadline(t)
}

// CloseTransport closes the underlying socket without attempting protocol
// cleanup. Use this after transport failures, where detach would only add more
// broken writes to the same dead connection.
func (wc *WireConnection) CloseTransport() error {
	if wc == nil || wc.conn == nil {
		return nil
	}
	return wc.conn.Close()
}

// --- Lazy send helpers ---

// deferResponse increments the deferred response counter.
// The writer data is NOT flushed.
func (wc *WireConnection) deferResponse() {
	wc.deferredCount++
}

// consumeDeferred reads and discards all deferred responses.
func (wc *WireConnection) consumeDeferred() error {
	for wc.deferredCount > 0 {
		_, err := wc.reader.ReadResponse()
		if err != nil {
			return err
		}
		wc.deferredCount--
	}
	return nil
}

// readResponse drains any deferred lazy responses, then reads the actual response.
func (wc *WireConnection) readResponse() (GenericResponse, error) {
	if err := wc.consumeDeferred(); err != nil {
		return GenericResponse{}, fmt.Errorf("consume deferred: %w", err)
	}
	return wc.reader.ReadResponse()
}

// flush sends buffered data to the wire.
func (wc *WireConnection) flush() error {
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()

	return wc.writer.Flush(wc.conn)
}

// --- Database operations ---

// InfoDatabase sends op_info_database and returns the raw info buffer.
func (wc *WireConnection) InfoDatabase(items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoDatabase)
	wc.writer.WriteInt32(0) // p_info_object
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_database: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_database: %w", err)
	}
	return resp.Data, nil
}

// Detach sends op_detach + op_disconnect and closes the connection.
func (wc *WireConnection) Detach() error {
	wc.writer.WriteInt32(opDetach)
	wc.writer.WriteInt32(wc.dbHandle)
	if err := wc.flush(); err != nil {
		wc.conn.Close()
		return fmt.Errorf("op_detach: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		wc.conn.Close()
		return fmt.Errorf("op_detach: %w", err)
	}

	// op_disconnect: no response
	wc.writer.WriteInt32(opDisconnect)
	_ = wc.flush()
	return wc.conn.Close()
}

// Cancel sends an asynchronous op_cancel to interrupt the current operation.
// This is safe to call from a different goroutine.
func (wc *WireConnection) Cancel(kind uint32) error {
	wc.cancelMu.Lock()
	defer wc.cancelMu.Unlock()

	if kind == CancelAbort {
		return wc.conn.Close()
	}

	// Write op_cancel directly to the underlying TCP socket.
	// This bypasses encryption layers intentionally -
	// the cancel packet is always in plaintext per the protocol spec.
	// Actually: cancel goes through the same layers since it's sent
	// on the same socket. Use the writer.
	w := NewWriter()
	w.WriteInt32(opCancel)
	w.WriteUInt32(kind)
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()

	return w.Flush(wc.conn)
}

// --- Transaction operations ---

// Transaction sends op_transaction with the given TPB and returns the handle.
func (wc *WireConnection) Transaction(tpb []byte) (int32, error) {
	wc.writer.WriteInt32(opTransaction)
	wc.writer.WriteInt32(0) // database handle (always 0)
	wc.writer.WriteBuffer(tpb)

	if err := wc.flush(); err != nil {
		return 0, fmt.Errorf("op_transaction: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, fmt.Errorf("op_transaction: %w", err)
	}
	return resp.Handle, nil
}

// Commit sends op_commit for the given transaction handle.
func (wc *WireConnection) Commit(txHandle int32) error {
	wc.writer.WriteInt32(opCommit)
	wc.writer.WriteInt32(txHandle)
	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_commit: flush: %w", err)
	}
	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_commit: %w", err)
	}
	return nil
}

// Rollback sends op_rollback for the given transaction handle.
func (wc *WireConnection) Rollback(txHandle int32) error {
	wc.writer.WriteInt32(opRollback)
	wc.writer.WriteInt32(txHandle)
	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_rollback: flush: %w", err)
	}
	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_rollback: %w", err)
	}
	return nil
}

// CommitRetaining sends op_commit_retaining (handle remains valid).
func (wc *WireConnection) CommitRetaining(txHandle int32) error {
	wc.writer.WriteInt32(opCommitRetaining)
	wc.writer.WriteInt32(txHandle)
	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_commit_retaining: flush: %w", err)
	}
	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_commit_retaining: %w", err)
	}
	return nil
}

// RollbackRetaining sends op_rollback_retaining (handle remains valid).
func (wc *WireConnection) RollbackRetaining(txHandle int32) error {
	wc.writer.WriteInt32(opRollbackRetaining)
	wc.writer.WriteInt32(txHandle)
	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_rollback_retaining: flush: %w", err)
	}
	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_rollback_retaining: %w", err)
	}
	return nil
}

// InfoTransaction sends op_info_transaction and returns the raw info buffer.
func (wc *WireConnection) InfoTransaction(txHandle int32, items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoTransaction)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_transaction: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_transaction: %w", err)
	}
	return resp.Data, nil
}

// --- Statement operations ---

// AllocateStatement sends op_allocate_statement and returns the handle.
func (wc *WireConnection) AllocateStatement() (int32, error) {
	wc.writer.WriteInt32(opAllocateStatement)
	wc.writer.WriteInt32(wc.dbHandle)

	if err := wc.flush(); err != nil {
		return 0, fmt.Errorf("op_allocate_statement: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, fmt.Errorf("op_allocate_statement: %w", err)
	}
	return resp.Handle, nil
}

// AllocateStatementLazy writes op_allocate_statement WITHOUT flushing (lazy send).
// The caller must read the response later.
func (wc *WireConnection) AllocateStatementLazy() {
	wc.writer.WriteInt32(opAllocateStatement)
	wc.writer.WriteInt32(wc.dbHandle)
	wc.deferResponse()
}

// PrepareStatement sends op_prepare_statement and returns the descriptor info.
// Uses the standard describe items for columns and parameters.
func (wc *WireConnection) PrepareStatement(txHandle, stmtHandle int32, sql string, bufferLength int32) ([]byte, error) {
	return wc.PrepareStatementWithItems(txHandle, stmtHandle, sql, bufferLength, PrepareInfoItems())
}

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

// ExecuteAndCommit batches op_execute + op_commit into a single flush,
// saving one network round-trip for auto-commit scenarios.
func (wc *WireConnection) ExecuteAndCommit(stmtHandle, txHandle int32, blr, params []byte) error {
	wc.writeExecuteOp(stmtHandle, txHandle, blr, params)

	// Append op_commit to the same buffer
	wc.writer.WriteInt32(opCommit)
	wc.writer.WriteInt32(txHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("execute+commit: flush: %w", err)
	}

	// Read execute response
	_, execErr := wc.readResponse()
	// Always read commit response to keep wire in sync
	_, commitErr := wc.reader.ReadResponse()

	if execErr != nil {
		return fmt.Errorf("execute+commit: execute: %w", execErr)
	}
	if commitErr != nil {
		return fmt.Errorf("execute+commit: commit: %w", commitErr)
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

// TransactionExecuteCommit batches op_transaction + op_execute + op_commit
// into a single flush using deferred handle resolution (lazy send).
// This reduces 3 round-trips to 1 for auto-commit prepared statements.
// Requires lazySend to be negotiated (protocol v13+).
func (wc *WireConnection) TransactionExecuteCommit(tpb []byte, stmtHandle int32, blr, params []byte) error {
	// op_transaction
	wc.writer.WriteInt32(opTransaction)
	wc.writer.WriteInt32(0) // database handle (always 0)
	wc.writer.WriteBuffer(tpb)

	// op_execute with deferred txHandle (0xFFFF = resolve from previous response)
	wc.writeExecuteOp(stmtHandle, int32(InvalidObject), blr, params)

	// op_commit with deferred txHandle
	wc.writer.WriteInt32(opCommit)
	wc.writer.WriteInt32(int32(InvalidObject))

	if err := wc.flush(); err != nil {
		return fmt.Errorf("tx+execute+commit: flush: %w", err)
	}

	// Read all 3 responses
	_, txErr := wc.readResponse()
	if txErr != nil {
		// Try to read remaining responses to keep wire in sync
		wc.reader.ReadResponse()
		wc.reader.ReadResponse()
		return fmt.Errorf("tx+execute+commit: transaction: %w", txErr)
	}

	_, execErr := wc.reader.ReadResponse()
	if execErr != nil {
		wc.reader.ReadResponse()
		return fmt.Errorf("tx+execute+commit: execute: %w", execErr)
	}

	_, commitErr := wc.reader.ReadResponse()
	if commitErr != nil {
		return fmt.Errorf("tx+execute+commit: commit: %w", commitErr)
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

// Fetch sends op_fetch and reads the response sequence.
// blr should be provided for the first fetch, empty for subsequent.
// Returns status (0=data, 100=EOF) and whether there is row data to read.
func (wc *WireConnection) Fetch(stmtHandle int32, blr []byte, fetchSize int32) (int32, int32, error) {
	wc.writer.WriteInt32(opFetch)
	wc.writer.WriteInt32(stmtHandle)
	wc.writer.WriteBuffer(blr)
	wc.writer.WriteInt32(0)         // p_sqldata_message_number
	wc.writer.WriteInt32(fetchSize) // p_sqldata_messages

	if err := wc.flush(); err != nil {
		return 0, 0, fmt.Errorf("op_fetch: flush: %w", err)
	}

	if err := wc.consumeDeferred(); err != nil {
		return 0, 0, fmt.Errorf("op_fetch: consume deferred: %w", err)
	}

	op := wc.reader.ReadOpcode()
	if wc.reader.Err() != nil {
		return 0, 0, fmt.Errorf("op_fetch: read opcode: %w", wc.reader.Err())
	}

	if op != opFetchResponse {
		if op == opResponse {
			// El servidor reporta un error del statement (p.ej. excepción
			// aritmética al evaluar una fila): consumir el cuerpo mantiene el
			// stream sincronizado y expone el error real.
			return 0, 0, fetchServerError(wc)
		}
		return 0, 0, fmt.Errorf("op_fetch: unexpected opcode %d, expected %d", op, opFetchResponse)
	}

	resp := wc.reader.readFetchResponse()
	if wc.reader.Err() != nil {
		return 0, 0, fmt.Errorf("op_fetch: read response: %w", wc.reader.Err())
	}

	return resp.Status, resp.Messages, nil
}

// FetchRows sends op_fetch and decodes all returned rows.
// Returns decoded rows, whether EOF was reached, and any error.
func (wc *WireConnection) FetchRows(stmtHandle int32, blr []byte, descs []ColumnDescriptor, fetchSize int32) ([][]any, bool, error) {
	rows, _, eof, err := wc.FetchRowsReuse(stmtHandle, blr, descs, fetchSize, nil, nil)
	return rows, eof, err
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

// --- BLOB operations ---

// CreateBlob sends op_create_blob2 and returns the handle and blob ID.
func (wc *WireConnection) CreateBlob(txHandle int32, bpb []byte) (int32, int64, error) {
	wc.writer.WriteInt32(opCreateBlob2)
	wc.writer.WriteBuffer(bpb)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(0) // p_blob_id = 0 for new

	if err := wc.flush(); err != nil {
		return 0, 0, fmt.Errorf("op_create_blob2: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, 0, fmt.Errorf("op_create_blob2: %w", err)
	}
	return resp.Handle, resp.BlobID, nil
}

// OpenBlob sends op_open_blob2 and returns the handle.
func (wc *WireConnection) OpenBlob(txHandle int32, blobID int64, bpb []byte) (int32, error) {
	wc.writer.WriteInt32(opOpenBlob2)
	wc.writer.WriteBuffer(bpb)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(blobID)

	if err := wc.flush(); err != nil {
		return 0, fmt.Errorf("op_open_blob2: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, fmt.Errorf("op_open_blob2: %w", err)
	}
	return resp.Handle, nil
}

// PutSegment sends op_put_segment with the given data.
func (wc *WireConnection) PutSegment(blobHandle int32, data []byte) error {
	wc.writer.WriteInt32(opPutSegment)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(int32(len(data)))
	wc.writer.WriteBuffer(data)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_put_segment: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_put_segment: %w", err)
	}
	return nil
}

// GetSegment sends op_get_segment and returns the packed segment data
// and a status (0=data, 1=partial, 2=EOF).
func (wc *WireConnection) GetSegment(blobHandle int32, maxLength int32) (int32, []byte, error) {
	wc.writer.WriteInt32(opGetSegment)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(maxLength)
	wc.writer.WriteBuffer(nil) // p_sgmt_segment (empty)

	if err := wc.flush(); err != nil {
		return 0, nil, fmt.Errorf("op_get_segment: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, nil, fmt.Errorf("op_get_segment: %w", err)
	}
	return resp.Handle, resp.Data, nil
}

// CloseBlob sends op_close_blob.
func (wc *WireConnection) CloseBlob(blobHandle int32) error {
	wc.writer.WriteInt32(opCloseBlob)
	wc.writer.WriteInt32(blobHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_close_blob: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_close_blob: %w", err)
	}
	return nil
}

// CancelBlob sends op_cancel_blob.
func (wc *WireConnection) CancelBlob(blobHandle int32) error {
	wc.writer.WriteInt32(opCancelBlob)
	wc.writer.WriteInt32(blobHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_cancel_blob: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_cancel_blob: %w", err)
	}
	return nil
}

// InfoBlob sends op_info_blob and returns the raw info buffer.
func (wc *WireConnection) InfoBlob(blobHandle int32, items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoBlob)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_blob: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_blob: %w", err)
	}
	return resp.Data, nil
}

// Reader returns the underlying wire reader for direct row data decoding.
func (wc *WireConnection) Reader() *Reader {
	return wc.reader
}

// ReadBlobData opens, reads, and closes a blob, returning the full content.
// Uses pipelining when lazy send is available: batches open + first get_segment
// into one flush, saving 1 round-trip for the common case of small blobs.
func (wc *WireConnection) ReadBlobData(txHandle int32, blobID int64) ([]byte, error) {
	const maxGetLen int32 = 65535

	if wc.lazySend {
		return wc.readBlobDataPipelined(txHandle, blobID, maxGetLen)
	}
	return wc.readBlobDataSequential(txHandle, blobID, maxGetLen)
}

// readBlobDataPipelined batches open_blob + first get_segment in one flush.
func (wc *WireConnection) readBlobDataPipelined(txHandle int32, blobID int64, maxGetLen int32) ([]byte, error) {
	// op_open_blob2 (deferred — no flush)
	wc.writer.WriteInt32(opOpenBlob2)
	wc.writer.WriteBuffer(nil) // empty BPB
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(blobID)

	// op_get_segment with deferred blob handle
	wc.writer.WriteInt32(opGetSegment)
	wc.writer.WriteInt32(int32(InvalidObject))
	wc.writer.WriteInt32(maxGetLen)
	wc.writer.WriteBuffer(nil)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("blob read pipeline: flush: %w", err)
	}

	// Read open response (contains blob handle)
	openResp, openErr := wc.readResponse()
	if openErr != nil {
		// Still need to read the get_segment response to keep wire in sync
		wc.reader.ReadResponse()
		return nil, fmt.Errorf("blob read pipeline: open: %w", openErr)
	}
	blobHandle := openResp.Handle

	// Read first get_segment response
	getResp, getErr := wc.reader.ReadResponse()
	if getErr != nil {
		_ = wc.CloseBlob(blobHandle)
		return nil, fmt.Errorf("blob read pipeline: get_segment: %w", getErr)
	}

	result := make([]byte, 0, 4096)
	result = unpackSegments(result, getResp.Data)

	if getResp.Handle == 2 { // EOF — single-segment blob (most common case)
		if err := wc.CloseBlob(blobHandle); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Multi-segment: continue reading sequentially
	for {
		status, packed, err := wc.GetSegment(blobHandle, maxGetLen)
		if err != nil {
			_ = wc.CloseBlob(blobHandle)
			return nil, err
		}
		result = unpackSegments(result, packed)
		if status == 2 { // EOF
			break
		}
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return nil, err
	}
	return result, nil
}

// readBlobDataSequential is the fallback for non-lazy-send connections.
func (wc *WireConnection) readBlobDataSequential(txHandle int32, blobID int64, maxGetLen int32) ([]byte, error) {
	blobHandle, err := wc.OpenBlob(txHandle, blobID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]byte, 0, 4096)
	for {
		status, packed, err := wc.GetSegment(blobHandle, maxGetLen)
		if err != nil {
			_ = wc.CloseBlob(blobHandle)
			return nil, err
		}
		result = unpackSegments(result, packed)
		if status == 2 { // EOF
			break
		}
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return nil, err
	}
	return result, nil
}

// unpackSegments appends unpacked segment data to dst.
// Each segment in packed is: 2-byte LE length + data.
func unpackSegments(dst, packed []byte) []byte {
	for off := 0; off+2 <= len(packed); {
		segLen := int(packed[off]) | int(packed[off+1])<<8
		off += 2
		if off+segLen > len(packed) {
			break
		}
		dst = append(dst, packed[off:off+segLen]...)
		off += segLen
	}
	return dst
}

// WriteBlobData creates a blob, writes data in segments, closes it, and returns the blob ID.
// Uses pipelining when lazy send is available to batch all operations in a single flush,
// reducing N+2 round-trips to 1.
func (wc *WireConnection) WriteBlobData(txHandle int32, data []byte) (int64, error) {
	const maxSegment = 32768

	// Pipeline path: batch create + all puts + close in one flush.
	// Uses deferred handle resolution (0xFFFF) — the server resolves it
	// to the blob handle from op_create_blob2.
	if wc.lazySend {
		// op_create_blob2 (deferred — no flush)
		wc.writer.WriteInt32(opCreateBlob2)
		wc.writer.WriteBuffer(nil) // empty BPB
		wc.writer.WriteInt32(txHandle)
		wc.writer.WriteInt64(0) // new blob

		// Count segments for response draining
		segCount := 0
		remaining := data
		for len(remaining) > 0 {
			seg := remaining
			if len(seg) > maxSegment {
				seg = seg[:maxSegment]
			}
			// op_put_segment with deferred blob handle
			wc.writer.WriteInt32(opPutSegment)
			wc.writer.WriteInt32(int32(InvalidObject))
			wc.writer.WriteInt32(int32(len(seg)))
			wc.writer.WriteBuffer(seg)
			segCount++
			remaining = remaining[len(seg):]
		}

		// op_close_blob with deferred blob handle
		wc.writer.WriteInt32(opCloseBlob)
		wc.writer.WriteInt32(int32(InvalidObject))

		if err := wc.flush(); err != nil {
			return 0, fmt.Errorf("blob write pipeline: flush: %w", err)
		}

		// Read create response (contains blob handle and blob ID)
		createResp, createErr := wc.readResponse()
		// Read all put_segment responses
		for i := 0; i < segCount; i++ {
			_, err := wc.reader.ReadResponse()
			if err != nil && createErr == nil {
				createErr = fmt.Errorf("blob write pipeline: put_segment[%d]: %w", i, err)
			}
		}
		// Read close response
		_, closeErr := wc.reader.ReadResponse()

		if createErr != nil {
			return 0, fmt.Errorf("blob write pipeline: create: %w", createErr)
		}
		if closeErr != nil {
			return 0, fmt.Errorf("blob write pipeline: close: %w", closeErr)
		}
		return createResp.BlobID, nil
	}

	// Fallback: sequential round-trips (non-lazy send)
	blobHandle, blobID, err := wc.CreateBlob(txHandle, nil)
	if err != nil {
		return 0, err
	}

	for len(data) > 0 {
		seg := data
		if len(seg) > maxSegment {
			seg = seg[:maxSegment]
		}
		if err := wc.PutSegment(blobHandle, seg); err != nil {
			_ = wc.CancelBlob(blobHandle)
			return 0, err
		}
		data = data[len(seg):]
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return 0, err
	}
	return blobID, nil
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
