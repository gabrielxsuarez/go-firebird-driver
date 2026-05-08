package firebird

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"time"

	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"
	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// rows implements driver.Rows and all optional metadata interfaces.
type rows struct {
	conn         *conn
	ctx          context.Context
	stmtHandle   int32
	txHandle     int32
	outputs      []wire.ColumnDescriptor
	fetchSize    int
	autoFreeStmt bool // if true, frees stmt on close (ad-hoc queries only)
	autoCommitTx bool // if true, commits tx on close
	hasCursor    bool

	// fetch buffer
	buf         [][]any
	bufIdx      int
	eof         bool
	closed      bool
	blr         []byte
	hasBlobs    bool
	fetchRows   [][]any
	fetchValues []any

	// Inline buffers to avoid separate heap allocs for small result sets.
	// Covers 1 column × 8 initial rows. For wider result sets, FetchRowsReuse
	// allocates as before.
	inlineValues [8]any
	inlineRows   [8][]any
	inlineBLR    [64]byte  // enough for most BLRs (1-8 columns)
	inlineCols   [4]string // inline column name storage

	// cached column names to avoid reallocation on each Columns() call
	cachedColumns []string
}

var (
	_ driver.Rows                           = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ driver.RowsColumnTypeLength           = (*rows)(nil)
	_ driver.RowsColumnTypeNullable         = (*rows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*rows)(nil)
	_ driver.RowsColumnTypeScanType         = (*rows)(nil)
)

// Columns implements driver.Rows.
func (r *rows) Columns() []string {
	if r.cachedColumns != nil {
		return r.cachedColumns
	}
	n := len(r.outputs)
	var cols []string
	if n <= len(r.inlineCols) {
		cols = r.inlineCols[:n]
	} else {
		cols = make([]string, n)
	}
	for i, c := range r.outputs {
		if c.AliasName != "" {
			cols[i] = c.AliasName
		} else {
			cols[i] = c.FieldName
		}
	}
	r.cachedColumns = cols
	return cols
}

// Close implements driver.Rows.
func (r *rows) Close() error {
	r.conn.mu.Lock()

	if r.closed {
		r.conn.mu.Unlock()
		return nil
	}
	r.closed = true

	if r.conn.closed || r.conn.bad {
		r.conn.mu.Unlock()
		return nil
	}

	var err error
	if r.autoFreeStmt {
		err = r.conn.handleFatalErrorLocked(r.conn.wc.RecycleStatement(r.stmtHandle, r.hasCursor))
	} else if r.hasCursor {
		// Close cursor but keep statement
		err = r.conn.handleFatalErrorLocked(r.conn.wc.FreeStatement(r.stmtHandle, wire.DSQLClose))
	}
	r.conn.mu.Unlock()
	if err != nil {
		return err
	}
	if r.autoCommitTx {
		// Persistent auto-tx: no commit needed, tx stays alive for reuse.
		// Data is already visible since we use READ COMMITTED.
		return nil
	}
	return nil
}

// Next implements driver.Rows.
func (r *rows) Next(dest []driver.Value) error {
	if r.closed {
		return io.EOF
	}
	if err := r.contextErr(); err != nil {
		return err
	}

	// Need more data?
	if r.bufIdx >= len(r.buf) {
		if r.eof {
			return io.EOF
		}
		if err := r.fetch(); err != nil {
			return err
		}
		if r.bufIdx >= len(r.buf) {
			return io.EOF
		}
	}

	row := r.buf[r.bufIdx]
	r.bufIdx++

	for i := range dest {
		if i < len(row) {
			dest[i] = row[i]
		}
	}
	return nil
}

