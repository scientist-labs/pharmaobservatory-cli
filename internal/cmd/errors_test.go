package cmd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// quotaBody is the server's 429 body shape (flat budget, as MachineQuota renders it).
const quotaBody = `{"error":"quota_exceeded","budget":{"bucket":"requests_per_minute","unit":"requests","calls_remaining":0,"calls_limit":60,"window":"1 minute","resets_at":"2026-10-18T12:01:00Z","keyed_by":"organization","enforced":true}}`

func refuse(status int, retryAfter, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// Every refusal word, through the real transport, to its exit class and a message naming it.
func TestEveryRefusalWordMapsThroughTheCommands(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		want   string
	}{
		{401, `{"error":"missing_token"}`, apierr.ExitAuth, "po login --profile local"},
		{401, `{"error":"invalid_token"}`, apierr.ExitAuth, "po login --profile local"},
		{401, `{"error":"invalid_audience"}`, apierr.ExitAuth, "po login --profile local"},
		{503, `{"error":"no_identity_provider"}`, apierr.ExitUnavail, "no_identity_provider"},
		{403, `{"error":"client_suspended"}`, apierr.ExitForbidden, "suspended"},
		{403, `{"error":"access_pending"}`, apierr.ExitForbidden, "access_pending"},
		{403, `{"error":"missing_role","role":"example:api"}`, apierr.ExitForbidden, "role example:api"},
		{403, `{"error":"not_entitled","tier":"licensable","license":"CC-BY","attribution":"Example Source"}`, apierr.ExitForbidden, "attribution: Example Source"},
		{404, `{"error":"unknown_page","message":"Unknown page \"x\".","valid_pages":["biomarkers","orgs"]}`, apierr.ExitNotFound, "valid: biomarkers, orgs"},
		{404, `{"error":"unknown_section","message":"no section","valid_sections":["a","b"]}`, apierr.ExitNotFound, "valid: a, b"},
		{404, `{"error":"not_found","message":"No record found for \"x\"."}`, apierr.ExitNotFound, "No record found"},
		{400, `{"error":"id_required","message":"needs an id."}`, apierr.ExitUsage, "needs an id"},
		{400, `{"error":"id_not_accepted","message":"takes no id."}`, apierr.ExitUsage, "takes no id"},
		{400, `{"error":"invalid_cursor","message":"The cursor is not valid."}`, apierr.ExitUsage, "cursor"},
		{400, `{"error":"unknown_sort","message":"Unknown sort \"z\".","valid_sorts":["name"]}`, apierr.ExitUsage, "valid: name"},
		{400, `{"error":"unknown_filter","message":"Unknown filter q.","valid_filters":["kind"]}`, apierr.ExitUsage, "valid: kind"},
		{409, `{"error":"generation_changed"}`, apierr.ExitUnavail, "restart from the first page"},
		{500, `oops`, apierr.ExitUnavail, "HTTP 500"},
		{418, `{"error":"teapot"}`, apierr.ExitUnexpected, "teapot"},
	}
	for _, c := range cases {
		e := loggedIn(t)
		e.api = refuse(c.status, "", c.body)
		out, err := e.po("cell-lines", "list", "--json")
		if err == nil || apierr.ExitCode(err) != c.code || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d %s: exit %d err %v", c.status, c.body, apierr.ExitCode(err), err)
		}
		if !strings.Contains(out, c.body) {
			t.Errorf("--json must print the server body unchanged: %q", out)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("message is not one line: %q", err)
		}
	}
}

func TestRefusalMessageSanitizesServerText(t *testing.T) {
	e := loggedIn(t)
	e.api = refuse(404, "", "{\"error\":\"unknown_page\",\"message\":\"bad\\u001b[2Jpage\",\"valid_pages\":[\"a\\u0007b\"]}")
	_, err := e.po("cell-lines", "list")
	if strings.ContainsAny(err.Error(), "\x1b\x07") || !strings.Contains(err.Error(), "ab") {
		t.Fatalf("%q", err)
	}
}

func TestQuotaWithRetryAfterDoesNotWaitByDefault(t *testing.T) {
	e := loggedIn(t)
	e.api = refuse(429, "42", quotaBody)
	_, err := e.po("cell-lines", "list")
	if apierr.ExitCode(err) != apierr.ExitRateLimited {
		t.Fatalf("exit %d: %v", apierr.ExitCode(err), err)
	}
	for _, want := range []string{"requests_per_minute", "limit 60 requests per 1 minute", "0 remaining", "resets at 2026-10-18T12:01:00Z", "try again in 42s", "--wait"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %q", want, err)
		}
	}
	if len(e.seen) != 1 || len(e.sleeps) != 0 {
		t.Fatalf("requests %d sleeps %v: the default must not wait or retry", len(e.seen), e.sleeps)
	}
}

func TestQuotaWithoutRetryAfterIsADepthRefusal(t *testing.T) {
	body := `{"error":"quota_exceeded","budget":{"bucket":"paging_depth","unit":"rows","calls_remaining":0,"calls_limit":1000,"window":"per request","resets_at":null,"enforced":true,"requested_depth":5000}}`
	for _, cmd := range [][]string{{"cell-lines", "list", "--wait"}, {"whoami", "--wait"}} {
		e := loggedIn(t)
		e.api = refuse(429, "", body)
		e.whoami = refuse(429, "", body)
		_, err := e.po(cmd...)
		if apierr.ExitCode(err) != apierr.ExitRateLimited {
			t.Fatalf("%v exit %d", cmd, apierr.ExitCode(err))
		}
		for _, want := range []string{"paging_depth", "waiting cannot clear it", "narrow the query", "depth 5000"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%v: missing %q in %q", cmd, want, err)
			}
		}
		if len(e.sleeps) != 0 || len(e.seen)+e.whoamis != 1 {
			t.Errorf("%v: --wait must not retry a refusal with no Retry-After (sleeps %v)", cmd, e.sleeps)
		}
	}
}

