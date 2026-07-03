// INT128 (FB4+): conversión entre valores Go y el formato wire de 16 bytes.

package wire

import (
	"fmt"
	"math/big"
	"strconv"
)

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
