// Package app is the layer of use cases. Each subpackage is one area of what the system does and declares
// the interfaces (ports) it needs; adapters implement them, delivery (HTTP, Temporal) calls the use cases. This file
// holds what all of them share: the kinds of errors.
package app

import (
	"errors"
	"fmt"
)

// Kinds of errors. A use case returns them through Fail with a message the user may see; delivery turns the kind into
// an HTTP status or tells Temporal not to retry. Any other error is internal: logged, never shown.
var (
	ErrInvalid      = errors.New("invalid")           // the request breaks a rule: HTTP 422, not retried
	ErrUnauthorized = errors.New("unauthorized")      // Moodle did not accept the login or the token: HTTP 401
	ErrForbidden    = errors.New("forbidden")         // no right to do it: HTTP 403, not retried
	ErrNotFound     = errors.New("not found")         // HTTP 404, not retried
	ErrConflict     = errors.New("conflict")          // the state does not allow it now: HTTP 409
	ErrUnavailable  = errors.New("unavailable")       // Moodle or the model server cannot be reached or is off: HTTP 502/503
	ErrPermanent    = errors.New("permanent failure") // retrying cannot help (a broken prompt template, a foreign file URL)
)

// Error is a failure with a message for the user; errors.Is(err, app.ErrConflict) finds its kind.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

// Fail returns an error of the given kind with a message for the user.
func Fail(kind error, format string, args ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Message is the text to show for err, and false when the error is internal.
func Message(err error) (string, bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Msg, true
	}

	return "", false
}

// Retryable reports whether trying the same step again may succeed: internal errors (network, database) may,
// rejected requests may not.
func Retryable(err error) bool {
	for _, k := range []error{ErrInvalid, ErrUnauthorized, ErrForbidden, ErrNotFound, ErrPermanent} {
		if errors.Is(err, k) {
			return false
		}
	}

	return true
}
