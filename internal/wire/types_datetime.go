// Conversiones fecha/hora del wire protocol: MJD, ticks y timezones (spec cap. 14).

package wire

import (
	"time"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/timezone"
)

// Modified Julian Date epoch: November 17, 1858.
var mjdEpoch = time.Date(1858, 11, 17, 0, 0, 0, 0, time.UTC)

// Time units: 100 microseconds per tick.
const timeTicksPerSecond = 10000

// DateToMJD converts a time.Time to Modified Julian Date (days since epoch).
// Uses the wall-clock date (like TimeToTicks uses the wall clock) and civil
// arithmetic: time.Duration saturates at ±292 years, which corrupts dates
// beyond ~2150, and converting to UTC shifts the date across midnight for
// non-UTC locations.
func DateToMJD(t time.Time) int32 {
	y, m, d := t.Date()
	return int32(civilToUnixDays(y, int(m), d) + unixToMJDOffset)
}

// unixToMJDOffset is the number of days between the MJD epoch (1858-11-17)
// and the Unix epoch (1970-01-01).
const unixToMJDOffset = 40587

// civilToUnixDays converts a civil date to days since the Unix epoch
// (Howard Hinnant's days_from_civil algorithm; proleptic Gregorian).
func civilToUnixDays(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400 // [0, 399]
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := (153*mp+2)/5 + d - 1            // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy // [0, 146096]
	return int64(era)*146097 + int64(doe) - 719468
}

// MJDToDate converts a Modified Julian Date to time.Time.
func MJDToDate(mjd int32) time.Time {
	return mjdEpoch.AddDate(0, 0, int(mjd))
}

// TimeToTicks converts a time.Time to 100µs ticks since midnight.
func TimeToTicks(t time.Time) uint32 {
	h, m, s := t.Clock()
	ns := t.Nanosecond()
	ticks := (h*3600+m*60+s)*timeTicksPerSecond + ns/100000
	return uint32(ticks)
}

// TicksToTime converts 100µs ticks since midnight to time.Time (date = zero).
func TicksToTime(ticks uint32) time.Time {
	totalMicro := int64(ticks) * 100
	sec := totalMicro / 1_000_000
	ns := (totalMicro % 1_000_000) * 1000

	h := int(sec / 3600)
	m := int((sec % 3600) / 60)
	s := int(sec % 60)
	return time.Date(0, 1, 1, h, m, s, int(ns), time.UTC)
}

// TimestampToTime converts MJD date + ticks to time.Time.
func TimestampToTime(mjd int32, ticks uint32) time.Time {
	date := MJDToDate(mjd)
	t := TicksToTime(ticks)
	return time.Date(date.Year(), date.Month(), date.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// TimestampTZExToTime converts date + time + timezone value + explicit offset
// to time.Time. The explicit offset is authoritative for the returned value, so
// a Go tzdata version mismatch cannot shift the decoded wall clock.
func TimestampTZExToTime(mjd int32, ticks uint32, tzValue uint32, offsetMinutes int32) time.Time {
	utcTime := TimestampToTime(mjd, ticks)
	loc := fixedLocationForTZ(tzValue, offsetMinutes)
	return utcTime.In(loc)
}

// TimeTZExToTime converts UTC time ticks + timezone value + explicit offset to
// time.Time. TIME WITH TIME ZONE has no date, so using the explicit Firebird
// offset avoids historical IANA rules for year zero.
func TimeTZExToTime(ticks uint32, tzValue uint32, offsetMinutes int32) time.Time {
	t := TicksToTime(ticks)
	loc := fixedLocationForTZ(tzValue, offsetMinutes)
	return t.In(loc)
}

func fixedLocationForTZ(tzValue uint32, offsetMinutes int32) *time.Location {
	loc := timezone.Resolve(tzValue)
	name := loc.String()
	if name == "UTC" && offsetMinutes != 0 {
		name = offsetLocationName(offsetMinutes)
	}
	return time.FixedZone(name, int(offsetMinutes)*60)
}

func timezoneParts(t time.Time) (utc time.Time, offsetSeconds int, offsetMinutes int32) {
	_, offsetSeconds = t.Zone()
	return t.UTC(), offsetSeconds, int32(offsetSeconds / 60)
}

func offsetLocationName(minutes int32) string {
	sign := byte('+')
	if minutes < 0 {
		sign = '-'
		minutes = -minutes
	}
	h := minutes / 60
	m := minutes % 60
	buf := [6]byte{sign, byte('0' + h/10), byte('0' + h%10), ':', byte('0' + m/10), byte('0' + m%10)}
	return string(buf[:])
}

// tzOffsetToID converts a timezone offset in seconds to a Firebird timezone ID.
// Firebird stores offset-based timezones as: offset_minutes + 1439.
func tzOffsetToID(offsetSeconds int) uint32 {
	offsetMin := offsetSeconds / 60
	return uint32(offsetMin + 1439)
}
