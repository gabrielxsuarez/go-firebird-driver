package wire

// TPB tag constants.
const (
	IscTpbConsistency      byte = 1
	IscTpbConcurrency      byte = 2
	IscTpbWait             byte = 6
	IscTpbNowait           byte = 7
	IscTpbRead             byte = 8
	IscTpbWrite            byte = 9
	IscTpbReadCommitted    byte = 15
	IscTpbAutocommit       byte = 16
	IscTpbRecVersion       byte = 17
	IscTpbNoRecVersion     byte = 18
	IscTpbLockTimeout      byte = 21
	IscTpbReadConsistency  byte = 22
	IscTpbAtSnapshotNumber byte = 23
)

// TPBBuilder constructs a Transaction Parameter Buffer.
type TPBBuilder struct {
	buf []byte
}

// NewTPBBuilder creates a TPB builder with TPB version 3.
func NewTPBBuilder() *TPBBuilder {
	b := &TPBBuilder{buf: make([]byte, 0, 32)}
	b.buf = append(b.buf, IscTpbVersion3)
	return b
}

// Reset clears the buffer and writes the version byte.
func (b *TPBBuilder) Reset() {
	b.buf = b.buf[:1]
	b.buf[0] = IscTpbVersion3
}

// WriteTag appends a single-byte tag (marker).
func (b *TPBBuilder) WriteTag(tag byte) {
	b.buf = append(b.buf, tag)
}

// WriteInt32LE appends a tag with 4-byte little-endian value.
func (b *TPBBuilder) WriteInt32LE(tag byte, value int32) {
	b.buf = append(b.buf, tag, 4,
		byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}

// Bytes returns the built TPB.
func (b *TPBBuilder) Bytes() []byte {
	return b.buf
}
