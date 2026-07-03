package wire

import "encoding/binary"

const maxSQLDescribeColumns = 4096

// Info item constants for database, transaction, SQL, and BLOB info requests.
const (
	// Database info items
	IscInfoDBID            byte = 4
	IscInfoIscVersion      byte = 12
	IscInfoBaseLevel       byte = 13
	IscInfoPageSize        byte = 14
	IscInfoOdsVersion      byte = 32
	IscInfoOdsMinorVersion byte = 33
	IscInfoDBSQLDialect    byte = 62
	IscInfoDBReadOnly      byte = 63
	IscInfoDBSizeInPages   byte = 64
	IscInfoDBProvider      byte = 108
	IscInfoFirebirdVersion byte = 103

	// Transaction info items
	IscInfoTraID        byte = 4
	IscInfoTraIsolation byte = 8
	IscInfoTraAccess    byte = 9

	// SQL info items
	IscInfoSQLSelect       byte = 4
	IscInfoSQLBind         byte = 5
	IscInfoSQLNumVariables byte = 6
	IscInfoSQLDescribeVars byte = 7
	IscInfoSQLDescribeEnd  byte = 8
	IscInfoSQLSQLDASeq     byte = 9
	IscInfoSQLType         byte = 11
	IscInfoSQLSubType      byte = 12
	IscInfoSQLScale        byte = 13
	IscInfoSQLLength       byte = 14
	IscInfoSQLNullInd      byte = 15
	IscInfoSQLField        byte = 16
	IscInfoSQLRelation     byte = 17
	IscInfoSQLOwner        byte = 18
	IscInfoSQLAlias        byte = 19
	IscInfoSQLSqldaStart   byte = 20
	IscInfoSQLStmtType     byte = 21

	// SQL record count items
	IscInfoSQLRecords     byte = 23
	IscInfoReqSelectCount byte = 13
	IscInfoReqInsertCount byte = 14
	IscInfoReqUpdateCount byte = 15
	IscInfoReqDeleteCount byte = 16

	// BLOB info items
	IscInfoBlobNumSegments byte = 4
	IscInfoBlobMaxSegment  byte = 5
	IscInfoBlobTotalLength byte = 6
	IscInfoBlobType        byte = 7
)

// DescribeItems is the standard set of info items for describing output columns.
// It intentionally omits relation/owner metadata because the driver never uses it.
var DescribeItems = []byte{
	IscInfoSQLNumVariables,
	IscInfoSQLDescribeVars,
	IscInfoSQLSQLDASeq,
	IscInfoSQLType,
	IscInfoSQLSubType,
	IscInfoSQLScale,
	IscInfoSQLLength,
	IscInfoSQLNullInd,
	IscInfoSQLField,
	IscInfoSQLAlias,
	IscInfoSQLDescribeEnd,
}

var prepareInfoItems = []byte{
	IscInfoSQLStmtType,
	IscInfoSQLSelect,
	IscInfoSQLNumVariables,
	IscInfoSQLDescribeVars,
	IscInfoSQLSQLDASeq,
	IscInfoSQLType,
	IscInfoSQLSubType,
	IscInfoSQLScale,
	IscInfoSQLLength,
	IscInfoSQLNullInd,
	IscInfoSQLField,
	IscInfoSQLAlias,
	IscInfoSQLDescribeEnd,
	IscInfoSQLBind,
	IscInfoSQLNumVariables,
	IscInfoSQLDescribeVars,
	IscInfoSQLSQLDASeq,
	IscInfoSQLType,
	IscInfoSQLSubType,
	IscInfoSQLScale,
	IscInfoSQLLength,
	IscInfoSQLDescribeEnd,
}

var prepareExecInfoItems = []byte{
	IscInfoSQLStmtType,
	IscInfoSQLBind,
	IscInfoSQLNumVariables,
	IscInfoSQLDescribeVars,
	IscInfoSQLSQLDASeq,
	IscInfoSQLType,
	IscInfoSQLSubType,
	IscInfoSQLScale,
	IscInfoSQLLength,
	IscInfoSQLDescribeEnd,
}

// PrepareInfoItems is the default set of items requested during
// op_prepare_statement for statements that may be queried later.
func PrepareInfoItems() []byte {
	return prepareInfoItems
}

// PrepareExecInfoItems returns the reduced item set needed for ad-hoc Exec paths.
func PrepareExecInfoItems() []byte {
	return prepareExecInfoItems
}

// InfoItem represents a parsed tag-length-value info item.
type InfoItem struct {
	Tag  byte
	Data []byte
}

