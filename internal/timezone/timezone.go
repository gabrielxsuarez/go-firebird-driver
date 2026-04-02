// Package timezone provides Firebird timezone ID to name mappings.
package timezone

import (
	"sync"
	"time"
)

// Firebird timezone encoding:
//   - 0..2878: offset in minutes, where offset = value - 1439
//   - 2879..65535: named timezone ID from RDB$TIME_ZONES table

const (
	offsetBase = 1439
	namedStart = 2879
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
	name, ok := namedZones[tzValue]
	var loc *time.Location
	if !ok {
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

// namedZones maps Firebird timezone IDs to IANA timezone names.
// This is a subset of RDB$TIME_ZONES; extends as needed.
var namedZones = map[uint32]string{
	65534: "GMT",
	65533: "ACT",
	65532: "AET",
	65531: "AGT",
	65530: "ART",
	65529: "AST",
	65528: "BET",
	65527: "BST",
	65526: "CAT",
	65525: "CNT",
	65524: "CST",
	65523: "CTT",
	65522: "EAT",
	65521: "ECT",
	65520: "IET",
	65519: "IST",
	65518: "JST",
	65517: "MIT",
	65516: "NET",
	65515: "NST",
	65514: "PLT",
	65513: "PNT",
	65512: "PRT",
	65511: "PST",
	65510: "SST",
	65509: "VST",
	// IANA zones
	65508: "Africa/Abidjan",
	65507: "Africa/Accra",
	65506: "Africa/Addis_Ababa",
	65505: "Africa/Algiers",
	65504: "Africa/Cairo",
	65503: "Africa/Casablanca",
	65502: "Africa/Johannesburg",
	65501: "Africa/Lagos",
	65500: "Africa/Nairobi",
	65499: "America/Anchorage",
	65498: "America/Argentina/Buenos_Aires",
	65497: "America/Bogota",
	65496: "America/Chicago",
	65495: "America/Denver",
	65494: "America/Halifax",
	65493: "America/Los_Angeles",
	65492: "America/Mexico_City",
	65491: "America/New_York",
	65490: "America/Phoenix",
	65489: "America/Santiago",
	65488: "America/Sao_Paulo",
	65487: "America/St_Johns",
	65486: "America/Toronto",
	65485: "America/Vancouver",
	65484: "Asia/Baghdad",
	65483: "Asia/Bangkok",
	65482: "Asia/Calcutta",
	65481: "Asia/Colombo",
	65480: "Asia/Dhaka",
	65479: "Asia/Dubai",
	65478: "Asia/Hong_Kong",
	65477: "Asia/Jakarta",
	65476: "Asia/Jerusalem",
	65475: "Asia/Karachi",
	65474: "Asia/Kolkata",
	65473: "Asia/Manila",
	65472: "Asia/Novosibirsk",
	65471: "Asia/Seoul",
	65470: "Asia/Shanghai",
	65469: "Asia/Singapore",
	65468: "Asia/Taipei",
	65467: "Asia/Tehran",
	65466: "Asia/Tokyo",
	65465: "Atlantic/Azores",
	65464: "Australia/Adelaide",
	65463: "Australia/Brisbane",
	65462: "Australia/Darwin",
	65461: "Australia/Hobart",
	65460: "Australia/Melbourne",
	65459: "Australia/Perth",
	65458: "Australia/Sydney",
	65457: "Europe/Amsterdam",
	65456: "Europe/Athens",
	65455: "Europe/Belgrade",
	65454: "Europe/Berlin",
	65453: "Europe/Brussels",
	65452: "Europe/Bucharest",
	65451: "Europe/Budapest",
	65450: "Europe/Copenhagen",
	65449: "Europe/Dublin",
	65448: "Europe/Helsinki",
	65447: "Europe/Istanbul",
	65446: "Europe/Kiev",
	65445: "Europe/Lisbon",
	65444: "Europe/London",
	65443: "Europe/Madrid",
	65442: "Europe/Moscow",
	65441: "Europe/Oslo",
	65440: "Europe/Paris",
	65439: "Europe/Prague",
	65438: "Europe/Rome",
	65437: "Europe/Stockholm",
	65436: "Europe/Vienna",
	65435: "Europe/Warsaw",
	65434: "Europe/Zurich",
	65433: "Pacific/Auckland",
	65432: "Pacific/Fiji",
	65431: "Pacific/Guam",
	65430: "Pacific/Honolulu",
	65429: "Pacific/Samoa",
	65428: "US/Alaska",
	65427: "US/Central",
	65426: "US/Eastern",
	65425: "US/Mountain",
	65424: "US/Pacific",
}
