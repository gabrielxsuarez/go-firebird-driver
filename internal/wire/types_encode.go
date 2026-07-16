// Codificación de parámetros para op_execute/op_execute2 (spec cap. 14).

package wire

import (
	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"

	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math"
)

// EncodeParams encodes parameter values into wire format for op_execute.
// Returns the null bitset + encoded data as a single buffer.
// Optimized: single-pass encoding that builds null bitset and writes values.
func EncodeParams(w *Writer, descs []ColumnDescriptor, values []any) {
	_ = EncodeParamsErr(w, descs, values)
}

// EncodeParamsErr encodes parameter values and reports conversion errors.
func EncodeParamsErr(w *Writer, descs []ColumnDescriptor, values []any) error {
	colCount := len(descs)
	bitsetStart := reserveNullBitset(w, colCount)

	// Single pass: write null bits and encode values
	for i := range descs {
		if i >= len(values) || values[i] == nil {
			// Set null bit
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			w.buf[bitsetStart+byteIdx] |= 1 << bitIdx
			continue
		}
		// Encode non-null value
		if err := encodeValue(w, &descs[i], values[i]); err != nil {
			return err
		}
	}
	return nil
}

// EncodeNamedParams encodes database/sql named values without first boxing them
// into a transient []any slice.
func EncodeNamedParams(w *Writer, descs []ColumnDescriptor, values []driver.NamedValue) {
	_ = EncodeNamedParamsErr(w, descs, values)
}

// EncodeNamedParamsErr encodes database/sql named values and reports conversion errors.
func EncodeNamedParamsErr(w *Writer, descs []ColumnDescriptor, values []driver.NamedValue) error {
	colCount := len(descs)
	bitsetStart := reserveNullBitset(w, colCount)

	for i := range descs {
		if i >= len(values) || values[i].Value == nil {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			w.buf[bitsetStart+byteIdx] |= 1 << bitIdx
			continue
		}
		if err := encodeValue(w, &descs[i], values[i].Value); err != nil {
			return err
		}
	}
	return nil
}

// EncodeParamsOptimal encodes named parameters, using a stack-allocated buffer
// for small parameter sets and falling back to a pooled writer for larger ones.
// The returned bytes reference the StackWriter's buffer when used (caller must
// consume them before sw goes out of scope) or a copy when falling back to pool.
//
// Usage:
//
//	var sw StackWriter
//	paramData := EncodeParamsOptimal(&sw, descs, values)
func EncodeParamsOptimal(sw *StackWriter, descs []ColumnDescriptor, values []driver.NamedValue) []byte {
	data, _ := EncodeParamsOptimalErr(sw, descs, values)
	return data
}

// EncodeParamsOptimalErr encodes named parameters and reports conversion errors.
func EncodeParamsOptimalErr(sw *StackWriter, descs []ColumnDescriptor, values []driver.NamedValue) ([]byte, error) {
	if EstimateParamSize(descs, values) <= 1024 {
		sw.Reset()
		if err := EncodeNamedParamsStackErr(sw, descs, values); err != nil {
			return nil, err
		}
		if !sw.Overflowed() {
			return sw.Bytes(), nil
		}
	}
	// Fallback to pooled writer
	w := GetWriter()
	if err := EncodeNamedParamsErr(w, descs, values); err != nil {
		PutWriter(w)
		return nil, err
	}
	data := make([]byte, w.Len())
	copy(data, w.Bytes())
	PutWriter(w)
	return data, nil
}

// EstimateParamSize estimates the wire size needed for encoding the given parameters.
// Used to decide if stack-allocated buffer can be used.
func EstimateParamSize(descs []ColumnDescriptor, values []driver.NamedValue) int {
	// Null bitset size (padded to 4 bytes)
	size := ((len(descs)+7)/8 + 3) & ^3

	for i, desc := range descs {
		if i >= len(values) || values[i].Value == nil {
			continue // null values take no space (just the bit in bitset)
		}
		size += estimateValueSize(&desc, values[i].Value)
	}
	return size
}

