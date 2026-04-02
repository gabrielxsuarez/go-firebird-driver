package wire

import "testing"

func FuzzParseInfoBuffer(f *testing.F) {
	// Valid buffer: tag=14 length=4 data=[0x10,0x00,0x00,0x00] end=1
	f.Add([]byte{14, 4, 0, 0x10, 0, 0, 0, IscInfoEnd})
	// Empty
	f.Add([]byte{})
	// Just end marker
	f.Add([]byte{IscInfoEnd})
	// Truncated marker
	f.Add([]byte{IscInfoTruncated})
	// Two items
	f.Add([]byte{14, 2, 0, 0xFF, 0x01, 32, 1, 0, 42, IscInfoEnd})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic.
		ParseInfoBuffer(data)
	})
}

func FuzzParseSQLDescribeInfo(f *testing.F) {
	// Minimal: stmt_type=1 (SELECT), one output column
	f.Add([]byte{
		IscInfoSQLStmtType, 1, 0, 1, // stmtType = 1
		IscInfoSQLSelect,
		IscInfoSQLNumVariables, 1, 0, 1,
		IscInfoSQLSQLDASeq, 1, 0, 1,
		IscInfoSQLType, 2, 0, 0x80, 0x01, // type 384
		IscInfoSQLLength, 2, 0, 4, 0,
		IscInfoSQLDescribeEnd,
		IscInfoEnd,
	})
	f.Add([]byte{})
	f.Add([]byte{IscInfoEnd})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic.
		ParseSQLDescribeInfo(data)
	})
}

func FuzzParseRecordCounts(f *testing.F) {
	// Valid nested structure: outer tag=23, inner items
	inner := []byte{
		IscInfoReqSelectCount, 4, 0, 5, 0, 0, 0,
		IscInfoReqInsertCount, 4, 0, 0, 0, 0, 0,
		IscInfoEnd,
	}
	outer := []byte{IscInfoSQLRecords, byte(len(inner)), byte(len(inner) >> 8)}
	outer = append(outer, inner...)
	outer = append(outer, IscInfoEnd)
	f.Add(outer)
	f.Add([]byte{})
	f.Add([]byte{IscInfoEnd})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic.
		ParseRecordCounts(data)
	})
}
