package wire

import (
	"bytes"
	"testing"
)

func TestWriteInt32(t *testing.T) {
	tests := []struct {
		name string
		val  int32
		want []byte
	}{
		{"zero", 0, []byte{0x00, 0x00, 0x00, 0x00}},
		{"one", 1, []byte{0x00, 0x00, 0x00, 0x01}},
		{"256", 256, []byte{0x00, 0x00, 0x01, 0x00}},
		{"max_int32", 0x7FFFFFFF, []byte{0x7F, 0xFF, 0xFF, 0xFF}},
		{"minus_one", -1, []byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{"min_int32", -2147483648, []byte{0x80, 0x00, 0x00, 0x00}},
		{"0x12345678", 0x12345678, []byte{0x12, 0x34, 0x56, 0x78}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteInt32(tt.val)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteInt32(%d) = %x, want %x", tt.val, got, tt.want)
			}
		})
	}
}

func TestWriteUInt32(t *testing.T) {
	tests := []struct {
		name string
		val  uint32
		want []byte
	}{
		{"zero", 0, []byte{0x00, 0x00, 0x00, 0x00}},
		{"one", 1, []byte{0x00, 0x00, 0x00, 0x01}},
		{"max_uint32", 0xFFFFFFFF, []byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{"0xDEADBEEF", 0xDEADBEEF, []byte{0xDE, 0xAD, 0xBE, 0xEF}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteUInt32(tt.val)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteUInt32(%d) = %x, want %x", tt.val, got, tt.want)
			}
		})
	}
}

func TestWriteInt64(t *testing.T) {
	tests := []struct {
		name string
		val  int64
		want []byte
	}{
		{"zero", 0, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"one", 1, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}},
		{"minus_one", -1, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"large", 0x0102030405060708, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteInt64(tt.val)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteInt64(%d) = %x, want %x", tt.val, got, tt.want)
			}
		})
	}
}

func TestWriteBuffer(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []byte
	}{
		{
			"empty",
			[]byte{},
			[]byte{0x00, 0x00, 0x00, 0x00},
		},
		{
			"len1_pad3",
			[]byte{0xAA},
			[]byte{0x00, 0x00, 0x00, 0x01, 0xAA, 0x00, 0x00, 0x00},
		},
		{
			"len2_pad2",
			[]byte{0xAA, 0xBB},
			[]byte{0x00, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x00, 0x00},
		},
		{
			"len3_pad1",
			[]byte{0xAB, 0xCD, 0xEF},
			[]byte{0x00, 0x00, 0x00, 0x03, 0xAB, 0xCD, 0xEF, 0x00},
		},
		{
			"len4_pad0",
			[]byte{0x01, 0x02, 0x03, 0x04},
			[]byte{0x00, 0x00, 0x00, 0x04, 0x01, 0x02, 0x03, 0x04},
		},
		{
			"len5_pad3",
			[]byte{0x48, 0x45, 0x4C, 0x4C, 0x4F}, // "HELLO"
			[]byte{
				0x00, 0x00, 0x00, 0x05,
				0x48, 0x45, 0x4C, 0x4C,
				0x4F, 0x00, 0x00, 0x00,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteBuffer(tt.data)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteBuffer(%x) = %x, want %x", tt.data, got, tt.want)
			}
		})
	}
}

func TestWriteString(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want []byte
	}{
		{
			"empty",
			"",
			[]byte{0x00, 0x00, 0x00, 0x00},
		},
		{
			"HELLO",
			"HELLO",
			[]byte{
				0x00, 0x00, 0x00, 0x05,
				0x48, 0x45, 0x4C, 0x4C,
				0x4F, 0x00, 0x00, 0x00,
			},
		},
		{
			"test_aligned",
			"test", // 4 bytes, no padding needed
			[]byte{0x00, 0x00, 0x00, 0x04, 0x74, 0x65, 0x73, 0x74},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteString(tt.s)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteString(%q) = %x, want %x", tt.s, got, tt.want)
			}
		})
	}
}

func TestWriteOpaque(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		n    int
		want []byte
	}{
		{
			"4bytes_no_pad",
			[]byte{0x01, 0x02, 0x03, 0x04},
			4,
			[]byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			"1byte_pad3",
			[]byte{0xFF},
			1,
			[]byte{0xFF, 0x00, 0x00, 0x00},
		},
		{
			"3bytes_pad1",
			[]byte{0xAA, 0xBB, 0xCC},
			3,
			[]byte{0xAA, 0xBB, 0xCC, 0x00},
		},
		{
			"5bytes_pad3",
			[]byte{0x01, 0x02, 0x03, 0x04, 0x05},
			5,
			[]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x00, 0x00, 0x00},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWriter()
			w.WriteOpaque(tt.data, tt.n)
			got := w.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteOpaque(%x, %d) = %x, want %x", tt.data, tt.n, got, tt.want)
			}
		})
	}
}

func TestFlush(t *testing.T) {
	w := NewWriter()
	w.WriteInt32(42)
	w.WriteInt32(100)

	var buf bytes.Buffer
	if err := w.Flush(&buf); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if w.Len() != 0 {
		t.Errorf("after Flush, Len() = %d, want 0", w.Len())
	}
	want := []byte{
		0x00, 0x00, 0x00, 0x2A,
		0x00, 0x00, 0x00, 0x64,
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Flush output = %x, want %x", buf.Bytes(), want)
	}
}

func TestFlushEmpty(t *testing.T) {
	w := NewWriter()
	var buf bytes.Buffer
	if err := w.Flush(&buf); err != nil {
		t.Fatalf("Flush empty: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Flush empty wrote %d bytes, want 0", buf.Len())
	}
}

func TestWriterGrow(t *testing.T) {
	w := &Writer{buf: make([]byte, 8), n: 0}
	// Writing 12 bytes should trigger grow
	w.WriteInt32(1)
	w.WriteInt32(2)
	w.WriteInt32(3) // needs 12 bytes, buf is 8
	if w.Len() != 12 {
		t.Errorf("Len() = %d, want 12", w.Len())
	}
}

func TestWriteBufferPaddingZeroed(t *testing.T) {
	w := NewWriter()
	// Write 5 bytes first to dirty the padding area
	w.WriteBuffer([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	w.Reset()
	// Now write 1 byte; the 3 padding bytes must be 0x00
	w.WriteBuffer([]byte{0xAA})
	got := w.Bytes()
	want := []byte{0x00, 0x00, 0x00, 0x01, 0xAA, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("padding not zeroed: got %x, want %x", got, want)
	}
}

func TestWriteMultipleSequential(t *testing.T) {
	w := NewWriter()
	w.WriteInt32(opConnect) // 1
	w.WriteString("test")
	w.WriteInt64(0x0102030405060708)

	got := w.Bytes()
	want := []byte{
		// opConnect = 1
		0x00, 0x00, 0x00, 0x01,
		// String "test": len=4, data, no padding
		0x00, 0x00, 0x00, 0x04, 0x74, 0x65, 0x73, 0x74,
		// Int64
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("sequential writes = %x, want %x", got, want)
	}
}
