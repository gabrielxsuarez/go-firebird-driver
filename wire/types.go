package wire

import (
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/timezone"
)

// Modified Julian Date epoch: November 17, 1858.
var mjdEpoch = time.Date(1858, 11, 17, 0, 0, 0, 0, time.UTC)

// Time units: 100 microseconds per tick.
const timeTicksPerSecond = 10000

// --- Date/Time conversions ---

// DateToMJD converts a time.Time to Modified Julian Date (days since epoch).
func DateToMJD(t time.Time) int32 {
	t = t.UTC()
	days := t.Sub(mjdEpoch).Hours() / 24
	return int32(days)
}

// MJDToDate converts a Modified Julian Date to time.Time.
func MJDToDate(mjd int32) time.Time {
	return mjdEpoch.AddDate(0, 0, int(mjd))
}

// TimeToTicks converts a time.Time to 100µs ticks since midnight.
func TimeToTicks(t time.Time) uint32 {
	h, m, s := t.Clock()
	ns := t.Nanosecond()
	ticks := (h*3600+m*60+s)*timeTicksPerSecond + ns/100000
	return uint32(ticks)
}

// TicksToTime converts 100µs ticks since midnight to time.Time (date = zero).
func TicksToTime(ticks uint32) time.Time {
	totalMicro := int64(ticks) * 100
	sec := totalMicro / 1_000_000
	ns := (totalMicro % 1_000_000) * 1000

	h := int(sec / 3600)
	m := int((sec % 3600) / 60)
	s := int(sec % 60)
	return time.Date(0, 1, 1, h, m, s, int(ns), time.UTC)
}

// TimestampToTime converts MJD date + ticks to time.Time.
func TimestampToTime(mjd int32, ticks uint32) time.Time {
	date := MJDToDate(mjd)
	t := TicksToTime(ticks)
	return time.Date(date.Year(), date.Month(), date.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// TimestampTZToTime converts date + time + timezone value to time.Time.
func TimestampTZToTime(mjd int32, ticks uint32, tzValue uint32) time.Time {
	utcTime := TimestampToTime(mjd, ticks)
	loc := timezone.Resolve(tzValue)
	return utcTime.In(loc)
}

// TimeTZToTime converts UTC time ticks + timezone value to time.Time.
func TimeTZToTime(ticks uint32, tzValue uint32) time.Time {
	t := TicksToTime(ticks)
	loc := timezone.Resolve(tzValue)
	return t.In(loc)
}

// --- Row data decoding ---

// DecodeColumn decodes a single column value from the reader based on its
// SQL type descriptor. Returns the Go value or nil for NULL.
func DecodeColumn(r *Reader, desc *ColumnDescriptor) any {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort:
		v := r.ReadInt32()
		if desc.Scale < 0 {
			return scaledInt64(int64(int16(v)), desc.Scale)
		}
		return int16(v)

	case SQLLong:
		v := r.ReadInt32()
		if desc.Scale < 0 {
			return scaledInt64(int64(v), desc.Scale)
		}
		return v

	case SQLInt64:
		v := r.ReadInt64()
		if desc.Scale < 0 {
			return scaledInt64(v, desc.Scale)
		}
		return v

	case SQLFloat:
		bits := r.ReadUInt32()
		return math.Float32frombits(bits)

	case SQLDouble:
		buf := r.readView(8)
		if r.Err() != nil {
			return float64(0)
		}
		bits := binary.BigEndian.Uint64(buf)
		return math.Float64frombits(bits)

	case SQLBoolean:
		v := r.ReadInt32()
		return v != 0

	case SQLTypeDate:
		mjd := r.ReadInt32()
		return MJDToDate(mjd)

	case SQLTypeTime:
		ticks := r.ReadUInt32()
		return TicksToTime(ticks)

	case SQLTimestamp:
		mjd := r.ReadInt32()
		ticks := r.ReadUInt32()
		return TimestampToTime(mjd, ticks)

	case SQLTimeTZ:
		ticks := r.ReadUInt32()
		tz := r.ReadUInt32()
		return TimeTZToTime(ticks, tz)

	case SQLTimestampTZ:
		mjd := r.ReadInt32()
		ticks := r.ReadUInt32()
		tz := r.ReadUInt32()
		return TimestampTZToTime(mjd, ticks, tz)

	case SQLText:
		length := int(desc.Length)
		if length <= 0 {
			return ""
		}
		pad := (4 - length) & 3
		result := r.readView(length + pad)
		if r.Err() != nil {
			return ""
		}
		result = result[:length]
		if desc.SubType != 1 { // not OCTETS
			result = trimRightSpaces(result)
		}
		return string(result)

	case SQLVarying:
		data := r.ReadBuffer()
		if r.Err() != nil {
			return ""
		}
		// ReadBuffer returns slice of internal buffer; convert directly
		return string(data)

	case SQLBlob:
		blobID := r.ReadInt64()
		return blobID

	case SQLDec16:
		buf := r.readView(8)
		if r.Err() != nil {
			return ""
		}
		return decfloatToString(buf, 16)

	case SQLDec34:
		buf := r.readView(16)
		if r.Err() != nil {
			return ""
		}
		return decfloatToString(buf, 34)

	case SQLInt128:
		buf := r.readView(16)
		if r.Err() != nil {
			return ""
		}
		v := new(big.Int).SetBytes(buf)
		if buf[0]&0x80 != 0 {
			// Two's complement: subtract 2^128
			twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
			v.Sub(v, twoTo128)
		}
		if desc.Scale < 0 {
			return scaledBigInt(v, desc.Scale)
		}
		return v.String()

	default:
		// Unknown type: try to read as varying
		return r.ReadString()
	}
}

// ReadNullBitset reads the null bitset for protocol 13+.
// Returns a byte slice where bit N=1 means column N is null.
// The returned slice is safe to use across subsequent read calls.
func ReadNullBitset(r *Reader, colCount int) []byte {
	byteCount := (colCount + 7) / 8
	padded := (byteCount + 3) & ^3 // pad to 4-byte boundary
	// Use a dedicated buffer so ReadBuffer/ReadOpaque don't overwrite the bitset.
	buf := make([]byte, padded)
	r.readFull(buf)
	return buf[:byteCount]
}

// readNullBitsetInto reads the null bitset into a pre-allocated buffer.
// buf must have capacity >= padded byte count (ceil(colCount/8) rounded up to 4).
func readNullBitsetInto(r *Reader, colCount int, buf []byte) {
	byteCount := (colCount + 7) / 8
	padded := (byteCount + 3) & ^3
	r.readFull(buf[:padded])
}

// IsNull checks if column idx is null in the bitset.
func IsNull(bitset []byte, idx int) bool {
	byteIdx := idx / 8
	bitIdx := uint(idx % 8)
	if byteIdx >= len(bitset) {
		return false
	}
	return (bitset[byteIdx] & (1 << bitIdx)) != 0
}

// EncodeParams encodes parameter values into wire format for op_execute.
// Returns the null bitset + encoded data as a single buffer.
// Optimized: single-pass encoding that builds null bitset and writes values.
func EncodeParams(w *Writer, descs []ColumnDescriptor, values []any) {
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
		encodeValue(w, &descs[i], values[i])
	}
}

