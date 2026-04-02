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
