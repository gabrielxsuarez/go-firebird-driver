package wire

// BPB tag constants.
const (
	IscBpbVersion1     byte = 1
	IscBpbSourceType   byte = 1
	IscBpbTargetType   byte = 2
	IscBpbType         byte = 3
	IscBpbSourceInterp byte = 4
	IscBpbTargetInterp byte = 5
)

// BPBBuilder constructs a Blob Parameter Buffer.
type BPBBuilder struct {
	buf []byte
}

// NewBPBBuilder creates a BPB builder with version 1.
func NewBPBBuilder() *BPBBuilder {
	b := &BPBBuilder{buf: make([]byte, 0, 16)}
	b.buf = append(b.buf, IscBpbVersion1)
	return b
}

// Reset clears the buffer and writes the version byte.
func (b *BPBBuilder) Reset() {
	b.buf = b.buf[:1]
	b.buf[0] = IscBpbVersion1
}

// WriteByteTag appends a single-byte value tag.
func (b *BPBBuilder) WriteByteTag(tag byte, value byte) {
	b.buf = append(b.buf, tag, 1, value)
}

// WriteInt16LE appends a 2-byte little-endian value tag.
func (b *BPBBuilder) WriteInt16LE(tag byte, value int16) {
	b.buf = append(b.buf, tag, 2, byte(value), byte(value>>8))
}

// Bytes returns the built BPB.
func (b *BPBBuilder) Bytes() []byte {
	return b.buf
}
