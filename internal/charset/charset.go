// Package charset provides Firebird charset ID mappings and transcoding.
package charset

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

const (
	IDNone       int32 = 0
	IDOctets     int32 = 1
	IDASCII      int32 = 2
	IDUnicodeFSS int32 = 3
	IDUTF8       int32 = 4
	IDISO88591   int32 = 21
)

// CharsetName returns the IANA charset name for a Firebird charset ID.
func CharsetName(id int32) string {
	if name, ok := charsets[id]; ok {
		return name
	}
	return "NONE"
}

// CharsetID returns the Firebird charset ID for a charset name.
func CharsetID(name string) int32 {
	if id, ok := charsetByName[normalizeName(name)]; ok {
		return id
	}
	return 0 // NONE
}

// CanonicalName returns the Firebird charset name for a user-provided name or
// alias. The boolean is false when the name is unknown to this package.
func CanonicalName(name string) (string, bool) {
	if id, ok := charsetByName[normalizeName(name)]; ok {
		return CharsetName(id), true
	}
	return strings.TrimSpace(name), false
}

// Decode converts text bytes from a Firebird charset to a Go UTF-8 string.
func Decode(id int32, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	switch id {
	case IDNone, IDOctets, IDUnicodeFSS, IDUTF8:
		return string(data)
	case IDASCII:
		return decodeASCII(data)
	case IDISO88591:
		return decodeISO88591(data)
	}

	if enc := encodingForID(id); enc != nil {
		if out, err := enc.NewDecoder().Bytes(data); err == nil {
			return string(out)
		}
	}
	return string(data)
}

// Encode converts a Go UTF-8 string to the requested Firebird charset.
func Encode(id int32, s string) (string, error) {
	if len(s) == 0 {
		return s, nil
	}
	switch id {
	case IDNone, IDOctets, IDUnicodeFSS, IDUTF8:
		return s, nil
	case IDASCII:
		return encodeASCII(s)
	case IDISO88591:
		return encodeISO88591(s)
	}

	if enc := encodingForID(id); enc != nil {
		out, err := enc.NewEncoder().String(s)
		if err != nil {
			return "", fmt.Errorf("charset %s: %w", CharsetName(id), err)
		}
		return out, nil
	}
	return s, nil
}

func decodeASCII(data []byte) string {
	for _, b := range data {
		if b >= utf8.RuneSelf {
			return decodeASCIISlow(data)
		}
	}
	return string(data)
}

func decodeASCIISlow(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		if c < utf8.RuneSelf {
			b.WriteByte(c)
		} else {
			b.WriteRune(utf8.RuneError)
		}
	}
	return b.String()
}

func encodeASCII(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			r, _ := utf8.DecodeRuneInString(s[i:])
			return "", fmt.Errorf("charset ASCII cannot encode rune %U", r)
		}
	}
	return s, nil
}

func decodeISO88591(data []byte) string {
	for _, b := range data {
		if b >= utf8.RuneSelf {
			return decodeISO88591Slow(data)
		}
	}
	return string(data)
}

func decodeISO88591Slow(data []byte) string {
	extra := 0
	for _, b := range data {
		if b >= utf8.RuneSelf {
			extra++
		}
	}

	var b strings.Builder
	b.Grow(len(data) + extra)
	for _, c := range data {
		if c < utf8.RuneSelf {
			b.WriteByte(c)
		} else {
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

func encodeISO88591(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return encodeISO88591Slow(s)
		}
	}
	return s, nil
}

func encodeISO88591Slow(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > 0xFF {
			return "", fmt.Errorf("charset ISO8859_1 cannot encode rune %U", r)
		}
		b.WriteByte(byte(r))
	}
	return b.String(), nil
}

