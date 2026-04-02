package timezone

import (
	"testing"
	"time"
)

func TestResolveUTCOffset(t *testing.T) {
	// tzValue 1439 = offset 0 = UTC
	loc := Resolve(1439)
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != 0 {
		t.Errorf("Resolve(1439) offset = %d, want 0 (UTC)", offset)
	}
}

func TestResolvePositiveOffset(t *testing.T) {
	// tzValue 1499 = offset +60 minutes = +01:00
	loc := Resolve(1499)
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != 3600 {
		t.Errorf("Resolve(1499) offset = %d, want 3600 (+01:00)", offset)
	}
}

func TestResolveNegativeOffset(t *testing.T) {
	// tzValue 1139 = offset -300 minutes = -05:00
	loc := Resolve(1139)
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != -18000 {
		t.Errorf("Resolve(1139) offset = %d, want -18000 (-05:00)", offset)
	}
}

func TestResolveNamedZone(t *testing.T) {
	// Named zones start at 2879+. Find one that exists.
	// The namedZones map is unexported, but we can test with a known value
	// by checking that Resolve returns a non-nil *time.Location.
	loc := Resolve(65535) // likely unknown → should fall back to UTC
	if loc == nil {
		t.Fatal("Resolve(65535) returned nil")
	}
}

func TestResolveCaching(t *testing.T) {
	// Call twice, should return the same pointer (cached)
	loc1 := Resolve(1439) // UTC offset
	loc2 := Resolve(1439)
	if loc1 != loc2 {
		t.Error("Resolve(1439) returned different pointers, expected cached result")
	}
}

func TestResolveEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		tzValue uint32
		wantOff int // expected offset in seconds
	}{
		{"min offset", 0, -1439 * 60},   // tzValue 0 = -1439 minutes
		{"max offset", 2878, 1439 * 60}, // tzValue 2878 = +1439 minutes
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := Resolve(tt.tzValue)
			now := time.Now().In(loc)
			_, offset := now.Zone()
			if offset != tt.wantOff {
				t.Errorf("Resolve(%d) offset = %d, want %d", tt.tzValue, offset, tt.wantOff)
			}
		})
	}
}
