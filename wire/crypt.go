package wire

import (
	"crypto/rc4"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/chacha20"
)

// streamCipher applies a keystream transformation for wire encryption.
// Both RC4 and ChaCha20 satisfy this interface via XORKeyStream.
type streamCipher interface {
	XORKeyStream(dst, src []byte)
}

// newArc4Cipher creates an RC4 stream cipher from the SRP session key.
// The key is used directly (typically 20 bytes from SRP).
func newArc4Cipher(key []byte) (streamCipher, error) {
	return rc4.NewCipher(key)
}

// newChaCha20Cipher creates a ChaCha20 stream cipher.
// The key is derived as SHA-256(sessionKey). Nonce (12 bytes) and optional
// counter (4 bytes, big-endian) are extracted from the server's plugin data.
func newChaCha20Cipher(sessionKey, serverData []byte) (streamCipher, error) {
	if len(sessionKey) < 16 {
		return nil, errors.New("chacha20: session key too short (need >= 16 bytes)")
	}

	h := sha256.Sum256(sessionKey)

	var nonce []byte
	var counter uint32

	switch {
	case len(serverData) >= 16:
		nonce = serverData[:12]
		counter = binary.BigEndian.Uint32(serverData[12:16])
	case len(serverData) >= 12:
		nonce = serverData[:12]
	default:
		return nil, errors.New("chacha20: server data too short for nonce (need >= 12 bytes)")
	}

	c, err := chacha20.NewUnauthenticatedCipher(h[:], nonce)
	if err != nil {
		return nil, err
	}
	c.SetCounter(counter)
	return c, nil
}

// cryptReader decrypts data read from an underlying reader.
// Decryption is applied in-place on the read buffer.
type cryptReader struct {
	r io.Reader
	c streamCipher
}

func (cr *cryptReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		cr.c.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

// cryptWriter encrypts data before writing to an underlying writer.
// A scratch buffer avoids modifying the caller's slice.
type cryptWriter struct {
	w   io.Writer
	c   streamCipher
	buf []byte
}

func (cw *cryptWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(cw.buf) < len(p) {
		cw.buf = make([]byte, len(p))
	}
	cw.c.XORKeyStream(cw.buf[:len(p)], p)
	return cw.w.Write(cw.buf[:len(p)])
}
