package wire

// Benchmarks de decodificación de fila completa (NULL bitmap + columnas) con
// perfiles de fila típicos, y de construcción de DPB/TPB/BLR (camino de
// conexión). Complementan xdr_bench_test.go (que mide campos XDR sueltos).

import (
	"bytes"
	"testing"
	"time"
)

// mixedDescs5 imita una fila típica: INTEGER, VARCHAR(20), BIGINT, DOUBLE, TIMESTAMP.
func mixedDescs5() []ColumnDescriptor {
	return []ColumnDescriptor{
		{SQLType: SQLLong, Length: 4},
		{SQLType: SQLVarying, Length: 20}, // charset NONE
		{SQLType: SQLInt64, Length: 8},
		{SQLType: SQLDouble, Length: 8},
		{SQLType: SQLTimestamp, Length: 8},
	}
}

func mixedValues5() []any {
	return []any{
		int64(42),
		"hello world",
		int64(1234567890123),
		3.14159,
		time.Date(2026, 7, 3, 12, 30, 0, 0, time.UTC),
	}
}

// buildRowBytes codifica una fila en formato wire (mismo layout que envía el
// servidor: NULL bitmap + valores) usando el encoder de parámetros.
func buildRowBytes(b *testing.B, descs []ColumnDescriptor, values []any) []byte {
	b.Helper()
	w := NewWriter()
	if err := EncodeParamsErr(w, descs, values); err != nil {
		b.Fatalf("encode row: %v", err)
	}
	return append([]byte(nil), w.Bytes()...)
}

func benchDecodeRow(b *testing.B, descs []ColumnDescriptor, values []any) {
	b.Helper()
	data := buildRowBytes(b, descs, values)

	// Sanity: la fila debe decodificar sin error y con los NULLs esperados.
	br := bytes.NewReader(data)
	r := NewReader(br)
	row := make([]any, len(descs))
	nullBuf := make([]byte, 32)
	if err := DecodeRow(r, descs, nullBuf, row); err != nil {
		b.Fatalf("decode row: %v", err)
	}
	for i, v := range values {
		if (v == nil) != (row[i] == nil) {
			b.Fatalf("columna %d: null esperado=%v, decodificado=%#v", i, v == nil, row[i])
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br.Reset(data)
		r.Reset(br)
		if err := DecodeRow(r, descs, nullBuf, row); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeRow_5Cols(b *testing.B) {
	benchDecodeRow(b, mixedDescs5(), mixedValues5())
}

func BenchmarkDecodeRow_5Cols_2Nulls(b *testing.B) {
	values := mixedValues5()
	values[1] = nil
	values[4] = nil
	benchDecodeRow(b, mixedDescs5(), values)
}

func descs30() ([]ColumnDescriptor, []any) {
	var descs []ColumnDescriptor
	var values []any
	for range 6 {
		descs = append(descs, mixedDescs5()...)
		values = append(values, mixedValues5()...)
	}
	return descs, values
}

func BenchmarkDecodeRow_30Cols(b *testing.B) {
	descs, values := descs30()
	benchDecodeRow(b, descs, values)
}

func BenchmarkDecodeRow_30Cols_HalfNulls(b *testing.B) {
	descs, values := descs30()
	for i := range values {
		if i%2 == 1 {
			values[i] = nil
		}
	}
	benchDecodeRow(b, descs, values)
}

// --- Construcción de DPB/TPB/BLR (camino de conexión / transacción) ---

func BenchmarkBuildDPB_Connect(b *testing.B) {
	b.ReportAllocs()
	builder := NewDPBBuilder()
	for i := 0; i < b.N; i++ {
		builder.Reset()
		builder.WriteByteTag(IscDpbSQLDialect, 3)
		builder.WriteString(IscDpbLcCtype, "UTF8")
		builder.WriteString(IscDpbUserName, "SYSDBA")
		builder.WriteMarker(IscDpbUtf8Filename)
		builder.WriteBytes(IscDpbSpecificAuthData, make([]byte, 0))
		_ = builder.Bytes()
	}
}

func BenchmarkBuildTPB_ReadCommitted(b *testing.B) {
	b.ReportAllocs()
	builder := NewTPBBuilder()
	for i := 0; i < b.N; i++ {
		builder.Reset()
		builder.WriteTag(IscTpbVersion3)
		builder.WriteTag(IscTpbReadCommitted)
		builder.WriteTag(IscTpbRecVersion)
		builder.WriteTag(IscTpbWait)
		builder.WriteTag(IscTpbWrite)
		_ = builder.Bytes()
	}
}

func BenchmarkAppendBLR_5Cols(b *testing.B) {
	descs := mixedDescs5()
	var buf [64]byte
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = AppendBLR(buf[:0], descs)
	}
}

func BenchmarkAppendBLR_30Cols(b *testing.B) {
	descs, _ := descs30()
	buf := make([]byte, 0, 256)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = AppendBLR(buf, descs)
	}
}
