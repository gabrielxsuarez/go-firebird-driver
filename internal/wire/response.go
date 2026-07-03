package wire

import "fmt"

// GenericResponse holds the parsed fields of an op_response message.
type GenericResponse struct {
	Handle int32
	BlobID int64
	Data   []byte
	Status StatusVector
}

// FetchResponse holds the parsed fields of an op_fetch_response message.
type FetchResponse struct {
	Status   int32 // 0 = row available, 100 = EOF
	Messages int32 // number of messages (0 or 1)
}

// SQLResponse holds the parsed fields of an op_sql_response message.
type SQLResponse struct {
	Messages int32 // 0 = no data, 1 = data follows
}

// readGenericResponse reads an op_response from the wire.
// The opcode has already been consumed.
// Data is stored in a reusable buffer; it is valid until the next
// readGenericResponse call.
func (r *Reader) readGenericResponse() GenericResponse {
	var resp GenericResponse
	resp.Handle = r.ReadInt32()
	resp.BlobID = r.ReadInt64()
	data := r.ReadBuffer()
	if r.err != nil {
		return resp
	}
	if len(data) > 0 {
		if cap(r.auxBuf) >= len(data) {
			r.auxBuf = r.auxBuf[:len(data)]
		} else {
			r.auxBuf = make([]byte, len(data))
		}
		copy(r.auxBuf, data)
		resp.Data = r.auxBuf
	}
	resp.Status = r.readStatusVector()
	return resp
}

// readFetchResponse reads an op_fetch_response from the wire.
// The opcode has already been consumed.
func (r *Reader) readFetchResponse() FetchResponse {
	var resp FetchResponse
	resp.Status = r.ReadInt32()
	resp.Messages = r.ReadInt32()
	return resp
}

// readSQLResponse reads an op_sql_response from the wire.
// The opcode has already been consumed.
func (r *Reader) readSQLResponse() SQLResponse {
	var resp SQLResponse
	resp.Messages = r.ReadInt32()
	return resp
}

// ReadResponse reads the next response from the wire, dispatching by opcode.
// Returns a GenericResponse for op_response. For other response types,
// callers should use the specific read methods.
func (r *Reader) ReadResponse() (GenericResponse, error) {
	op := r.ReadOpcode()
	if r.err != nil {
		return GenericResponse{}, r.err
	}

	switch op {
	case opResponse:
		resp := r.readGenericResponse()
		if r.err != nil {
			return resp, r.err
		}
		if resp.Status.HasError() {
			return resp, &StatusError{SV: resp.Status}
		}
		return resp, nil

	default:
		return GenericResponse{}, fmt.Errorf("firebird: unexpected opcode %d, expected op_response (%d)", op, opResponse)
	}
}

// ReadLazyResponse reads a deferred response, used with lazy send protocol.
func (r *Reader) ReadLazyResponse() (GenericResponse, error) {
	return r.ReadResponse()
}
