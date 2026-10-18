package apierr

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	in := "bad\x1b[31m red\x1b]0;title\x07 \r\nnext\x7f\u009b end"
	got := Sanitize(in)
	if strings.ContainsAny(got, "\x1b\x07\r\n\x7f\u009b") {
		t.Fatalf("control characters survived: %q", got)
	}
	if got != "bad[31m red]0;title next end" {
		t.Fatalf("%q", got)
	}
	if Sanitize("plain text, accents é") != "plain text, accents é" {
		t.Fatal("printable text altered")
	}
}

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
	err := FromResponse(418, []byte("\x1b[2Jteapot"), "p")
	if strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("%q", err)
	}
	err = FromResponse(403, []byte(`{"error":"missing_role","role":"a\u001b[0mb"}`), "p")
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "a[0mb") {
		t.Fatalf("%q", err)
	}
}
