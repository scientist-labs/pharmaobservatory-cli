package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd/pages"
)

// queryBody is a POST /api/v1/query answer in the published shape: positional rows beside columns.
const queryBody = `{"dataset":"biomarkers","columns":[{"name":"biomarker_kind","type":"text"},{"name":"n","type":"bigint"}],` +
	`"rows":[["gene",12431],["protein_variant",null]],"total_rows":3,"truncated":false,"result_id":"r-1","next_cursor":"c1",` +
	`"attribution":[{"name":"OpenFDA","license":"CC0","href":"https://open.fda.gov"}],"source_credit":"Source: OpenFDA",` +
	`"withheld_tiers":["licensable"],"withheld_columns":["secret_col"],"build_state":{"built":true,"generation":"g1","last_loaded":"2026-10-18"},` +
	`"notice":"Only the first 2 rows are shown."}`

// resultBody is a GET /api/v1/results/{result_id} page.
func resultBody(next string, rows string) string {
	n := "null"
	if next != "" {
		n = `"` + next + `"`
	}
	return `{"columns":[{"name":"biomarker_kind","type":"text"},{"name":"n","type":"bigint"}],"rows":` + rows +
		`,"total_rows":3,"result_id":"r-1","next_cursor":` + n + `,"attribution":[{"name":"OpenFDA","license":"CC0","href":"https://open.fda.gov"}]}`
}

type seenReq struct {
	method, uri, ctype, body string
}

// queryServer answers POST /api/v1/query with queryBody and each GET of the stored result from pages
// (keyed by the cursor), recording every request.
func queryServer(e *env, pages map[string]func(http.ResponseWriter)) *[]seenReq {
	var reqs []seenReq
	e.api = func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, seenReq{r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), string(b)})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/query" {
			_, _ = w.Write([]byte(queryBody))
			return
		}
		if f, ok := pages[r.URL.Query().Get("cursor")]; ok && strings.HasPrefix(r.URL.Path, "/api/v1/results/") {
			f(w)
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}
	return &reqs
}

func page(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(body)) }
}

func TestQueryPostsTheSQLAndRendersPositionalRows(t *testing.T) {
	e := loggedIn(t)
	reqs := queryServer(e, nil)
	sql := "SELECT biomarker_kind, count(*) AS n FROM biomarkers GROUP BY 1"
	out, err := e.po("query", sql)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := (*reqs)[0]
	if r.method != "POST" || r.uri != "/api/v1/query" || r.ctype != "application/json" {
		t.Fatalf("request: %+v", r)
	}
	var sent map[string]string
	if json.Unmarshal([]byte(r.body), &sent) != nil || sent["sql"] != sql || len(sent) != 1 {
		t.Fatalf("body: %s", r.body)
	}
	if want := "Bearer " + e.stored().AccessToken; e.apiAuth[0] != want {
		t.Fatal("bearer not sent from the stored credential")
	}
	want := "biomarker_kind\tn\ngene\t12431\nprotein_variant\t\n# Source: OpenFDA (CC0) https://open.fda.gov\n"
	if out != want {
		t.Fatalf("stdout:\n%q\nwant\n%q", out, want)
	}
	stderr := e.errBuf.String()
	for _, s := range []string{
		"OpenFDA (CC0) https://open.fda.gov",
		"notice: Only the first 2 rows are shown.",
		"withheld columns: secret_col",
		"as of 2026-10-18 (profile local)",
		"showing 2 of 3 stored; next: po query --result r-1 --cursor c1; result r-1",
	} {
		if !strings.Contains(stderr, s) {
			t.Errorf("stderr lacks %q:\n%s", s, stderr)
		}
	}
	if strings.Contains(stderr, "licensable") {
		t.Errorf("the withheld tier is an internal label and must not print:\n%s", stderr)
	}
}

