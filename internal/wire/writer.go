package wire

import (
	"encoding/binary"
	"io"
	"sync"
)

const defaultWriteBufSize = 32 * 1024

var zeroPad = [4]byte{}

// writerPool pools Writer objects to avoid repeated 32KB allocations.
var writerPool = sync.Pool{
	New: func() any {
		return NewWriter()
	},
}

// GetWriter returns a Writer from the pool, ready for use.
func GetWriter() *Writer {
	w := writerPool.Get().(*Writer)
	w.n = 0
	return w
}

// PutWriter returns a Writer to the pool.
func PutWriter(w *Writer) {
	writerPool.Put(w)
}

// StackWriter is a Writer that uses a stack-allocated buffer.
// Use this for small, short-lived encoding operations to avoid pool overhead.
// If the buffer overflows, the overflow flag is set and can be checked with Overflowed().
type StackWriter struct {
	buf      [1024]byte
	n        int
	overflow bool
}

// Reset clears the buffer for reuse.
func (w *StackWriter) Reset() {
	w.n = 0
	w.overflow = false
}

// Overflowed returns true if any write operation exceeded the buffer capacity.
func (w *StackWriter) Overflowed() bool {
	return w.overflow
}

// WriteInt32 writes a 32-bit signed integer.
func (w *StackWriter) WriteInt32(v int32) {
	if w.n+4 > len(w.buf) {
		w.overflow = true
		return
	}
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(v))
	w.n += 4
}

// WriteUInt32 writes a 32-bit unsigned integer.
func (w *StackWriter) WriteUInt32(v uint32) {
	if w.n+4 > len(w.buf) {
		w.overflow = true
		return
	}
	binary.BigEndian.PutUint32(w.buf[w.n:], v)
	w.n += 4
}

// WriteInt64 writes a 64-bit signed integer.
func (w *StackWriter) WriteInt64(v int64) {
	if w.n+8 > len(w.buf) {
		w.overflow = true
		return
	}
	binary.BigEndian.PutUint64(w.buf[w.n:], uint64(v))
	w.n += 8
}

// WriteBuffer writes a length-prefixed buffer.
func (w *StackWriter) WriteBuffer(data []byte) {
	l := len(data)
	pad := (4 - l) & 3
	if w.n+4+l+pad > len(w.buf) {
		w.overflow = true
		return
	}
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], data)
	w.n += l
	copy(w.buf[w.n:], zeroPad[:pad])
	w.n += pad
}

// WriteString writes a string as length-prefixed buffer.
func (w *StackWriter) WriteString(s string) {
	l := len(s)
	pad := (4 - l) & 3
	if w.n+4+l+pad > len(w.buf) {
		w.overflow = true
		return
	}
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], s)
	w.n += l
	copy(w.buf[w.n:], zeroPad[:pad])
	w.n += pad
}

// Bytes returns the buffered bytes.
func (w *StackWriter) Bytes() []byte {
	return w.buf[:w.n]
}

// Writer accumulates XDR-encoded data in an internal buffer.
// All Write methods append to the buffer without allocating.
// Call Flush to send the buffered data to the underlying writer.
type Writer struct {
	buf []byte
	n   int
}

// NewWriter returns a new Writer with a 32KB initial buffer.
func NewWriter() *Writer {
	return &Writer{
		buf: make([]byte, defaultWriteBufSize),
	}
}

// ResetWriter returns a Writer to a usable state with the buffer cleared.
// This is used by the pool to reuse writers without allocating.
func (w *Writer) ResetWriter() {
	w.n = 0
}

// grow ensures the buffer has room for at least n more bytes.
func (w *Writer) grow(n int) {
	if w.n+n <= len(w.buf) {
		return
	}
	newSize := len(w.buf) * 2
	for newSize < w.n+n {
		newSize *= 2
	}
	newBuf := make([]byte, newSize)
	copy(newBuf, w.buf[:w.n])
	w.buf = newBuf
}

// WriteInt32 writes a 32-bit signed integer in big-endian byte order.
func (w *Writer) WriteInt32(v int32) {
	w.grow(4)
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(v))
	w.n += 4
}

// WriteUInt32 writes a 32-bit unsigned integer in big-endian byte order.
func (w *Writer) WriteUInt32(v uint32) {
	w.grow(4)
	binary.BigEndian.PutUint32(w.buf[w.n:], v)
	w.n += 4
}

// WriteInt64 writes a 64-bit signed integer in big-endian byte order.
func (w *Writer) WriteInt64(v int64) {
	w.grow(8)
	binary.BigEndian.PutUint64(w.buf[w.n:], uint64(v))
	w.n += 8
}

// WriteBuffer writes a length-prefixed buffer: Int32(len) + data + padding(0x00).
func (w *Writer) WriteBuffer(data []byte) {
	l := len(data)
	pad := (4 - l) & 3
	w.grow(4 + l + pad)
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], data)
	w.n += l
	copy(w.buf[w.n:], zeroPad[:pad])
	w.n += pad
}

// WriteString writes a string as a length-prefixed buffer.
// Does not allocate: copies directly from string to buffer.
func (w *Writer) WriteString(s string) {
	l := len(s)
	pad := (4 - l) & 3
	w.grow(4 + l + pad)
	binary.BigEndian.PutUint32(w.buf[w.n:], uint32(l))
	w.n += 4
	copy(w.buf[w.n:], s)
	w.n += l
	copy(w.buf[w.n:], zeroPad[:pad])
	w.n += pad
}

// WriteOpaque writes exactly n bytes of data plus padding to align to 4 bytes.
// No length prefix is written. The caller must ensure len(data) >= n.
func (w *Writer) WriteOpaque(data []byte, n int) {
	pad := (4 - n) & 3
	w.grow(n + pad)
	copy(w.buf[w.n:], data[:n])
	w.n += n
	copy(w.buf[w.n:], zeroPad[:pad])
	w.n += pad
}

// WriteRaw writes raw bytes without any length prefix or padding.
func (w *Writer) WriteRaw(data []byte) {
	w.grow(len(data))
	copy(w.buf[w.n:], data)
	w.n += len(data)
}

// Flush writes the buffered data to wr and resets the buffer position.
func (w *Writer) Flush(wr io.Writer) error {
	if w.n == 0 {
		return nil
	}
	_, err := wr.Write(w.buf[:w.n])
	w.n = 0
	return err
}

// Reset discards all buffered data without writing.
func (w *Writer) Reset() {
	w.n = 0
}

// Len returns the number of buffered bytes.
func (w *Writer) Len() int {
	return w.n
}

// Bytes returns the buffered bytes. The slice is valid until the next
// write operation or Reset.
func (w *Writer) Bytes() []byte {
	return w.buf[:w.n]
}
