package wire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReadInt32(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int32
	}{
		{"zero", []byte{0x00, 0x00, 0x00, 0x00}, 0},
		{"one", []byte{0x00, 0x00, 0x00, 0x01}, 1},
		{"256", []byte{0x00, 0x00, 0x01, 0x00}, 256},
		{"max_int32", []byte{0x7F, 0xFF, 0xFF, 0xFF}, 0x7FFFFFFF},
		{"minus_one", []byte{0xFF, 0xFF, 0xFF, 0xFF}, -1},
		{"min_int32", []byte{0x80, 0x00, 0x00, 0x00}, -2147483648},
		{"0x12345678", []byte{0x12, 0x34, 0x56, 0x78}, 0x12345678},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.data))
			got := r.ReadInt32()
			if err := r.Err(); err != nil {
				t.Fatalf("ReadInt32: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReadInt32(%x) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

func TestReadUInt32(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint32
	}{
		{"zero", []byte{0x00, 0x00, 0x00, 0x00}, 0},
		{"one", []byte{0x00, 0x00, 0x00, 0x01}, 1},
		{"max_uint32", []byte{0xFF, 0xFF, 0xFF, 0xFF}, 0xFFFFFFFF},
		{"0xDEADBEEF", []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0xDEADBEEF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.data))
			got := r.ReadUInt32()
			if err := r.Err(); err != nil {
				t.Fatalf("ReadUInt32: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReadUInt32(%x) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

func TestReadInt64(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int64
	}{
		{"zero", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 0},
		{"one", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}, 1},
		{"minus_one", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, -1},
		{"large", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, 0x0102030405060708},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.data))
			got := r.ReadInt64()
			if err := r.Err(); err != nil {
				t.Fatalf("ReadInt64: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReadInt64(%x) = %d, want %d", tt.data, got, tt.want)
			}
		})
	}
}

func TestReadBuffer(t *testing.T) {
	tests := []struct {
		name string
		data []byte // raw wire bytes
		want []byte // expected decoded buffer content
	}{
		{
			"empty",
			[]byte{0x00, 0x00, 0x00, 0x00},
			[]byte{},
		},
		{
			"len1_pad3",
			[]byte{0x00, 0x00, 0x00, 0x01, 0xAA, 0x00, 0x00, 0x00},
			[]byte{0xAA},
		},
		{
			"len2_pad2",
			[]byte{0x00, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x00, 0x00},
			[]byte{0xAA, 0xBB},
		},
		{
			"len3_pad1",
			[]byte{0x00, 0x00, 0x00, 0x03, 0xAB, 0xCD, 0xEF, 0x00},
			[]byte{0xAB, 0xCD, 0xEF},
		},
		{
			"len4_pad0",
			[]byte{0x00, 0x00, 0x00, 0x04, 0x01, 0x02, 0x03, 0x04},
			[]byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			"len5_pad3",
			[]byte{
				0x00, 0x00, 0x00, 0x05,
				0x48, 0x45, 0x4C, 0x4C,
				0x4F, 0x00, 0x00, 0x00,
			},
			[]byte{0x48, 0x45, 0x4C, 0x4C, 0x4F},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.data))
			got := r.ReadBuffer()
			if err := r.Err(); err != nil {
				t.Fatalf("ReadBuffer: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ReadBuffer = %x, want %x", got, tt.want)
			}
		})
	}
}

func TestReadString(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			"empty",
			[]byte{0x00, 0x00, 0x00, 0x00},
			"",
		},
		{
			"HELLO",
			[]byte{
				0x00, 0x00, 0x00, 0x05,
				0x48, 0x45, 0x4C, 0x4C,
				0x4F, 0x00, 0x00, 0x00,
			},
			"HELLO",
		},
		{
			"test_aligned",
			[]byte{0x00, 0x00, 0x00, 0x04, 0x74, 0x65, 0x73, 0x74},
			"test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.data))
			got := r.ReadString()
			if err := r.Err(); err != nil {
				t.Fatalf("ReadString: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReadString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadOpaque(t *testing.T) {
	tests := []struct {
		name string
		wire []byte // raw wire bytes
		n    int
		want []byte // expected data
	}{
		{
			"4bytes_no_pad",
			[]byte{0x01, 0x02, 0x03, 0x04},
			4,
			[]byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			"1byte_pad3",
			[]byte{0xFF, 0x00, 0x00, 0x00},
			1,
			[]byte{0xFF},
		},
		{
			"3bytes_pad1",
			[]byte{0xAA, 0xBB, 0xCC, 0x00},
			3,
			[]byte{0xAA, 0xBB, 0xCC},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bytes.NewReader(tt.wire))
			buf := make([]byte, tt.n)
			r.ReadOpaque(buf, tt.n)
			if err := r.Err(); err != nil {
				t.Fatalf("ReadOpaque: %v", err)
			}
			if !bytes.Equal(buf[:tt.n], tt.want) {
				t.Errorf("ReadOpaque = %x, want %x", buf[:tt.n], tt.want)
			}
		})
	}
}