// EncodeNamedParamsStackErr encodes parameters using a StackWriter and reports conversion errors.
func EncodeNamedParamsStackErr(w *StackWriter, descs []ColumnDescriptor, values []driver.NamedValue) error {
	colCount := len(descs)
	// Reserve null bitset space
	byteCount := (colCount + 7) / 8
	padded := (byteCount + 3) & ^3
	if w.n+padded > len(w.buf) {
		w.overflow = true
		return nil
	}
	for i := range padded {
		w.buf[w.n+i] = 0
	}
	bitsetStart := w.n
	w.n += padded

	for i := range descs {
		if i >= len(values) || values[i].Value == nil {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			w.buf[bitsetStart+byteIdx] |= 1 << bitIdx
			continue
		}
		if err := encodeValueStack(w, &descs[i], values[i].Value); err != nil {
			return err
		}
	}
	return nil
}

func copyTextParam(dst []byte, desc *ColumnDescriptor, value any) error {
	var data string
	var raw []byte
	switch v := value.(type) {
	case string:
		s, err := fbcharset.Encode(charsetID(desc), v)
		if err != nil {
			return err
		}
		data = s
	case []byte:
		raw = v
	default:
		s, err := fbcharset.Encode(charsetID(desc), toString(value))
		if err != nil {
			return err
		}
		data = s
	}

	var n int
	if raw != nil {
		if len(raw) > len(dst) {
			return fmt.Errorf("text parameter too long: encoded length %d exceeds field length %d", len(raw), len(dst))
		}
		n = copy(dst, raw)
	} else {
		if len(data) > len(dst) {
			return fmt.Errorf("text parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(charsetID(desc)), len(data), len(dst))
		}
		n = copy(dst, data)
	}
	for i := n; i < len(dst); i++ {
		dst[i] = 0x20
	}
	return nil
}

func varyingPaddingByte(desc *ColumnDescriptor) byte {
	if desc.SubType == fbcharset.IDOctets {
		return 0x00
	}
	return 0x20
}

func writeVaryingParam(w *Writer, desc *ColumnDescriptor, data []byte) error {
	if len(data) > int(desc.Length) {
		return fmt.Errorf("varying parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(charsetID(desc)), len(data), desc.Length)
	}
	l := len(data)
	pad := (4 - l) & 3
	w.grow(4 + l + pad)
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], data)
	w.n += l
	for i := 0; i < pad; i++ {
		w.buf[w.n+i] = varyingPaddingByte(desc)
	}
	w.n += pad
	return nil
}

func writeVaryingParamStack(w *StackWriter, desc *ColumnDescriptor, data []byte) error {
	if len(data) > int(desc.Length) {
		return fmt.Errorf("varying parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(charsetID(desc)), len(data), desc.Length)
	}
	l := len(data)
	pad := (4 - l) & 3
	if w.n+4+l+pad > len(w.buf) {
		w.overflow = true
		return nil
	}
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], data)
	w.n += l
	for i := 0; i < pad; i++ {
		w.buf[w.n+i] = varyingPaddingByte(desc)
	}
	w.n += pad
	return nil
}

