package wire

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"testing"
)

func TestNewTextCodecUTF8Passthrough(t *testing.T) {
	codec, err := NewTextCodec("UTF8", "")
	if err != nil {
		t.Fatalf("NewTextCodec() error = %v", err)
	}
	if codec != nil {
		t.Fatalf("codec = %#v, want nil passthrough", codec)
	}
}

func TestTextCodecISO88591(t *testing.T) {
	codec, err := NewTextCodec("ISO8859_1", "")
	if err != nil {
		t.Fatalf("NewTextCodec() error = %v", err)
	}

	got := codec.Decode([]byte{'M', 'a', 'n', 'u', 'a', 'l', ' ', 0xe9, ' ', 0xba})
	want := "Manual \u00e9 \u00ba"
	if got != want {
		t.Fatalf("Decode() = %q, want %q", got, want)
	}

	encoded, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got, want := []byte(encoded), []byte{'M', 'a', 'n', 'u', 'a', 'l', ' ', 0xe9, ' ', 0xba}; !bytes.Equal(got, want) {
		t.Fatalf("Encode() = %x, want %x", got, want)
	}
}

func TestTextCodecClientEncodingOverride(t *testing.T) {
	codec, err := NewTextCodec("ISO8859_1", "WIN1252")
	if err != nil {
		t.Fatalf("NewTextCodec() error = %v", err)
	}

	if got, want := codec.Decode([]byte{0x93, 'O', 'K', 0x94}), "\u201cOK\u201d"; got != want {
		t.Fatalf("Decode() = %q, want %q", got, want)
	}
}

func TestEncodeParamsOptimalErrWithCodecEncodesText(t *testing.T) {
	codec, err := NewTextCodec("ISO8859_1", "WIN1252")
	if err != nil {
		t.Fatalf("NewTextCodec() error = %v", err)
	}

	descs := []ColumnDescriptor{{SQLType: SQLVarying}}
	values := []driver.NamedValue{{Ordinal: 1, Value: "Farmac\u00e9utico N\u00ba \u201cOK\u201d"}}

	var sw StackWriter
	data, err := EncodeParamsOptimalErrWithCodec(&sw, descs, values, codec)
	if err != nil {
		t.Fatalf("EncodeParamsOptimalErrWithCodec() error = %v", err)
	}

	if got := data[:4]; !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("null bitset = %x, want 00000000", got)
	}
	length := int(binary.BigEndian.Uint32(data[4:8]))
	got := data[8 : 8+length]
	want := []byte{'F', 'a', 'r', 'm', 'a', 'c', 0xe9, 'u', 't', 'i', 'c', 'o', ' ', 'N', 0xba, ' ', 0x93, 'O', 'K', 0x94}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded value = %x, want %x", got, want)
	}
}

func TestDecodeColumnWithCodec(t *testing.T) {
	codec, err := NewTextCodec("ISO8859_1", "WIN1252")
	if err != nil {
		t.Fatalf("NewTextCodec() error = %v", err)
	}

	w := NewWriter()
	w.WriteBuffer([]byte{0x93, 'O', 'K', 0x94})
	r := NewReader(bytes.NewReader(w.Bytes()))

	got := DecodeColumnWithCodec(r, &ColumnDescriptor{SQLType: SQLVarying}, codec)
	if r.Err() != nil {
		t.Fatalf("reader error = %v", r.Err())
	}
	if want := "\u201cOK\u201d"; got != want {
		t.Fatalf("DecodeColumnWithCodec() = %q, want %q", got, want)
	}
}
