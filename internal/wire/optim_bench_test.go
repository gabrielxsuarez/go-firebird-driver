package wire

import (
	"database/sql/driver"
	"testing"
)

// --- toString benchmarks ---

func BenchmarkToString_String(b *testing.B) {
	v := any("hello world")
	b.ReportAllocs()
	for range b.N {
		_ = toString(v)
	}
}

func BenchmarkToString_Int64(b *testing.B) {
	v := any(int64(1234567890))
	b.ReportAllocs()
	for range b.N {
		_ = toString(v)
	}
}

func BenchmarkToString_Int(b *testing.B) {
	v := any(int(42))
	b.ReportAllocs()
	for range b.N {
		_ = toString(v)
	}
}

func BenchmarkToString_Float64(b *testing.B) {
	v := any(float64(3.14159))
	b.ReportAllocs()
	for range b.N {
		_ = toString(v)
	}
}

func BenchmarkToString_Uint64(b *testing.B) {
	v := any(uint64(9876543210))
	b.ReportAllocs()
	for range b.N {
		_ = toString(v)
	}
}

// --- estimateValueSize benchmarks ---

func BenchmarkEstimateValueSize_Int(b *testing.B) {
	desc := &ColumnDescriptor{SQLType: SQLLong}
	v := any(int64(42))
	b.ReportAllocs()
	for range b.N {
		_ = estimateValueSize(desc, v)
	}
}

func BenchmarkEstimateValueSize_String(b *testing.B) {
	desc := &ColumnDescriptor{SQLType: SQLVarying}
	v := any("hello world")
	b.ReportAllocs()
	for range b.N {
		_ = estimateValueSize(desc, v)
	}
}

func BenchmarkEstimateValueSize_VaryingFloat(b *testing.B) {
	desc := &ColumnDescriptor{SQLType: SQLVarying}
	v := any(float64(3.14))
	b.ReportAllocs()
	for range b.N {
		_ = estimateValueSize(desc, v)
	}
}

// --- repeatZeros benchmarks ---

func BenchmarkRepeatZeros_3(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = repeatZeros(3)
	}
}

func BenchmarkRepeatZeros_10(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = repeatZeros(10)
	}
}

// --- scaledInt64 benchmarks ---

func BenchmarkScaledInt64_Simple(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = scaledInt64(12345, -2)
	}
}

func BenchmarkScaledInt64_LeadingZeros(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = scaledInt64(5, -3)
	}
}

// --- EncodeParamsOptimal benchmarks ---

func BenchmarkEncodeParamsOptimal_3Ints(b *testing.B) {
	descs := []ColumnDescriptor{
		{SQLType: SQLLong},
		{SQLType: SQLLong},
		{SQLType: SQLLong},
	}
	args := []driver.NamedValue{
		{Ordinal: 1, Value: int64(100)},
		{Ordinal: 2, Value: int64(200)},
		{Ordinal: 3, Value: int64(300)},
	}
	b.ReportAllocs()
	for range b.N {
		var sw StackWriter
		_ = EncodeParamsOptimal(&sw, descs, args)
	}
}

func BenchmarkEncodeParamsOptimal_MixedTypes(b *testing.B) {
	descs := []ColumnDescriptor{
		{SQLType: SQLVarying, Length: 100},
		{SQLType: SQLLong},
		{SQLType: SQLDouble},
		{SQLType: SQLVarying, Length: 50},
	}
	args := []driver.NamedValue{
		{Ordinal: 1, Value: "hello world"},
		{Ordinal: 2, Value: int64(42)},
		{Ordinal: 3, Value: float64(3.14)},
		{Ordinal: 4, Value: "test string"},
	}
	b.ReportAllocs()
	for range b.N {
		var sw StackWriter
		_ = EncodeParamsOptimal(&sw, descs, args)
	}
}

// --- StackWriter vs Writer int32 comparison ---

func BenchmarkStackWriterInt32(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var sw StackWriter
		sw.WriteInt32(42)
		sw.WriteInt32(100)
		sw.WriteInt32(200)
		_ = sw.Bytes()
	}
}

func BenchmarkStackWriterInt64(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		var sw StackWriter
		sw.WriteInt64(0x0102030405060708)
		sw.WriteInt64(0x090A0B0C0D0E0F10)
		_ = sw.Bytes()
	}
}