func TestReadBufferLengthFixup(t *testing.T) {
	// Simulate sign-extended 16-bit length: 0xFFFF0005 should be treated as 5
	var data bytes.Buffer
	binary.Write(&data, binary.BigEndian, uint32(0xFFFF0005))
	data.Write([]byte{0x48, 0x45, 0x4C, 0x4C, 0x4F, 0x00, 0x00, 0x00}) // "HELLO" + padding

	r := NewReader(&data)
	got := r.ReadBuffer()
	if err := r.Err(); err != nil {
		t.Fatalf("ReadBuffer with fixup: %v", err)
	}
	if string(got) != "HELLO" {
		t.Errorf("ReadBuffer fixup = %q, want %q", string(got), "HELLO")
	}
}

func TestReadBufferLengthFixupZero(t *testing.T) {
	// 0xFFFF0000 should be treated as length 0
	var data bytes.Buffer
	binary.Write(&data, binary.BigEndian, uint32(0xFFFF0000))

	r := NewReader(&data)
	got := r.ReadBuffer()
	if err := r.Err(); err != nil {
		t.Fatalf("ReadBuffer fixup zero: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadBuffer fixup zero: got len %d, want 0", len(got))
	}
}

func TestReadOpcodeSkipsDummy(t *testing.T) {
	var data bytes.Buffer
	// Write 3 op_dummy (71) followed by op_connect (1)
	binary.Write(&data, binary.BigEndian, opDummy)
	binary.Write(&data, binary.BigEndian, opDummy)
	binary.Write(&data, binary.BigEndian, opDummy)
	binary.Write(&data, binary.BigEndian, opConnect)

	r := NewReader(&data)
	got := r.ReadOpcode()
	if err := r.Err(); err != nil {
		t.Fatalf("ReadOpcode: %v", err)
	}
	if got != opConnect {
		t.Errorf("ReadOpcode = %d, want %d (opConnect)", got, opConnect)
	}
}

func TestReadOpcodeNoDummy(t *testing.T) {
	var data bytes.Buffer
	binary.Write(&data, binary.BigEndian, opAttach)

	r := NewReader(&data)
	got := r.ReadOpcode()
	if err := r.Err(); err != nil {
		t.Fatalf("ReadOpcode: %v", err)
	}
	if got != opAttach {
		t.Errorf("ReadOpcode = %d, want %d (opAttach)", got, opAttach)
	}
}

func TestReaderErrStopsReads(t *testing.T) {
	// Only 2 bytes — not enough for an Int32
	r := NewReader(bytes.NewReader([]byte{0x00, 0x01}))
	v := r.ReadInt32()
	if r.Err() == nil {
		t.Fatal("expected error on short read")
	}
	if v != 0 {
		t.Errorf("expected 0 on error, got %d", v)
	}
	// Subsequent reads should also return zero
	v2 := r.ReadInt32()
	if v2 != 0 {
		t.Errorf("expected 0 on subsequent read after error, got %d", v2)
	}
}

func TestReaderResetErr(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0x00}))
	r.ReadInt32() // will fail
	if r.Err() == nil {
		t.Fatal("expected error")
	}
	r.ResetErr()
	if r.Err() != nil {
		t.Errorf("expected nil after ResetErr, got %v", r.Err())
	}
}

func TestReaderReset(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{0x00}))
	r.ReadInt32() // will fail
	if r.Err() == nil {
		t.Fatal("expected error")
	}

	// Reset with new data
	r.Reset(bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x2A}))
	got := r.ReadInt32()
	if err := r.Err(); err != nil {
		t.Fatalf("ReadInt32 after Reset: %v", err)
	}
	if got != 42 {
		t.Errorf("ReadInt32 after Reset = %d, want 42", got)
	}
}

func TestSkipPadding(t *testing.T) {
	tests := []struct {
		name     string
		n        int // bytes of data written
		padBytes int // expected padding bytes
	}{
		{"0_pad0", 0, 0},
		{"1_pad3", 1, 3},
		{"2_pad2", 2, 2},
		{"3_pad1", 3, 1},
		{"4_pad0", 4, 0},
		{"5_pad3", 5, 3},
		{"8_pad0", 8, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build data: n bytes of 0xFF followed by padBytes of 0x00, then a marker Int32
			var wire bytes.Buffer
			for range tt.n {
				wire.WriteByte(0xFF)
			}
			for range tt.padBytes {
				wire.WriteByte(0x00)
			}
			binary.Write(&wire, binary.BigEndian, int32(42)) // marker

			r := NewReader(&wire)
			// Read and discard the n data bytes
			if tt.n > 0 {
				discard := make([]byte, tt.n)
				r.readFull(discard)
			}
			r.SkipPadding(tt.n)
			// Now read the marker
			marker := r.ReadInt32()
			if err := r.Err(); err != nil {
				t.Fatalf("SkipPadding(%d): %v", tt.n, err)
			}
			if marker != 42 {
				t.Errorf("after SkipPadding(%d), marker = %d, want 42", tt.n, marker)
			}
		})
	}
}
