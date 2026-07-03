package firebird

import (
	"database/sql/driver"
	"fmt"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
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

var rowsAffectedInfoItems = []byte{wire.IscInfoSQLRecords}

func getRowsAffected(wc *wire.WireConnection, stmtHandle int32, stmtType int32) (int64, error) {
	data, err := wc.InfoSQL(stmtHandle, rowsAffectedInfoItems, 256)
	if err != nil {
		return 0, err
	}

	_, insertCount, updateCount, deleteCount := wire.ParseRecordCounts(data)

	switch stmtType {
	case wire.StmtInsert:
		return insertCount, nil
	case wire.StmtUpdate:
		return updateCount, nil
	case wire.StmtDelete:
		return deleteCount, nil
	default:
		return insertCount + updateCount + deleteCount, nil
	}
}
