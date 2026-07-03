package wire

import (
	"bytes"
	"testing"
)

// TestRoundTripInt32 verifies write → read round-trip for Int32.
func TestRoundTripInt32(t *testing.T) {
	values := []int32{0, 1, -1, 42, 256, 0x7FFFFFFF, -2147483648, 0x12345678}
	for _, v := range values {
		w := NewWriter()
		w.WriteInt32(v)

		r := NewReader(bytes.NewReader(w.Bytes()))
		got := r.ReadInt32()
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip Int32(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("round-trip Int32: wrote %d, read %d", v, got)
		}
	}
}

// TestRoundTripUInt32 verifies write → read round-trip for UInt32.
func TestRoundTripUInt32(t *testing.T) {
	values := []uint32{0, 1, 0xFFFFFFFF, 0xDEADBEEF, 0x8000}
	for _, v := range values {
		w := NewWriter()
		w.WriteUInt32(v)

		r := NewReader(bytes.NewReader(w.Bytes()))
		got := r.ReadUInt32()
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip UInt32(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("round-trip UInt32: wrote %d, read %d", v, got)
		}
	}
}

// TestRoundTripInt64 verifies write → read round-trip for Int64.
func TestRoundTripInt64(t *testing.T) {
	values := []int64{0, 1, -1, 0x0102030405060708, -9223372036854775808}
	for _, v := range values {
		w := NewWriter()
		w.WriteInt64(v)

		r := NewReader(bytes.NewReader(w.Bytes()))
		got := r.ReadInt64()
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip Int64(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("round-trip Int64: wrote %d, read %d", v, got)
		}
	}
}

// TestRoundTripBuffer verifies write → read round-trip for Buffer with
// various lengths that exercise all padding cases (0, 1, 2, 3).
func TestRoundTripBuffer(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 7, 8, 100, 255, 1024}
	for _, l := range lengths {
		data := make([]byte, l)
		for i := range data {
			data[i] = byte(i & 0xFF)
		}

		w := NewWriter()
		w.WriteBuffer(data)

		r := NewReader(bytes.NewReader(w.Bytes()))
		got := r.ReadBuffer()
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip Buffer(len=%d): %v", l, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("round-trip Buffer(len=%d): data mismatch", l)
		}
	}
}

// TestRoundTripString verifies write → read round-trip for String.
func TestRoundTripString(t *testing.T) {
	strings := []string{"", "a", "ab", "abc", "abcd", "hello", "HELLO WORLD", "café ☕"}
	for _, s := range strings {
		w := NewWriter()
		w.WriteString(s)

		r := NewReader(bytes.NewReader(w.Bytes()))
		got := r.ReadString()
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip String(%q): %v", s, err)
		}
		if got != s {
			t.Errorf("round-trip String: wrote %q, read %q", s, got)
		}
	}
}

// TestRoundTripOpaque verifies write → read round-trip for Opaque.
func TestRoundTripOpaque(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 5, 8, 16}
	for _, n := range lengths {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(0xA0 + i)
		}

		w := NewWriter()
		w.WriteOpaque(data, n)

		r := NewReader(bytes.NewReader(w.Bytes()))
		buf := make([]byte, n)
		r.ReadOpaque(buf, n)
		if err := r.Err(); err != nil {
			t.Fatalf("round-trip Opaque(n=%d): %v", n, err)
		}
		if !bytes.Equal(buf, data) {
			t.Errorf("round-trip Opaque(n=%d): got %x, want %x", n, buf, data)
		}
	}
}

// TestRoundTripMixedSequence verifies a mixed sequence of writes and reads.
func TestRoundTripMixedSequence(t *testing.T) {
	w := NewWriter()
	w.WriteInt32(opConnect)
	w.WriteString("Firebird")
	w.WriteInt64(-3819410105021120785) // 0xCAFEBABEDEADBEEF as int64
	w.WriteBuffer([]byte{1, 2, 3})
	w.WriteUInt32(ProtocolVersion13)

	r := NewReader(bytes.NewReader(w.Bytes()))

	if v := r.ReadInt32(); v != opConnect {
		t.Errorf("Int32 = %d, want %d", v, opConnect)
	}
	if v := r.ReadString(); v != "Firebird" {
		t.Errorf("String = %q, want %q", v, "Firebird")
	}
	if v := r.ReadInt64(); v != -3819410105021120785 {
		t.Errorf("Int64 = %d, want %d", v, int64(-3819410105021120785))
	}
	if v := r.ReadBuffer(); !bytes.Equal(v, []byte{1, 2, 3}) {
		t.Errorf("Buffer = %x, want %x", v, []byte{1, 2, 3})
	}
	if v := r.ReadUInt32(); v != ProtocolVersion13 {
		t.Errorf("UInt32 = %x, want %x", v, ProtocolVersion13)
	}

	if err := r.Err(); err != nil {
		t.Fatalf("mixed sequence: %v", err)
	}
}