func (r *rows) fetch() error {
	if err := r.contextErr(); err != nil {
		return err
	}
	// Cache BLR on first fetch to avoid rebuilding on subsequent fetches
	if r.blr == nil {
		r.blr = wire.AppendBLR(r.inlineBLR[:0], r.outputs)
		// Use inline buffers for first fetch to avoid separate heap allocs
		if len(r.outputs) > 0 {
			r.fetchValues = r.inlineValues[:0]
			r.fetchRows = r.inlineRows[:0]
		}
	}

	r.conn.mu.Lock()
	if err := r.contextErr(); err != nil {
		r.conn.mu.Unlock()
		return err
	}
	stop := r.conn.withCancel(r.ctx)

	fetched, values, eof, err := r.conn.wc.FetchRowsReuse(
		r.stmtHandle,
		r.blr,
		r.outputs,
		int32(r.fetchSize),
		r.fetchRows,
		r.fetchValues,
	)
	stop()
	if err != nil {
		ctxErr := r.contextErr()
		handled := r.conn.handleFatalErrorLocked(err)
		r.conn.mu.Unlock()
		if ctxErr != nil {
			return ctxErr
		}
		return handled
	}
	r.fetchRows = fetched[:0]
	r.fetchValues = values

	// Materialize BLOBs: replace blobIDs with actual content.
	// Skip the check entirely if no blob columns exist for this result set.
	if r.hasBlobs {
		for _, row := range fetched {
			for ci, col := range r.outputs {
				if col.SQLType&^int32(1) != wire.SQLBlob || ci >= len(row) || row[ci] == nil {
					continue
				}
				blobID, ok := row[ci].(int64)
				if !ok || blobID == 0 {
					continue
				}
				data, err := r.conn.wc.ReadBlobData(r.txHandle, blobID)
				if err != nil {
					r.conn.mu.Unlock()
					return fmt.Errorf("read blob column %d: %w", ci, r.conn.handleFatalErrorLocked(err))
				}
				if col.SubType == 1 {
					row[ci] = fbcharset.Decode(fbcharset.CharsetID(r.conn.config.Charset), data) // text blob
				} else {
					row[ci] = data // binary blob
				}
			}
		}
	}

	r.conn.mu.Unlock()

	r.buf = fetched
	r.bufIdx = 0

	if eof {
		r.eof = true
	}

	return nil
}

func (r *rows) contextErr() error {
	if r.ctx == nil {
		return nil
	}
	return r.ctx.Err()
}

// hasBlobs returns true if any column is a BLOB type.
func hasBlobs(cols []wire.ColumnDescriptor) bool {
	for i := range cols {
		if cols[i].SQLType&^int32(1) == wire.SQLBlob {
			return true
		}
	}
	return false
}

// --- Column metadata interfaces ---

// ColumnTypeDatabaseTypeName implements driver.RowsColumnTypeDatabaseTypeName.
func (r *rows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.outputs) {
		return ""
	}
	col := &r.outputs[index]
	sqlType := col.SQLType & ^int32(1) // strip nullable bit
	switch sqlType {
	case wire.SQLText:
		return "CHAR"
	case wire.SQLVarying:
		return "VARCHAR"
	case wire.SQLShort:
		if col.SubType == 1 || col.Scale < 0 {
			return "NUMERIC"
		}
		return "SMALLINT"
	case wire.SQLLong:
		if col.SubType == 1 || col.Scale < 0 {
			return "NUMERIC"
		}
		return "INTEGER"
	case wire.SQLInt64:
		if col.SubType == 1 || col.Scale < 0 {
			return "NUMERIC"
		}
		return "BIGINT"
	case wire.SQLInt128:
		if col.SubType == 1 || col.Scale < 0 {
			return "NUMERIC"
		}
		return "INT128"
	case wire.SQLFloat:
		return "FLOAT"
	case wire.SQLDouble:
		return "DOUBLE PRECISION"
	case wire.SQLTimestamp:
		return "TIMESTAMP"
	case wire.SQLTypeDate:
		return "DATE"
	case wire.SQLTypeTime:
		return "TIME"
	case wire.SQLTimestampTZ, wire.SQLTimestampTZEx:
		return "TIMESTAMP WITH TIME ZONE"
	case wire.SQLTimeTZ, wire.SQLTimeTZEx:
		return "TIME WITH TIME ZONE"
	case wire.SQLBlob:
		if col.SubType == 1 {
			return "BLOB SUB_TYPE TEXT"
		}
		return "BLOB"
	case wire.SQLBoolean:
		return "BOOLEAN"
	case wire.SQLDec16:
		return "DECFLOAT(16)"
	case wire.SQLDec34:
		return "DECFLOAT(34)"
	default:
		return "TYPE_" + strconv.FormatInt(int64(sqlType), 10)
	}
}

