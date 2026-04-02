package wire

import (
	"testing"
	"time"
)

func TestDateToMJD(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
		want int32
	}{
		{"MJD epoch", time.Date(1858, 11, 17, 0, 0, 0, 0, time.UTC), 0},
		{"Unix epoch", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 40587},
		{"2000-01-01", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), 51544},
		{"2024-06-15", time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC), 60476},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DateToMJD(tt.date)
			if got != tt.want {
				t.Errorf("DateToMJD(%v) = %d, want %d", tt.date, got, tt.want)
			}
		})
	}
}

func TestMJDToDate(t *testing.T) {
	tests := []struct {
		mjd  int32
		year int
		mon  time.Month
		day  int
	}{
		{0, 1858, time.November, 17},
		{40587, 1970, time.January, 1},
		{51544, 2000, time.January, 1},
	}
	for _, tt := range tests {
		got := MJDToDate(tt.mjd)
		if got.Year() != tt.year || got.Month() != tt.mon || got.Day() != tt.day {
			t.Errorf("MJDToDate(%d) = %v, want %04d-%02d-%02d",
				tt.mjd, got, tt.year, tt.mon, tt.day)
		}
	}
}

func TestDateRoundTrip(t *testing.T) {
	dates := []time.Time{
		time.Date(1858, 11, 17, 0, 0, 0, 0, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2000, 6, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	for _, d := range dates {
		mjd := DateToMJD(d)
		got := MJDToDate(mjd)
		if got.Year() != d.Year() || got.Month() != d.Month() || got.Day() != d.Day() {
			t.Errorf("roundtrip failed: %v -> %d -> %v", d, mjd, got)
		}
	}
}

func TestTimeToTicks(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want uint32
	}{
		{"midnight", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), 0},
		{"noon", time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC), 12 * 3600 * 10000},
		{"13:30:45", time.Date(0, 1, 1, 13, 30, 45, 0, time.UTC), (13*3600+30*60+45)*10000},
		{"with microseconds", time.Date(0, 1, 1, 0, 0, 1, 500000000, time.UTC), 1*10000 + 5000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeToTicks(tt.time)
			if got != tt.want {
				t.Errorf("TimeToTicks(%v) = %d, want %d", tt.time, got, tt.want)
			}
		})
	}
}

func TestTicksToTime(t *testing.T) {
	tests := []struct {
		ticks uint32
		h, m, s int
	}{
		{0, 0, 0, 0},
		{12 * 3600 * 10000, 12, 0, 0},
		{(13*3600 + 30*60 + 45) * 10000, 13, 30, 45},
	}
	for _, tt := range tests {
		got := TicksToTime(tt.ticks)
		if got.Hour() != tt.h || got.Minute() != tt.m || got.Second() != tt.s {
			t.Errorf("TicksToTime(%d) = %02d:%02d:%02d, want %02d:%02d:%02d",
				tt.ticks, got.Hour(), got.Minute(), got.Second(), tt.h, tt.m, tt.s)
		}
	}
}

func TestTimeRoundTrip(t *testing.T) {
	times := []time.Time{
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC),
		time.Date(0, 1, 1, 23, 59, 59, 0, time.UTC),
		time.Date(0, 1, 1, 8, 15, 30, 0, time.UTC),
	}
	for _, tm := range times {
		ticks := TimeToTicks(tm)
		got := TicksToTime(ticks)
		if got.Hour() != tm.Hour() || got.Minute() != tm.Minute() || got.Second() != tm.Second() {
			t.Errorf("roundtrip failed: %v -> %d -> %v", tm, ticks, got)
		}
	}
}