// EncodeNamedParams encodes database/sql named values without first boxing them
// into a transient []any slice.
func EncodeNamedParams(w *Writer, descs []ColumnDescriptor, values []driver.NamedValue) {
	colCount := len(descs)
	bitsetStart := reserveNullBitset(w, colCount)

	for i := range descs {
		if i >= len(values) || values[i].Value == nil {
			byteIdx := i / 8
			bitIdx := uint(i % 8)
			w.buf[bitsetStart+byteIdx] |= 1 << bitIdx
			continue
		}
		encodeValue(w, &descs[i], values[i].Value)
	}
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
	if EstimateParamSize(descs, values) <= 1024 {
		sw.Reset()
		EncodeNamedParamsStack(sw, descs, values)
		if !sw.Overflowed() {
			return sw.Bytes()
		}
	}
	// Fallback to pooled writer
	w := GetWriter()
	EncodeNamedParams(w, descs, values)
	data := make([]byte, w.Len())
	copy(data, w.Bytes())
	PutWriter(w)
	return data
}

// EstimateParamSize estimates the wire size needed for encoding the given parameters.
// Used to decide if stack-allocated buffer can be used.
func EstimateParamSize(descs []ColumnDescriptor, values []driver.NamedValue) int {
	// Null bitset size (padded to 4 bytes)
	size := ((len(descs) + 7) / 8 + 3) & ^3

	for i, desc := range descs {
		if i >= len(values) || values[i].Value == nil {
			continue // null values take no space (just the bit in bitset)
		}
		size += estimateValueSize(&desc, values[i].Value)
	}
	return size
}

// EncodeNamedParamsStack encodes parameters using a StackWriter (stack-allocated buffer).
// This avoids allocation and sync.Pool overhead for small parameter sets.
func EncodeNamedParamsStack(w *StackWriter, descs []ColumnDescriptor, values []driver.NamedValue) {
	colCount := len(descs)
	// Reserve null bitset space
	byteCount := (colCount + 7) / 8
	padded := (byteCount + 3) & ^3
	if w.n+padded > len(w.buf) {
		w.overflow = true
		return
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
		encodeValueStack(w, &descs[i], values[i].Value)
	}
}