// encodeValueStack encodes a single value into a StackWriter.
func encodeValueStack(w *StackWriter, desc *ColumnDescriptor, value any) error {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		if v < minInt16 || v > maxInt16 {
			return numericOverflow(value, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLLong:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		if v < minInt32 || v > maxInt32 {
			return numericOverflow(value, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLInt64:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		w.WriteInt64(v)

	case SQLFloat:
		v, err := floatValue(value)
		if err != nil {
			return err
		}
		w.WriteUInt32(math.Float32bits(float32(v)))

	case SQLDouble:
		v, err := floatValue(value)
		if err != nil {
			return err
		}
		if w.n+8 > len(w.buf) {
			return nil
		}
		binary.BigEndian.PutUint64(w.buf[w.n:], math.Float64bits(v))
		w.n += 8

	case SQLBoolean:
		// XDR de BOOLEAN: el byte significativo va primero, con 3 bytes de padding.
		w.WriteUInt32(boolWireValue(toBool(value)))

	case SQLTypeDate:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteInt32(DateToMJD(t))

	case SQLTypeTime:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteUInt32(TimeToTicks(t))

	case SQLTimestamp:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))

	case SQLText:
		length := int(desc.Length)
		pad := (4 - length) & 3
		if w.n+length+pad > len(w.buf) {
			w.overflow = true
			return nil
		}
		if err := copyTextParam(w.buf[w.n:w.n+length], desc, value); err != nil {
			return err
		}
		w.n += length
		copy(w.buf[w.n:], zeroPad[:pad])
		w.n += pad

	case SQLVarying:
		switch v := value.(type) {
		case string:
			s, err := fbcharset.Encode(charsetID(desc), v)
			if err != nil {
				return err
			}
			return writeVaryingParamStack(w, desc, []byte(s))
		case []byte:
			return writeVaryingParamStack(w, desc, v)
		default:
			s, err := fbcharset.Encode(charsetID(desc), toString(value))
			if err != nil {
				return err
			}
			return writeVaryingParamStack(w, desc, []byte(s))
		}

	case SQLBlob:
		v := toInt64(value)
		w.WriteInt64(v)

	case SQLDec16:
		w.WriteString(toString(value))

	case SQLDec34:
		w.WriteString(toString(value))

	case SQLInt128:
		data, err := valueToInt128(value, desc.Scale)
		if err != nil {
			return err
		}
		if w.n+16 > len(w.buf) {
			w.overflow = true
			return nil
		}
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLTimestampTZ, SQLTimestampTZEx:
		tv, err := timeValue(value)
		if err != nil {
			return err
		}
		utc, offset, offsetMinutes := timezoneParts(tv)
		w.WriteInt32(DateToMJD(utc))
		w.WriteUInt32(TimeToTicks(utc))
		w.WriteUInt32(tzOffsetToID(offset))
		w.WriteInt32(offsetMinutes)

	case SQLTimeTZ, SQLTimeTZEx:
		tv, err := timeValue(value)
		if err != nil {
			return err
		}
		utc, offset, offsetMinutes := timezoneParts(tv)
		w.WriteUInt32(TimeToTicks(utc))
		w.WriteUInt32(tzOffsetToID(offset))
		w.WriteInt32(offsetMinutes)

	default:
		// Fallback: write as string
		switch v := value.(type) {
		case string:
			w.WriteString(v)
		case []byte:
			w.WriteBuffer(v)
		default:
			w.WriteString(toString(value))
		}
	}
	return nil
}

// estimateValueSize estimates the wire size for a single value.
func estimateValueSize(desc *ColumnDescriptor, value any) int {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort, SQLLong, SQLFloat, SQLTypeDate, SQLTypeTime:
		return 4
	case SQLDouble, SQLTimestamp, SQLBlob, SQLInt64:
		return 8
	case SQLTimeTZ, SQLTimeTZEx:
		return 12
	case SQLTimestampTZ, SQLTimestampTZEx:
		return 16
	case SQLDec16, SQLDec34:
		s := toString(value)
		return 4 + len(s) + ((4 - len(s)) & 3)
	case SQLInt128:
		return 16
	case SQLText:
		return int(desc.Length) + ((4 - int(desc.Length)) & 3) // padded
	case SQLVarying:
		switch v := value.(type) {
		case string:
			return 4 + len(v) + ((4 - len(v)) & 3) // length prefix + data + padding
		case []byte:
			return 4 + len(v) + ((4 - len(v)) & 3)
		default:
			return 4 + 32 + ((4 - 32) & 3) // conservative estimate for non-string types
		}
	case SQLBoolean:
		return 4
	default:
		return 32 // conservative estimate for unknown types
	}
}

