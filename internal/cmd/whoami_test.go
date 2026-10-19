package cmd

import (
	"net/http"
	"strings"
	"testing"
)

// fullWhoami is the #5082 payload with every field set, in the documented shape. The odd spacing
// lets the --json test prove the body is passed through byte for byte.
const fullWhoami = `{"ok":true,  "sub":"user_1","roles":["example:api","example:read"],"name":"Ada Lovelace","email":"ada@example.test",` +
	`"organization":{"id":7,"workos_id":"org_01","name":"Example Labs"},"plan":{"name":"pro","status":"active","entitled":true},` +
	`"limits":{"defaults_from":"plan","requests_per_minute":60,"requests_per_day":10000,"rows_per_day":500000,"paging_depth":10000,` +
	`"rwd_catalogue_calls_per_day":100,"benzinga_calls_per_day":50,"query_seconds_per_day":600},` +
	`"quota":{"keyed_by":"organization","buckets":{` +
	`"rows_per_day":{"unit":"rows","window":"1 day","limit":500000,"used":12431,"remaining":487569,"resets_at":"2026-10-20T00:00:00Z"},` +
	`"requests_per_minute":{"unit":"requests","window":"1 minute","limit":60,"used":1,"remaining":59,"resets_at":"2026-10-19T12:01:00Z"},` +
	`"requests_per_day":{"unit":"requests","window":"1 day","limit":10000,"used":40,"remaining":9960,"resets_at":"2026-10-20T00:00:00Z"},` +
	`"query_seconds_per_day":{"unit":"seconds","window":"1 day","limit":600,"used":0,"remaining":600,"resets_at":"2026-10-20T00:00:00Z"},` +
	`"rwd_catalogue_calls_per_day":{"unit":"calls","window":"1 day","limit":100,"used":0,"remaining":100,"resets_at":"2026-10-20T00:00:00Z"},` +
	`"benzinga_calls_per_day":{"unit":"calls","window":"1 day","limit":50,"used":5,"remaining":45,"resets_at":"2026-10-20T00:00:00Z"}}}}`

func whoamiEnv(t *testing.T, body string) *env {
	t.Helper()
	e := newEnv(t)
	e.stdin = "c\n"
	if _, err := e.login("--no-browser"); err != nil {
		t.Fatal(err)
	}
	e.whoami = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
	return e
}

func TestWhoamiRendersTheFullProfile(t *testing.T) {
	e := whoamiEnv(t, fullWhoami)
	out, err := e.run("whoami", "--profile", "local")
	if err != nil {
		t.Fatal(err)
	}
	want := "profile: local\n" +
		"origin:  " + e.srv.URL + "\n" +
		"sub:     user_1\n" +
		"name:    Ada Lovelace\n" +
		"email:   ada@example.test\n" +
		"org:     Example Labs\n" +
		"plan:    pro (active)\n" +
		"roles:   example:api, example:read\n" +
		"quota:   remaining per bucket, keyed by organization\n" +
		"  requests_per_minute          59 of 60 requests left per 1 minute; resets 2026-10-19T12:01:00Z\n" +
		"  requests_per_day             9960 of 10000 requests left per 1 day; resets 2026-10-20T00:00:00Z\n" +
		"  rows_per_day                 487569 of 500000 rows left per 1 day; resets 2026-10-20T00:00:00Z\n" +
		"  query_seconds_per_day        600 of 600 seconds left per 1 day; resets 2026-10-20T00:00:00Z\n" +
		"  rwd_catalogue_calls_per_day  100 of 100 calls left per 1 day; resets 2026-10-20T00:00:00Z\n" +
		"  benzinga_calls_per_day       45 of 50 calls left per 1 day; resets 2026-10-20T00:00:00Z\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestWhoamiJSONIsTheBodyVerbatim(t *testing.T) {
	e := whoamiEnv(t, fullWhoami)
	out, err := e.run("whoami", "--profile", "local", "--json")
	if err != nil || out != fullWhoami+"\n" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestWhoamiNullsAndMissingFieldsPrintPlaceholders(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"organization and plan null, name and email null": {
			`{"ok":true,"sub":"user_1","roles":[],"name":null,"email":null,"organization":null,"plan":null,` +
				`"quota":{"keyed_by":"user","buckets":{"requests_per_day":{"unit":"requests","window":"1 day","limit":null,"used":0,"remaining":null,"resets_at":null}}}}`,
			[]string{"name:    (not provided)\n", "email:   (not provided)\n", "org:     (none: this login is not tied to an organization)\n",
				"plan:    (none: no current subscription)\n", "roles:   (not provided)\n", "quota:   remaining per bucket, keyed by user\n",
				"  requests_per_day  ? of ? requests left per 1 day\n"},
		},
		"a server from before #5082": {
			`{"ok":true,"sub":"user_1","roles":["example:api"]}`,
			[]string{"name:    (not provided)\n", "email:   (not provided)\n", "org:     (none: this login is not tied to an organization)\n",
				"plan:    (none: no current subscription)\n", "roles:   example:api\n", "quota:   (not reported)\n"},
		},
		"an organization and plan with null members, a lapsed plan": {
			`{"ok":true,"sub":"user_1","roles":[],"organization":{"id":1,"workos_id":"org_1","name":null},"plan":{"name":"pro","status":"past_due","entitled":false},"quota":null}`,
			[]string{"org:     (not provided)\n", "plan:    pro (past_due, not entitled)\n", "quota:   (not reported)\n"},
		},
	}
	for name, c := range cases {
		e := whoamiEnv(t, c.body)
		out, err := e.run("whoami", "--profile", "local")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in\n%s", name, w, out)
			}
		}
	}
}

