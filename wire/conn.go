package wire

import (
	"io"
	"net"
	"time"
)

const defaultCryptBufSize = 32 * 1024

// connPipe is a mutable io.ReadWriter that acts as a pivot between
// the caller and the encryption layer. Swapping its internals transparently
// reroutes traffic through the cipher layer without recreating existing ones.
type connPipe struct {
	r io.Reader
	w io.Writer
}

func (p *connPipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *connPipe) Write(b []byte) (int, error) { return p.w.Write(b) }

// Conn encapsulates a network connection with optional encryption.
//
// Layer order (write): caller → encrypt → TCP
// Layer order (read):  TCP → decrypt → caller
//
// The pipe acts as a pivot point. When encryption is activated, pipe's
// internals are swapped to route through the cipher layer.
type Conn struct {
	conn net.Conn
	pipe *connPipe

	r io.Reader // top-level read endpoint
	w io.Writer // top-level write endpoint

	cryptR *cryptReader
	cryptW *cryptWriter
}

// NewConn wraps a network connection for use with the wire protocol.
func NewConn(conn net.Conn) *Conn {
	pipe := &connPipe{r: conn, w: conn}
	return &Conn{
		conn: conn,
		pipe: pipe,
		r:    pipe,
		w:    pipe,
	}
}

// Read reads from the top of the layer stack.
func (c *Conn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// Write writes to the top of the layer stack.
func (c *Conn) Write(p []byte) (int, error) {
	return c.w.Write(p)
}

// enableEncryption activates stream cipher encryption on the connection.
// Separate cipher instances are used for each direction since stream
// cipher state evolves independently.
func (c *Conn) enableEncryption(readCipher, writeCipher streamCipher) {
	c.cryptR = &cryptReader{r: c.conn, c: readCipher}
	c.cryptW = &cryptWriter{w: c.conn, c: writeCipher, buf: make([]byte, defaultCryptBufSize)}
	c.pipe.r = c.cryptR
	c.pipe.w = c.cryptW
}

// SetDeadline sets the read and write deadline on the underlying connection.
func (c *Conn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline on the underlying connection.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline on the underlying connection.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// Close closes the underlying TCP connection and releases layer resources.
// The TCP connection is closed first since the Firebird disconnect sequence
// (op_detach + op_disconnect) has already been sent through the layers.
func (c *Conn) Close() error {
	return c.conn.Close()
}
