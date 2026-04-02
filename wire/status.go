package wire

import "fmt"

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
	if e.SQLState != "" {
		return fmt.Sprintf("GDS %d (SQLSTATE %s): %s", e.Code, e.SQLState, e.Message)
	}
	return fmt.Sprintf("GDS %d: %s", e.Code, e.Message)
}

// HasError returns true if the status vector contains errors.
func (sv *StatusVector) HasError() bool {
	return len(sv.Errors) > 0 && sv.Errors[0].Code != 0
}

// HasWarning returns true if the status vector contains warnings.
func (sv *StatusVector) HasWarning() bool {
	return len(sv.Warnings) > 0
}

// Error returns the first error as a string, or empty if no errors.
func (sv *StatusVector) Error() string {
	if !sv.HasError() {
		return ""
	}
	return sv.Errors[0].Error()
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
