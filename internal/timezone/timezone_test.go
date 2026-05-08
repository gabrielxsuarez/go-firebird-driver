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
	tests := []struct {
		tzValue uint32
		want    string
	}{
		{65361, "America/New_York"},
		{65069, "Europe/Madrid"},
		{64909, "UTC"},
		{65470, "America/Argentina/Buenos_Aires"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			loc := Resolve(tt.tzValue)
			if loc == nil {
				t.Fatalf("Resolve(%d) returned nil", tt.tzValue)
			}
			if got := loc.String(); got != tt.want {
				t.Fatalf("Resolve(%d) = %q, want %q", tt.tzValue, got, tt.want)
			}
		})
	}
}

func TestNamedZoneNameBounds(t *testing.T) {
	tests := []struct {
		tzValue uint32
		want    string
	}{
		{65535, "GMT"},
		{65361, "America/New_York"},
		{64898, "America/Coyhaique"},
		{64897, ""},
		{2879, ""},
	}
	for _, tt := range tests {
		if got := namedZoneName(tt.tzValue); got != tt.want {
			t.Errorf("namedZoneName(%d) = %q, want %q", tt.tzValue, got, tt.want)
		}
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
