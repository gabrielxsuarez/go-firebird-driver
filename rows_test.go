package firebird

import "testing"

// Verifica el mapeo contra RDB$CHARACTER_SETS.RDB$BYTES_PER_CHARACTER.
func TestCharsetMaxBytesPerChar(t *testing.T) {
	tests := []struct {
		name string
		id   int32
		want int32
	}{
		{"NONE", 0, 1},
		{"OCTETS", 1, 1},
		{"ASCII", 2, 1},
		{"UNICODE_FSS", 3, 3},
		{"UTF8", 4, 4},
		{"SJIS_0208", 5, 2},
		{"EUCJ_0208", 6, 2},
		{"ISO8859_1", 21, 1},
		{"KSC_5601", 44, 2},
		{"WIN1252", 53, 1},
		{"BIG_5", 56, 2},
		{"GB_2312", 57, 2},
		{"KOI8R", 63, 1},
		{"KOI8U", 64, 1},
		{"TIS620", 66, 1},
		{"GBK", 67, 2},
		{"CP943C", 68, 2},
		{"GB18030", 69, 4},
	}
	for _, tt := range tests {
		if got := charsetMaxBytesPerChar(tt.id); got != tt.want {
			t.Errorf("charsetMaxBytesPerChar(%s=%d) = %d, want %d", tt.name, tt.id, got, tt.want)
		}
	}
}
