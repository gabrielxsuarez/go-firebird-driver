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
		{"UTF8", 4},
		{"ISO8859_1", 21},
		{"unknown_charset", 0}, // unknown returns 0 (NONE)
	}
	for _, tt := range tests {
		got := CharsetID(tt.name)
		if got != tt.want {
			t.Errorf("CharsetID(%q) = %d, want %d", tt.name, got, tt.want)
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