// encodeValueStack encodes a single value into a StackWriter.
func encodeValueStack(w *StackWriter, desc *ColumnDescriptor, value any) {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLLong:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLInt64:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt64(v)

	case SQLFloat:
		v := toFloat64(value)
		w.WriteUInt32(math.Float32bits(float32(v)))

	case SQLDouble:
		v := toFloat64(value)
		if w.n+8 > len(w.buf) {
			return
		}
		binary.BigEndian.PutUint64(w.buf[w.n:], math.Float64bits(v))
		w.n += 8

	case SQLBoolean:
		v := toBool(value)
		if v {
			w.WriteInt32(1)
		} else {
			w.WriteInt32(0)
		}

	case SQLTypeDate:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))

	case SQLTypeTime:
		t := toTime(value)
		w.WriteUInt32(TimeToTicks(t))

	case SQLTimestamp:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))

	case SQLText:
		length := int(desc.Length)
		pad := (4 - length) & 3
		if w.n+length+pad > len(w.buf) {
			return
		}
		switch v := value.(type) {
		case string:
			n := copy(w.buf[w.n:w.n+length], v)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		case []byte:
			n := copy(w.buf[w.n:w.n+length], v)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		default:
			s := toString(value)
			n := copy(w.buf[w.n:w.n+length], s)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		}
		w.n += length
		copy(w.buf[w.n:], zeroPad[:pad])
		w.n += pad

	case SQLVarying:
		switch v := value.(type) {
		case string:
			w.WriteString(v)
		case []byte:
			w.WriteBuffer(v)
		default:
			w.WriteString(toString(value))
		}

	case SQLBlob:
		v := toInt64(value)
		w.WriteInt64(v)

	case SQLDec16:
		s := toString(value)
		data := stringToDecfloat64(s)
		if w.n+8 > len(w.buf) {
			w.overflow = true
			return
		}
		copy(w.buf[w.n:], data[:])
		w.n += 8

	case SQLDec34:
		s := toString(value)
		data := stringToDecfloat128(s)
		if w.n+16 > len(w.buf) {
			w.overflow = true
			return
		}
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLInt128:
		s := toString(value)
		data := stringToInt128(s, desc.Scale)
		if w.n+16 > len(w.buf) {
			w.overflow = true
			return
		}
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLTimestampTZ:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))
		_, offset := t.Zone()
		w.WriteUInt32(tzOffsetToID(offset))

	case SQLTimeTZ:
		t := toTime(value)
		w.WriteUInt32(TimeToTicks(t))
		_, offset := t.Zone()
		w.WriteUInt32(tzOffsetToID(offset))

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
}

// estimateValueSize estimates the wire size for a single value.
func estimateValueSize(desc *ColumnDescriptor, value any) int {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort, SQLLong, SQLFloat, SQLTypeDate, SQLTypeTime:
		return 4
	case SQLDouble, SQLTimestamp, SQLBlob, SQLInt64:
		return 8
	case SQLTimestampTZ, SQLTimeTZ:
		return 12
	case SQLDec16:
		return 8
	case SQLDec34, SQLInt128:
		return 16
	case SQLText:
		return int(desc.Length) + ((4-int(desc.Length))&3) // padded
	case SQLVarying:
		switch v := value.(type) {
		case string:
			return 4 + len(v) + ((4-len(v))&3) // length prefix + data + padding
		case []byte:
			return 4 + len(v) + ((4-len(v))&3)
		default:
			return 4 + 32 + ((4-32)&3) // conservative estimate for non-string types
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
func encodeValue(w *Writer, desc *ColumnDescriptor, value any) {
	sqlType := desc.SQLType & ^int32(1)

	switch sqlType {
	case SQLShort:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLLong:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt32(int32(v))

	case SQLInt64:
		v := toInt64(value)
		if desc.Scale < 0 {
			v = applyScale(v, desc.Scale)
		}
		w.WriteInt64(v)

	case SQLFloat:
		v := toFloat64(value)
		w.WriteUInt32(math.Float32bits(float32(v)))

	case SQLDouble:
		v := toFloat64(value)
		w.grow(8)
		binary.BigEndian.PutUint64(w.buf[w.n:], math.Float64bits(v))
		w.n += 8

	case SQLBoolean:
		v := toBool(value)
		if v {
			w.WriteInt32(1)
		} else {
			w.WriteInt32(0)
		}

	case SQLTypeDate:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))

	case SQLTypeTime:
		t := toTime(value)
		w.WriteUInt32(TimeToTicks(t))

	case SQLTimestamp:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))

	case SQLText:
		length := int(desc.Length)
		pad := (4 - length) & 3
		w.grow(length + pad)

		switch v := value.(type) {
		case string:
			n := copy(w.buf[w.n:w.n+length], v)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		case []byte:
			n := copy(w.buf[w.n:w.n+length], v)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		default:
			s := toString(value)
			n := copy(w.buf[w.n:w.n+length], s)
			for i := n; i < length; i++ {
				w.buf[w.n+i] = 0x20
			}
		}
		w.n += length
		copy(w.buf[w.n:], zeroPad[:pad])
		w.n += pad

	case SQLVarying:
		switch v := value.(type) {
		case string:
			w.WriteString(v)
		case []byte:
			w.WriteBuffer(v)
		default:
			w.WriteString(toString(value))
		}

	case SQLBlob:
		// Blob ID already set by separate blob creation
		v := toInt64(value)
		w.WriteInt64(v)

	case SQLDec16:
		s := toString(value)
		data := stringToDecfloat64(s)
		w.grow(8)
		copy(w.buf[w.n:], data[:])
		w.n += 8

	case SQLDec34:
		s := toString(value)
		data := stringToDecfloat128(s)
		w.grow(16)
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLInt128:
		s := toString(value)
		data := stringToInt128(s, desc.Scale)
		w.grow(16)
		copy(w.buf[w.n:], data[:])
		w.n += 16

	case SQLTimestampTZ:
		t := toTime(value)
		w.WriteInt32(DateToMJD(t))
		w.WriteUInt32(TimeToTicks(t))
		_, offset := t.Zone()
		w.WriteUInt32(tzOffsetToID(offset))

	case SQLTimeTZ:
		t := toTime(value)
		w.WriteUInt32(TimeToTicks(t))
		_, offset := t.Zone()
		w.WriteUInt32(tzOffsetToID(offset))

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
}

