package charset

import "testing"

func TestCharsetName(t *testing.T) {
	tests := []struct {
		id   int32
		want string
	}{
		{0, "NONE"},
		{4, "UTF8"},
		{21, "ISO8859_1"},
		{3, "UNICODE_FSS"},
		{-1, "NONE"}, // unknown returns NONE
		{9999, "NONE"},
	}
	for _, tt := range tests {
		got := CharsetName(tt.id)
		if got != tt.want {
			t.Errorf("CharsetName(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestCharsetID(t *testing.T) {
	tests := []struct {
		name string
		want int32
	}{
		{"NONE", 0},
		{" none ", 0},
		{"UTF8", 4},
		{"utf8", 4},
		{"UTF-8", 4},
		{"ISO8859_1", 21},
		{"iso-8859-1", 21},
		{"latin1", 21},
		{"windows-1252", 53},
		{"cp1251", 52},
		{"unknown_charset", 0}, // unknown returns 0 (NONE)
	}
	for _, tt := range tests {
		got := CharsetID(tt.name)
		if got != tt.want {
			t.Errorf("CharsetID(%q) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestCanonicalName(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"utf-8", "UTF8", true},
		{"cp1251", "WIN1251", true},
		{"windows-1252", "WIN1252", true},
		{" none ", "NONE", true},
		{"unknown_charset", "unknown_charset", false},
	}
	for _, tt := range tests {
		got, ok := CanonicalName(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Errorf("CanonicalName(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCharsetRoundTrip(t *testing.T) {
	// All known charsets should round-trip: ID -> Name -> ID
	for id, name := range charsets {
		gotID := CharsetID(name)
		if gotID != id {
			t.Errorf("roundtrip failed: ID %d -> %q -> ID %d", id, name, gotID)
		}
	}
}

func TestDecodeISO88591(t *testing.T) {
	got := Decode(IDISO88591, []byte{'V', 'A', 'R', 'T', 'A', 0xA0, 'C', 'R'})
	want := "VARTA\u00a0CR"
	if got != want {
		t.Fatalf("Decode(ISO8859_1) = %q, want %q", got, want)
	}
}

func TestEncodeISO88591(t *testing.T) {
	got, err := Encode(IDISO88591, "VARTA\u00a0CR")
	if err != nil {
		t.Fatalf("Encode(ISO8859_1): %v", err)
	}
	want := "VARTA\xa0CR"
	if got != want {
		t.Fatalf("Encode(ISO8859_1) = % x, want % x", got, want)
	}
}

func TestEncodeISO88591RejectsUnsupportedRune(t *testing.T) {
	if _, err := Encode(IDISO88591, "precio €"); err == nil {
		t.Fatal("Encode(ISO8859_1) error = nil, want unsupported rune error")
	}
}

func TestEncodeASCIIRejectsUnsupportedRune(t *testing.T) {
	if _, err := Encode(IDASCII, "cafe\u00e9"); err == nil {
		t.Fatal("Encode(ASCII) error = nil, want unsupported rune error")
	}
}

func TestDecodeASCIIReplacesInvalidBytes(t *testing.T) {
	got := Decode(IDASCII, []byte{'A', 0xE1, 'B'})
	want := "A\uFFFDB"
	if got != want {
		t.Fatalf("Decode(ASCII) = %q, want %q", got, want)
	}
}

func TestEncodeDecodeWindows1251(t *testing.T) {
	want := "\u041f\u0440\u0438\u0432\u0435\u0442"
	encoded, err := Encode(52, want)
	if err != nil {
		t.Fatalf("Encode(WIN1251): %v", err)
	}
	if got := []byte(encoded); string(got) != "\xcf\xf0\xe8\xe2\xe5\xf2" {
		t.Fatalf("Encode(WIN1251) = % x", got)
	}
	if got := Decode(52, []byte(encoded)); got != want {
		t.Fatalf("Decode(WIN1251) = %q, want %q", got, want)
	}
}

func TestEncodeDecodeWindows1252Euro(t *testing.T) {
	want := "precio 10\u20ac"
	encoded, err := Encode(53, want)
	if err != nil {
		t.Fatalf("Encode(WIN1252): %v", err)
	}
	if got := []byte(encoded); got[len(got)-1] != 0x80 {
		t.Fatalf("Encode(WIN1252) = % x, want trailing 80", got)
	}
	if got := Decode(53, []byte(encoded)); got != want {
		t.Fatalf("Decode(WIN1252) = %q, want %q", got, want)
	}
}

func TestSupportedTranscodersRepresentativeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		id   int32
		text string
	}{
		{"ASCII", IDASCII, "plain ascii"},
		{"ISO8859_1", IDISO88591, "VARTA\u00a0CR"},
		{"ISO8859_2", 22, "Zazolc gesla jazn"},
		{"ISO8859_5", 35, "\u041f\u0440\u0438\u0432\u0435\u0442"},
		{"ISO8859_7", 37, "\u039a\u03b1\u03bb\u03b7\u03bc\u03ad\u03c1\u03b1"},
		{"WIN1250", 51, "Zazolc gesla jazn"},
		{"WIN1251", 52, "\u041f\u0440\u0438\u0432\u0435\u0442"},
		{"WIN1252", 53, "precio 10\u20ac"},
		{"WIN1253", 54, "\u039a\u03b1\u03bb\u03b7\u03bc\u03ad\u03c1\u03b1"},
		{"WIN1254", 55, "\u0130stanbul \u011f\u00fc\u015f\u00f6\u00e7"},
		{"WIN1255", 58, "\u05e9\u05dc\u05d5\u05dd"},
		{"WIN1256", 59, "\u0633\u0644\u0627\u0645"},
		{"WIN1257", 60, "\u0105\u010d\u0119\u0117\u012f\u0161\u0173\u016b\u017e"},
		{"KOI8R", 63, "\u041f\u0440\u0438\u0432\u0435\u0442"},
		{"KOI8U", 64, "\u041f\u0440\u0438\u0432\u0456\u0442"},
		{"WIN1258", 65, "Tieng Viet"},
		{"GBK", 67, "\u4e2d\u6587"},
		{"GB18030", 69, "\u4e2d\u6587"},
		{"GB_2312", 57, "\u4e2d\u6587"},
		{"DOS437", 10, "Caf\u00e9 se\u00f1or"},
		{"DOS850", 11, "\u00d1and\u00fa \u00e1cido"},
		{"DOS852", 45, "Z\u0119za \u017c\u00f3\u0142\u0107"},
		{"DOS858", 16, "precio 10\u20ac"},
		{"DOS860", 13, "cora\u00e7\u00e3o"},
		{"DOS862", 17, "\u05e9\u05dc\u05d5\u05dd"},
		{"DOS863", 14, "Caf\u00e9 \u00eatre"},
		{"DOS865", 12, "K\u00f8benhavn \u00e6\u00f8"},
		{"DOS866", 48, "\u041f\u0440\u0438\u0432\u0435\u0442"},
		{"TIS620", 66, "\u0e2a\u0e27\u0e31\u0e2a\u0e14\u0e35"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := Encode(tt.id, tt.text)
			if err != nil {
				t.Fatalf("Encode(%s): %v", tt.name, err)
			}
			if got := Decode(tt.id, []byte(encoded)); got != tt.text {
				t.Fatalf("Decode(%s) = %q, want %q", tt.name, got, tt.text)
			}
		})
	}
}

func TestUnsupportedLegacyCharsetsPassThrough(t *testing.T) {
	tests := []struct {
		name string
		id   int32
	}{
		{"DOS737", 9},
		{"DOS775", 15},
		{"DOS864", 18},
		{"DOS857", 46},
		{"DOS861", 47},
		{"DOS869", 49},
		{"NEXT", 19},
		{"CYRL", 50},
		{"CP943C", 68},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const value = "abc"
			encoded, err := Encode(tt.id, value)
			if err != nil {
				t.Fatalf("Encode(%s): %v", tt.name, err)
			}
			if encoded != value {
				t.Fatalf("Encode(%s) = %q, want passthrough %q", tt.name, encoded, value)
			}
			if got := Decode(tt.id, []byte(encoded)); got != value {
				t.Fatalf("Decode(%s) = %q, want passthrough %q", tt.name, got, value)
			}
		})
	}
}