func encodingForID(id int32) encoding.Encoding {
	switch id {
	case 5: // SJIS_0208
		return japanese.ShiftJIS
	case 6: // EUCJ_0208
		return japanese.EUCJP
	case 22: // ISO8859_2
		return charmap.ISO8859_2
	case 23: // ISO8859_3
		return charmap.ISO8859_3
	case 34: // ISO8859_4
		return charmap.ISO8859_4
	case 35: // ISO8859_5
		return charmap.ISO8859_5
	case 36: // ISO8859_6
		return charmap.ISO8859_6
	case 37: // ISO8859_7
		return charmap.ISO8859_7
	case 38: // ISO8859_8
		return charmap.ISO8859_8
	case 39: // ISO8859_9
		return charmap.ISO8859_9
	case 40: // ISO8859_13
		return charmap.ISO8859_13
	case 44: // KSC_5601
		return korean.EUCKR
	case 51: // WIN1250
		return charmap.Windows1250
	case 52: // WIN1251
		return charmap.Windows1251
	case 53: // WIN1252
		return charmap.Windows1252
	case 54: // WIN1253
		return charmap.Windows1253
	case 55: // WIN1254
		return charmap.Windows1254
	case 56: // BIG_5
		return traditionalchinese.Big5
	case 57: // GB_2312
		return simplifiedchinese.HZGB2312
	case 58: // WIN1255
		return charmap.Windows1255
	case 59: // WIN1256
		return charmap.Windows1256
	case 60: // WIN1257
		return charmap.Windows1257
	case 63: // KOI8R
		return charmap.KOI8R
	case 64: // KOI8U
		return charmap.KOI8U
	case 65: // WIN1258
		return charmap.Windows1258
	case 67: // GBK
		return simplifiedchinese.GBK
	case 69: // GB18030
		return simplifiedchinese.GB18030
	default:
		return nil
	}
}

var charsets = map[int32]string{
	0:  "NONE",
	1:  "OCTETS",
	2:  "ASCII",
	3:  "UNICODE_FSS",
	4:  "UTF8",
	5:  "SJIS_0208",
	6:  "EUCJ_0208",
	9:  "DOS737",
	10: "DOS437",
	11: "DOS850",
	12: "DOS865",
	13: "DOS860",
	14: "DOS863",
	15: "DOS775",
	16: "DOS858",
	17: "DOS862",
	18: "DOS864",
	19: "NEXT",
	21: "ISO8859_1",
	22: "ISO8859_2",
	23: "ISO8859_3",
	34: "ISO8859_4",
	35: "ISO8859_5",
	36: "ISO8859_6",
	37: "ISO8859_7",
	38: "ISO8859_8",
	39: "ISO8859_9",
	40: "ISO8859_13",
	44: "KSC_5601",
	45: "DOS852",
	46: "DOS857",
	47: "DOS861",
	48: "DOS866",
	49: "DOS869",
	50: "CYRL",
	51: "WIN1250",
	52: "WIN1251",
	53: "WIN1252",
	54: "WIN1253",
	55: "WIN1254",
	56: "BIG_5",
	57: "GB_2312",
	58: "WIN1255",
	59: "WIN1256",
	60: "WIN1257",
	63: "KOI8R",
	64: "KOI8U",
	65: "WIN1258",
	66: "TIS620",
	67: "GBK",
	68: "CP943C",
	69: "GB18030",
}

var charsetByName = func() map[string]int32 {
	m := make(map[string]int32, len(charsets)+24)
	for id, name := range charsets {
		m[name] = id
	}
	m["UTF_8"] = IDUTF8
	m["ISO_8859_1"] = IDISO88591
	m["ISO88591"] = IDISO88591
	m["LATIN1"] = IDISO88591
	m["LATIN_1"] = IDISO88591
	for i := 0; i <= 8; i++ {
		name := fmt.Sprintf("WIN125%d", i)
		id, ok := m[name]
		if !ok {
			continue
		}
		m[fmt.Sprintf("CP125%d", i)] = id
		m[fmt.Sprintf("WINDOWS_125%d", i)] = id
	}
	return m
}()

func normalizeName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	return name
}
