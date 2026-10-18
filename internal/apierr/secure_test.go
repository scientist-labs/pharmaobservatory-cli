package apierr

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestRequireSecure(t *testing.T) {
	for _, ok := range []string{
		"https://pharmaobservatory.com", "http://127.0.0.1:3000", "http://[::1]:3000/x",
		"http://localhost:1/token", "HTTPS://Example.com", "http://LOCALHOST",
	} {
		if err := RequireSecure("origin", ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://example.com", "http://127.0.0.1.evil.com", "http://localhost.evil.com/x", "ftp://127.0.0.1",
		"//example.com", "example.com", "", "http://", "http://user@evil.com", "http://0.0.0.0:1", "http://[::2]/",
	} {
		err := RequireSecure("token endpoint", bad)
		if err == nil || ExitCode(err) != ExitUsage || !strings.Contains(err.Error(), "token endpoint") {
			t.Errorf("%q accepted or wrong class: %v", bad, err)
		}
	}
}

func TestFromResponseSanitizesServerText(t *testing.T) {
	err := FromResponse(418, nil, []byte("\x1b[2Jteapot"), "p")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("%q", err)
	}
	err = FromResponse(403, nil, []byte(`{"error":"missing_role","role":"a\u001b[0mb"}`), "p")
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "ab") {
		t.Fatalf("%q", err)
	}
}

const hostile = "\u2028\u2029\u202E\u200B"

func TestRefusalCreditAndMessageStripHostileUnicode(t *testing.T) {
	body := `{"error":"not_entitled","license":"CC\u2028BY\u202E","attribution":[{"name":"HG\u200BNC\u2029","href":"h\u202Etp"}]}`
	err := FromResponse(403, nil, []byte(body), "p")
	if m := err.Error(); strings.ContainsAny(m, hostile) || !strings.Contains(m, "license: CC BY") || !strings.Contains(m, "attribution: HGNC") {
		t.Fatalf("%q", err)
	}
	err = FromResponse(404, nil, []byte(`{"error":"not_found","message":"a\u2028b\u2029c\u202Ed\u200Be"}`), "p")
	if err.Error() != "a b cde (not_found)" {
		t.Fatalf("%q", err)
	}
	err = FromResponse(403, nil, []byte(`{"error":"not_entitled","attribution":"x\u2028y\u202Ez\u200B"}`), "p")
	if !strings.HasSuffix(err.Error(), "attribution: x yz") {
		t.Fatalf("%q", err)
	}
}

func TestHugeRetryAfterIsOverTheCapNotNegative(t *testing.T) {
	for _, v := range []string{"9300000000", "9223372036", "9223372037", "99999999999999999"} {
		h := http.Header{"Retry-After": {v}}
		d, ok := RetryDelay(h)
		if ok {
			t.Errorf("Retry-After %s should be over the cap, got delay %v", v, d)
		}
		if ra, has := retryAfter(h); has && ra <= MaxWait {
			t.Errorf("Retry-After %s wrapped to %v", v, ra)
		}
	}
	if d, ok := RetryDelay(http.Header{"Retry-After": {"120"}}); !ok || d != MaxWait {
		t.Fatalf("at the cap must wait: %v %v", d, ok)
	}
	err := FromResponse(429, http.Header{"Retry-After": {"9300000000"}}, []byte(`{"error":"quota_exceeded"}`), "p")
	var r *RateLimited
	if !errors.As(err, &r) || r.RetryAfter <= MaxWait || !strings.Contains(r.Msg, "longer than --wait will sleep") {
		t.Fatalf("%v", err)
	}
}

func TestRateLimitPhraseForEachWindowLabel(t *testing.T) {
	for _, c := range []struct{ unit, window, want string }{
		{"requests", "1 minute", "(limit 60 requests per 1 minute, 0 remaining)"},
		{"rows", "24 hours", "(limit 60 rows per 24 hours, 0 remaining)"},
		{"rows", "per request", "(limit 60 rows per request, 0 remaining)"},
	} {
		body := `{"error":"quota_exceeded","budget":{"unit":"` + c.unit + `","calls_limit":60,"calls_remaining":0,"window":"` + c.window + `"}}`
		msg := FromResponse(429, http.Header{"Retry-After": {"5"}}, []byte(body), "p").Error()
		if !strings.Contains(msg, c.want) || strings.Contains(msg, "per per") {
			t.Errorf("%s: %q", c.window, msg)
		}
	}
}