func TestWaitHonoursRetryAfterAndRetriesOnce(t *testing.T) {
	for _, cmd := range [][]string{{"cell-lines", "list", "--json", "--wait"}, {"whoami", "--json", "--wait"}, {"api", "cell-lines", "--wait"}} {
		e := loggedIn(t)
		calls := 0
		ok := func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				refuse(429, "7", quotaBody)(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"sub":"u","roles":[]}`))
		}
		e.api, e.whoami = ok, ok
		_, err := e.po(cmd...)
		if err != nil || calls != 2 || len(e.sleeps) != 1 || e.sleeps[0] != 7*time.Second {
			t.Fatalf("%v: err %v calls %d sleeps %v", cmd, err, calls, e.sleeps)
		}
		if !strings.Contains(e.notice.String(), "waiting 7s") {
			t.Errorf("%v: no notice: %q", cmd, e.notice.String())
		}
	}
}

func TestWaitRetriesOnlyOnceAndThenReports(t *testing.T) {
	for _, cmd := range [][]string{{"cell-lines", "list", "--wait"}, {"whoami", "--wait"}} {
		e := loggedIn(t)
		e.api = refuse(429, "3", quotaBody)
		e.whoami = refuse(429, "3", quotaBody)
		_, err := e.po(cmd...)
		if apierr.ExitCode(err) != apierr.ExitRateLimited || len(e.sleeps) != 1 || len(e.seen)+e.whoamis != 2 {
			t.Fatalf("%v: exit %d sleeps %v requests %d", cmd, apierr.ExitCode(err), e.sleeps, len(e.seen)+e.whoamis)
		}
	}
}

func TestWaitCapsAtMaxWait(t *testing.T) {
	for _, cmd := range [][]string{{"cell-lines", "list", "--wait"}, {"whoami", "--wait"}} {
		e := loggedIn(t)
		e.api = refuse(429, "3600", quotaBody)
		e.whoami = refuse(429, "3600", quotaBody)
		_, err := e.po(cmd...)
		if apierr.ExitCode(err) != apierr.ExitRateLimited || len(e.sleeps) != 0 {
			t.Fatalf("%v: exit %d sleeps %v: a Retry-After past the cap must not be slept", cmd, apierr.ExitCode(err), e.sleeps)
		}
		if !strings.Contains(err.Error(), "try again in 3600s") || !strings.Contains(err.Error(), "longer than --wait will sleep") {
			t.Errorf("%v: %q", cmd, err)
		}
	}
	// The boundary: exactly MaxWait is slept.
	e := loggedIn(t)
	e.api = refuse(429, "120", quotaBody)
	_, _ = e.po("cell-lines", "list", "--wait")
	if len(e.sleeps) != 1 || e.sleeps[0] != apierr.MaxWait {
		t.Fatalf("sleeps %v", e.sleeps)
	}
}

func TestQuotaBodyWithoutBudgetStillRateLimited(t *testing.T) {
	e := loggedIn(t)
	e.api = refuse(429, "5", `slow down`)
	_, err := e.po("cell-lines", "list")
	if apierr.ExitCode(err) != apierr.ExitRateLimited || !strings.Contains(err.Error(), "try again in 5s") {
		t.Fatalf("%v", err)
	}
}

func TestAllWalkMarksAPartialResultOnQuota(t *testing.T) {
	e := loggedIn(t)
	n := 0
	e.api = func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			_, _ = w.Write([]byte(`{"columns":[{"key":"a","label":"A"}],"rows":[{"a":"1"}],"pagination":{"next_cursor":"c2"}}`))
			return
		}
		refuse(429, "9", quotaBody)(w, r)
	}
	_, err := e.po("cell-lines", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("exit %d: %v", apierr.ExitCode(err), err)
	}
}

func TestStrayArgumentIsUsageEverywhere(t *testing.T) {
	e := loggedIn(t)
	for _, args := range [][]string{{"version", "x"}, {"whoami", "x"}, {"logout", "x"}, {"login", "x"}, {"auth", "refresh", "x"}, {"exit-codes", "x"}} {
		_, err := e.po(args...)
		if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "unexpected argument") {
			t.Errorf("%v: exit %d %v", args, apierr.ExitCode(err), err)
		}
	}
	if e.whoamis != 0 {
		t.Fatal("a stray argument reached the server")
	}
}

func TestExitCodesPrintsTheSingleSourceTable(t *testing.T) {
	e := newEnv(t)
	out, err := e.run("exit-codes")
	if err != nil || out != apierr.TableText() {
		t.Fatalf("%v %q", err, out)
	}
	out, _ = e.run("help", "exit-codes")
	if !strings.Contains(out, "rate limited") {
		t.Fatalf("help topic: %q", out)
	}
	out, _ = e.run("exit-codes", "--markdown")
	if out != apierr.TableMarkdown() {
		t.Fatalf("%q", out)
	}
}

func TestUnreachableServerIsUnavailableWithoutAStackTrace(t *testing.T) {
	e := loggedIn(t)
	e.srv.Close()
	_, err := e.po("whoami")
	if apierr.ExitCode(err) != apierr.ExitUnavail || strings.Contains(err.Error(), "goroutine") {
		t.Fatalf("%v", err)
	}
}
