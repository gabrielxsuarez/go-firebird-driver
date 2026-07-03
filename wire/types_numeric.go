// Conversiones numéricas y de valores Go: NUMERIC escalado, strings decimales.

package wire

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
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
	// Magnitud en uint64: -MinInt64 no existe en int64 (negarlo lo deja
	// negativo y el string salia con doble signo); en complemento a dos
	// la negacion uint64 da la magnitud correcta para todo el rango.
	u := uint64(v)
	if neg {
		u = -u
	}

	decPos := int(-scale)

	s := strconv.FormatUint(u, 10)
	if decPos >= len(s) {
		// Need leading zeros: e.g., 5 with scale -3 -> "0.005"
		return addNeg("0."+repeatZeros(decPos-len(s))+s, neg)
	}
	return addNeg(s[:len(s)-decPos]+"."+s[len(s)-decPos:], neg)
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
