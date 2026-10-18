// Package apierr holds the single error table: exit classes for the CLI.
// This is the skeleton; the word-to-class table lands with the tickets that
// produce API errors.
package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Exit classes, per the design note's exit table.
const (
	ExitOK         = 0
	ExitUnexpected = 1
	ExitUsage      = 2
	ExitAuth       = 3
	ExitForbidden  = 4
	ExitUnavail    = 7
)

// ClassError carries an explicit exit class and a message that names the remedy.
type ClassError struct {
	Class int
	Msg   string
}

func (e *ClassError) Error() string { return e.Msg }

// Auth builds a class-3 error (not logged in, refused credential, failed login).
func Auth(format string, a ...any) error {
	return &ClassError{Class: ExitAuth, Msg: fmt.Sprintf(format, a...)}
}

// Forbidden builds a class-4 error.
func Forbidden(format string, a ...any) error {
	return &ClassError{Class: ExitForbidden, Msg: fmt.Sprintf(format, a...)}
}

// Unavailable builds a class-7 error (5xx, DNS, connection failure).
func Unavailable(format string, a ...any) error {
	return &ClassError{Class: ExitUnavail, Msg: fmt.Sprintf(format, a...)}
}

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
	var c *ClassError
	if errors.As(err, &c) {
		return c.Class
	}
	return ExitUnexpected
}

// FromResponse renders a non-200 API response plainly, with the remedy. Only the words a login
// can produce are mapped here; the full table lands with the error-mapping ticket. An unknown
// word is class 1 with the raw body, never silently ok.
func FromResponse(status int, body []byte, profile string) error {
	var b struct {
		Error string `json:"error"`
		Role  string `json:"role"`
	}
	_ = json.Unmarshal(body, &b)
	login := "run: po login --profile " + profile
	switch {
	case status == 401 && b.Error == "invalid_audience":
		return Auth("the access token is for another resource (invalid_audience); %s", login)
	case status == 401 && b.Error == "expired_token":
		return Auth("the session expired and could not be renewed (expired_token); %s", login)
	case status == 401 && (b.Error == "missing_token" || b.Error == "invalid_token"):
		return Auth("the server refused the token (%s); %s", b.Error, login)
	case status == 403 && b.Error == "missing_role":
		if b.Role == "" {
			return Forbidden("your organization lacks a role the API requires (missing_role)")
		}
		return Forbidden("your organization lacks the role %s (missing_role); ask your organization admin", b.Role)
	case status == 403 && b.Error == "access_pending":
		return Forbidden("this account has no entitlement yet (access_pending); request access at the Pharma Observatory site")
	case status == 503 && b.Error == "no_identity_provider":
		return Unavailable("the server has no identity provider configured (no_identity_provider); try again later")
	case status >= 500:
		return Unavailable("the server answered HTTP %d", status)
	}
	raw := string(body)
	if len(raw) > 300 {
		raw = raw[:300]
	}
	return fmt.Errorf("unexpected response: HTTP %d %s", status, raw)
}