// --- Type conversion helpers ---

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float32:
		return int64(x)
	case float64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		return 0
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case string:
		return x != "" && x != "0" && x != "false"
	default:
		return false
	}
}

func toTime(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	default:
		return time.Time{}
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// scaledInt64 converts a scaled integer to a string representation.
// scale is negative, e.g., -2 means 2 decimal places.
// Optimized to avoid big.Int allocations for values up to 19 digits.
func scaledInt64(v int64, scale int32) string {
	if scale >= 0 {
		return int64ToString(v)
	}

	neg := v < 0
	if neg {
		v = -v
	}

	decPos := int(-scale)

	// Fast path: v fits in uint64 and has <= 19 digits (max for int64)
	if v >= 0 {
		s := int64ToString(v)
		if decPos >= len(s) {
			// Need leading zeros: e.g., 5 with scale -3 → "0.005"
			return addNeg("0."+repeatZeros(decPos-len(s))+s, neg)
		}
		return addNeg(s[:len(s)-decPos]+"."+s[len(s)-decPos:], neg)
	}

	// Slow path: use big.Int (should be rare for int64)
	s := big.NewInt(v).String()
	if decPos >= len(s) {
		s = "0." + repeatZeros(decPos-len(s)) + s
	} else {
		s = s[:len(s)-decPos] + "." + s[len(s)-decPos:]
	}
	return addNeg(s, neg)
}

// int64ToString converts int64 to string without allocation (for most cases).
func int64ToString(v int64) string {
	if v == 0 {
		return "0"
	}

	// Use a buffer on stack
	var buf [20]byte // max 19 digits for int64 + sign
	i := len(buf)

	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}

	return string(buf[i:])
}

// addNeg adds minus sign if needed.
func addNeg(s string, neg bool) string {
	if neg {
		return "-" + s
	}
	return s
}

// scaledBigInt converts a big.Int with scale to string.
func scaledBigInt(v *big.Int, scale int32) string {
	if scale >= 0 {
		return v.String()
	}
	neg := v.Sign() < 0
	if neg {
		v = new(big.Int).Neg(v)
	}
	s := v.String()
	decPos := int(-scale)

	if decPos >= len(s) {
		s = "0." + repeatZeros(decPos-len(s)) + s
	} else {
		s = s[:len(s)-decPos] + "." + s[len(s)-decPos:]
	}

	if neg {
		s = "-" + s
	}
	return s
}

// Pre-computed zero strings for small counts (covers decimal scales up to 20).
var zeroStrings = [21]string{
	"", "0", "00", "000", "0000", "00000", "000000", "0000000",
	"00000000", "000000000", "0000000000", "00000000000",
	"000000000000", "0000000000000", "00000000000000", "000000000000000",
	"0000000000000000", "00000000000000000", "000000000000000000",
	"0000000000000000000", "00000000000000000000",
}

func repeatZeros(n int) string {
	if n >= 0 && n < len(zeroStrings) {
		return zeroStrings[n]
	}
	return strings.Repeat("0", n)
}

