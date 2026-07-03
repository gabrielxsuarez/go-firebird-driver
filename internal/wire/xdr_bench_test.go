package wire

import (
	"bytes"
	"testing"
)

// --- Writer benchmarks ---

func BenchmarkWriteInt32(b *testing.B) {
	w := NewWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteInt32(42)
		w.Reset()
	}
}

func BenchmarkWriteUInt32(b *testing.B) {
	w := NewWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteUInt32(0xDEADBEEF)
		w.Reset()
	}
}

func BenchmarkWriteInt64(b *testing.B) {
	w := NewWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteInt64(0x0102030405060708)
		w.Reset()
	}
}

func BenchmarkWriteBuffer_16(b *testing.B) {
	w := NewWriter()
	data := make([]byte, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteBuffer(data)
		w.Reset()
	}
}

func BenchmarkWriteBuffer_256(b *testing.B) {
	w := NewWriter()
	data := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteBuffer(data)
		w.Reset()
	}
}

func BenchmarkWriteBuffer_4096(b *testing.B) {
	w := NewWriter()
	data := make([]byte, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteBuffer(data)
		w.Reset()
	}
}

func BenchmarkWriteString_Short(b *testing.B) {
	w := NewWriter()
	s := "hello"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteString(s)
		w.Reset()
	}
}

func BenchmarkWriteString_Long(b *testing.B) {
	w := NewWriter()
	s := "the quick brown fox jumps over the lazy dog and some more text to make this longer"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w.WriteString(s)
		w.Reset()
	}
}

// --- Reader benchmarks ---

func BenchmarkReadInt32(b *testing.B) {
	data := []byte{0x00, 0x00, 0x00, 0x2A}
	rd := bytes.NewReader(data)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(data)
		r.Reset(rd)
		r.ReadInt32()
	}
}

func BenchmarkReadUInt32(b *testing.B) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	rd := bytes.NewReader(data)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(data)
		r.Reset(rd)
		r.ReadUInt32()
	}
}

func BenchmarkReadInt64(b *testing.B) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	rd := bytes.NewReader(data)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(data)
		r.Reset(rd)
		r.ReadInt64()
	}
}

func BenchmarkReadBuffer_16(b *testing.B) {
	w := NewWriter()
	w.WriteBuffer(make([]byte, 16))
	wire := w.Bytes()
	rd := bytes.NewReader(wire)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(wire)
		r.Reset(rd)
		r.ReadBuffer()
	}
}

func BenchmarkReadBuffer_256(b *testing.B) {
	w := NewWriter()
	w.WriteBuffer(make([]byte, 256))
	wire := w.Bytes()
	rd := bytes.NewReader(wire)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(wire)
		r.Reset(rd)
		r.ReadBuffer()
	}
}

func BenchmarkReadBuffer_4096(b *testing.B) {
	w := NewWriter()
	w.WriteBuffer(make([]byte, 4096))
	wire := w.Bytes()
	rd := bytes.NewReader(wire)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(wire)
		r.Reset(rd)
		r.ReadBuffer()
	}
}

func BenchmarkReadString_Short(b *testing.B) {
	w := NewWriter()
	w.WriteString("hello")
	wire := w.Bytes()
	rd := bytes.NewReader(wire)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(wire)
		r.Reset(rd)
		r.ReadString()
	}
}

func BenchmarkReadString_Long(b *testing.B) {
	w := NewWriter()
	w.WriteString("the quick brown fox jumps over the lazy dog and some more text to make this longer")
	wire := w.Bytes()
	rd := bytes.NewReader(wire)
	r := NewReader(rd)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(wire)
		r.Reset(rd)
		r.ReadString()
	}
}
