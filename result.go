package firebird

import (
	"database/sql/driver"
	"fmt"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// result implements driver.Result with lazy RowsAffected evaluation.
// This avoids an expensive InfoSQL round-trip when the caller doesn't need
// the row count (which is common in many workloads).
type result struct {
	wc         *wire.WireConnection
	stmtHandle int32
	stmtType   int32
	cached     int64
	computed   bool
}

var _ driver.Result = (*result)(nil)

// LastInsertId is not supported by Firebird — returns an error.
func (r *result) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("firebird: LastInsertId not supported; use RETURNING clause")
}

// RowsAffected returns the number of rows affected by the statement.
// The first call triggers an InfoSQL round-trip to get the actual count.
// Subsequent calls return the cached value.
func (r *result) RowsAffected() (int64, error) {
	if r.computed {
		return r.cached, nil
	}
	// Lazy evaluation: only fetch when actually needed
	r.cached = getRowsAffected(r.wc, r.stmtHandle, r.stmtType)
	r.computed = true
	return r.cached, nil
}