func TestQueryQuietKeepsCreditAndNotice(t *testing.T) {
	e := loggedIn(t)
	queryServer(e, nil)
	if out, err := e.po("query", "SELECT 1 FROM biomarkers", "--quiet"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	stderr := e.errBuf.String()
	if !strings.Contains(stderr, "OpenFDA") || !strings.Contains(stderr, "notice:") {
		t.Fatalf("credit and notice must survive --quiet:\n%s", stderr)
	}
	if strings.Contains(stderr, "as of") || strings.Contains(stderr, "showing") {
		t.Fatalf("--quiet keeps the as-of or paging line:\n%s", stderr)
	}
}

func TestQueryReadsStdinForDash(t *testing.T) {
	e := loggedIn(t)
	reqs := queryServer(e, nil)
	e.stdin = "  SELECT count(*)\nFROM biomarkers\n\n"
	if out, err := e.po("query", "-"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains((*reqs)[0].body, `"SELECT count(*)\nFROM biomarkers"`) {
		t.Fatalf("body: %s", (*reqs)[0].body)
	}
	e.stdin = "  \n"
	if _, err := e.po("query", "-"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("empty stdin: %v", err)
	}
}

func TestQueryJSONIsTheRawBody(t *testing.T) {
	e := loggedIn(t)
	queryServer(e, nil)
	out, err := e.po("query", "SELECT 1 FROM biomarkers", "--json")
	if err != nil || out != queryBody+"\n" {
		t.Fatalf("%v\n%q", err, out)
	}
}

func TestQueryCSVHeaderIsColumnNamesAndCreditsTrail(t *testing.T) {
	e := loggedIn(t)
	queryServer(e, nil)
	out, err := e.po("query", "SELECT 1 FROM biomarkers", "--csv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "biomarker_kind,n\ngene,12431\nprotein_variant,\n# Source: OpenFDA") {
		t.Fatalf("%q", out)
	}
}

func TestQueryResultPagesAStoredResult(t *testing.T) {
	e := loggedIn(t)
	reqs := queryServer(e, map[string]func(http.ResponseWriter){"c 1/x": page(resultBody("", `[["x",1]]`))})
	out, err := e.po("query", "--result", "r/1", "--cursor", "c 1/x")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := (*reqs)[0]; got.method != "GET" || got.uri != "/api/v1/results/r%2F1?cursor=c+1%2Fx" {
		t.Fatalf("request: %+v", got)
	}
	if !strings.Contains(out, "x\t1\n") || !strings.Contains(e.errBuf.String(), "showing 1 of 3 stored; result r/1") {
		t.Fatalf("%s\n%s", out, e.errBuf.String())
	}
}

func TestQueryAllWalksTheStoredResult(t *testing.T) {
	e := loggedIn(t)
	reqs := queryServer(e, map[string]func(http.ResponseWriter){"c1": page(resultBody("", `[["last",3]]`))})
	out, err := e.po("query", "SELECT 1 FROM biomarkers", "--all")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(*reqs) != 2 || (*reqs)[1].uri != "/api/v1/results/r-1?cursor=c1" {
		t.Fatalf("requests: %+v", *reqs)
	}
	if !strings.Contains(out, "gene\t12431\nprotein_variant\t\nlast\t3\n") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(e.errBuf.String(), "fetched 3 rows of 3 stored") {
		t.Fatalf("%s", e.errBuf.String())
	}
}

func TestQueryAllStopsCleanlyOnResultExpired(t *testing.T) {
	e := loggedIn(t)
	queryServer(e, map[string]func(http.ResponseWriter){"c1": func(w http.ResponseWriter) {
		w.WriteHeader(410)
		_, _ = w.Write([]byte(`{"error_code":"RESULT_EXPIRED","message":"That result is gone.","hint":"Run the query again.","docs":"https://example.test/docs"}`))
	}})
	out, err := e.po("query", "SELECT 1 FROM biomarkers", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "RESULT_EXPIRED: That result is gone.; hint: Run the query again.") {
		t.Fatalf("exit %d: %v", apierr.ExitCode(err), err)
	}
	if !strings.Contains(out, "gene\t12431") || !strings.Contains(e.errBuf.String(), "partial: fetched 2 rows, then stopped (RESULT_EXPIRED") {
		t.Fatalf("%s\n%s", out, e.errBuf.String())
	}
}

func TestQueryAllStopsOnAGenerationChange(t *testing.T) {
	e := loggedIn(t)
	queryServer(e, map[string]func(http.ResponseWriter){"c1": func(w http.ResponseWriter) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error_code":"GENERATION_CHANGED","message":"The warehouse was rebuilt.","hint":null,"docs":"d"}`))
	}})
	_, err := e.po("query", "SELECT 1 FROM biomarkers", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "The warehouse was rebuilt.") {
		t.Fatalf("exit %d: %v", apierr.ExitCode(err), err)
	}
}

func TestQueryAllRefusesARepeatedCursor(t *testing.T) {
	e := loggedIn(t)
	reqs := queryServer(e, map[string]func(http.ResponseWriter){"c1": page(resultBody("c1", `[["again",1]]`))})
	_, err := e.po("query", "SELECT 1 FROM biomarkers", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "already fetched") || len(*reqs) != 2 {
		t.Fatalf("exit %d: %v (%d requests)", apierr.ExitCode(err), err, len(*reqs))
	}
}

func TestQueryAllStopsAtThePageCap(t *testing.T) {
	old := pages.MaxPages
	pages.MaxPages = 3
	t.Cleanup(func() { pages.MaxPages = old })
	e := loggedIn(t)
	pg := map[string]func(http.ResponseWriter){}
	for i := 1; i <= 10; i++ {
		pg[fmt.Sprintf("c%d", i)] = page(resultBody(fmt.Sprintf("c%d", i+1), `[["p",1]]`))
	}
	reqs := queryServer(e, pg)
	_, err := e.po("query", "SELECT 1 FROM biomarkers", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "3 page limit") || len(*reqs) != 3 {
		t.Fatalf("exit %d: %v (%d requests)", apierr.ExitCode(err), err, len(*reqs))
	}
}

// Every teaching code family, through the real transport, to its exit class, with the server's
// error_code, message, hint and docs on one sanitized line.
func TestQueryTeachingErrorsMapToTheExitTable(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		want   string
	}{
		{400, `{"error_code":"UNKNOWN_COLUMN","message":"No column nme in biomarkers.","hint":"Did you mean name?","docs":"https://example.test/agents","close_matches":["name"]}`,
			apierr.ExitUsage, "UNKNOWN_COLUMN: No column nme in biomarkers.; hint: Did you mean name?; docs: https://example.test/agents"},
		{400, `{"error_code":"JOIN_NOT_ALLOWED","message":"One dataset per query.","hint":null,"docs":"d"}`, apierr.ExitUsage, "JOIN_NOT_ALLOWED: One dataset per query.; docs: d"},
		{400, `{"error_code":"SQL_TOO_LONG","message":"Too long.","docs":"d"}`, apierr.ExitUsage, "SQL_TOO_LONG"},
		{413, `{"error_code":"RESULT_TOO_LARGE","message":"Too big.","docs":"d"}`, apierr.ExitUsage, "RESULT_TOO_LARGE"},
		{403, `{"error_code":"NOT_ENTITLED","message":"Not in your plan.","docs":"d","tier":"licensable","license":"Example License","attribution":[{"name":"Example Source","href":"https://example.test"}]}`,
			apierr.ExitForbidden, "NOT_ENTITLED: Not in your plan.; docs: d; license: Example License; attribution: Example Source https://example.test"},
		{410, `{"error_code":"RESULT_EXPIRED","message":"Gone.","docs":"d"}`, apierr.ExitNotFound, "RESULT_EXPIRED: Gone."},
		{404, `{"error_code":"UNKNOWN_RECORD","message":"No such record.","docs":"d"}`, apierr.ExitNotFound, "UNKNOWN_RECORD"},
		{504, `{"error_code":"TIMEOUT","message":"Took too long.","docs":"d"}`, apierr.ExitUnavail, "TIMEOUT: Took too long."},
		{500, `{"error_code":"QUERY_FAILED","message":"Failed.","docs":"d"}`, apierr.ExitUnavail, "QUERY_FAILED"},
		{409, `{"error_code":"GENERATION_CHANGED","message":"Rebuilt.","docs":"d"}`, apierr.ExitUnavail, "GENERATION_CHANGED"},
		{400, `{"error_code":"BRAND_NEW_CODE","message":"Something new.","docs":"d"}`, apierr.ExitUnexpected, "BRAND_NEW_CODE: Something new."},
		{503, `{"error_code":"BRAND_NEW_CODE","message":"Down.","docs":"d"}`, apierr.ExitUnavail, "BRAND_NEW_CODE: Down."},
	}
	for _, c := range cases {
		e := loggedIn(t)
		e.api = refuse(c.status, "", c.body)
		_, err := e.po("query", "SELECT nme FROM biomarkers")
		if err == nil || apierr.ExitCode(err) != c.code || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d %s: exit %d err %v", c.status, c.body, apierr.ExitCode(err), err)
			continue
		}
		if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "licensable") {
			t.Errorf("message is not one line, or prints the tier: %q", err)
		}
		e2 := loggedIn(t)
		e2.api = refuse(c.status, "", c.body)
		out, _ := e2.po("query", "SELECT nme FROM biomarkers", "--json")
		if out != c.body+"\n" {
			t.Errorf("--json must print the server body unchanged: %q", out)
		}
	}
}

