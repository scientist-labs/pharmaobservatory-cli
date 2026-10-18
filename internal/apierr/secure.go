package apierr

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Sanitize removes control characters (ANSI escapes included, C0, DEL and C1) from text a
// server supplied before it reaches the terminal. Printable text is kept as is.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// isLoopbackHost reports whether host is one of the loopback literals http is allowed for.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// RequireSecure refuses a URL that is not https, unless its host is a loopback literal
// (127.0.0.1, ::1 or localhost). what names the URL's role for the message. The error is a
// usage error (exit 2) so nothing is sent to the refused address.
func RequireSecure(what, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Usage(fmt.Errorf("refusing the %s %q: it is not a valid absolute URL", what, Sanitize(raw)))
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return Usage(fmt.Errorf("refusing the %s %s: it must be https (http is only allowed for 127.0.0.1, ::1 or localhost)", what, Sanitize(raw)))
}
