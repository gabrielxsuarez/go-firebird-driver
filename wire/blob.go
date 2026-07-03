// Operaciones de blobs: create/open/get/put segment y lectura/escritura materializada (spec cap. 9).

package wire

import "fmt"

// CreateBlob sends op_create_blob2 and returns the handle and blob ID.
func (wc *WireConnection) CreateBlob(txHandle int32, bpb []byte) (int32, int64, error) {
	wc.writer.WriteInt32(opCreateBlob2)
	wc.writer.WriteBuffer(bpb)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(0) // p_blob_id = 0 for new

	if err := wc.flush(); err != nil {
		return 0, 0, fmt.Errorf("op_create_blob2: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, 0, fmt.Errorf("op_create_blob2: %w", err)
	}
	return resp.Handle, resp.BlobID, nil
}

// OpenBlob sends op_open_blob2 and returns the handle.
func (wc *WireConnection) OpenBlob(txHandle int32, blobID int64, bpb []byte) (int32, error) {
	wc.writer.WriteInt32(opOpenBlob2)
	wc.writer.WriteBuffer(bpb)
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(blobID)

	if err := wc.flush(); err != nil {
		return 0, fmt.Errorf("op_open_blob2: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, fmt.Errorf("op_open_blob2: %w", err)
	}
	return resp.Handle, nil
}

// PutSegment sends op_put_segment with the given data.
func (wc *WireConnection) PutSegment(blobHandle int32, data []byte) error {
	wc.writer.WriteInt32(opPutSegment)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(int32(len(data)))
	wc.writer.WriteBuffer(data)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_put_segment: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_put_segment: %w", err)
	}
	return nil
}

// GetSegment sends op_get_segment and returns the packed segment data
// and a status (0=data, 1=partial, 2=EOF).
func (wc *WireConnection) GetSegment(blobHandle int32, maxLength int32) (int32, []byte, error) {
	wc.writer.WriteInt32(opGetSegment)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(maxLength)
	wc.writer.WriteBuffer(nil) // p_sgmt_segment (empty)

	if err := wc.flush(); err != nil {
		return 0, nil, fmt.Errorf("op_get_segment: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return 0, nil, fmt.Errorf("op_get_segment: %w", err)
	}
	return resp.Handle, resp.Data, nil
}

// CloseBlob sends op_close_blob.
func (wc *WireConnection) CloseBlob(blobHandle int32) error {
	wc.writer.WriteInt32(opCloseBlob)
	wc.writer.WriteInt32(blobHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_close_blob: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_close_blob: %w", err)
	}
	return nil
}

// CancelBlob sends op_cancel_blob.
func (wc *WireConnection) CancelBlob(blobHandle int32) error {
	wc.writer.WriteInt32(opCancelBlob)
	wc.writer.WriteInt32(blobHandle)

	if err := wc.flush(); err != nil {
		return fmt.Errorf("op_cancel_blob: flush: %w", err)
	}

	_, err := wc.readResponse()
	if err != nil {
		return fmt.Errorf("op_cancel_blob: %w", err)
	}
	return nil
}

// InfoBlob sends op_info_blob and returns the raw info buffer.
func (wc *WireConnection) InfoBlob(blobHandle int32, items []byte, bufferLength int32) ([]byte, error) {
	wc.writer.WriteInt32(opInfoBlob)
	wc.writer.WriteInt32(blobHandle)
	wc.writer.WriteInt32(0) // p_info_incarnation
	wc.writer.WriteBuffer(items)
	wc.writer.WriteInt32(bufferLength)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("op_info_blob: flush: %w", err)
	}

	resp, err := wc.readResponse()
	if err != nil {
		return nil, fmt.Errorf("op_info_blob: %w", err)
	}
	return resp.Data, nil
}

// ReadBlobData opens, reads, and closes a blob, returning the full content.
// Uses pipelining when lazy send is available: batches open + first get_segment
// into one flush, saving 1 round-trip for the common case of small blobs.
func (wc *WireConnection) ReadBlobData(txHandle int32, blobID int64) ([]byte, error) {
	const maxGetLen int32 = 65535

	if wc.lazySend {
		return wc.readBlobDataPipelined(txHandle, blobID, maxGetLen)
	}
	return wc.readBlobDataSequential(txHandle, blobID, maxGetLen)
}

// readBlobDataPipelined batches open_blob + first get_segment in one flush.
func (wc *WireConnection) readBlobDataPipelined(txHandle int32, blobID int64, maxGetLen int32) ([]byte, error) {
	// op_open_blob2 (deferred — no flush)
	wc.writer.WriteInt32(opOpenBlob2)
	wc.writer.WriteBuffer(nil) // empty BPB
	wc.writer.WriteInt32(txHandle)
	wc.writer.WriteInt64(blobID)

	// op_get_segment with deferred blob handle
	wc.writer.WriteInt32(opGetSegment)
	wc.writer.WriteInt32(int32(InvalidObject))
	wc.writer.WriteInt32(maxGetLen)
	wc.writer.WriteBuffer(nil)

	if err := wc.flush(); err != nil {
		return nil, fmt.Errorf("blob read pipeline: flush: %w", err)
	}

	// Read open response (contains blob handle)
	openResp, openErr := wc.readResponse()
	if openErr != nil {
		// Still need to read the get_segment response to keep wire in sync
		wc.reader.ReadResponse()
		return nil, fmt.Errorf("blob read pipeline: open: %w", openErr)
	}
	blobHandle := openResp.Handle

	// Read first get_segment response
	getResp, getErr := wc.reader.ReadResponse()
	if getErr != nil {
		_ = wc.CloseBlob(blobHandle)
		return nil, fmt.Errorf("blob read pipeline: get_segment: %w", getErr)
	}

	result := make([]byte, 0, 4096)
	result = unpackSegments(result, getResp.Data)

	if getResp.Handle == 2 { // EOF — single-segment blob (most common case)
		if err := wc.CloseBlob(blobHandle); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Multi-segment: continue reading sequentially
	for {
		status, packed, err := wc.GetSegment(blobHandle, maxGetLen)
		if err != nil {
			_ = wc.CloseBlob(blobHandle)
			return nil, err
		}
		result = unpackSegments(result, packed)
		if status == 2 { // EOF
			break
		}
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return nil, err
	}
	return result, nil
}

// readBlobDataSequential is the fallback for non-lazy-send connections.
func (wc *WireConnection) readBlobDataSequential(txHandle int32, blobID int64, maxGetLen int32) ([]byte, error) {
	blobHandle, err := wc.OpenBlob(txHandle, blobID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]byte, 0, 4096)
	for {
		status, packed, err := wc.GetSegment(blobHandle, maxGetLen)
		if err != nil {
			_ = wc.CloseBlob(blobHandle)
			return nil, err
		}
		result = unpackSegments(result, packed)
		if status == 2 { // EOF
			break
		}
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return nil, err
	}
	return result, nil
}

// unpackSegments appends unpacked segment data to dst.
// Each segment in packed is: 2-byte LE length + data.
func unpackSegments(dst, packed []byte) []byte {
	for off := 0; off+2 <= len(packed); {
		segLen := int(packed[off]) | int(packed[off+1])<<8
		off += 2
		if off+segLen > len(packed) {
			break
		}
		dst = append(dst, packed[off:off+segLen]...)
		off += segLen
	}
	return dst
}

// WriteBlobData creates a blob, writes data in segments, closes it, and returns the blob ID.
// Uses pipelining when lazy send is available to batch all operations in a single flush,
// reducing N+2 round-trips to 1.
func (wc *WireConnection) WriteBlobData(txHandle int32, data []byte) (int64, error) {
	const maxSegment = 32768

	// Pipeline path: batch create + all puts + close in one flush.
	// Uses deferred handle resolution (0xFFFF) — the server resolves it
	// to the blob handle from op_create_blob2.
	if wc.lazySend {
		// op_create_blob2 (deferred — no flush)
		wc.writer.WriteInt32(opCreateBlob2)
		wc.writer.WriteBuffer(nil) // empty BPB
		wc.writer.WriteInt32(txHandle)
		wc.writer.WriteInt64(0) // new blob

		// Count segments for response draining
		segCount := 0
		remaining := data
		for len(remaining) > 0 {
			seg := remaining
			if len(seg) > maxSegment {
				seg = seg[:maxSegment]
			}
			// op_put_segment with deferred blob handle
			wc.writer.WriteInt32(opPutSegment)
			wc.writer.WriteInt32(int32(InvalidObject))
			wc.writer.WriteInt32(int32(len(seg)))
			wc.writer.WriteBuffer(seg)
			segCount++
			remaining = remaining[len(seg):]
		}

		// op_close_blob with deferred blob handle
		wc.writer.WriteInt32(opCloseBlob)
		wc.writer.WriteInt32(int32(InvalidObject))

		if err := wc.flush(); err != nil {
			return 0, fmt.Errorf("blob write pipeline: flush: %w", err)
		}

		// Read create response (contains blob handle and blob ID)
		createResp, createErr := wc.readResponse()
		// Read all put_segment responses
		for i := 0; i < segCount; i++ {
			_, err := wc.reader.ReadResponse()
			if err != nil && createErr == nil {
				createErr = fmt.Errorf("blob write pipeline: put_segment[%d]: %w", i, err)
			}
		}
		// Read close response
		_, closeErr := wc.reader.ReadResponse()

		if createErr != nil {
			return 0, fmt.Errorf("blob write pipeline: create: %w", createErr)
		}
		if closeErr != nil {
			return 0, fmt.Errorf("blob write pipeline: close: %w", closeErr)
		}
		return createResp.BlobID, nil
	}

	// Fallback: sequential round-trips (non-lazy send)
	blobHandle, blobID, err := wc.CreateBlob(txHandle, nil)
	if err != nil {
		return 0, err
	}

	for len(data) > 0 {
		seg := data
		if len(seg) > maxSegment {
			seg = seg[:maxSegment]
		}
		if err := wc.PutSegment(blobHandle, seg); err != nil {
			_ = wc.CancelBlob(blobHandle)
			return 0, err
		}
		data = data[len(seg):]
	}

	if err := wc.CloseBlob(blobHandle); err != nil {
		return 0, err
	}
	return blobID, nil
}