// Int32LE returns the item data as a little-endian int32.
func (item *InfoItem) Int32LE() int32 {
	if len(item.Data) == 0 {
		return 0
	}
	switch len(item.Data) {
	case 1:
		return int32(item.Data[0])
	case 2:
		return int32(binary.LittleEndian.Uint16(item.Data))
	case 4:
		return int32(binary.LittleEndian.Uint32(item.Data))
	default:
		// Best effort: use first 4 bytes
		if len(item.Data) >= 4 {
			return int32(binary.LittleEndian.Uint32(item.Data[:4]))
		}
		return int32(item.Data[0])
	}
}

// String returns the item data as a string.
func (item *InfoItem) String() string {
	return string(item.Data)
}

// ParseInfoBuffer parses a TLV info buffer with Int16 LE lengths.
// Returns the parsed items and whether the buffer was truncated.
// Item Data fields reference the original buffer (zero-copy); callers
// must not modify the input buffer while items are in use.
//
// Only isc_info_end (1) and isc_info_truncated (2) are treated as
// control tags.  Tag values 3+ are parsed as normal TLV items because
// the same byte values are reused across info contexts (e.g. tag 4 is
// isc_info_tra_id in transaction info, isc_info_blob_num_segments in
// blob info, isc_info_db_id in database info).
func ParseInfoBuffer(buf []byte) (items []InfoItem, truncated bool) {
	pos := 0
	for pos < len(buf) {
		tag := buf[pos]
		pos++

		switch tag {
		case IscInfoEnd:
			return items, false
		case IscInfoTruncated:
			return items, true
		}

		if pos+2 > len(buf) {
			return items, false
		}
		length := int(binary.LittleEndian.Uint16(buf[pos : pos+2]))
		pos += 2

		if pos+length > len(buf) {
			return items, false
		}

		items = append(items, InfoItem{Tag: tag, Data: buf[pos : pos+length]})
		pos += length
	}
	return items, false
}

// ParseSQLDescribeInfo parses the info buffer from op_prepare_statement
// and returns column descriptors for output columns and input parameters.
// If the buffer was truncated (isc_info_truncated) the result is partial;
// use WireConnection.CompleteSQLDescribe to follow continuations.
func ParseSQLDescribeInfo(buf []byte) (stmtType int32, outputs []ColumnDescriptor, inputs []ColumnDescriptor) {
	var st describeState
	parseSQLDescribeChunk(buf, &st)
	return st.stmtType, st.outputs, st.inputs
}

// describeState accumulates statement describe data across possibly
// multiple info buffers (initial prepare response + truncation continuations).
type describeState struct {
	stmtType    int32
	outputs     []ColumnDescriptor
	inputs      []ColumnDescriptor
	numOutputs  int // total declarado por el servidor (0 = desconocido)
	numInputs   int
	doneOutputs int // descriptores completos (isc_info_sql_describe_end vistos)
	doneInputs  int
}

// parseSQLDescribeChunk parses one info buffer into st. Returns true when the
// buffer ends in isc_info_truncated, meaning a continuation request is needed.
// A descriptor cut mid-way by truncation is not counted as done: the
// continuation re-requests it from scratch (mirrors jaybird's
// StatementInfoProcessor).
func parseSQLDescribeChunk(buf []byte, st *describeState) (truncated bool) {
	pos := 0
	var currentList *[]ColumnDescriptor
	var totalVars *int
	var doneVars *int
	var current *ColumnDescriptor

	for pos < len(buf) {
		tag := buf[pos]
		pos++

		if tag == IscInfoEnd {
			return false
		}
		if tag == IscInfoTruncated {
			return true
		}

		// Section markers don't have length
		if tag == IscInfoSQLSelect {
			currentList, totalVars, doneVars = &st.outputs, &st.numOutputs, &st.doneOutputs
			current = nil
			continue
		}
		if tag == IscInfoSQLBind {
			currentList, totalVars, doneVars = &st.inputs, &st.numInputs, &st.doneInputs
			current = nil
			continue
		}
		if tag == IscInfoSQLDescribeEnd {
			if current != nil && doneVars != nil {
				*doneVars++
			}
			current = nil
			continue
		}

		if pos+2 > len(buf) {
			break
		}
		length := int(binary.LittleEndian.Uint16(buf[pos : pos+2]))
		pos += 2

		if pos+length > len(buf) {
			break
		}
		data := buf[pos : pos+length]
		pos += length

		switch tag {
		case IscInfoSQLStmtType:
			st.stmtType = readInfoInt32LE(data)

		case IscInfoSQLNumVariables, IscInfoSQLDescribeVars:
			n := describeCount(readInfoInt32LE(data))
			if totalVars != nil {
				*totalVars = n
				if currentList != nil && n > 0 && cap(*currentList) < n {
					grown := make([]ColumnDescriptor, len(*currentList), n)
					copy(grown, *currentList)
					*currentList = grown
				}
			}

		case IscInfoSQLSQLDASeq:
			if currentList != nil {
				seq := describeCount(readInfoInt32LE(data))
				if seq > 0 {
					if totalVars != nil && *totalVars > 0 && seq > *totalVars {
						current = nil
						continue
					}
					for len(*currentList) < seq {
						*currentList = append(*currentList, ColumnDescriptor{})
					}
					current = &(*currentList)[seq-1]
				} else {
					current = nil
				}
			}

		case IscInfoSQLType:
			if current != nil {
				current.SQLType = readInfoInt32LE(data)
			}
		case IscInfoSQLSubType:
			if current != nil {
				current.SubType = readInfoInt32LE(data)
			}
		case IscInfoSQLScale:
			if current != nil {
				current.Scale = readInfoInt32LE(data)
			}
		case IscInfoSQLLength:
			if current != nil {
				current.Length = readInfoInt32LE(data)
			}
		case IscInfoSQLNullInd:
			if current != nil {
				current.Nullable = readInfoInt32LE(data) != 0
			}
		case IscInfoSQLField:
			if current != nil {
				current.FieldName = string(data)
			}
		case IscInfoSQLRelation:
			if current != nil {
				current.RelationName = string(data)
			}
		case IscInfoSQLOwner:
			if current != nil {
				current.OwnerName = string(data)
			}
		case IscInfoSQLAlias:
			if current != nil {
				current.AliasName = string(data)
			}
		}
	}
	return false
}

