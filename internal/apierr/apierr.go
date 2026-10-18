// Package apierr holds the single error table: the exit classes (ExitTable) and the mapping from
// the server's published refusal words to a one-line message and an exit class (FromResponse).
// Both the README table and `exit-codes` print from here.
package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
	"math"
	"net/http"
	"strings"
	"time"
)

// Bin is the executable name quoted in remedies, help text, `version` and the User-Agent. It is the
// one place Go reads the name from: GoReleaser sets it with -X (see .goreleaser.yml), and "po" is
// only the value of a plain go build.
var Bin = "po"

// MaxWait is the longest --wait will sleep for a Retry-After. A longer one is reported, not waited.
const MaxWait = 2 * time.Minute

// Exit classes, per the design note's exit table.
const (
	ExitOK          = 0
	ExitUnexpected  = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitForbidden   = 4
	ExitNotFound    = 5
	ExitRateLimited = 6
	ExitUnavail     = 7
	ExitNotAnswered = 8
	ExitPartial     = 9
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

// RateLimited is a 429. It is exit class 6; the wait fields let the transport decide on --wait.
type RateLimited struct {
	Msg string
	// RetryAfter is the Retry-After header; HasRetryAfter is false when it was absent or unreadable
	// (a depth refusal, which no wait can clear).
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *RateLimited) Error() string { return e.Msg }

// ExitCode maps an error to its exit class. Unknown errors are class 1.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var u *UsageError
	if errors.As(err, &u) {
		return ExitUsage
	}
	var r *RateLimited
	if errors.As(err, &r) {
		return ExitRateLimited
	}
	var c *ClassError
	if errors.As(err, &c) {
		return c.Class
	}
	return ExitUnexpected
}

// body is every field of a refusal body the table reads. Server text is sanitized where printed.
type body struct {
	Error       string         `json:"error"`
	Message     string         `json:"message"`
	Role        string         `json:"role"`
	License     string         `json:"license"`
	Attribution any            `json:"attribution"`
	Budget      map[string]any `json:"budget"`
	Lists       map[string]any `json:"-"`
}

// listKeys are the body fields that name the valid choices after a refused request.
var listKeys = map[string]string{
	"unknown_page":    "valid_pages",
	"unknown_section": "valid_sections",
	"unknown_sort":    "valid_sorts",
	"unknown_dir":     "valid_dirs",
	"unknown_filter":  "valid_filters",
}

// refusal is one row of the word table: the exit class and the one-line message.
type refusal struct {
	class int
	msg   func(b *body, profile string) string
}

func login(profile string) string { return "run: " + Bin + " login --profile " + profile }

func fixed(s string) func(*body, string) string { return func(*body, string) string { return s } }

// serverMsg renders a refusal the server explained itself, naming the valid choices when it lists
// them.
func serverMsg(word, fallback string) func(*body, string) string {
	return func(b *body, _ string) string {
		m := sanitize.Line(b.Message)
		if m == "" {
			m = fallback
		}
		out := m + " (" + word + ")"
		if k := listKeys[word]; k != "" {
			if l, ok := b.Lists[k].([]any); ok && len(l) > 0 {
				var names []string
				for _, v := range l {
					names = append(names, sanitize.Line(fmt.Sprint(v)))
				}
				out += "; valid: " + strings.Join(names, ", ")
			}
		}
		return out
	}
}

// refusals is THE word table: every refusal word the server publishes, in the order it evaluates
// them. Status is not part of the key; each word has one status on the server.
var refusals = map[string]refusal{
	"missing_token": {ExitAuth, func(_ *body, p string) string { return "no token was sent (missing_token); " + login(p) }},
	"invalid_token": {ExitAuth, func(_ *body, p string) string { return "the server refused the token (invalid_token); " + login(p) }},
	"expired_token": {ExitAuth, func(_ *body, p string) string {
		return "the session expired and could not be renewed (expired_token); " + login(p)
	}},
	"invalid_audience": {ExitAuth, func(_ *body, p string) string {
		return "the access token is for another resource (invalid_audience); " + login(p)
	}},
	"no_identity_provider": {ExitUnavail, fixed("the server has no identity provider configured (no_identity_provider); try again later")},
	"client_suspended":     {ExitForbidden, fixed("this client application is suspended (client_suspended); ask your organization admin")},
	"access_pending":       {ExitForbidden, fixed("this account has no entitlement yet (access_pending); request access at the Pharma Observatory site")},
	"missing_role": {ExitForbidden, func(b *body, _ string) string {
		if b.Role == "" {
			return "your organization lacks a role the API requires (missing_role); ask your organization admin"
		}
		return "your organization lacks the role " + sanitize.Line(b.Role) + " (missing_role); ask your organization admin"
	}},
	"not_entitled": {ExitForbidden, func(b *body, _ string) string {
		return "not entitled to this section (not_entitled)" + creditSuffix(b.License, b.Attribution)
	}},
	"unknown_page":          {ExitNotFound, serverMsg("unknown_page", "unknown page")},
	"unknown_section":       {ExitNotFound, serverMsg("unknown_section", "unknown section")},
	"not_found":             {ExitNotFound, serverMsg("not_found", "no such record")},
	"id_required":           {ExitUsage, serverMsg("id_required", "this command needs an id")},
	"id_not_accepted":       {ExitUsage, serverMsg("id_not_accepted", "this command takes no id")},
	"invalid_cursor":        {ExitUsage, serverMsg("invalid_cursor", "the cursor is not valid")},
	"unknown_sort":          {ExitUsage, serverMsg("unknown_sort", "unknown sort")},
	"unknown_dir":           {ExitUsage, serverMsg("unknown_dir", "dir is asc or desc")},
	"unknown_filter":        {ExitUsage, serverMsg("unknown_filter", "unknown filter")},
	"unsupported_parameter": {ExitUsage, serverMsg("unsupported_parameter", "unsupported parameter")},
	"generation_changed":    {ExitUnavail, fixed("the warehouse was rebuilt since this cursor was issued (generation_changed); restart from the first page")},
}

