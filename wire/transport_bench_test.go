package wire

import (
	"bytes"
	"io"
	"testing"
)

// --- Encryption benchmarks ---

func BenchmarkArc4Encrypt_1KB(b *testing.B) {
	c, _ := newArc4Cipher([]byte("bench-key-20-bytes!!"))
	data := make([]byte, 1024)
	dst := make([]byte, 1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.XORKeyStream(dst, data)
	}
}

func BenchmarkArc4Encrypt_32KB(b *testing.B) {
	c, _ := newArc4Cipher([]byte("bench-key-20-bytes!!"))
	data := make([]byte, 32*1024)
	dst := make([]byte, 32*1024)
	b.SetBytes(32 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.XORKeyStream(dst, data)
	}
}

func BenchmarkChaCha20Encrypt_1KB(b *testing.B) {
	key := []byte("bench-key-20-bytes!!")
	nonce := make([]byte, 12)
	c, _ := newChaCha20Cipher(key, nonce)
	data := make([]byte, 1024)
	dst := make([]byte, 1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.XORKeyStream(dst, data)
	}
}

func BenchmarkChaCha20Encrypt_32KB(b *testing.B) {
	key := []byte("bench-key-20-bytes!!")
	nonce := make([]byte, 12)
	c, _ := newChaCha20Cipher(key, nonce)
	data := make([]byte, 32*1024)
	dst := make([]byte, 32*1024)
	b.SetBytes(32 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.XORKeyStream(dst, data)
	}
}

// --- cryptReader/cryptWriter benchmarks ---

func BenchmarkCryptWriter_1KB(b *testing.B) {
	c, _ := newArc4Cipher([]byte("bench-key-20-bytes!!"))
	cw := &cryptWriter{w: io.Discard, c: c, buf: make([]byte, defaultCryptBufSize)}
	data := make([]byte, 1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cw.Write(data)
	}
}

func BenchmarkCryptWriter_32KB(b *testing.B) {
	c, _ := newArc4Cipher([]byte("bench-key-20-bytes!!"))
	cw := &cryptWriter{w: io.Discard, c: c, buf: make([]byte, defaultCryptBufSize)}
	data := make([]byte, 32*1024)
	b.SetBytes(32 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cw.Write(data)
	}
}

func BenchmarkCryptReader_1KB(b *testing.B) {
	c, _ := newArc4Cipher([]byte("bench-key-20-bytes!!"))
	data := make([]byte, 1024)
	rd := bytes.NewReader(data)
	cr := &cryptReader{r: rd, c: c}
	buf := make([]byte, 1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rd.Reset(data)
		cr.Read(buf)
	}
}
