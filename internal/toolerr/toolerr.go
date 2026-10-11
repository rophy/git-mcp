// Package toolerr attaches a stable error code to tool errors, so clients can
// act on the kind of error without matching its text.
package toolerr

import (
	"errors"
	"fmt"
)

type Code string

const (
	// InvalidInput: the input failed validation, or no upstream is configured
	// for the repo's host. Permanent.
	InvalidInput Code = "invalid_input"
	// AccessDenied: the service may not read the repo. Permanent.
	AccessDenied Code = "access_denied"
	// RefNotFound: clone or fetch worked, but the ref does not resolve. Permanent.
	RefNotFound Code = "ref_not_found"
	// Timeout: a git command hit the timeout. Transient.
	Timeout Code = "timeout"
	// UpstreamError: clone or fetch failed (network, credentials, or the repo
	// does not exist upstream). Usually transient.
	UpstreamError Code = "upstream_error"
	// Internal: any other error.
	Internal Code = "internal"
)

// Error is an error with a code. Its message is the message of Err.
type Error struct {
	Code Code
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// New returns err with code.
func New(code Code, err error) error {
	return &Error{Code: code, Err: err}
}

// Errorf returns a new error with code.
func Errorf(code Code, format string, args ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, args...)}
}

// CodeOf returns the code of the first Error in err's chain, or Internal.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Internal
}
