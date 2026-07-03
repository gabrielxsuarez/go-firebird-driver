package wire

import (
	"fmt"
	"strings"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/errmsg"
)

// StatusVector represents a parsed Firebird status vector containing
// errors and warnings from a server response.
type StatusVector struct {
	Errors   []GDSError
	Warnings []GDSError
}

// GDSError represents a single GDS error or warning entry.
type GDSError struct {
	Code     int32
	SQLState string
	Message  string
	Params   []any
}

// Error returns a human-readable representation of the GDS error.
func (e *GDSError) Error() string {
	var b strings.Builder
	e.appendMessage(&b)
	fmt.Fprintf(&b, " (GDS %d", e.Code)
	if e.SQLState != "" {
		fmt.Fprintf(&b, ", SQLSTATE %s", e.SQLState)
	}
	b.WriteString(")")
	return b.String()
}

// appendMessage writes the rendered message text for this entry: the client-side
// Firebird template with @n placeholders substituted, falling back to the raw
// code and arguments when the code is unknown.
func (e *GDSError) appendMessage(b *strings.Builder) {
	if msg := errmsg.Render(e.Code, e.Params); msg != "" {
		b.WriteString(msg)
		return
	}
	fmt.Fprintf(b, "GDS %d", e.Code)
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
		return
	}
	for _, p := range e.Params {
		if n, ok := p.(int32); ok {
			fmt.Fprintf(b, " [%d]", n)
		}
	}
}

// HasError returns true if the status vector contains errors.
func (sv *StatusVector) HasError() bool {
	return len(sv.Errors) > 0 && sv.Errors[0].Code != 0
}

// HasWarning returns true if the status vector contains warnings.
func (sv *StatusVector) HasWarning() bool {
	return len(sv.Warnings) > 0
}

// Error returns a human-readable representation of the full error chain.
// Firebird reports errors as a chain of GDS entries where the first code
// is often generic (e.g. "unsuccessful metadata update") and the details
// (object names, SQLCODE) arrive in later entries, so all entries are
// rendered. The primary GDS code and SQLSTATE are appended for logs and
// support; use errors.As with *StatusError for programmatic access.
func (sv *StatusVector) Error() string {
	if !sv.HasError() {
		return ""
	}
	var b strings.Builder
	for i := range sv.Errors {
		if i > 0 {
			b.WriteString("; ")
		}
		sv.Errors[i].appendMessage(&b)
	}
	primary := &sv.Errors[0]
	fmt.Fprintf(&b, " (GDS %d", primary.Code)
	if primary.SQLState != "" {
		fmt.Fprintf(&b, ", SQLSTATE %s", primary.SQLState)
	}
	b.WriteString(")")
	return b.String()
}

// StatusError is returned whenever a server response carries a non-zero
// status vector. It retains the full StatusVector so callers can inspect
// individual GDS codes with errors.As.
type StatusError struct {
	SV StatusVector
}

// Error implements the error interface.
func (e *StatusError) Error() string {
	return "firebird: " + e.SV.Error()
}

// GDSCode returns the primary GDS error code, or 0 if the vector is empty.
func (e *StatusError) GDSCode() int32 {
	if e.SV.HasError() {
		return e.SV.Errors[0].Code
	}
	return 0
}

// SQLState returns the SQLSTATE of the primary error entry, or "" when it
// cannot be determined. Firebird does not send SQLSTATE in the wire status
// vector: like fbclient/jaybird, it is derived client-side from the GDS code
// (tabla generada en internal/errmsg). Si el vector trae isc_arg_sql_state
// (poco común), ese valor tiene prioridad.
func (e *StatusError) SQLState() string {
	if !e.SV.HasError() {
		return ""
	}
	if st := e.SV.Errors[0].SQLState; st != "" {
		return st
	}
	return errmsg.SQLState(e.SV.Errors[0].Code)
}

// readStatusVector reads and parses a status vector from the wire.
// Format: sequence of [tag Int32][value ...] terminated by isc_arg_end (0).
//
// Fast path: 99%+ of responses are success (isc_arg_gds + 0 + isc_arg_end).
// We detect this 12-byte pattern with a single peek, avoiding the full parse loop.
func (r *Reader) readStatusVector() StatusVector {
	// Fast path: peek at first 12 bytes to detect success pattern.
	// Success = [IscArgGds(1), code(0), IscArgEnd(0)] as three big-endian int32s.
	if peek := r.peekBytes(12); len(peek) == 12 &&
		peek[0] == 0 && peek[1] == 0 && peek[2] == 0 && peek[3] == 1 && // IscArgGds = 1
		peek[4] == 0 && peek[5] == 0 && peek[6] == 0 && peek[7] == 0 && // code = 0
		peek[8] == 0 && peek[9] == 0 && peek[10] == 0 && peek[11] == 0 { // IscArgEnd = 0
		r.advance(12)
		return StatusVector{}
	}

	return r.readStatusVectorSlow()
}

// readStatusVectorSlow is the full status vector parser, used when the fast
// path (success pattern) doesn't match.
func (r *Reader) readStatusVectorSlow() StatusVector {
	var sv StatusVector
	var current *GDSError
	isWarning := false

	for {
		tag := r.ReadInt32()
		if r.err != nil {
			return sv
		}

		switch tag {
		case IscArgEnd:
			return sv

		case IscArgGds:
			code := r.ReadInt32()
			if r.err != nil {
				return sv
			}
			entry := GDSError{Code: code}
			isWarning = false
			if code != 0 {
				sv.Errors = append(sv.Errors, entry)
				current = &sv.Errors[len(sv.Errors)-1]
			} else {
				current = nil
			}

		case IscArgWarning:
			code := r.ReadInt32()
			if r.err != nil {
				return sv
			}
			entry := GDSError{Code: code}
			isWarning = true
			if code != 0 {
				sv.Warnings = append(sv.Warnings, entry)
				current = &sv.Warnings[len(sv.Warnings)-1]
			} else {
				current = nil
			}

		case IscArgString, IscArgCstring, IscArgInterpreted:
			s := r.ReadString()
			if r.err != nil {
				return sv
			}
			if current != nil {
				if current.Message == "" {
					current.Message = s
				} else {
					current.Message += ": " + s
				}
				current.Params = append(current.Params, s)
			}

		case IscArgNumber:
			n := r.ReadInt32()
			if r.err != nil {
				return sv
			}
			if current != nil {
				current.Params = append(current.Params, n)
			}

		case IscArgSQLState:
			s := r.ReadString()
			if r.err != nil {
				return sv
			}
			if isWarning && len(sv.Warnings) > 0 {
				sv.Warnings[len(sv.Warnings)-1].SQLState = s
			} else if !isWarning && len(sv.Errors) > 0 {
				sv.Errors[len(sv.Errors)-1].SQLState = s
			}

		default:
			// Unknown tag: treat value as Int32 and skip.
			r.ReadInt32()
			if r.err != nil {
				return sv
			}
		}
	}
}
