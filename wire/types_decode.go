// Decodificación de filas: DecodeColumn/DecodeRow y NULL bitmap (spec cap. 14).

package wire

import (
	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"

	"encoding/binary"
	"fmt"
	"math"
	"math/big"
)

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
