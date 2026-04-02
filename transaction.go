package firebird

import "database/sql/driver"

// transaction implements driver.Tx.
type transaction struct {
	conn   *conn
	handle int32
	done   bool
}

var _ driver.Tx = (*transaction)(nil)

// Commit implements driver.Tx.
func (tx *transaction) Commit() error {
	tx.conn.mu.Lock()

	if tx.done {
		tx.conn.mu.Unlock()
		return nil
	}
	tx.done = true
	tx.conn.activeTx = 0
	err := tx.conn.wc.Commit(tx.handle)
	tx.conn.mu.Unlock()
	return err
}

// Rollback implements driver.Tx.
func (tx *transaction) Rollback() error {
	tx.conn.mu.Lock()

	if tx.done {
		tx.conn.mu.Unlock()
		return nil
	}
	tx.done = true
	tx.conn.activeTx = 0
	err := tx.conn.wc.Rollback(tx.handle)
	tx.conn.mu.Unlock()
	return err
}