// applyScale multiplies a value by 10^(-scale) for storage.
func applyScale(v int64, scale int32) int64 {
	// scale is negative; multiply by 10^|scale|
	for i := int32(0); i > scale; i-- {
		v *= 10
	}
	return v
}

// trimRightSpaces trims trailing 0x20 bytes.
func trimRightSpaces(b []byte) []byte {
	i := len(b)
	for i > 0 && b[i-1] == 0x20 {
		i--
	}
	return b[:i]
}

// decfloatToString converts IEEE-754 decimal floating point bytes to string.
func decfloatToString(data []byte, precision int) string {
	if precision == 16 {
		bits := binary.BigEndian.Uint64(data[:8])
		return decfloat64ToString(bits)
	}
	hi := binary.BigEndian.Uint64(data[:8])
	lo := binary.BigEndian.Uint64(data[8:16])
	return decfloat128ToString(hi, lo)
}

// dpdToDigits decodes a 10-bit DPD (Densely Packed Decimal) value to 3 decimal digits.
func dpdToDigits(dpd uint32) (d0, d1, d2 uint8) {
	// Table-free DPD decoding per IEEE 754-2008 spec
	p := uint8((dpd >> 7) & 0x7)
	q := uint8((dpd >> 4) & 0x7)
	r := uint8(dpd & 0x7)
	// Bits at positions 6, 5, 3 determine encoding
	f := uint8((dpd >> 6) & 1)
	e := uint8((dpd >> 5) & 1)
	g := uint8((dpd >> 3) & 1)

	if f == 0 && e == 0 && g == 0 {
		d0, d1, d2 = p, q, r
	} else if f == 0 && e == 0 && g == 1 {
		d0, d1, d2 = p, q, 8|r&1
	} else if f == 0 && e == 1 && g == 0 {
		d0, d1, d2 = p, 8|q&1, r
	} else if f == 0 && e == 1 && g == 1 {
		d0, d1, d2 = p, 8|q&1, 8|r&1
	} else if f == 1 && e == 0 && g == 0 {
		d0, d1, d2 = 8|p&1, q, r
	} else if f == 1 && e == 0 && g == 1 {
		d0, d1, d2 = 8|p&1, q, 8|r&1
	} else if f == 1 && e == 1 && g == 0 {
		d0, d1, d2 = 8|p&1, 8|q&1, r
	} else {
		d0, d1, d2 = 8|p&1, 8|q&1, 8|r&1
	}
	return
}

// decfloat64ToString converts a decimal64 value to string.
func decfloat64ToString(bits uint64) string {
	sign := bits >> 63
	combo := (bits >> 50) & 0x1FFF

	// Special values: top 5 bits of combo (combo >> 8)
	// 11110 = Infinity (0x1E00-0x1EFF), 11111 = NaN (0x1F00-0x1FFF)
	if combo >= 0x1E00 {
		if combo >= 0x1F00 {
			return "NaN"
		}
		if sign != 0 {
			return "-Infinity"
		}
		return "Infinity"
	}

	// Extract exponent and leading digit
	var exp int
	var leadDigit uint8
	trailing := bits & 0x3FFFFFFFFFFFF // 50 bits

	if combo>>11 == 0x03 {
		// Large coefficient: bits [12:11] = 11
		exp = int((combo>>1)&0x3FF) - 398
		leadDigit = uint8(8 + (combo & 1))
	} else {
		exp = int((combo>>3)&0x3FF) - 398
		leadDigit = uint8((combo >> 0) & 7)
	}

	// Decode 5 DPD declets (50 bits → 15 digits) from trailing significand
	var digits [16]byte
	digits[0] = '0' + leadDigit
	for i := 0; i < 5; i++ {
		shift := uint(40 - i*10)
		dpd := uint32((trailing >> shift) & 0x3FF)
		d0, d1, d2 := dpdToDigits(dpd)
		digits[1+i*3] = '0' + d0
		digits[2+i*3] = '0' + d1
		digits[3+i*3] = '0' + d2
	}

	return formatDecfloat(digits[:16], exp, sign != 0)
}

