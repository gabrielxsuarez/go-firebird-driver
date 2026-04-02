// Package charset provides Firebird charset ID to name mappings.
package charset

// CharsetName returns the IANA charset name for a Firebird charset ID.
func CharsetName(id int32) string {
	if name, ok := charsets[id]; ok {
		return name
	}
	return "NONE"
}

// CharsetID returns the Firebird charset ID for a charset name.
func CharsetID(name string) int32 {
	if id, ok := charsetByName[name]; ok {
		return id
	}
	return 0 // NONE
}

var charsets = map[int32]string{
	0:   "NONE",
	1:   "OCTETS",
	2:   "ASCII",
	3:   "UNICODE_FSS",
	4:   "UTF8",
	5:   "SJIS_0208",
	6:   "EUCJ_0208",
	9:   "DOS737",
	10:  "DOS437",
	11:  "DOS850",
	12:  "DOS865",
	13:  "DOS860",
	14:  "DOS863",
	15:  "DOS775",
	16:  "DOS858",
	17:  "DOS862",
	18:  "DOS864",
	19:  "NEXT",
	21:  "ISO8859_1",
	22:  "ISO8859_2",
	23:  "ISO8859_3",
	34:  "ISO8859_4",
	35:  "ISO8859_5",
	36:  "ISO8859_6",
	37:  "ISO8859_7",
	38:  "ISO8859_8",
	39:  "ISO8859_9",
	40:  "ISO8859_13",
	44:  "KSC_5601",
	45:  "DOS852",
	46:  "DOS857",
	47:  "DOS861",
	48:  "DOS866",
	49:  "DOS869",
	50:  "CYRL",
	51:  "WIN1250",
	52:  "WIN1251",
	53:  "WIN1252",
	54:  "WIN1253",
	55:  "WIN1254",
	56:  "BIG_5",
	57:  "GB_2312",
	58:  "WIN1255",
	59:  "WIN1256",
	60:  "WIN1257",
	63:  "KOI8R",
	64:  "KOI8U",
	65:  "WIN1258",
	66:  "TIS620",
	67:  "GBK",
	68:  "CP943C",
	69:  "GB18030",
}

var charsetByName = func() map[string]int32 {
	m := make(map[string]int32, len(charsets))
	for id, name := range charsets {
		m[name] = id
	}
	return m
}()
