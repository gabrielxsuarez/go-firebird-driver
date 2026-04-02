package wire

import "io"

const defaultReadBufSize = 16 * 1024

// Reader reads XDR-encoded data from an underlying io.Reader.
// It keeps a sliding window over an internal buffer so small reads can be
// satisfied without calling io.ReadFull for every field.
type Reader struct {
	rd     io.Reader
	err    error
	buf    []byte
	start  int
	end    int
	auxBuf []byte // reusable buffer for response data copies
}

// NewReader returns a new Reader with a reusable 16KB internal buffer.
func NewReader(r io.Reader) *Reader {
	return &Reader{
		rd:     r,
		buf:    make([]byte, defaultReadBufSize),
		auxBuf: make([]byte, 0, 256),
	}
}

// Err returns the first error encountered during reads.
func (r *Reader) Err() error {
	return r.err
}

// ResetErr clears any stored error, allowing reads to resume.
func (r *Reader) ResetErr() {
	r.err = nil
}

// Reset replaces the underlying reader and clears any stored error.
func (r *Reader) Reset(rd io.Reader) {
	r.rd = rd
	r.err = nil
	r.start = 0
	r.end = 0
}

// readFull reads exactly len(buf) bytes from the internal window into buf.
func (r *Reader) readFull(buf []byte) {
	if len(buf) == 0 || r.err != nil {
		return
	}
	view := r.readView(len(buf))
	if r.err != nil {
		return
	}
	copy(buf, view)
}

// readView returns a slice of the next n bytes from the internal buffer.
// The returned slice is valid until the next reader call that may refill
// or compact the internal buffer.
func (r *Reader) readView(n int) []byte {
	if n == 0 {
		return r.buf[r.start:r.start]
	}
	if !r.ensure(n) {
		return nil
	}
	view := r.buf[r.start : r.start+n]
	r.start += n
	return view
}

// ensure makes sure at least n unread bytes are available in the internal
// buffer, compacting or growing it if needed.
func (r *Reader) ensure(n int) bool {
	if r.err != nil {
		return false
	}
	if n <= r.end-r.start {
		return true
	}

	unread := r.end - r.start
	if n > len(r.buf) {
		newSize := len(r.buf)
		if newSize == 0 {
			newSize = defaultReadBufSize
		}
		for newSize < n {
			newSize *= 2
		}
		newBuf := make([]byte, newSize)
		copy(newBuf, r.buf[r.start:r.end])
		r.buf = newBuf
		r.start = 0
		r.end = unread
	} else if unread > 0 && r.start > 0 {
		copy(r.buf, r.buf[r.start:r.end])
		r.start = 0
		r.end = unread
	} else if unread == 0 {
		r.start = 0
		r.end = 0
	}

	for r.end-r.start < n {
		m, err := r.rd.Read(r.buf[r.end:])
		if m > 0 {
			r.end += m
		}
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			r.err = err
			return false
		}
		if m == 0 {
			r.err = io.ErrNoProgress
			return false
		}
	}
	return true
}

// peekBytes returns a view of the next n bytes without advancing the position.
// If fewer than n bytes are available after refill, returns whatever is available.
func (r *Reader) peekBytes(n int) []byte {
	if !r.ensure(n) {
		return nil
	}
	return r.buf[r.start : r.start+n]
}

// advance skips n bytes in the internal buffer.
func (r *Reader) advance(n int) {
	r.start += n
}

// ReadInt32 reads a 32-bit signed integer in big-endian byte order.
func (r *Reader) ReadInt32() int32 {
	b := r.readView(4)
	if r.err != nil {
		return 0
	}
	_ = b[3]
	return int32(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
}

// ReadUInt32 reads a 32-bit unsigned integer in big-endian byte order.
func (r *Reader) ReadUInt32() uint32 {
	b := r.readView(4)
	if r.err != nil {
		return 0
	}
	_ = b[3]
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// ReadInt64 reads a 64-bit signed integer in big-endian byte order.
func (r *Reader) ReadInt64() int64 {
	b := r.readView(8)
	if r.err != nil {
		return 0
	}
	_ = b[7]
	return int64(uint64(b[0])<<56 |
		uint64(b[1])<<48 |
		uint64(b[2])<<40 |
		uint64(b[3])<<32 |
		uint64(b[4])<<24 |
		uint64(b[5])<<16 |
		uint64(b[6])<<8 |
		uint64(b[7]))
}

// ReadBuffer reads a length-prefixed buffer and returns a slice over the
// reader's internal buffer. The returned slice is valid until the next read
// that refills or compacts that buffer.
//
// Applies sign-extension fixup for compatibility with older servers:
// if the upper 16 bits of the length are 0xFFFF, they are masked off.
func (r *Reader) ReadBuffer() []byte {
	raw := r.ReadInt32()
	if r.err != nil {
		return nil
	}
	ulen := uint32(raw)
	if ulen>>16 == 0xFFFF {
		ulen &= 0xFFFF
	}
	length := int(ulen)
	pad := (4 - length) & 3
	data := r.readView(length + pad)
	if r.err != nil {
		return nil
	}
	return data[:length]
}

// ReadString reads a length-prefixed string from the wire.
func (r *Reader) ReadString() string {
	buf := r.ReadBuffer()
	if r.err != nil {
		return ""
	}
	return string(buf)
}

// ReadOpaque reads exactly n bytes of opaque data into buf, plus alignment
// padding. The caller must ensure len(buf) >= n.
func (r *Reader) ReadOpaque(buf []byte, n int) {
	pad := (4 - n) & 3
	view := r.readView(n + pad)
	if r.err != nil {
		return
	}
	copy(buf[:n], view[:n])
}

// skipPadding discards (4 - n) & 3 bytes of alignment padding.
func (r *Reader) skipPadding(n int) {
	pad := (4 - n) & 3
	if pad > 0 {
		r.readView(pad)
	}
}

// SkipPadding discards alignment padding for n bytes of data.
func (r *Reader) SkipPadding(n int) {
	r.skipPadding(n)
}

// ReadOpcode reads an operation code, skipping any op_dummy (71) keep-alives.
func (r *Reader) ReadOpcode() int32 {
	for {
		op := r.ReadInt32()
		if r.err != nil || op != opDummy {
			return op
		}
	}
}