// decfloat128ToString converts a decimal128 value to string.
func decfloat128ToString(hi, lo uint64) string {
	sign := hi >> 63
	combo := (hi >> 46) & 0x1FFFF

	// Special values: top 5 bits of combo (combo >> 12)
	// 11110 = Infinity (0x1E000-0x1EFFF), 11111 = NaN (0x1F000-0x1FFFF)
	if combo >= 0x1E000 {
		if combo >= 0x1F000 {
			return "NaN"
		}
		if sign != 0 {
			return "-Infinity"
		}
		return "Infinity"
	}

	var exp int
	var leadDigit uint8
	trailingHi := hi & 0x3FFFFFFFFFFF // 46 bits from hi

	if combo>>15 == 0x03 {
		exp = int((combo>>1)&0x3FFF) - 6176
		leadDigit = uint8(8 + (combo & 1))
	} else {
		exp = int((combo>>3)&0x3FFF) - 6176
		leadDigit = uint8(combo & 7)
	}

	// Combine trailing: 46 bits from hi + 64 bits from lo = 110 bits
	// Contains 11 DPD declets (33 digits)
	var digits [34]byte
	digits[0] = '0' + leadDigit

	// Extract 11 DPD declets from 110 trailing bits
	// Declet 0-3 from trailingHi, declet 4 spans hi/lo, declet 5-10 from lo
	for i := 0; i < 11; i++ {
		var dpd uint32
		bitPos := 100 - i*10 // bit position counting from LSB of the 110-bit field
		if bitPos >= 64 {
			// Entirely in trailingHi
			shift := uint(bitPos - 64)
			dpd = uint32((trailingHi >> shift) & 0x3FF)
		} else if bitPos+10 > 64 {
			// Spans hi and lo
			loBits := 64 - bitPos
			hiPart := trailingHi & ((1 << uint(10-loBits)) - 1)
			loPart := lo >> uint(64-loBits)
			dpd = uint32((hiPart << uint(loBits)) | loPart)
			dpd &= 0x3FF
		} else {
			// Entirely in lo
			shift := uint(bitPos)
			dpd = uint32((lo >> shift) & 0x3FF)
		}
		d0, d1, d2 := dpdToDigits(dpd)
		digits[1+i*3] = '0' + d0
		digits[2+i*3] = '0' + d1
		digits[3+i*3] = '0' + d2
	}

	return formatDecfloat(digits[:34], exp, sign != 0)
}

// formatDecfloat formats coefficient digits with exponent into a string.
func formatDecfloat(digits []byte, exp int, negative bool) string {
	// Find first non-zero digit
	firstNonZero := 0
	for firstNonZero < len(digits)-1 && digits[firstNonZero] == '0' {
		firstNonZero++
	}

	// All zeros
	allZero := true
	for _, d := range digits {
		if d != '0' {
			allZero = false
			break
		}
	}
	if allZero {
		if negative {
			return "-0"
		}
		return "0"
	}

	coeff := digits[firstNonZero:]
	adjExp := exp + len(digits) - 1 - firstNonZero

	var buf []byte
	if negative {
		buf = append(buf, '-')
	}

	// Use scientific notation for very large/small exponents
	if adjExp < -6 || adjExp > len(digits)+2 {
		buf = append(buf, coeff[0])
		if len(coeff) > 1 {
			buf = append(buf, '.')
			buf = append(buf, coeff[1:]...)
			// Trim trailing zeros
			for len(buf) > 0 && buf[len(buf)-1] == '0' {
				buf = buf[:len(buf)-1]
			}
			if buf[len(buf)-1] == '.' {
				buf = buf[:len(buf)-1]
			}
		}
		buf = append(buf, 'E')
		if adjExp >= 0 {
			buf = append(buf, '+')
		}
		buf = append(buf, []byte(fmt.Sprintf("%d", adjExp))...)
		return string(buf)
	}

	// Fixed notation
	dotPos := adjExp + 1
	if dotPos <= 0 {
		buf = append(buf, '0', '.')
		for range -dotPos {
			buf = append(buf, '0')
		}
		buf = append(buf, coeff...)
	} else if dotPos >= len(coeff) {
		buf = append(buf, coeff...)
		for range dotPos - len(coeff) {
			buf = append(buf, '0')
		}
	} else {
		buf = append(buf, coeff[:dotPos]...)
		buf = append(buf, '.')
		buf = append(buf, coeff[dotPos:]...)
	}

	// Trim trailing zeros after decimal point
	result := string(buf)
	if strings.Contains(result, ".") {
		result = strings.TrimRight(result, "0")
		result = strings.TrimRight(result, ".")
	}

	return result
}

// --- Encoding helpers for FB4/FB5 types ---

