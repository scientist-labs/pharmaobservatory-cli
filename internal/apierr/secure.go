package apierr

import (
	"errors"
	"fmt"
	"net/http"
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

// MaxRedirects is the hop limit, the same as the Go default.
const MaxRedirects = 10

// SecureRedirect is the http.Client CheckRedirect for every client in this CLI. It applies
// RequireSecure to each redirect target, so an https endpoint cannot bounce a request (and the
// token or bearer in it) to plain http. The refusal is a usage error (exit 2).
func SecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return Usage(fmt.Errorf("stopped after %d redirects", MaxRedirects))
	}
	if err := RequireSecure("redirect target", req.URL.String()); err != nil {
		return Usage(fmt.Errorf("refusing to follow a redirect from %s: %w", Sanitize(via[len(via)-1].URL.Redacted()), err))
	}
	return nil
}

// UsageCause returns the usage error inside err (an http.Client wraps a CheckRedirect refusal
// in a url.Error), or nil when there is none.
func UsageCause(err error) error {
	var u *UsageError
	if errors.As(err, &u) {
		return u
	}
	return nil
}