func TestQuerySanitizesServerText(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"columns":[{"name":"na\u001b[31mme","type":"text"}],"rows":[["a\u001b]8;;http://x\u0007b\u202ec","=cmd\u0007"]],` +
			`"result_id":"r\u001b[2J1","next_cursor":"c\u0007","attribution":[{"name":"Src\u001b[0m","href":"h"}],"notice":"no\u001b[1mtice\nline"}`))
	}
	out, err := e.po("query", "SELECT 1 FROM biomarkers")
	if err != nil {
		t.Fatal(err)
	}
	all := out + e.errBuf.String()
	if strings.ContainsAny(all, "\x1b\x07\u202e") {
		t.Fatalf("control text reached the terminal: %q", all)
	}
	if !strings.HasPrefix(out, "name\t\nabc\t=cmd\n") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(all, "notice: notice line") {
		t.Fatalf("%q", all)
	}
	e2 := loggedIn(t)
	e2.api = refuse(400, "", "{\"error_code\":\"PARSE_ERROR\",\"message\":\"bad\\u001b[2J\\nsql\",\"hint\":\"h\\u0007int\",\"docs\":\"d\"}")
	_, err = e2.po("query", "SELECT")
	if strings.ContainsAny(err.Error(), "\x1b\x07\n") || !strings.Contains(err.Error(), "PARSE_ERROR: bad sql; hint: hint") {
		t.Fatalf("%q", err)
	}
}

func TestQueryExpiredTokenRefreshesAndResendsTheSQL(t *testing.T) {
	e := loggedIn(t)
	var bodies []string
	e.api = func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"expired_token"}`))
			return
		}
		_, _ = w.Write([]byte(queryBody))
	}
	if out, err := e.po("query", "SELECT 1 FROM biomarkers"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[1], "SELECT 1") || e.apiAuth[0] == e.apiAuth[1] {
		t.Fatalf("bodies %q", bodies)
	}
}

func TestQueryUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"query"},
		{"query", "a", "b"},
		{"query", "SELECT 1", "--result", "r"},
		{"query", "--cursor", "c"},
		{"query", "SELECT 1", "--json", "--all"},
		{"query", "SELECT 1", "--json", "--csv"},
	} {
		e := loggedIn(t)
		n := len(e.seen)
		_, err := e.po(args...)
		if apierr.ExitCode(err) != apierr.ExitUsage {
			t.Errorf("%v: exit %d %v", args, apierr.ExitCode(err), err)
		}
		if len(e.seen) != n {
			t.Errorf("%v: a usage error sent a request", args)
		}
	}
}

func TestQueryNotLoggedInIsAuth(t *testing.T) {
	e := loggedIn(t)
	if _, err := e.po("logout"); err != nil {
		t.Fatal(err)
	}
	n := len(e.seen)
	_, err := e.po("query", "SELECT 1")
	if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "not logged in to local") || len(e.seen) != n {
		t.Fatalf("exit %d: %v", apierr.ExitCode(err), err)
	}
}
