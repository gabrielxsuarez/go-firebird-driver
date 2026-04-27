package wire

import (
	"fmt"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// TextCodec converts between Firebird single-byte/legacy encodings and Go UTF-8.
// A nil codec means the connection is already UTF-8-compatible and uses the
// existing zero-overhead string conversion path.
type TextCodec struct {
	name string
	enc  encoding.Encoding
}

// NewTextCodec returns the conversion codec for connection text bytes.
// clientEncoding, when set, overrides charset for Go-side byte conversion while
// leaving charset available for the Firebird lc_ctype negotiation.
func NewTextCodec(charset, clientEncoding string) (*TextCodec, error) {
	name := normalizeEncodingName(clientEncoding)
	explicit := name != ""
	if name == "" {
		name = normalizeEncodingName(charset)
	}
	if name == "" || isPassthroughEncoding(name) {
		return nil, nil
	}
	enc := charsetEncoding(name)
	if enc == nil {
		if explicit {
			return nil, fmt.Errorf("firebird: unsupported client encoding %q", clientEncoding)
		}
		return nil, nil
	}
	return &TextCodec{name: name, enc: enc}, nil
}

func normalizeEncodingName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ToUpper(name)
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

func isPassthroughEncoding(name string) bool {
	switch name {
	case "UTF8", "UTF_8", "UNICODE_FSS", "NONE", "OCTETS", "ASCII":
		return true
	default:
		return false
	}
}

func charsetEncoding(name string) encoding.Encoding {
	switch name {
	case "ISO8859_1":
		return charmap.ISO8859_1
	case "ISO8859_2":
		return charmap.ISO8859_2
	case "ISO8859_3":
		return charmap.ISO8859_3
	case "ISO8859_4":
		return charmap.ISO8859_4
	case "ISO8859_5":
		return charmap.ISO8859_5
	case "ISO8859_6":
		return charmap.ISO8859_6
	case "ISO8859_7":
		return charmap.ISO8859_7
	case "ISO8859_8":
		return charmap.ISO8859_8
	case "ISO8859_9":
		return charmap.ISO8859_9
	case "ISO8859_13":
		return charmap.ISO8859_13
	case "WIN1250":
		return charmap.Windows1250
	case "WIN1251":
		return charmap.Windows1251
	case "WIN1252":
		return charmap.Windows1252
	case "WIN1253":
		return charmap.Windows1253
	case "WIN1254":
		return charmap.Windows1254
	case "WIN1255":
		return charmap.Windows1255
	case "WIN1256":
		return charmap.Windows1256
	case "WIN1257":
		return charmap.Windows1257
	case "WIN1258":
		return charmap.Windows1258
	case "DOS437":
		return charmap.CodePage437
	case "DOS850":
		return charmap.CodePage850
	case "DOS852":
		return charmap.CodePage852
	case "DOS855", "CYRL":
		return charmap.CodePage855
	case "DOS858":
		return charmap.CodePage858
	case "DOS860":
		return charmap.CodePage860
	case "DOS862":
		return charmap.CodePage862
	case "DOS863":
		return charmap.CodePage863
	case "DOS865":
		return charmap.CodePage865
	case "DOS866":
		return charmap.CodePage866
	case "KOI8R":
		return charmap.KOI8R
	case "KOI8U":
		return charmap.KOI8U
	case "SJIS_0208", "CP943C":
		return japanese.ShiftJIS
	case "EUCJ_0208":
		return japanese.EUCJP
	case "KSC_5601":
		return korean.EUCKR
	case "BIG_5":
		return traditionalchinese.Big5
	case "GB_2312":
		return simplifiedchinese.HZGB2312
	case "GBK":
		return simplifiedchinese.GBK
	case "GB18030":
		return simplifiedchinese.GB18030
	default:
		return nil
	}
}

func allASCIIBytes(data []byte) bool {
	for _, b := range data {
		if b >= 0x80 {
			return false
		}
	}
	return true
}

func allASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Decode converts encoded Firebird bytes to a Go UTF-8 string.
func (c *TextCodec) Decode(data []byte) string {
	if c == nil || len(data) == 0 || allASCIIBytes(data) {
		return string(data)
	}
	v, err := c.enc.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(v)
}

// Encode converts a Go UTF-8 string to the configured Firebird byte encoding.
func (c *TextCodec) Encode(s string) (string, error) {
	if c == nil || s == "" || allASCIIString(s) {
		return s, nil
	}
	return c.enc.NewEncoder().String(s)
}

func decodeText(codec *TextCodec, data []byte) string {
	if codec == nil {
		return string(data)
	}
	return codec.Decode(data)
}

func encodeText(codec *TextCodec, s string) (string, error) {
	if codec == nil {
		return s, nil
	}
	return codec.Encode(s)
}
