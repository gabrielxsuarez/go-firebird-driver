package wire

import (
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"
	"github.com/gabrielxsuarez/go-firebird-driver/internal/timezone"
)

const (
	minInt16 = -1 << 15
	maxInt16 = 1<<15 - 1
	minInt32 = -1 << 31
	maxInt32 = 1<<31 - 1
	minInt64 = -1 << 63
	maxInt64 = 1<<63 - 1
)

var errInvalidDecimal = errors.New("invalid decimal value")

var (
	decfloat64DPDMask  = mustBigIntFromHex("3ffffffffffff")
	decfloat128DPDMask = mustBigIntFromHex("3fffffffffffffffffffffffffff")
)

// Modified Julian Date epoch: November 17, 1858.
var mjdEpoch = time.Date(1858, 11, 17, 0, 0, 0, 0, time.UTC)

// Time units: 100 microseconds per tick.
const timeTicksPerSecond = 10000

// --- Date/Time conversions ---

// DateToMJD converts a time.Time to Modified Julian Date (days since epoch).
// Uses the wall-clock date (like TimeToTicks uses the wall clock) and civil
// arithmetic: time.Duration saturates at ±292 years, which corrupts dates
// beyond ~2150, and converting to UTC shifts the date across midnight for
// non-UTC locations.
func DateToMJD(t time.Time) int32 {
	y, m, d := t.Date()
	return int32(civilToUnixDays(y, int(m), d) + unixToMJDOffset)
}

// unixToMJDOffset is the number of days between the MJD epoch (1858-11-17)
// and the Unix epoch (1970-01-01).
const unixToMJDOffset = 40587