func TestTimestampToTime(t *testing.T) {
	// 2000-01-01 12:30:45 UTC
	mjd := DateToMJD(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	ticks := TimeToTicks(time.Date(0, 1, 1, 12, 30, 45, 0, time.UTC))

	got := TimestampToTime(mjd, ticks)
	want := time.Date(2000, 1, 1, 12, 30, 45, 0, time.UTC)

	if !got.Equal(want) {
		t.Errorf("TimestampToTime(%d, %d) = %v, want %v", mjd, ticks, got, want)
	}
}

func TestIOLength(t *testing.T) {
	tests := []struct {
		sqlType int32
		length  int32
		want    int
	}{
		{SQLShort, 0, -4},
		{SQLLong, 0, -4},
		{SQLFloat, 0, -4},
		{SQLDouble, 0, -8},
		{SQLInt64, 0, -8},
		{SQLTimestamp, 0, -8},
		{SQLBlob, 0, -8},
		{SQLText, 100, 101},
		{SQLVarying, 0, 0},
		{SQLBoolean, 0, 2},
		// nullable variants (type | 1)
		{SQLShort | 1, 0, -4},
		{SQLVarying | 1, 0, 0},
	}
	for _, tt := range tests {
		got := IOLength(tt.sqlType, tt.length)
		if got != tt.want {
			t.Errorf("IOLength(%d, %d) = %d, want %d", tt.sqlType, tt.length, got, tt.want)
		}
	}
}

func TestBuildBLR(t *testing.T) {
	descs := []ColumnDescriptor{
		{SQLType: SQLLong, Scale: 0, Length: 4},
		{SQLType: SQLVarying, SubType: 4, Length: 100},
	}

	blr := BuildBLR(descs)

	// Verify BLR structure
	if len(blr) < 6 {
		t.Fatalf("BLR too short: %d bytes", len(blr))
	}

	// Header: version5, begin, message, 0
	if blr[0] != BlrVersion5 {
		t.Errorf("blr[0] = %d, want %d (version5)", blr[0], BlrVersion5)
	}
	if blr[1] != BlrBegin {
		t.Errorf("blr[1] = %d, want %d (begin)", blr[1], BlrBegin)
	}
	if blr[2] != BlrMessage {
		t.Errorf("blr[2] = %d, want %d (message)", blr[2], BlrMessage)
	}

	// Parameter count: 2 columns * 2 = 4
	paramCount := int(blr[4]) | int(blr[5])<<8
	if paramCount != 4 {
		t.Errorf("param count = %d, want 4", paramCount)
	}

	// Trailer: end, eoc
	if blr[len(blr)-2] != BlrEnd {
		t.Errorf("blr[-2] = %d, want %d (end)", blr[len(blr)-2], BlrEnd)
	}
	if blr[len(blr)-1] != BlrEOC {
		t.Errorf("blr[-1] = %d, want %d (eoc)", blr[len(blr)-1], BlrEOC)
	}
}

func TestBuildBLRTypes(t *testing.T) {
	tests := []struct {
		name    string
		desc    ColumnDescriptor
		wantBLR byte
	}{
		{"short", ColumnDescriptor{SQLType: SQLShort}, BlrShort},
		{"long", ColumnDescriptor{SQLType: SQLLong}, BlrLong},
		{"int64", ColumnDescriptor{SQLType: SQLInt64}, BlrInt64},
		{"float", ColumnDescriptor{SQLType: SQLFloat}, BlrFloat},
		{"double", ColumnDescriptor{SQLType: SQLDouble}, BlrDouble},
		{"boolean", ColumnDescriptor{SQLType: SQLBoolean}, BlrBool},
		{"date", ColumnDescriptor{SQLType: SQLTypeDate}, BlrSQLDate},
		{"time", ColumnDescriptor{SQLType: SQLTypeTime}, BlrSQLTime},
		{"timestamp", ColumnDescriptor{SQLType: SQLTimestamp}, BlrTimestamp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blr := BuildBLR([]ColumnDescriptor{tt.desc})
			// BLR layout: [ver5][begin][msg][0][count_lo][count_hi] [TYPE] ...
			if len(blr) < 7 {
				t.Fatalf("BLR too short: %d bytes", len(blr))
			}
			typeByte := blr[6]
			if typeByte != tt.wantBLR {
				t.Errorf("BLR type byte = %d, want %d", typeByte, tt.wantBLR)
			}
		})
	}
}

func TestBuildBLREmpty(t *testing.T) {
	blr := BuildBLR(nil)
	// Should still have header + trailer
	if len(blr) < 6 {
		t.Fatalf("BLR for empty descs too short: %d bytes", len(blr))
	}
	paramCount := int(blr[4]) | int(blr[5])<<8
	if paramCount != 0 {
		t.Errorf("param count = %d, want 0", paramCount)
	}
}

func TestAppendBLRReusesBuffer(t *testing.T) {
	buf := make([]byte, 0, 256)
	descs := []ColumnDescriptor{
		{SQLType: SQLLong},
	}
	result := AppendBLR(buf, descs)
	if cap(result) != 256 {
		t.Errorf("AppendBLR allocated: cap went from 256 to %d", cap(result))
	}
}
