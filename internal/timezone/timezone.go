// Package timezone provides Firebird timezone ID to name mappings.
package timezone

import (
	"sync"
	"time"
)

// Firebird timezone encoding:
//   - 0..2878: offset in minutes, where offset = value - 1439
//   - 64898..65535: named timezone ID from RDB$TIME_ZONES table

const (
	offsetBase = 1439
)

var (
	locationCache sync.Map // map[uint32]*time.Location

	// fastCache is a fixed-size array for the most common timezone values
	// (indices 0-2878 for offsets, plus a few named zones at the end).
	// This avoids sync.Map overhead for common cases.
	fastCache [2880]*time.Location
)

// Resolve converts a Firebird timezone value to a *time.Location.
func Resolve(tzValue uint32) *time.Location {
	// Fast path: offset timezones (0-2878) using fixed array cache
	if tzValue < 2880 {
		if loc := fastCache[tzValue]; loc != nil {
			return loc
		}
		return resolveOffsetFast(tzValue)
	}

	// Medium path: sync.Map cache for named zones
	if loc, ok := locationCache.Load(tzValue); ok {
		return loc.(*time.Location)
	}

	// Slow path: resolve and cache named timezone
	return resolveNamedSlow(tzValue)
}

// resolveOffsetFast handles offset timezones (0-2878) with minimal allocations.
func resolveOffsetFast(tzValue uint32) *time.Location {
	offsetMin := int(tzValue) - offsetBase
	loc := time.FixedZone(formatOffset(offsetMin), offsetMin*60)
	fastCache[tzValue] = loc
	return loc
}

// resolveNamedSlow handles named timezones with caching.
func resolveNamedSlow(tzValue uint32) *time.Location {
	var loc *time.Location
	if name := namedZoneName(tzValue); name == "" {
		loc = time.UTC
	} else {
		var err error
		loc, err = time.LoadLocation(name)
		if err != nil {
			loc = time.UTC
		}
	}
	locationCache.Store(tzValue, loc)
	return loc
}

// formatOffset formats a minute offset as "+HH:MM" or "-HH:MM".
// Uses a static lookup table for common offsets to avoid allocations.
func formatOffset(minutes int) string {
	// Fast path: common offset range (-12:00 to +14:00, i.e., -720 to +840 min)
	if minutes >= -720 && minutes <= 840 {
		idx := minutes + 720 // 0-1560 range
		if cached := offsetStrings[idx]; cached != "" {
			return cached
		}
	}

	// Slow path: format dynamically
	sign := '+'
	if minutes < 0 {
		sign = '-'
		minutes = -minutes
	}
	h := minutes / 60
	m := minutes % 60
	buf := make([]byte, 6)
	buf[0] = byte(sign)
	buf[1] = byte('0' + h/10)
	buf[2] = byte('0' + h%10)
	buf[3] = ':'
	buf[4] = byte('0' + m/10)
	buf[5] = byte('0' + m%10)
	return string(buf)
}

// offsetStrings caches formatted offset strings for the common range.
// Range: -720 (-12:00) to +840 (+14:00), total 1561 entries.
var offsetStrings [1561]string

func init() {
	// Pre-populate common offset strings to avoid allocations
	for min := -720; min <= 840; min++ {
		sign := '+'
		m := min
		if m < 0 {
			sign = '-'
			m = -m
		}
		h := m / 60
		mm := m % 60
		buf := [6]byte{}
		buf[0] = byte(sign)
		buf[1] = byte('0' + h/10)
		buf[2] = byte('0' + h%10)
		buf[3] = ':'
		buf[4] = byte('0' + mm/10)
		buf[5] = byte('0' + mm%10)
		offsetStrings[min+720] = string(buf[:])
	}
}