// civilToUnixDays converts a civil date to days since the Unix epoch
// (Howard Hinnant's days_from_civil algorithm; proleptic Gregorian).
func civilToUnixDays(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400 // [0, 399]
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := (153*mp+2)/5 + d - 1            // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy // [0, 146096]
	return int64(era)*146097 + int64(doe) - 719468
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

// TimestampTZExToTime converts date + time + timezone value + explicit offset
// to time.Time. The explicit offset is authoritative for the returned value, so
// a Go tzdata version mismatch cannot shift the decoded wall clock.
func TimestampTZExToTime(mjd int32, ticks uint32, tzValue uint32, offsetMinutes int32) time.Time {
	utcTime := TimestampToTime(mjd, ticks)
	loc := fixedLocationForTZ(tzValue, offsetMinutes)
	return utcTime.In(loc)
}

// TimeTZToTime converts UTC time ticks + timezone value to time.Time.
func TimeTZToTime(ticks uint32, tzValue uint32) time.Time {
	t := TicksToTime(ticks)
	loc := timezone.Resolve(tzValue)
	return t.In(loc)
}

// TimeTZExToTime converts UTC time ticks + timezone value + explicit offset to
// time.Time. TIME WITH TIME ZONE has no date, so using the explicit Firebird
// offset avoids historical IANA rules for year zero.
func TimeTZExToTime(ticks uint32, tzValue uint32, offsetMinutes int32) time.Time {
	t := TicksToTime(ticks)
	loc := fixedLocationForTZ(tzValue, offsetMinutes)
	return t.In(loc)
}

func fixedLocationForTZ(tzValue uint32, offsetMinutes int32) *time.Location {
	loc := timezone.Resolve(tzValue)
	name := loc.String()
	if name == "UTC" && offsetMinutes != 0 {
		name = offsetLocationName(offsetMinutes)
	}
	return time.FixedZone(name, int(offsetMinutes)*60)
}

func timezoneParts(t time.Time) (utc time.Time, offsetSeconds int, offsetMinutes int32) {
	_, offsetSeconds = t.Zone()
	return t.UTC(), offsetSeconds, int32(offsetSeconds / 60)
}

func offsetLocationName(minutes int32) string {
	sign := byte('+')
	if minutes < 0 {
		sign = '-'
		minutes = -minutes
	}
	h := minutes / 60
	m := minutes % 60
	buf := [6]byte{sign, byte('0' + h/10), byte('0' + h%10), ':', byte('0' + m/10), byte('0' + m%10)}
	return string(buf[:])
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

	case SQLTimeTZ, SQLTimeTZEx:
		ticks := r.ReadUInt32()
		tz := r.ReadUInt32()
		offsetMinutes := r.ReadInt32()
		return TimeTZExToTime(ticks, tz, offsetMinutes)

	case SQLTimestampTZ, SQLTimestampTZEx:
		mjd := r.ReadInt32()
		ticks := r.ReadUInt32()
		tz := r.ReadUInt32()
		offsetMinutes := r.ReadInt32()
		return TimestampTZExToTime(mjd, ticks, tz, offsetMinutes)

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
		if desc.SubType == fbcharset.IDOctets {
			out := make([]byte, len(result))
			copy(out, result)
			return out
		}
		result = trimRightSpaces(result)
		return fbcharset.Decode(desc.SubType, result)

	case SQLVarying:
		data := r.ReadBuffer()
		if r.Err() != nil {
			return ""
		}
		if desc.SubType == fbcharset.IDOctets {
			out := make([]byte, len(data))
			copy(out, data)
			return out
		}
		return fbcharset.Decode(desc.SubType, data)

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

// DecodeRow decodes one protocol 13+ row using the supplied descriptors.
func DecodeRow(r *Reader, descs []ColumnDescriptor, nullBuf []byte, row []any) error {
	if len(row) < len(descs) {
		return fmt.Errorf("row buffer has %d columns, need %d", len(row), len(descs))
	}

	byteCount := (len(descs) + 7) / 8
	padded := (byteCount + 3) & ^3
	if len(nullBuf) < padded {
		nullBuf = make([]byte, padded)
	} else {
		nullBuf = nullBuf[:padded]
	}

	readNullBitsetInto(r, len(descs), nullBuf)
	bitset := nullBuf[:byteCount]
	for i := range descs {
		if IsNull(bitset, i) {
			row[i] = nil
		} else {
			row[i] = DecodeColumn(r, &descs[i])
		}
	}
	if r.Err() != nil {
		return r.Err()
	}
	return nil
}

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

// EncodeNamedParamsStack encodes parameters using a StackWriter (stack-allocated buffer).
// This avoids allocation and sync.Pool overhead for small parameter sets.
func EncodeNamedParamsStack(w *StackWriter, descs []ColumnDescriptor, values []driver.NamedValue) {
	_ = EncodeNamedParamsStackErr(w, descs, values)
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
		s, err := fbcharset.Encode(desc.SubType, v)
		if err != nil {
			return err
		}
		data = s
	case []byte:
		raw = v
	default:
		s, err := fbcharset.Encode(desc.SubType, toString(value))
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
			return fmt.Errorf("text parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(desc.SubType), len(data), len(dst))
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
		return fmt.Errorf("varying parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(desc.SubType), len(data), desc.Length)
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
		return fmt.Errorf("varying parameter too long for charset %s: encoded length %d exceeds field length %d", fbcharset.CharsetName(desc.SubType), len(data), desc.Length)
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
			s, err := fbcharset.Encode(desc.SubType, v)
			if err != nil {
				return err
			}
			return writeVaryingParamStack(w, desc, []byte(s))
		case []byte:
			return writeVaryingParamStack(w, desc, v)
		default:
			s, err := fbcharset.Encode(desc.SubType, toString(value))
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
			s, err := fbcharset.Encode(desc.SubType, v)
			if err != nil {
				return err
			}
			return writeVaryingParam(w, desc, []byte(s))
		case []byte:
			return writeVaryingParam(w, desc, v)
		default:
			s, err := fbcharset.Encode(desc.SubType, toString(value))
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

func numericInt64(v any, scale int32) (int64, error) {
	switch x := v.(type) {
	case int:
		return scaleIntegralInt64(int64(x), scale)
	case int8:
		return scaleIntegralInt64(int64(x), scale)
	case int16:
		return scaleIntegralInt64(int64(x), scale)
	case int32:
		return scaleIntegralInt64(int64(x), scale)
	case int64:
		return scaleIntegralInt64(x, scale)
	case uint:
		if uint64(x) > uint64(maxInt64) {
			return 0, numericOverflow(v, scale)
		}
		return scaleIntegralInt64(int64(x), scale)
	case uint8:
		return scaleIntegralInt64(int64(x), scale)
	case uint16:
		return scaleIntegralInt64(int64(x), scale)
	case uint32:
		return scaleIntegralInt64(int64(x), scale)
	case uint64:
		if x > uint64(maxInt64) {
			return 0, numericOverflow(v, scale)
		}
		return scaleIntegralInt64(int64(x), scale)
	case float32:
		return decimalStringToScaledInt64(strconv.FormatFloat(float64(x), 'f', -1, 32), scale)
	case float64:
		return decimalStringToScaledInt64(strconv.FormatFloat(x, 'f', -1, 64), scale)
	case bool:
		if x {
			return scaleIntegralInt64(1, scale)
		}
		return 0, nil
	case string:
		return decimalStringToScaledInt64(x, scale)
	case []byte:
		return decimalStringToScaledInt64(string(x), scale)
	default:
		return 0, fmt.Errorf("firebird: cannot convert %T to numeric", v)
	}
}

func scaleIntegralInt64(v int64, scale int32) (int64, error) {
	if scale >= 0 {
		return v, nil
	}
	for i := int32(0); i > scale; i-- {
		if v > maxInt64/10 || v < minInt64/10 {
			return 0, numericOverflow(v, scale)
		}
		v *= 10
	}
	return v, nil
}

func decimalStringToScaledInt64(s string, scale int32) (int64, error) {
	v, err := decimalStringToScaledBigInt(s, scale)
	if err != nil {
		return 0, err
	}
	if !v.IsInt64() {
		return 0, numericOverflow(s, scale)
	}
	return v.Int64(), nil
}

func decimalStringToScaledBigInt(s string, scale int32) (*big.Int, error) {
	negative, digits, exp, err := parseDecimalStringStrict(s)
	if err != nil {
		return nil, err
	}

	coeff := new(big.Int)
	if _, ok := coeff.SetString(string(digits), 10); !ok {
		return nil, errInvalidDecimal
	}

	shift := exp - int(scale)
	if shift > 128 && coeff.Sign() != 0 {
		return nil, numericOverflow(s, scale)
	}

	if shift >= 0 {
		coeff.Mul(coeff, pow10Big(shift))
	} else {
		divisor := pow10Big(-shift)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(coeff, divisor, remainder)

		// Round half away from zero, matching common numeric conversion semantics.
		if new(big.Int).Lsh(remainder, 1).Cmp(divisor) >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		}
		coeff = quotient
	}

	if negative && coeff.Sign() != 0 {
		coeff.Neg(coeff)
	}
	return coeff, nil
}

func parseDecimalStringStrict(s string) (negative bool, digits []byte, exp int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return false, nil, 0, errInvalidDecimal
	}

	i := 0
	if s[i] == '-' {
		negative = true
		i++
	} else if s[i] == '+' {
		i++
	}
	if i == len(s) {
		return false, nil, 0, errInvalidDecimal
	}

	fracDigits := 0
	seenDot := false
	for i < len(s) {
		ch := s[i]
		if ch >= '0' && ch <= '9' {
			digits = append(digits, ch)
			if seenDot {
				fracDigits++
			}
			i++
			continue
		}
		if ch == '.' {
			if seenDot {
				return false, nil, 0, errInvalidDecimal
			}
			seenDot = true
			i++
			continue
		}
		break
	}
	if len(digits) == 0 {
		return false, nil, 0, errInvalidDecimal
	}

	exp = -fracDigits
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		expStart := i
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		digitStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if digitStart == i {
			return false, nil, 0, errInvalidDecimal
		}
		exponent, parseErr := strconv.Atoi(s[expStart:i])
		if parseErr != nil {
			return false, nil, 0, parseErr
		}
		exp += exponent
	}
	if i != len(s) {
		return false, nil, 0, errInvalidDecimal
	}

	firstNonZero := 0
	for firstNonZero < len(digits)-1 && digits[firstNonZero] == '0' {
		firstNonZero++
	}
	digits = digits[firstNonZero:]
	return negative, digits, exp, nil
}

func pow10Big(n int) *big.Int {
	if n <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func numericOverflow(value any, scale int32) error {
	return fmt.Errorf("firebird: numeric value %v overflows scale %d", value, scale)
}

// floatValue converts a parameter value to float64, rejecting values that
// would otherwise be silently encoded as 0.
func floatValue(v any) (float64, error) {
	switch x := v.(type) {
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, fmt.Errorf("firebird: cannot convert string %q to FLOAT/DOUBLE parameter", x)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("firebird: unsupported type %T for FLOAT/DOUBLE parameter", v)
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

// timeValue converts a parameter value to time.Time, rejecting values that
// would otherwise be silently encoded as the zero time.
func timeValue(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	default:
		return time.Time{}, fmt.Errorf("firebird: unsupported type %T for DATE/TIME/TIMESTAMP parameter", v)
	}
}

// boolWireValue returns the XDR wire encoding of a BOOLEAN parameter:
// the significant byte goes first, followed by 3 bytes of padding.
func boolWireValue(v bool) uint32 {
	if v {
		return 1 << 24
	}
	return 0
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
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], bits)
	return decfloat64BytesToString(data[:])
}

// decfloat128ToString converts a decimal128 value to string.
func decfloat128ToString(hi, lo uint64) string {
	var data [16]byte
	binary.BigEndian.PutUint64(data[:8], hi)
	binary.BigEndian.PutUint64(data[8:], lo)
	return decfloat128BytesToString(data[:])
}

func decfloat64BytesToString(data []byte) string {
	if len(data) < 8 {
		return ""
	}
	sign := data[0]&0x80 != 0
	cf := (uint32(data[0]) >> 2) & 0x1f
	exponent := ((int32(data[0]) & 3) << 6) + ((int32(data[1]) >> 2) & 0x3f)

	dpdBits := new(big.Int).SetBytes(data[:8])
	dpdBits.And(dpdBits, decfloat64DPDMask)

	var prefix int64
	switch {
	case cf == 0x1f:
		return "NaN"
	case cf == 0x1e:
		if sign {
			return "-Infinity"
		}
		return "Infinity"
	case cf&0x18 == 0x00:
		prefix = int64(cf & 0x07)
	case cf&0x18 == 0x08:
		exponent += 0x100
		prefix = int64(cf & 0x07)
	case cf&0x18 == 0x10:
		exponent += 0x200
		prefix = int64(cf & 0x07)
	case cf&0x1e == 0x18:
		prefix = int64(8 + cf&1)
	case cf&0x1e == 0x1a:
		exponent += 0x100
		prefix = int64(8 + cf&1)
	case cf&0x1e == 0x1c:
		exponent += 0x200
		prefix = int64(8 + cf&1)
	default:
		return ""
	}
	exponent -= 398

	digits, ok := calcDPDSignificand(prefix, dpdBits, 50)
	if !ok {
		return ""
	}
	return formatDecimalCoefficient(digits, exponent, sign)
}

func decfloat128BytesToString(data []byte) string {
	if len(data) < 16 {
		return ""
	}
	sign := data[0]&0x80 != 0
	cf := (uint32(data[0]&0x7f) << 10) + (uint32(data[1]) << 2) + uint32(data[2]>>6)

	var prefix int64
	var exponent int32
	switch {
	case cf&0x1f000 == 0x1f000:
		return "NaN"
	case cf&0x1f000 == 0x1e000:
		if sign {
			return "-Infinity"
		}
		return "Infinity"
	case cf&0x18000 == 0x00000:
		exponent = int32(cf & 0x00fff)
		prefix = int64((cf >> 12) & 0x07)
	case cf&0x18000 == 0x08000:
		exponent = 0x1000 + int32(cf&0x00fff)
		prefix = int64((cf >> 12) & 0x07)
	case cf&0x18000 == 0x10000:
		exponent = 0x2000 + int32(cf&0x00fff)
		prefix = int64((cf >> 12) & 0x07)
	case cf&0x1e000 == 0x18000:
		exponent = int32(cf & 0x00fff)
		prefix = int64(8 + (cf>>12)&0x01)
	case cf&0x1e000 == 0x1a000:
		exponent = 0x1000 + int32(cf&0x00fff)
		prefix = int64(8 + (cf>>12)&0x01)
	case cf&0x1e000 == 0x1c000:
		exponent = 0x2000 + int32(cf&0x00fff)
		prefix = int64(8 + (cf>>12)&0x01)
	default:
		return ""
	}
	exponent -= 6176

	dpdBits := new(big.Int).SetBytes(data[:16])
	dpdBits.And(dpdBits, decfloat128DPDMask)
	digits, ok := calcDPDSignificand(prefix, dpdBits, 110)
	if !ok {
		return ""
	}
	return formatDecimalCoefficient(digits, exponent, sign)
}

func calcDPDSignificand(prefix int64, dpdBits *big.Int, numBits int) (*big.Int, bool) {
	result := big.NewInt(prefix)
	chunk := new(big.Int)
	divisor := big.NewInt(1024)
	thousand := big.NewInt(1000)
	segments := numBits / 10
	values := make([]int64, segments)

	for i := segments - 1; i >= 0; i-- {
		chunk.Mod(dpdBits, divisor)
		v, ok := dpdToInt(uint32(chunk.Uint64()))
		if !ok {
			return nil, false
		}
		values[i] = v
		dpdBits.Rsh(dpdBits, 10)
	}
	for _, v := range values {
		result.Mul(result, thousand)
		result.Add(result, big.NewInt(v))
	}
	return result, true
}

func dpdToInt(dpd uint32) (int64, bool) {
	b0 := dpdBit(dpd, 0x0001)
	b1 := dpdBit(dpd, 0x0002)
	b2 := dpdBit(dpd, 0x0004)
	b3 := dpdBit(dpd, 0x0008)
	b4 := dpdBit(dpd, 0x0010)
	b5 := dpdBit(dpd, 0x0020)
	b6 := dpdBit(dpd, 0x0040)
	b7 := dpdBit(dpd, 0x0080)
	b8 := dpdBit(dpd, 0x0100)
	b9 := dpdBit(dpd, 0x0200)

	var d0, d1, d2 int
	switch {
	case b3 == 0:
		d2 = b9*4 + b8*2 + b7
		d1 = b6*4 + b5*2 + b4
		d0 = b2*4 + b1*2 + b0
	case b3 == 1 && b2 == 0 && b1 == 0:
		d2 = b9*4 + b8*2 + b7
		d1 = b6*4 + b5*2 + b4
		d0 = 8 + b0
	case b3 == 1 && b2 == 0 && b1 == 1:
		d2 = b9*4 + b8*2 + b7
		d1 = 8 + b4
		d0 = b6*4 + b5*2 + b0
	case b3 == 1 && b2 == 1 && b1 == 0:
		d2 = 8 + b7
		d1 = b6*4 + b5*2 + b4
		d0 = b9*4 + b8*2 + b0
	case b6 == 0 && b5 == 0 && b3 == 1 && b2 == 1 && b1 == 1:
		d2 = 8 + b7
		d1 = 8 + b4
		d0 = b9*4 + b8*2 + b0
	case b6 == 0 && b5 == 1 && b3 == 1 && b2 == 1 && b1 == 1:
		d2 = 8 + b7
		d1 = b9*4 + b8*2 + b4
		d0 = 8 + b0
	case b6 == 1 && b5 == 0 && b3 == 1 && b2 == 1 && b1 == 1:
		d2 = b9*4 + b8*2 + b7
		d1 = 8 + b4
		d0 = 8 + b0
	case b6 == 1 && b5 == 1 && b3 == 1 && b2 == 1 && b1 == 1:
		d2 = 8 + b7
		d1 = 8 + b4
		d0 = 8 + b0
	default:
		return 0, false
	}
	return int64(d2*100 + d1*10 + d0), true
}

func dpdBit(dpd, mask uint32) int {
	if dpd&mask != 0 {
		return 1
	}
	return 0
}

func mustBigIntFromHex(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("invalid hex big.Int constant")
	}
	return v
}

func formatDecimalCoefficient(digits *big.Int, exponent int32, negative bool) string {
	if digits.Sign() == 0 {
		if negative {
			return "-0"
		}
		return "0"
	}
	coeff := digits.String()
	adjExp := int(exponent) + len(coeff) - 1

	var buf []byte
	if negative {
		buf = append(buf, '-')
	}
	if adjExp < -6 || adjExp > len(coeff)+2 {
		buf = append(buf, coeff[0])
		if len(coeff) > 1 {
			buf = append(buf, '.')
			buf = append(buf, coeff[1:]...)
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
		buf = strconv.AppendInt(buf, int64(adjExp), 10)
		return string(buf)
	}

	dotPos := len(coeff) + int(exponent)
	switch {
	case dotPos <= 0:
		buf = append(buf, '0', '.')
		for range -dotPos {
			buf = append(buf, '0')
		}
		buf = append(buf, coeff...)
	case dotPos >= len(coeff):
		buf = append(buf, coeff...)
		for range dotPos - len(coeff) {
			buf = append(buf, '0')
		}
	default:
		buf = append(buf, coeff[:dotPos]...)
		buf = append(buf, '.')
		buf = append(buf, coeff[dotPos:]...)
	}
	result := string(buf)
	if strings.Contains(result, ".") {
		result = strings.TrimRight(result, "0")
		result = strings.TrimRight(result, ".")
	}
	return result
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

func valueToInt128(value any, scale int32) ([16]byte, error) {
	var s string
	switch v := value.(type) {
	case int:
		s = strconv.FormatInt(int64(v), 10)
	case int8:
		s = strconv.FormatInt(int64(v), 10)
	case int16:
		s = strconv.FormatInt(int64(v), 10)
	case int32:
		s = strconv.FormatInt(int64(v), 10)
	case int64:
		s = strconv.FormatInt(v, 10)
	case uint:
		s = strconv.FormatUint(uint64(v), 10)
	case uint8:
		s = strconv.FormatUint(uint64(v), 10)
	case uint16:
		s = strconv.FormatUint(uint64(v), 10)
	case uint32:
		s = strconv.FormatUint(uint64(v), 10)
	case uint64:
		s = strconv.FormatUint(v, 10)
	case float32:
		s = strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			s = "1"
		} else {
			s = "0"
		}
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return [16]byte{}, fmt.Errorf("firebird: cannot convert %T to INT128", value)
	}
	return stringToInt128Err(s, scale)
}

// stringToInt128 converts a string to 16 bytes big-endian two's complement INT128.
// If scale < 0, the string value is multiplied by 10^(-scale).
func stringToInt128(s string, scale int32) [16]byte {
	result, _ := stringToInt128Err(s, scale)
	return result
}

func stringToInt128Err(s string, scale int32) ([16]byte, error) {
	var result [16]byte

	v, err := decimalStringToScaledBigInt(s, scale)
	if err != nil {
		return result, err
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
	return result, nil
}

// tzOffsetToID converts a timezone offset in seconds to a Firebird timezone ID.
// Firebird stores offset-based timezones as: offset_minutes + 1439.
func tzOffsetToID(offsetSeconds int) uint32 {
	offsetMin := offsetSeconds / 60
	return uint32(offsetMin + 1439)
}
