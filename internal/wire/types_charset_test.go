package wire

import (
	"bytes"
	"database/sql/driver"
	"testing"
	"unicode/utf8"
)

func TestDecodeColumnISO88591Varying(t *testing.T) {
	w := NewWriter()
	w.WriteBuffer([]byte{'V', 'A', 'R', 'T', 'A', 0xA0, 'C', 'R'})

	r := NewReader(bytes.NewReader(w.Bytes()))
	desc := &ColumnDescriptor{SQLType: SQLVarying, SubType: 21, Length: 50}

	value := DecodeColumn(r, desc)
	got, ok := value.(string)
	if !ok {
		t.Fatalf("DecodeColumn type = %T, want string", value)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("DecodeColumn returned invalid UTF-8: % x", got)
	}
	want := "VARTA\u00a0CR"
	if got != want {
		t.Fatalf("DecodeColumn = %q, want %q", got, want)
	}
}

func TestDecodeColumnISO88591TextTrimsAfterDecode(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{'A', 0xA0, ' ', ' '}))
	desc := &ColumnDescriptor{SQLType: SQLText, SubType: 21, Length: 4}

	got := DecodeColumn(r, desc)
	want := "A\u00a0"
	if got != want {
		t.Fatalf("DecodeColumn = %q, want %q", got, want)
	}
}

func TestEncodeParamsISO88591Varying(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 50}}
	values := []any{"VARTA\u00a0CR"}

	if err := EncodeParamsErr(w, descs, values); err != nil {
		t.Fatalf("EncodeParamsErr: %v", err)
	}

	want := []byte{
		0, 0, 0, 0,
		0, 0, 0, 8,
		'V', 'A', 'R', 'T', 'A', 0xA0, 'C', 'R',
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("EncodeParamsErr = % x, want % x", w.Bytes(), want)
	}
}

func TestEncodeParamsISO88591VaryingPadsWithSpaces(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 50}}
	values := []any{"ABC"}

	if err := EncodeParamsErr(w, descs, values); err != nil {
		t.Fatalf("EncodeParamsErr: %v", err)
	}

	want := []byte{
		0, 0, 0, 0,
		0, 0, 0, 3,
		'A', 'B', 'C', ' ',
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("EncodeParamsErr = % x, want % x", w.Bytes(), want)
	}
}

func TestEncodeParamsOptimalISO88591Varying(t *testing.T) {
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 50}}
	values := []driver.NamedValue{{Ordinal: 1, Value: "VARTA\u00a0CR"}}

	var sw StackWriter
	got, err := EncodeParamsOptimalErr(&sw, descs, values)
	if err != nil {
		t.Fatalf("EncodeParamsOptimalErr: %v", err)
	}

	want := []byte{
		0, 0, 0, 0,
		0, 0, 0, 8,
		'V', 'A', 'R', 'T', 'A', 0xA0, 'C', 'R',
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeParamsOptimalErr = % x, want % x", got, want)
	}
}

func TestEncodeParamsVaryingRejectsEncodedOverflow(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 4}}
	values := []any{"ABCDE"}

	if err := EncodeParamsErr(w, descs, values); err == nil {
		t.Fatal("EncodeParamsErr error = nil, want varying parameter too long")
	}
}

func TestEncodeParamsOptimalVaryingRejectsEncodedOverflow(t *testing.T) {
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 4}}
	values := []driver.NamedValue{{Ordinal: 1, Value: "ABCDE"}}

	var sw StackWriter
	if _, err := EncodeParamsOptimalErr(&sw, descs, values); err == nil {
		t.Fatal("EncodeParamsOptimalErr error = nil, want varying parameter too long")
	}
}

func TestEncodeParamsISO88591Text(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLText, SubType: 21, Length: 4}}
	values := []any{"A\u00a0"}

	if err := EncodeParamsErr(w, descs, values); err != nil {
		t.Fatalf("EncodeParamsErr: %v", err)
	}

	want := []byte{0, 0, 0, 0, 'A', 0xA0, ' ', ' '}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("EncodeParamsErr = % x, want % x", w.Bytes(), want)
	}
}

func TestEncodeParamsISO88591UnsupportedRune(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 21, Length: 50}}
	values := []any{"precio €"}

	if err := EncodeParamsErr(w, descs, values); err == nil {
		t.Fatal("EncodeParamsErr error = nil, want charset conversion error")
	}
}

func TestDecodeColumnOctetsVaryingReturnsBytes(t *testing.T) {
	w := NewWriter()
	want := []byte{0x00, 0x41, 0xff, 0x20}
	w.WriteBuffer(want)

	r := NewReader(bytes.NewReader(w.Bytes()))
	desc := &ColumnDescriptor{SQLType: SQLVarying, SubType: 1, Length: 10}

	value := DecodeColumn(r, desc)
	got, ok := value.([]byte)
	if !ok {
		t.Fatalf("DecodeColumn type = %T, want []byte", value)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DecodeColumn = % x, want % x", got, want)
	}
}

func TestDecodeColumnOctetsTextKeepsPaddingAndReturnsBytes(t *testing.T) {
	want := []byte{0x00, 0x41, 0xff, 0x20}
	r := NewReader(bytes.NewReader(want))
	desc := &ColumnDescriptor{SQLType: SQLText, SubType: 1, Length: int32(len(want))}

	value := DecodeColumn(r, desc)
	got, ok := value.([]byte)
	if !ok {
		t.Fatalf("DecodeColumn type = %T, want []byte", value)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DecodeColumn = % x, want % x", got, want)
	}
}

func TestEncodeParamsASCIIUnsupportedRune(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLVarying, SubType: 2, Length: 50}}
	values := []any{"cafe\u00e9"}

	if err := EncodeParamsErr(w, descs, values); err == nil {
		t.Fatal("EncodeParamsErr error = nil, want charset conversion error")
	}
}

func TestEncodeParamsTextRejectsEncodedOverflow(t *testing.T) {
	w := NewWriter()
	descs := []ColumnDescriptor{{SQLType: SQLText, SubType: 21, Length: 4}}
	values := []any{"ABCDE"}

	if err := EncodeParamsErr(w, descs, values); err == nil {
		t.Fatal("EncodeParamsErr error = nil, want text parameter too long")
	}
}
