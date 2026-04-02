package firebird

import (
	"fmt"
	"strings"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// FirebirdError represents an error returned by the Firebird server.
type FirebirdError struct {
	GDSCode  int32
	SQLState string
	Message  string
}

// Error implements the error interface.
func (e *FirebirdError) Error() string {
	if e.SQLState != "" {
		return fmt.Sprintf("firebird: GDS %d (SQLSTATE %s): %s", e.GDSCode, e.SQLState, e.Message)
	}
	return fmt.Sprintf("firebird: GDS %d: %s", e.GDSCode, e.Message)
}

// Is reports whether target matches this error.
func (e *FirebirdError) Is(target error) bool {
	if t, ok := target.(*FirebirdError); ok {
		return e.GDSCode == t.GDSCode
	}
	return false
}

// Unwrap returns nil (FirebirdError is a leaf error).
func (e *FirebirdError) Unwrap() error {
	return nil
}

// NewFirebirdError creates a FirebirdError from a status vector.
func NewFirebirdError(sv wire.StatusVector) *FirebirdError {
	if !sv.HasError() {
		return nil
	}
	first := sv.Errors[0]
	msgs := make([]string, 0, len(sv.Errors))
	for i := range sv.Errors {
		if sv.Errors[i].Message != "" {
			msgs = append(msgs, sv.Errors[i].Message)
		}
	}
	return &FirebirdError{
		GDSCode:  first.Code,
		SQLState: first.SQLState,
		Message:  strings.Join(msgs, "; "),
	}
}

// FirebirdWarning represents a warning from the Firebird server.
type FirebirdWarning struct {
	GDSCode  int32
	SQLState string
	Message  string
}

// Error implements the error interface.
func (w *FirebirdWarning) Error() string {
	return fmt.Sprintf("firebird warning: GDS %d: %s", w.GDSCode, w.Message)
}
