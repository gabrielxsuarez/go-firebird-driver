package wire

import (
	"bytes"
	"testing"
)

// --- Arc4 tests ---

func TestArc4RoundTrip(t *testing.T) {
	key := []byte("test-session-key-20b")
	plain := []byte("Hello, Firebird wire protocol!")

	encCipher, err := newArc4Cipher(key)
	if err != nil {
		t.Fatal(err)
	}
	decCipher, err := newArc4Cipher(key)
	if err != nil {
		t.Fatal(err)
	}

	encrypted := make([]byte, len(plain))
	encCipher.XORKeyStream(encrypted, plain)

	if bytes.Equal(encrypted, plain) {
		t.Fatal("encrypted data should differ from plaintext")
	}

	decrypted := make([]byte, len(encrypted))
	decCipher.XORKeyStream(decrypted, encrypted)

	if !bytes.Equal(decrypted, plain) {
		t.Errorf("round-trip failed: got %x, want %x", decrypted, plain)
	}
}

func TestArc4IndependentStreams(t *testing.T) {
	key := []byte("same-key-for-both!!")
	c1, _ := newArc4Cipher(key)
	c2, _ := newArc4Cipher(key)

	data := make([]byte, 64)
	out1 := make([]byte, 64)
	out2 := make([]byte, 64)

	c1.XORKeyStream(out1, data)
	c2.XORKeyStream(out2, data)

	if !bytes.Equal(out1, out2) {
		t.Error("two ciphers with same key should produce identical keystream")
	}
}

func TestArc4InvalidKey(t *testing.T) {
	_, err := newArc4Cipher([]byte{})
	if err == nil {
		t.Error("expected error for empty key")
	}
}

// --- ChaCha20 tests ---

func TestChaCha20RoundTrip(t *testing.T) {
	sessionKey := []byte("twenty-byte-sess-key")
	nonce := make([]byte, 12)
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}

	plain := []byte("ChaCha20 test data for Firebird 4.0+")

	encCipher, err := newChaCha20Cipher(sessionKey, nonce)
	if err != nil {
		t.Fatal(err)
	}
	decCipher, err := newChaCha20Cipher(sessionKey, nonce)
	if err != nil {
		t.Fatal(err)
	}

	encrypted := make([]byte, len(plain))
	encCipher.XORKeyStream(encrypted, plain)

	if bytes.Equal(encrypted, plain) {
		t.Fatal("encrypted data should differ from plaintext")
	}

	decrypted := make([]byte, len(encrypted))
	decCipher.XORKeyStream(decrypted, encrypted)

	if !bytes.Equal(decrypted, plain) {
		t.Errorf("round-trip failed: got %x, want %x", decrypted, plain)
	}
}

func TestChaCha20KeyDerivation(t *testing.T) {
	sessionKey := []byte("twenty-byte-sess-key")

	// Two ciphers with same session key should produce identical keystream
	nonce := make([]byte, 12)
	c1, _ := newChaCha20Cipher(sessionKey, nonce)
	c2, _ := newChaCha20Cipher(sessionKey, nonce)

	data := make([]byte, 64)
	out1 := make([]byte, 64)
	out2 := make([]byte, 64)
	c1.XORKeyStream(out1, data)
	c2.XORKeyStream(out2, data)

	if !bytes.Equal(out1, out2) {
		t.Error("same session key should produce identical ciphers")
	}
}

func TestChaCha20Nonce16Bytes(t *testing.T) {
	sessionKey := []byte("twenty-byte-sess-key")
	serverData := make([]byte, 16)
	for i := range serverData {
		serverData[i] = byte(i + 0x10)
	}

	c, err := newChaCha20Cipher(sessionKey, serverData)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the cipher works
	plain := []byte("test")
	enc := make([]byte, len(plain))
	c.XORKeyStream(enc, plain)
	if bytes.Equal(enc, plain) {
		t.Error("encryption should change the data")
	}
}

func TestChaCha20Nonce12Bytes(t *testing.T) {
	sessionKey := []byte("twenty-byte-sess-key")
	serverData := make([]byte, 12)
	for i := range serverData {
		serverData[i] = byte(i + 0x20)
	}

	c, err := newChaCha20Cipher(sessionKey, serverData)
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte("test")
	enc := make([]byte, len(plain))
	c.XORKeyStream(enc, plain)
	if bytes.Equal(enc, plain) {
		t.Error("encryption should change the data")
	}
}

func TestChaCha20ShortKey(t *testing.T) {
	_, err := newChaCha20Cipher([]byte("short"), make([]byte, 12))
	if err == nil {
		t.Error("expected error for session key < 16 bytes")
	}
}

