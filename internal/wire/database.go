// Núcleo de WireConnection: transporte, lazy send y operaciones de base (spec cap. 6).

package wire

import (
	"fmt"
	"sync"
	"time"
)

// WireConnection manages a single Firebird database connection at the wire
// protocol level. It owns the transport layers (conn), XDR reader/writer,
// and tracks protocol state (version, handles, deferred responses).
type WireConnection struct {
	conn   *Conn
	reader *Reader
	writer *Writer

	dbHandle        int32
	protocolVersion uint32
	charset         string
	// noneCharsetID es el charset con el que se interpretan las columnas de
	// texto declaradas NONE. Se resuelve una vez al conectar; IDNone (0) deja
	// esas columnas como bytes crudos. Ver applyNoneCharset.
	noneCharsetID int32
	lazySend      bool // true when ptype_lazy_send is negotiated

	// Lazy send: count of responses not yet consumed.
	deferredCount int

	// nullBuf is a reusable buffer for null bitset reads (avoids per-row alloc).
	// Sized for up to 256 columns; enlarged if needed.
	nullBuf [32]byte

	// Statement handle pool: reuse freed handles to avoid op_allocate round-trips.
	// Handles are closed with DSQLClose (cursor closed, handle stays allocated)
	// and re-prepared when needed.
	freeHandles [maxFreeHandles]int32
	freeCount   int

	// cancelMu protects async cancel writes to the socket.
	cancelMu sync.Mutex

	// writeMu serializes all writes to the transport. This matters when
	// cancellation is sent from a goroutine while another operation is flushing
	// through the encrypted connection layers.
	writeMu sync.Mutex
}

const maxFreeHandles = 8

// ProtocolVersion returns the negotiated protocol version.
func (wc *WireConnection) ProtocolVersion() uint32 {
	return wc.protocolVersion
}

// SetDeadline sets the read/write deadline on the underlying connection.
func (wc *WireConnection) SetDeadline(t time.Time) {
	_ = wc.conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline on the underlying connection. Setting
// a past time forces a blocked read to return, used to honor a cancelled
// context when the server can't interrupt the current operation (e.g. a lock
// wait). The connection must be treated as broken afterwards.
func (wc *WireConnection) SetReadDeadline(t time.Time) {
	_ = wc.conn.SetReadDeadline(t)
}

// CloseTransport closes the underlying socket without attempting protocol
// cleanup. Use this after transport failures, where detach would only add more
// broken writes to the same dead connection.
func (wc *WireConnection) CloseTransport() error {
	if wc == nil || wc.conn == nil {
		return nil
	}
	return wc.conn.Close()
}

// consumeDeferred reads and discards all deferred responses.
func (wc *WireConnection) consumeDeferred() error {
	for wc.deferredCount > 0 {
		_, err := wc.reader.ReadResponse()
		if err != nil {
			return err
		}
		wc.deferredCount--
	}
	return nil
}

// readResponse drains any deferred lazy responses, then reads the actual response.
func (wc *WireConnection) readResponse() (GenericResponse, error) {
	if err := wc.consumeDeferred(); err != nil {
		return GenericResponse{}, fmt.Errorf("consume deferred: %w", err)
	}
	return wc.reader.ReadResponse()
}

// flush sends buffered data to the wire.
func (wc *WireConnection) flush() error {
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()

	return wc.writer.Flush(wc.conn)
}

// InfoDatabase sends op_info_database and returns the raw info buffer.
func (wc *WireConnection) InfoDatabase(items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoDatabase)
	wc.writer.WriteInt32(0) // p_info_object
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_database: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_database: %w", err)
	}
	return resp.Data, nil
}

// Detach sends op_detach + op_disconnect and closes the connection.
func (wc *WireConnection) Detach() error {
	wc.writer.WriteInt32(opDetach)
	wc.writer.WriteInt32(wc.dbHandle)
	if err := wc.flush(); err != nil {
		wc.conn.Close()
		return fmt.Errorf("op_detach: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		wc.conn.Close()
		return fmt.Errorf("op_detach: %w", err)
	}

	// op_disconnect: no response
	wc.writer.WriteInt32(opDisconnect)
	_ = wc.flush()
	return wc.conn.Close()
}

// Cancel sends an asynchronous op_cancel to interrupt the current operation.
// This is safe to call from a different goroutine.
func (wc *WireConnection) Cancel(kind uint32) error {
	wc.cancelMu.Lock()
	defer wc.cancelMu.Unlock()

	if kind == CancelAbort {
		return wc.conn.Close()
	}

	// Write op_cancel directly to the underlying TCP socket.
	// This bypasses encryption layers intentionally -
	// the cancel packet is always in plaintext per the protocol spec.
	// Actually: cancel goes through the same layers since it's sent
	// on the same socket. Use the writer.
	w := NewWriter()
	w.WriteInt32(opCancel)
	w.WriteUInt32(kind)
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()

	return w.Flush(wc.conn)
}

// Reader returns the underlying wire reader for direct row data decoding.
func (wc *WireConnection) Reader() *Reader {
	return wc.reader
}
