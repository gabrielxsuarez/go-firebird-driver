package firebird

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

func TestIsTransportError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "broken pipe wrapped",
			err: fmt.Errorf("op_transaction: flush: %w", &net.OpError{
				Op:  "write",
				Net: "tcp",
				Err: syscall.EPIPE,
			}),
			want: true,
		},
		{
			name: "unexpected eof",
			err:  io.ErrUnexpectedEOF,
			want: true,
		},
		{
			name: "firebird status error",
			err: &wire.StatusError{
				SV: wire.StatusVector{
					Errors: []wire.GDSError{{Code: 335544466, Message: "deadlock"}},
				},
			},
			want: false,
		},
		{
			name: "generic error",
			err:  errors.New("some other failure"),
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTransportError(tc.err); got != tc.want {
				t.Fatalf("isTransportError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWrapBadConnPreservesSignal(t *testing.T) {
	t.Parallel()

	err := wrapBadConn(fmt.Errorf("op_transaction: flush: %w", syscall.EPIPE))
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("expected wrapped error to match driver.ErrBadConn, got %v", err)
	}
	if got := err.Error(); got == driver.ErrBadConn.Error() {
		t.Fatalf("expected wrapped error to retain context, got %q", got)
	}
}

func TestResetSessionRejectsBadConn(t *testing.T) {
	t.Parallel()

	c := &conn{bad: true}
	if err := c.ResetSession(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("ResetSession() = %v, want driver.ErrBadConn", err)
	}
}

func TestIsValidRejectsBadConn(t *testing.T) {
	t.Parallel()

	if (&conn{}).IsValid() != true {
		t.Fatal("new connection should be valid")
	}
	if (&conn{bad: true}).IsValid() {
		t.Fatal("bad connection should be invalid")
	}
	if (&conn{closed: true}).IsValid() {
		t.Fatal("closed connection should be invalid")
	}
}