func TestChaCha20ShortNonce(t *testing.T) {
	_, err := newChaCha20Cipher([]byte("twenty-byte-sess-key"), make([]byte, 8))
	if err == nil {
		t.Error("expected error for server data < 12 bytes")
	}
}

func TestChaCha20CounterFromServerData(t *testing.T) {
	sessionKey := []byte("twenty-byte-sess-key")

	// 12-byte nonce (counter defaults to 0)
	serverData12 := make([]byte, 12)
	c12, _ := newChaCha20Cipher(sessionKey, serverData12)

	// 16-byte server data with counter = 0 (same as 12-byte)
	serverData16 := make([]byte, 16)
	// Bytes 12-15 are 0x00 = counter 0
	c16, _ := newChaCha20Cipher(sessionKey, serverData16)

	data := make([]byte, 64)
	out12 := make([]byte, 64)
	out16 := make([]byte, 64)
	c12.XORKeyStream(out12, data)
	c16.XORKeyStream(out16, data)

	if !bytes.Equal(out12, out16) {
		t.Error("12-byte nonce (counter=0) should match 16-byte with zero counter")
	}

	// Now with non-zero counter
	serverData16NZ := make([]byte, 16)
	serverData16NZ[15] = 5 // counter = 5 (big-endian)
	cNZ, _ := newChaCha20Cipher(sessionKey, serverData16NZ)
	outNZ := make([]byte, 64)
	cNZ.XORKeyStream(outNZ, data)

	if bytes.Equal(outNZ, out12) {
		t.Error("different counter values should produce different keystreams")
	}
}

// --- cryptReader/cryptWriter tests ---

func TestCryptReaderWriter(t *testing.T) {
	key := []byte("twenty-byte-sess-key")

	var buf bytes.Buffer

	// Create write-side cipher and cryptWriter
	wCipher, _ := newArc4Cipher(key)
	cw := &cryptWriter{w: &buf, c: wCipher, buf: make([]byte, 1024)}

	plain := []byte("data flowing through the wire protocol")
	n, err := cw.Write(plain)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(plain) {
		t.Fatalf("wrote %d bytes, want %d", n, len(plain))
	}

	// The buffer should contain encrypted (different from plain) data
	if bytes.Equal(buf.Bytes(), plain) {
		t.Fatal("wire data should be encrypted")
	}

	// Create read-side cipher and cryptReader
	rCipher, _ := newArc4Cipher(key)
	cr := &cryptReader{r: &buf, c: rCipher}

	got := make([]byte, len(plain))
	n, err = cr.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(plain) {
		t.Fatalf("read %d bytes, want %d", n, len(plain))
	}
	if !bytes.Equal(got[:n], plain) {
		t.Errorf("decrypted data mismatch: got %x, want %x", got[:n], plain)
	}
}

func TestCryptWriterEmptyWrite(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newArc4Cipher([]byte("key-for-empty-test!!"))
	cw := &cryptWriter{w: &buf, c: c, buf: make([]byte, 64)}

	n, err := cw.Write([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty buffer, got %d bytes", buf.Len())
	}
}

func TestCryptWriterBufferGrowth(t *testing.T) {
	var buf bytes.Buffer
	c, _ := newArc4Cipher([]byte("growth-test-key12345"))
	cw := &cryptWriter{w: &buf, c: c, buf: make([]byte, 8)}

	// Write more than initial buffer size
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	n, err := cw.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 {
		t.Fatalf("wrote %d bytes, want 100", n)
	}
	if len(cw.buf) < 100 {
		t.Errorf("buffer should have grown to >= 100, got %d", len(cw.buf))
	}
}

func TestCryptReaderWriterLargeData(t *testing.T) {
	key := []byte("large-data-test-key!")

	var buf bytes.Buffer
	wCipher, _ := newArc4Cipher(key)
	cw := &cryptWriter{w: &buf, c: wCipher, buf: make([]byte, defaultCryptBufSize)}

	// Write 64KB of data
	plain := make([]byte, 64*1024)
	for i := range plain {
		plain[i] = byte(i & 0xFF)
	}
	if _, err := cw.Write(plain); err != nil {
		t.Fatal(err)
	}

	// Read it back
	rCipher, _ := newArc4Cipher(key)
	cr := &cryptReader{r: &buf, c: rCipher}

	got := make([]byte, len(plain))
	total := 0
	for total < len(plain) {
		n, err := cr.Read(got[total:])
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if !bytes.Equal(got, plain) {
		t.Error("large data round-trip mismatch")
	}
}