func reserveNullBitset(w *Writer, colCount int) int {
	byteCount := (colCount + 7) / 8
	padded := (byteCount + 3) & ^3
	w.grow(padded)
	bitsetStart := w.n
	for i := range padded {
		w.buf[bitsetStart+i] = 0
	}
	w.n += padded
	return bitsetStart
}

// encodeValue encodes a single parameter value based on its SQL type.
func encodeValue(w *Writer, desc *ColumnDescriptor, value any) error {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		if v < minInt16 || v > maxInt16 {
			return numericOverflow(value, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLLong:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		if v < minInt32 || v > maxInt32 {
			return numericOverflow(value, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLInt64:
		v, err := numericInt64(value, desc.Scale)
		if err != nil {
			return err
		}
		w.WriteInt64(v)

	case SQLFloat:
		v, err := floatValue(value)
		if err != nil {
			return err
		}
		w.WriteUInt32(math.Float32bits(float32(v)))

	case SQLDouble:
		v, err := floatValue(value)
		if err != nil {
			return err
		}
		w.grow(8)
		binary.BigEndian.PutUint64(w.buf[w.n:], math.Float64bits(v))
		w.n += 8

	case SQLBoolean:
		// XDR de BOOLEAN: el byte significativo va primero, con 3 bytes de padding.
		w.WriteUInt32(boolWireValue(toBool(value)))

	case SQLTypeDate:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteInt32(DateToMJD(t))

	case SQLTypeTime:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteUInt32(TimeToTicks(t))

	case SQLTimestamp:
		t, err := timeValue(value)
		if err != nil {
			return err
		}
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))

	case SQLText:
		length := int(desc.Length)
		pad := (4 - length) & 3
		w.grow(length + pad)

		if err := copyTextParam(w.buf[w.n:w.n+length], desc, value); err != nil {
			return err
		}
		w.n += length
		copy(w.buf[w.n:], zeroPad[:pad])
		w.n += pad

	case SQLVarying:
		switch v := value.(type) {
		case string:
			s, err := fbcharset.Encode(charsetID(desc), v)
			if err != nil {
				return err
			}
			return writeVaryingParam(w, desc, []byte(s))
		case []byte:
			return writeVaryingParam(w, desc, v)
		default:
			s, err := fbcharset.Encode(charsetID(desc), toString(value))
			if err != nil {
				return err
			}
			return writeVaryingParam(w, desc, []byte(s))
		}

	case SQLBlob:
		// Blob ID already set by separate blob creation
		v := toInt64(value)
		w.WriteInt64(v)

	case SQLDec16:
		w.WriteString(toString(value))

	case SQLDec34:
		w.WriteString(toString(value))

	case SQLInt128:
		data, err := valueToInt128(value, desc.Scale)
		if err != nil {
			return err
		}
		w.grow(16)
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLTimestampTZ, SQLTimestampTZEx:
		tv, err := timeValue(value)
		if err != nil {
			return err
		}
		utc, offset, offsetMinutes := timezoneParts(tv)
		w.WriteInt32(DateToMJD(utc))
		w.WriteUInt32(TimeToTicks(utc))
		w.WriteUInt32(tzOffsetToID(offset))
		w.WriteInt32(offsetMinutes)

	case SQLTimeTZ, SQLTimeTZEx:
		tv, err := timeValue(value)
		if err != nil {
			return err
		}
		utc, offset, offsetMinutes := timezoneParts(tv)
		w.WriteUInt32(TimeToTicks(utc))
		w.WriteUInt32(tzOffsetToID(offset))
		w.WriteInt32(offsetMinutes)

	default:
		// Fallback: write as string
		switch v := value.(type) {
		case string:
			w.WriteString(v)
		case []byte:
			w.WriteBuffer(v)
		default:
			w.WriteString(toString(value))
		}
	}
	return nil
}