// digitsToDpd encodes 3 decimal digits (0-9) into a 10-bit DPD value.
// This is the inverse of dpdToDigits.
func digitsToDpd(d0, d1, d2 uint8) uint32 {
	p, q, r := uint32(d0), uint32(d1), uint32(d2)

	large0 := d0 >= 8
	large1 := d1 >= 8
	large2 := d2 >= 8

	switch {
	case !large0 && !large1 && !large2:
		return (p << 7) | (q << 4) | r
	case !large0 && !large1 && large2:
		return (p << 7) | (q << 4) | (r & 1) | 0x08
	case !large0 && large1 && !large2:
		return (p << 7) | ((q & 1) << 4) | r | 0x20
	case !large0 && large1 && large2:
		return (p << 7) | ((q & 1) << 4) | (r & 1) | 0x28
	case large0 && !large1 && !large2:
		return ((p & 1) << 7) | (q << 4) | r | 0x40
	case large0 && !large1 && large2:
		return ((p & 1) << 7) | (q << 4) | (r & 1) | 0x48
	case large0 && large1 && !large2:
		return ((p & 1) << 7) | ((q & 1) << 4) | r | 0x60
	default: // all large
		return ((p & 1) << 7) | ((q & 1) << 4) | (r & 1) | 0x68
	}
}

// parseDecimalString parses a decimal string into its components.
// Returns sign, coefficient digits, and exponent.
func parseDecimalString(s string) (negative bool, digits []uint8, exp int) {
	i := 0
	if i < len(s) && s[i] == '-' {
		negative = true
		i++
	} else if i < len(s) && s[i] == '+' {
		i++
	}

	dotPos := -1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		if s[i] == '.' {
			dotPos = len(digits)
			i++
			continue
		}
		digits = append(digits, s[i]-'0')
		i++
	}

	if i < len(s) && (s[i] == 'E' || s[i] == 'e') {
		i++
		expStr := s[i:]
		fmt.Sscanf(expStr, "%d", &exp)
	}

	if dotPos >= 0 {
		exp -= len(digits) - dotPos
	}

	return
}

// stringToDecfloat64 converts a string to IEEE-754 decimal64 DPD format (8 bytes big-endian).
func stringToDecfloat64(s string) [8]byte {
	var result [8]byte

	// Handle special values
	switch {
	case s == "NaN" || s == "nan" || s == "sNaN":
		binary.BigEndian.PutUint64(result[:], 0x7C00000000000000)
		return result
	case s == "Infinity" || s == "infinity" || s == "Inf" || s == "inf" || s == "+Infinity" || s == "+Inf":
		binary.BigEndian.PutUint64(result[:], 0x7800000000000000)
		return result
	case s == "-Infinity" || s == "-infinity" || s == "-Inf" || s == "-inf":
		binary.BigEndian.PutUint64(result[:], 0xF800000000000000)
		return result
	}

	negative, digits, exp := parseDecimalString(s)

	// Remove leading zeros
	for len(digits) > 1 && digits[0] == 0 {
		digits = digits[1:]
		exp++
	}

	// Handle zero
	if len(digits) == 0 || (len(digits) == 1 && digits[0] == 0) {
		digits = []uint8{0}
		exp = 0
	}

	// Truncate to 16 digits maximum (decimal64 coefficient)
	if len(digits) > 16 {
		exp += len(digits) - 16
		digits = digits[:16]
	}

	// Right-align in 16-digit array
	var coeff [16]uint8
	offset := 16 - len(digits)
	copy(coeff[offset:], digits)

	// Biased exponent
	biasedExp := exp + 398 + (len(digits) - 1)
	if biasedExp < 0 {
		biasedExp = 0
	}
	if biasedExp > 767 {
		biasedExp = 767
	}

	leadDigit := coeff[0]

	// Encode 5 DPD declets from digits 1-15 -> 50 trailing bits
	var trailing uint64
	for i := 0; i < 5; i++ {
		d0 := coeff[1+i*3]
		d1 := coeff[2+i*3]
		d2 := coeff[3+i*3]
		dpd := digitsToDpd(d0, d1, d2)
		trailing |= uint64(dpd) << uint(40-i*10)
	}

	// Build combo field (13 bits)
	var combo uint64
	if leadDigit >= 8 {
		// Large leading digit: combo = 11 | exp[9:0] | (leadDigit & 1)
		combo = (0x03 << 11) | (uint64(biasedExp) << 1) | uint64(leadDigit&1)
	} else {
		// Small leading digit: combo = exp[9:8] | leadDigit[2:0] | exp[7:0]
		combo = (uint64(biasedExp) << 3) | uint64(leadDigit)
	}

	var bits uint64
	if negative {
		bits |= 1 << 63
	}
	bits |= combo << 50
	bits |= trailing & 0x3FFFFFFFFFFFF

	binary.BigEndian.PutUint64(result[:], bits)
	return result
}