// FromResponse renders a non-200 API response as one plain line with the remedy. A known word maps
// by the table; a 429 is class 6 whatever its word; any other 5xx is class 7; an unknown word is
// class 1 with the raw body (never silently ok, never a crash). h may be nil.
func FromResponse(status int, h http.Header, raw []byte, profile string) error {
	var b body
	_ = json.Unmarshal(raw, &b)
	var all map[string]any
	if json.Unmarshal(raw, &all) == nil {
		b.Lists = all
	}
	if status == http.StatusTooManyRequests {
		return rateLimited(&b, h)
	}
	if r, ok := refusals[b.Error]; ok {
		return &ClassError{Class: r.class, Msg: r.msg(&b, profile)}
	}
	if status >= 500 {
		return Unavailable("the server answered HTTP %d", status)
	}
	text := sanitize.Line(string(raw))
	if r := []rune(text); len(r) > 300 {
		text = string(r[:300])
	}
	return fmt.Errorf("unexpected response: HTTP %d %s", status, text)
}

// retryAfter reads Retry-After as whole seconds (the form the server sends).
func retryAfter(h http.Header) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(h.Get("Retry-After")), "%d", &n); err != nil || n < 0 {
		return 0, false
	}
	if n > int64(MaxWait/time.Second) {
		// Bound before multiplying: n * time.Second wraps negative past about 9.2e9, and a negative
		// delay would pass the cap check. Over the cap, saturate rather than multiply.
		if n > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64), true
		}
	}
	return time.Duration(n) * time.Second, true
}

// RetryDelay is how long --wait should sleep for a 429's headers, and whether it should at all:
// there must be a Retry-After and it must not exceed MaxWait.
func RetryDelay(h http.Header) (time.Duration, bool) {
	d, ok := retryAfter(h)
	return d, ok && d <= MaxWait
}

// windowPhrase renders the server's window label naturally: "1 minute" and "24 hours" read "per 1
// minute" and "per 24 hours", while "per request" (a depth refusal) already carries its preposition
// and is used as sent, never "per per request".
func windowPhrase(label string) string {
	if strings.HasPrefix(label, "per ") {
		return label
	}
	return "per " + label
}

func rateLimited(b *body, h http.Header) error {
	ra, has := retryAfter(h)
	bk := b.Budget
	str := func(k string) string {
		if v, ok := bk[k]; ok && v != nil {
			return sanitize.Line(fmt.Sprint(v))
		}
		return ""
	}
	which := "a quota"
	if s := str("bucket"); s != "" {
		which = "the " + s + " quota"
	}
	var parts []string
	if l := str("calls_limit"); l != "" {
		p := "limit " + l
		if u := str("unit"); u != "" {
			p += " " + u
		}
		if w := str("window"); w != "" {
			p += " " + windowPhrase(w)
		}
		parts = append(parts, p)
	}
	if r := str("calls_remaining"); r != "" {
		parts = append(parts, r+" remaining")
	}
	detail := ""
	if len(parts) > 0 {
		detail = " (" + strings.Join(parts, ", ") + ")"
	}
	msg := "rate limited: " + which + " is used up" + detail
	if str("bucket") == "paging_depth" || !has {
		// No Retry-After: a depth refusal is refused again on every retry.
		msg = "rate limited: " + which + " refused this request" + detail + "; waiting cannot clear it, so narrow the query (add filters, or stop paging sooner)"
		if d := str("requested_depth"); d != "" {
			msg += "; it asked for depth " + d
		}
		return &RateLimited{Msg: msg}
	}
	if at := str("resets_at"); at != "" {
		msg += "; resets at " + at
	}
	secs := int64(ra / time.Second)
	if ra > MaxWait {
		msg += fmt.Sprintf("; try again in %ds (that is longer than --wait will sleep, %s)", secs, MaxWait)
	} else {
		msg += fmt.Sprintf("; try again in %ds, or pass --wait to wait and retry once", secs)
	}
	return &RateLimited{Msg: msg, RetryAfter: ra, HasRetryAfter: true}
}

// creditSuffix renders a refusal's license and attribution. The credit is shown; the restriction
// tier the body also carries is an internal label and is deliberately not printed.
func creditSuffix(license string, attribution any) string {
	var parts []string
	if license != "" {
		parts = append(parts, "license: "+sanitize.Line(license))
	}
	switch a := attribution.(type) {
	case string:
		if a != "" {
			parts = append(parts, "attribution: "+sanitize.Line(a))
		}
	case []any:
		for _, e := range a {
			if m, ok := e.(map[string]any); ok {
				name, _ := m["name"].(string)
				href, _ := m["href"].(string)
				parts = append(parts, "attribution: "+sanitize.Line(strings.TrimSpace(name+" "+href)))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}