// itemsWithSqldaStart rebuilds an info item list inserting an
// isc_info_sql_sqlda_start (with the 1-based index of the first unprocessed
// descriptor of the corresponding section) before each select/bind item,
// so the server resumes the describe where the previous buffer was cut.
func itemsWithSqldaStart(items []byte, nextOutput, nextInput int) []byte {
	out := make([]byte, 0, len(items)+8)
	for _, it := range items {
		if it == IscInfoSQLSelect || it == IscInfoSQLBind {
			idx := nextOutput
			if it == IscInfoSQLBind {
				idx = nextInput
			}
			out = append(out, IscInfoSQLSqldaStart, 2, byte(idx&0xFF), byte(idx>>8))
		}
		out = append(out, it)
	}
	return out
}

func describeCount(v int32) int {
	if v <= 0 || v > maxSQLDescribeColumns {
		return 0
	}
	return int(v)
}

// ColumnDescriptor describes a column or parameter from SQL statement metadata.
// Fields are ordered by alignment size (largest first) to minimize padding.
type ColumnDescriptor struct {
	FieldName    string
	RelationName string
	OwnerName    string
	AliasName    string
	SQLType      int32
	SubType      int32
	Scale        int32
	Length       int32
	Nullable     bool
}

// readInfoInt32LE reads a little-endian int32 from variable-length data.
func readInfoInt32LE(data []byte) int32 {
	switch len(data) {
	case 0:
		return 0
	case 1:
		return int32(data[0])
	case 2:
		return int32(binary.LittleEndian.Uint16(data))
	case 4:
		return int32(binary.LittleEndian.Uint32(data))
	default:
		if len(data) >= 4 {
			return int32(binary.LittleEndian.Uint32(data[:4]))
		}
		return int32(data[0])
	}
}

// ParseRecordCounts parses an isc_info_sql_records response to extract
// select, insert, update, and delete counts.
func ParseRecordCounts(data []byte) (selectCount, insertCount, updateCount, deleteCount int64) {
	pos := 0
	for pos < len(data) {
		tag := data[pos]
		pos++

		switch tag {
		case IscInfoEnd, IscInfoTruncated, IscInfoError, IscInfoDataNotReady:
			return
		}

		if pos+2 > len(data) {
			return
		}
		length := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+length > len(data) {
			return
		}

		if tag == IscInfoSQLRecords {
			parseRecordCountItems(data[pos:pos+length], &selectCount, &insertCount, &updateCount, &deleteCount)
			return
		}
		pos += length
	}
	return
}

func parseRecordCountItems(data []byte, selectCount, insertCount, updateCount, deleteCount *int64) {
	pos := 0
	for pos < len(data) {
		tag := data[pos]
		pos++

		switch tag {
		case IscInfoEnd, IscInfoTruncated, IscInfoError, IscInfoDataNotReady:
			return
		}

		if pos+2 > len(data) {
			return
		}
		length := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+length > len(data) {
			return
		}

		value := int64(readInfoInt32LE(data[pos : pos+length]))
		switch tag {
		case IscInfoReqSelectCount:
			*selectCount = value
		case IscInfoReqInsertCount:
			*insertCount = value
		case IscInfoReqUpdateCount:
			*updateCount = value
		case IscInfoReqDeleteCount:
			*deleteCount = value
		}
		pos += length
	}
}
