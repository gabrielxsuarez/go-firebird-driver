// DECFLOAT(16/34): codificación IEEE 754-2008 decimal con dígitos DPD (FB4+).

package wire

import (
	"encoding/binary"
	"math/big"
	"strconv"
	"strings"
)

var (
	decfloat64DPDMask  = mustBigIntFromHex("3ffffffffffff")
	decfloat128DPDMask = mustBigIntFromHex("3fffffffffffffffffffffffffff")
)

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