// ColumnTypeLength implements driver.RowsColumnTypeLength.
func (r *rows) ColumnTypeLength(index int) (length int64, ok bool) {
	if index < 0 || index >= len(r.outputs) {
		return 0, false
	}
	col := &r.outputs[index]
	sqlType := col.SQLType & ^int32(1) // strip nullable bit
	switch sqlType {
	case wire.SQLText, wire.SQLVarying:
		return int64(col.Length), true
	case wire.SQLBlob:
		return 0, false
	default:
		return 0, false
	}
}

// ColumnTypeNullable implements driver.RowsColumnTypeNullable.
func (r *rows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if index < 0 || index >= len(r.outputs) {
		return false, false
	}
	return r.outputs[index].Nullable, true
}

// ColumnTypePrecisionScale implements driver.RowsColumnTypePrecisionScale.
func (r *rows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	if index < 0 || index >= len(r.outputs) {
		return 0, 0, false
	}
	col := &r.outputs[index]
	if col.Scale < 0 {
		sqlType := col.SQLType & ^int32(1) // strip nullable bit
		switch sqlType {
		case wire.SQLShort:
			return 4, int64(-col.Scale), true
		case wire.SQLLong:
			return 9, int64(-col.Scale), true
		case wire.SQLInt64:
			return 18, int64(-col.Scale), true
		case wire.SQLInt128:
			return 38, int64(-col.Scale), true
		}
	}
	return 0, 0, false
}

// ColumnTypeScanType implements driver.RowsColumnTypeScanType.
func (r *rows) ColumnTypeScanType(index int) reflect.Type {
	if index < 0 || index >= len(r.outputs) {
		return reflect.TypeOf((*any)(nil)).Elem()
	}
	col := &r.outputs[index]
	sqlType := col.SQLType & ^int32(1) // strip nullable bit
	switch sqlType {
	case wire.SQLText, wire.SQLVarying:
		if col.SubType == 1 {
			return reflect.TypeOf([]byte{})
		}
		return reflect.TypeOf("")
	case wire.SQLShort:
		if col.Scale < 0 {
			return reflect.TypeOf(float64(0))
		}
		return reflect.TypeOf(int16(0))
	case wire.SQLLong:
		if col.Scale < 0 {
			return reflect.TypeOf(float64(0))
		}
		return reflect.TypeOf(int32(0))
	case wire.SQLInt64:
		if col.Scale < 0 {
			return reflect.TypeOf(float64(0))
		}
		return reflect.TypeOf(int64(0))
	case wire.SQLInt128, wire.SQLDec16, wire.SQLDec34:
		return reflect.TypeOf("")
	case wire.SQLFloat:
		return reflect.TypeOf(float32(0))
	case wire.SQLDouble:
		return reflect.TypeOf(float64(0))
	case wire.SQLTimestamp, wire.SQLTypeDate, wire.SQLTypeTime,
		wire.SQLTimestampTZ, wire.SQLTimeTZ, wire.SQLTimestampTZEx, wire.SQLTimeTZEx:
		return reflect.TypeOf(time.Time{})
	case wire.SQLBlob:
		if col.SubType == 1 {
			return reflect.TypeOf("")
		}
		return reflect.TypeOf([]byte{})
	case wire.SQLBoolean:
		return reflect.TypeOf(false)
	default:
		return reflect.TypeOf((*any)(nil)).Elem()
	}
}
