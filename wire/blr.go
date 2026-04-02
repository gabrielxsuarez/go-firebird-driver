package wire

// AppendBLR appends the BLR for the given column descriptors to dst and
// returns the extended buffer.
func AppendBLR(dst []byte, descs []ColumnDescriptor) []byte {
	count := len(descs)

	dst = append(dst, BlrVersion5, BlrBegin, BlrMessage, 0)

	// param_count = columns × 2 (type + null indicator)
	paramCount := count * 2
	dst = append(dst, byte(paramCount), byte(paramCount>>8))

	for i := range descs {
		dst = appendBLRType(dst, &descs[i])
		// Null indicator: blr_short + scale 0
		dst = append(dst, BlrShort, 0)
	}

	dst = append(dst, BlrEnd, BlrEOC)
	return dst
}

// BuildBLR generates the Binary Language Representation for a set of column
// descriptors. The BLR tells the server how to format row data.
//
// Format: version5 + begin + message(0, count*2) + [type + null_ind]... + end + eoc
func BuildBLR(descs []ColumnDescriptor) []byte {
	estSize := 8 + len(descs)*10
	return AppendBLR(make([]byte, 0, estSize), descs)
}

// BuildParamBLR generates BLR for input parameters.
// For parameters, we use the declared types from the descriptor.
func BuildParamBLR(descs []ColumnDescriptor) []byte {
	return BuildBLR(descs)
}

// appendBLRType appends the BLR type descriptor for a column.
func appendBLRType(buf []byte, desc *ColumnDescriptor) []byte {
	sqlType := desc.SQLType & ^int32(1) // strip nullable flag

	switch sqlType {
	case SQLShort:
		buf = append(buf, BlrShort, byte(desc.Scale))

	case SQLLong:
		buf = append(buf, BlrLong, byte(desc.Scale))

	case SQLInt64:
		buf = append(buf, BlrInt64, byte(desc.Scale))

	case SQLInt128:
		buf = append(buf, BlrInt128, byte(desc.Scale))

	case SQLFloat:
		buf = append(buf, BlrFloat)

	case SQLDouble:
		buf = append(buf, BlrDouble)

	case SQLText:
		// blr_text2: [15][charset_lo][charset_hi][len_lo][len_hi]
		charset := desc.SubType & 0xFF
		buf = append(buf, BlrText2,
			byte(charset), byte(charset>>8),
			byte(desc.Length), byte(desc.Length>>8))

	case SQLVarying:
		// blr_varying2: [38][charset_lo][charset_hi][len_lo][len_hi]
		charset := desc.SubType & 0xFF
		buf = append(buf, BlrVarying2,
			byte(charset), byte(charset>>8),
			byte(desc.Length), byte(desc.Length>>8))

	case SQLBoolean:
		buf = append(buf, BlrBool)

	case SQLTypeDate:
		buf = append(buf, BlrSQLDate)

	case SQLTypeTime:
		buf = append(buf, BlrSQLTime)

	case SQLTimestamp:
		buf = append(buf, BlrTimestamp)

	case SQLTimeTZ:
		buf = append(buf, BlrSQLTimeTZ)

	case SQLTimestampTZ:
		buf = append(buf, BlrTimestampTZ)

	case SQLDec16:
		buf = append(buf, BlrDec64)

	case SQLDec34:
		buf = append(buf, BlrDec128)

	case SQLBlob:
		// blr_blob2: [17][subtype_lo][subtype_hi][charset][collation]
		subType := desc.SubType
		buf = append(buf, BlrBlob2,
			byte(subType), byte(subType>>8),
			0, 0) // charset and collation default

	case SQLNull:
		// NULL type: blr_text [0] [0]
		buf = append(buf, BlrText, 0, 0)

	default:
		// Fallback: treat as varying
		buf = append(buf, BlrVarying2, 0, 0,
			byte(desc.Length), byte(desc.Length>>8))
	}

	return buf
}

// IOLength returns the wire size for a given SQL type.
// Negative = fixed size in bytes, positive = (length+1), 0 = variable (XDR buffer).
func IOLength(sqlType, length int32) int {
	baseType := sqlType & ^int32(1) // strip nullable flag

	switch baseType {
	case SQLShort, SQLLong, SQLFloat, SQLTypeDate, SQLTypeTime:
		return -4
	case SQLDouble, SQLTimestamp, SQLBlob, SQLArray, SQLInt64, SQLDec16, SQLTimeTZ:
		return -8
	case SQLTimestampTZ, SQLTimeTZEx:
		return -12
	case SQLTimestampTZEx, SQLDec34, SQLInt128:
		return -16
	case SQLText:
		return int(length) + 1
	case SQLVarying:
		return 0
	case SQLBoolean:
		return 2
	default:
		return 0
	}
}
