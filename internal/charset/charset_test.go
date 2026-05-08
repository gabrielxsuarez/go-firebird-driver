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
