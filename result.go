package firebird

import (
	"database/sql/driver"
	"fmt"
)

// result implements driver.Result. The row count is computed eagerly, under
// the connection lock, at Exec time: computing it lazily would perform wire
// I/O on a connection the pool may have already handed to another goroutine.
type result struct {
	rowsAffected int64
	err          error
}

var _ driver.Result = (*result)(nil)

// LastInsertId is not supported by Firebird -- returns an error.
func (r *result) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("firebird: LastInsertId not supported; use RETURNING clause")
}

// RowsAffected returns the number of rows affected by the statement.
func (r *result) RowsAffected() (int64, error) {
	return r.rowsAffected, r.err
}
