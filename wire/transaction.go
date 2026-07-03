// Operaciones de transacción del wire protocol (spec cap. 7).

package wire

import "fmt"

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
