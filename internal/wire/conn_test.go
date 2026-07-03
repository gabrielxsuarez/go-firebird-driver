package wire

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestConnPlainReadWrite(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	wc := NewConn(client)

	want := []byte("plain data without any layers")

	done := make(chan error, 1)
	go func() {
		_, err := wc.Write(want)
		done <- err
	}()

	got := make([]byte, len(want))
	n, err := io.ReadFull(server, got)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("read %d bytes, want %d", n, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnEncryptionOnly(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	wc := NewConn(client)

	key := []byte("twenty-byte-sess-key")
	rc, _ := newArc4Cipher(key)
	wcc, _ := newArc4Cipher(key)
	wc.enableEncryption(rc, wcc)

	plain := []byte("encrypted wire data")

	// Write encrypted data from client
	done := make(chan error, 1)
	go func() {
		_, err := wc.Write(plain)
		done <- err
	}()

	// Read raw bytes from server side — should be encrypted
	raw := make([]byte, len(plain))
	if _, err := io.ReadFull(server, raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, plain) {
		t.Fatal("data on wire should be encrypted (different from plaintext)")
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Verify the encrypted data is correct by decrypting manually
	decCipher, _ := newArc4Cipher(key)
	decrypted := make([]byte, len(raw))
	decCipher.XORKeyStream(decrypted, raw)
	if !bytes.Equal(decrypted, plain) {
		t.Errorf("decrypted data mismatch: got %x, want %x", decrypted, plain)
	}
}

func TestConnEncryptionReadWrite(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	key := []byte("twenty-byte-sess-key")

	// Client side
	client := NewConn(clientConn)
	crc, _ := newArc4Cipher(key)
	cwc, _ := newArc4Cipher(key)
	client.enableEncryption(crc, cwc)

	// Server side
	server := NewConn(serverConn)
	src, _ := newArc4Cipher(key)
	swc, _ := newArc4Cipher(key)
	server.enableEncryption(src, swc)

	plain := []byte("bidirectional encrypted communication")

	// Client writes, server reads
	done := make(chan error, 1)
	go func() {
		_, err := client.Write(plain)
		done <- err
	}()

	got := make([]byte, len(plain))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("server read: got %q, want %q", got, plain)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnWireDataIsEncrypted(t *testing.T) {
	// Verify that with encryption, the raw TCP data is not plaintext
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	wc := NewConn(client)
	key := []byte("twenty-byte-sess-key")
	rc, _ := newArc4Cipher(key)
	wcc, _ := newArc4Cipher(key)
	wc.enableEncryption(rc, wcc)

	plain := []byte("this should not appear in plaintext on the wire")

	done := make(chan error, 1)
	go func() {
		_, err := wc.Write(plain)
		done <- err
	}()

	raw := make([]byte, len(plain))
	io.ReadFull(server, raw)

	if bytes.Contains(raw, plain) {
		t.Error("plaintext should not appear in raw wire data")
	}

	<-done
}

func TestConnClose(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	wc := NewConn(client)

	key := []byte("twenty-byte-sess-key")
	rc, _ := newArc4Cipher(key)
	wcc, _ := newArc4Cipher(key)
	wc.enableEncryption(rc, wcc)

	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	// Writing after close should fail
	_, err := wc.Write([]byte("test"))
	if err == nil {
		t.Error("expected error writing to closed connection")
	}
}

func TestConnCloseWithEncryption(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	wc := NewConn(client)

	key := []byte("twenty-byte-sess-key")
	rc, _ := newArc4Cipher(key)
	wcc, _ := newArc4Cipher(key)
	wc.enableEncryption(rc, wcc)

	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConnMultipleMessages(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	key := []byte("twenty-byte-sess-key")

	client := NewConn(clientConn)
	server := NewConn(serverConn)

	crc, _ := newArc4Cipher(key)
	cwc, _ := newArc4Cipher(key)
	client.enableEncryption(crc, cwc)

	src, _ := newArc4Cipher(key)
	swc, _ := newArc4Cipher(key)
	server.enableEncryption(src, swc)

	messages := []string{
		"first message",
		"second message",
		"third message with more data to process",
	}

	for _, msg := range messages {
		plain := []byte(msg)

		done := make(chan error, 1)
		go func() {
			_, err := client.Write(plain)
			done <- err
		}()

		got := make([]byte, len(plain))
		if _, err := io.ReadFull(server, got); err != nil {
			t.Fatalf("msg %q: read: %v", msg, err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("msg %q: got %q", msg, got)
		}
		<-done
	}
}
