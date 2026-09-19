package koutendb

import (
	"context"
	"errors"
	"net"
)

// Error identifies a failure without exposing server messages or credentials.
type Error struct {
	kind    string
	message string
	cause   error
}

func (e *Error) Error() string { return e.message }
func (e *Error) Unwrap() error { return e.cause }
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && (e.kind == t.kind || e.kind == "closed" && t.kind == "connection")
}

var (
	ErrConnection         = &Error{kind: "connection", message: "koutendb: connection failed"}
	ErrTimeout            = &Error{kind: "timeout", message: "koutendb: operation timed out"}
	ErrAuthentication     = &Error{kind: "authentication", message: "koutendb: authentication rejected"}
	ErrProtocol           = &Error{kind: "protocol", message: "koutendb: invalid wire response"}
	ErrVersionMismatch    = &Error{kind: "version", message: "koutendb: unsupported wire version"}
	ErrServer             = &Error{kind: "server", message: "koutendb: server rejected request"}
	ErrIndeterminateWrite = &Error{kind: "indeterminate", message: "koutendb: write outcome unknown; do not automatically retry"}
	ErrInvalidInput       = &Error{kind: "input", message: "koutendb: invalid option or request"}
	ErrClosed             = &Error{kind: "closed", message: "koutendb: client closed"}
)

func ioError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return ErrTimeout
	}
	return ErrConnection
}
