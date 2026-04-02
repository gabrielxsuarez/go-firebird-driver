package wire

import (
	"bytes"
	"database/sql/driver"
	"testing"
)

func TestPrepareExecInfoItemsReduced(t *testing.T) {
	items := PrepareExecInfoItems()

	if !bytes.Contains(items, []byte{IscInfoSQLBind}) {
		t.Fatalf("PrepareExecInfoItems missing bind section")
	}
	if bytes.Contains(items, []byte{IscInfoSQLSelect}) {
		t.Fatalf("PrepareExecInfoItems unexpectedly requests output metadata")
	}
	if bytes.Contains(items, []byte{IscInfoSQLField}) || bytes.Contains(items, []byte{IscInfoSQLAlias}) {
		t.Fatalf("PrepareExecInfoItems unexpectedly requests output column names")
	}
}

func TestParseRecordCounts(t *testing.T) {
	data := []byte{
		IscInfoSQLRecords, 16, 0,
		IscInfoReqSelectCount, 1, 0, 7,
		IscInfoReqInsertCount, 1, 0, 3,
		IscInfoReqUpdateCount, 1, 0, 2,
		IscInfoReqDeleteCount, 1, 0, 1,
		IscInfoEnd,
	}

	selectCount, insertCount, updateCount, deleteCount := ParseRecordCounts(data)

	if selectCount != 7 || insertCount != 3 || updateCount != 2 || deleteCount != 1 {
		t.Fatalf(
			"ParseRecordCounts = (%d,%d,%d,%d), want (7,3,2,1)",
			selectCount,
			insertCount,
			updateCount,
			deleteCount,
		)
	}
}

func TestEncodeNamedParamsMatchesEncodeParams(t *testing.T) {
	descs := []ColumnDescriptor{
		{SQLType: SQLLong, Length: 4},
		{SQLType: SQLVarying, Length: 10},
		{SQLType: SQLText, Length: 4},
	}
	anyValues := []any{int64(42), "fire", []byte("go")}
	namedValues := []driver.NamedValue{
		{Ordinal: 1, Value: int64(42)},
		{Ordinal: 2, Value: "fire"},
		{Ordinal: 3, Value: []byte("go")},
	}

	anyWriter := NewWriter()
	EncodeParams(anyWriter, descs, anyValues)

	namedWriter := NewWriter()
	EncodeNamedParams(namedWriter, descs, namedValues)

	if !bytes.Equal(anyWriter.Bytes(), namedWriter.Bytes()) {
		t.Fatalf("EncodeNamedParams output mismatch")
	}
}