// stringToDecfloat128 converts a string to IEEE-754 decimal128 DPD format (16 bytes big-endian).
func stringToDecfloat128(s string) [16]byte {
	var result [16]byte

	// Handle special values
	switch {
	case s == "NaN" || s == "nan" || s == "sNaN":
		binary.BigEndian.PutUint64(result[:8], 0x7C00000000000000)
		binary.BigEndian.PutUint64(result[8:], 0)
		return result
	case s == "Infinity" || s == "infinity" || s == "Inf" || s == "inf" || s == "+Infinity" || s == "+Inf":
		binary.BigEndian.PutUint64(result[:8], 0x7800000000000000)
		binary.BigEndian.PutUint64(result[8:], 0)
		return result
	case s == "-Infinity" || s == "-infinity" || s == "-Inf" || s == "-inf":
		binary.BigEndian.PutUint64(result[:8], 0xF800000000000000)
		binary.BigEndian.PutUint64(result[8:], 0)
		return result
	}

	negative, digits, exp := parseDecimalString(s)

	// Remove leading zeros
	for len(digits) > 1 && digits[0] == 0 {
		digits = digits[1:]
		exp++
	}

	// Handle zero
	if len(digits) == 0 || (len(digits) == 1 && digits[0] == 0) {
		digits = []uint8{0}
		exp = 0
	}

	// Truncate to 34 digits maximum (decimal128 coefficient)
	if len(digits) > 34 {
		exp += len(digits) - 34
		digits = digits[:34]
	}

	// Right-align in 34-digit array
	var coeff [34]uint8
	offset := 34 - len(digits)
	copy(coeff[offset:], digits)

	// Biased exponent
	biasedExp := exp + 6176 + (len(digits) - 1)
	if biasedExp < 0 {
		biasedExp = 0
	}
	if biasedExp > 12287 {
		biasedExp = 12287
	}

	leadDigit := coeff[0]

	// Encode 11 DPD declets from digits 1-33 -> 110 trailing bits
	// Split across hi (46 bits) and lo (64 bits)
	var trailingHi uint64
	var lo uint64

	for i := 0; i < 11; i++ {
		d0 := coeff[1+i*3]
		d1 := coeff[2+i*3]
		d2 := coeff[3+i*3]
		dpd := uint64(digitsToDpd(d0, d1, d2))

		bitPos := 100 - i*10 // bit position from LSB of the 110-bit field
		if bitPos >= 64 {
			trailingHi |= dpd << uint(bitPos-64)
		} else if bitPos+10 > 64 {
			// Spans hi and lo
			loBits := 64 - bitPos
			trailingHi |= dpd >> uint(loBits)
			lo |= dpd << uint(64-loBits)
		} else {
			lo |= dpd << uint(bitPos)
		}
	}

	// Build combo field (17 bits)
	var combo uint64
	if leadDigit >= 8 {
		combo = (0x03 << 15) | (uint64(biasedExp) << 1) | uint64(leadDigit&1)
	} else {
		combo = (uint64(biasedExp) << 3) | uint64(leadDigit)
	}

	var hi uint64
	if negative {
		hi |= 1 << 63
	}
	hi |= combo << 46
	hi |= trailingHi & 0x3FFFFFFFFFFF

	binary.BigEndian.PutUint64(result[:8], hi)
	binary.BigEndian.PutUint64(result[8:], lo)
	return result
}

// stringToInt128 converts a string to 16 bytes big-endian two's complement INT128.
// If scale < 0, the string value is multiplied by 10^(-scale).
func stringToInt128(s string, scale int32) [16]byte {
	var result [16]byte

	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		// Try parsing as float string (e.g., "123.456")
		negative, digits, exp := parseDecimalString(s)
		if len(digits) == 0 {
			return result
		}
		v = new(big.Int)
		for _, d := range digits {
			v.Mul(v, big.NewInt(10))
			v.Add(v, big.NewInt(int64(d)))
		}
		if exp > 0 {
			pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)
			v.Mul(v, pow)
		} else if exp < 0 {
			// Digits after decimal: these are already incorporated in v,
			// but we need to account for scale separately
			pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-exp)), nil)
			v.Div(v, pow)
		}
		if negative {
			v.Neg(v)
		}
	}

	if scale < 0 {
		pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil)
		v.Mul(v, pow)
	}

	if v.Sign() >= 0 {
		b := v.Bytes()
		if len(b) > 16 {
			b = b[len(b)-16:]
		}
		copy(result[16-len(b):], b)
	} else {
		// Two's complement: add 2^128
		twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
		v.Add(v, twoTo128)
		b := v.Bytes()
		if len(b) > 16 {
			b = b[len(b)-16:]
		}
		copy(result[16-len(b):], b)
	}
	return result
}

// tzOffsetToID converts a timezone offset in seconds to a Firebird timezone ID.
// Firebird stores offset-based timezones as: offset_minutes + 1439.
func tzOffsetToID(offsetSeconds int) uint32 {
	offsetMin := offsetSeconds / 60
	return uint32(offsetMin + 1439)
}
