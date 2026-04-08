package firebird

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
)

// isTransportError reports whether err came from the underlying network
// transport rather than from the Firebird server itself.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}

	switch {
	case errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, io.ErrClosedPipe),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.EPIPE),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.ECONNABORTED),
		errors.Is(err, syscall.ENOTCONN),
		errors.Is(err, syscall.ESHUTDOWN),
		errors.Is(err, syscall.ETIMEDOUT):
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection")
}

func wrapBadConn(err error) error {
	if err == nil || errors.Is(err, driver.ErrBadConn) {
		return err
	}
	return fmt.Errorf("%w: %v", driver.ErrBadConn, err)
}