func TestWhoamiSanitizesEveryServerValue(t *testing.T) {
	esc := `\u001b[2J\u001b]8;;http://x\u0007L\nFAKE: line\u0000\u202eEVIL`
	body := `{"ok":true,"sub":"u` + esc + `","roles":["r` + esc + `"],"name":"n` + esc + `","email":"e` + esc + `",` +
		`"organization":{"id":1,"workos_id":"w","name":"o` + esc + `"},"plan":{"name":"p` + esc + `","status":"s` + esc + `","entitled":true},` +
		`"quota":{"keyed_by":"k` + esc + `","buckets":{"b` + esc + `":{"unit":"u` + esc + `","window":"w` + esc + `","limit":1,"used":0,"remaining":"x` + esc + `","resets_at":"t` + esc + `"}}}}`
	e := whoamiEnv(t, body)
	out, err := e.run("whoami", "--profile", "local")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07\x00\u202e") {
		t.Fatalf("control text reached the terminal: %q", out)
	}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(l, "FAKE") || strings.Contains(l, "\r") {
			t.Fatalf("a server value forged a line: %q", out)
		}
	}
	if !strings.Contains(out, "name:    nL FAKE: lineEVIL\n") {
		t.Fatalf("%q", out)
	}
}

// A bucket whose value is not an object, under an escape-laden key, renders a placeholder; nothing
// the server sent reaches the terminal or the error line raw.
func TestWhoamiNonObjectBucketUnderAHostileKey(t *testing.T) {
	key := `\u001b[31m\u001b]8;;http://x\u0007K\u0000\u202e\nFAKE: line`
	e := whoamiEnv(t, `{"sub":"u","quota":{"keyed_by":"user","buckets":{"`+key+`":5,"":{"remaining":1,"limit":2}}}}`)
	out, err := e.run("whoami", "--profile", "local")
	if err != nil {
		t.Fatalf("a non-object bucket must not fail the command: %v", err)
	}
	if strings.ContainsAny(out, "\x1b\x07\x00\u202e") || strings.Contains(out, "\nFAKE") {
		t.Fatalf("server text reached the terminal: %q", out)
	}
	if !strings.Contains(out, "K FAKE: line  (not provided)\n") || !strings.Contains(out, "  (unnamed)") || !strings.Contains(out, "1 of 2 left") {
		t.Fatalf("%q", out)
	}
}

// Fields of an unexpected type render placeholders instead of failing.
func TestWhoamiWrongTypesRenderPlaceholders(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"organization, plan, roles and quota as strings": {
			`{"sub":"u","organization":"Example","plan":"pro","roles":"admin","quota":"lots"}`,
			[]string{"org:     (not provided)\n", "plan:    (not provided)\n", "roles:   (not provided)\n", "quota:   (not reported)\n"},
		},
		"buckets as an array": {
			`{"sub":"u","quota":{"keyed_by":"user","buckets":[1,2]}}`,
			[]string{"quota:   (not reported)\n"},
		},
		"sub, name and email as objects or numbers": {
			`{"sub":7,"name":{"first":"Ada"},"email":["a@example.test"]}`,
			[]string{"sub:     7\n", "name:    {\"first\":\"Ada\"}\n", "email:   [\"a@example.test\"]\n"},
		},
	}
	for name, c := range cases {
		e := whoamiEnv(t, c.body)
		out, err := e.run("whoami", "--profile", "local")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q in\n%s", name, w, out)
			}
		}
	}
}

// A body that is not a JSON object, or not JSON at all, is an error that names no server text.
func TestWhoamiUnreadableBodyErrorIsClean(t *testing.T) {
	for _, body := range []string{"[1,2]", `"just a string"`, "\u202e\x1b[2Jnot json", `{"sub":"u",` + "\u202e" + `}`} {
		e := whoamiEnv(t, body)
		_, err := e.run("whoami", "--profile", "local")
		if err == nil || !strings.HasPrefix(err.Error(), "whoami: unreadable response") {
			t.Fatalf("%q: %v", body, err)
		}
		if strings.ContainsAny(err.Error(), "\x1b\u202e\n") {
			t.Fatalf("%q: server text in the error: %q", body, err)
		}
	}
}
