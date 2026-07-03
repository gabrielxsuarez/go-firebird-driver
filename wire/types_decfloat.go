// DECFLOAT(16/34): codificación IEEE 754-2008 decimal con dígitos DPD (FB4+).

package wire

import (
	"encoding/binary"
	"fmt"
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
