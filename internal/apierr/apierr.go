// Package apierr holds the single error table: exit classes for the CLI.
// This is the skeleton; the word-to-class table lands with the tickets that
// produce API errors.
package apierr

import "errors"

// Exit classes, per the design note's exit table.
const (
	ExitOK         = 0
	ExitUnexpected = 1
	ExitUsage      = 2
)

// UsageError marks an error as a usage error (exit 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// Usage wraps err as a usage error.
func Usage(err error) error { return &UsageError{Err: err} }

// ExitCode maps an error to its exit class. Unknown errors are class 1.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var u *UsageError
	if errors.As(err, &u) {
		return ExitUsage
	}
	return ExitUnexpected
}
