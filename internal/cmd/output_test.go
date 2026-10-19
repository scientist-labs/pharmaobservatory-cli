package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd/pages"
)

func page1(cursorNext, total, basis string) string {
	next := "null"
	if cursorNext != "" {
		next = `"` + cursorNext + `"`
	}
	return `{"page":"biomarkers","id":null,"section":"list","heading":"Biomarkers","lens":null,"web_url":"https://example.test/biomarkers",
"query":{"q":null,"filters":{},"sort":"symbol","dir":"asc","evidence_floor":false,"validated_floor":false},
"shape":{"kind":"table"},
"columns":[{"key":"symbol","label":"Symbol","type":"string"},{"key":"note","label":"Note","type":"string"},{"key":"n","label":"Count","type":"integer"}],
"rows":[{"symbol":"EGFR","note":"has, comma","n":3},{"symbol":"KRAS","note":"say \"hi\"","n":null}],
"attribution":[{"name":"HGNC","license":"CC0","href":"https://example.test/hgnc"},{"name":"Open Targets","license":null,"href":"https://example.test/ot"}],
"pagination":{"limit":2,"total":` + total + `,"total_basis":"` + basis + `","page":1,"next_cursor":` + next + `,"prev_cursor":null},
"build_state":{"built":true,"generation":"g7","last_loaded":"2026-10-17T06:00:00Z"},
"resolution_confidence":{"match_tier":"exact","verdict":"accepted"},
"tier":"licensable"}`
}

const lastPage = `{"query":{"q":null,"filters":{},"sort":"symbol","dir":"asc","evidence_floor":false,"validated_floor":false},
"shape":{"kind":"table"},
"columns":[{"key":"symbol","label":"Symbol","type":"string"},{"key":"note","label":"Note","type":"string"},{"key":"n","label":"Count","type":"integer"}],
"rows":[{"symbol":"BRAF","note":"","n":1}],
"attribution":[{"name":"HGNC","license":"CC0","href":"https://example.test/hgnc"}],
"pagination":{"limit":2,"total":3,"total_basis":"exact","page":2,"next_cursor":null,"prev_cursor":"c1"},
"build_state":{"built":true,"generation":"g7","last_loaded":"2026-10-17T06:00:00Z"}}`

func serve(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// po runs a command and returns stdout, stderr and the error.
func (e *env) split(args ...string) (string, string, error) {
	e.errBuf = &bytes.Buffer{}
	out, err := e.po(args...)
	// The fixtures are partial envelopes, so the SDK's typed decode warns; that path has its own
	// test in pages_test.go and is not what these assertions are about.
	var kept []string
	for _, l := range strings.SplitAfter(e.errBuf.String(), "\n") {
		if !strings.HasPrefix(l, "warning: the SDK") {
			kept = append(kept, l)
		}
	}
	return out, strings.Join(kept, ""), err
}

func TestTableFromColumnsIsTabSeparatedWithLabelsAndFooterOnStderr(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("c1", "12431", "exact"))
	out, errOut, err := e.split("biomarkers", "list")
	if err != nil {
		t.Fatal(err)
	}
	want := "Symbol\tNote\tCount\nEGFR\thas, comma\t3\nKRAS\tsay \"hi\"\t\n# Source: HGNC (CC0) https://example.test/hgnc\n# Source: Open Targets https://example.test/ot\n"
	if out != want {
		t.Fatalf("stdout %q want %q", out, want)
	}
	for _, s := range []string{
		"HGNC (CC0) https://example.test/hgnc\n",
		"Open Targets https://example.test/ot\n",
		"as of 2026-10-17T06:00:00Z (profile local)\n",
		"resolved: exact (accepted)\n",
		"https://example.test/biomarkers\n",
		"showing 2 of 12,431 (exact); next: --cursor c1\n",
	} {
		if !strings.Contains(errOut, s) {
			t.Errorf("footer missing %q in %q", s, errOut)
		}
	}
	if strings.Contains(strings.ReplaceAll(out, "# Source: ", ""), "as of") || strings.Contains(out, "showing") {
		t.Fatal("footer leaked to stdout")
	}
}

func TestCSVIsKeysThenRowsQuotedPerRFC4180(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("", "2", "exact"))
	out, errOut, err := e.split("biomarkers", "list", "--csv")
	if err != nil {
		t.Fatal(err)
	}
	want := "symbol,note,n\nEGFR,\"has, comma\",3\nKRAS,\"say \"\"hi\"\"\",\n# Source: HGNC (CC0) https://example.test/hgnc\n# Source: Open Targets https://example.test/ot\n"
	if out != want {
		t.Fatalf("csv %q want %q", out, want)
	}
	if !strings.Contains(errOut, "HGNC (CC0)") {
		t.Fatalf("no attribution on stderr: %q", errOut)
	}
}

func TestEstimatedTotalIsLabelledAsAnEstimate(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("c1", "12400", "estimated"))
	_, errOut, err := e.split("biomarkers", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "showing 2 of about 12,400 (estimated); next: --cursor c1") {
		t.Fatalf("%q", errOut)
	}
	if strings.Contains(errOut, "12,400 (exact)") || strings.Contains(errOut, "of 12,400") {
		t.Fatalf("an estimate was printed as exact: %q", errOut)
	}
}

// Attribution survives every flag combination and every format, and the tier is never printed.
func TestAttributionSurvivesEveryFlagCombination(t *testing.T) {
	combos := [][]string{
		{}, {"--csv"}, {"--quiet"}, {"--no-footer"}, {"--quiet", "--no-footer"}, {"--csv", "--quiet"},
		{"--csv", "--no-footer"}, {"--all"}, {"--all", "--quiet"}, {"--all", "--csv", "--no-footer"}, {"--limit", "2"},
		{"--cursor", "c1", "--quiet"},
	}
	for _, flags := range combos {
		e := loggedIn(t)
		e.api = serve(page1("", "2", "exact"))
		out, errOut, err := e.split(append([]string{"biomarkers", "list"}, flags...)...)
		if err != nil {
			t.Fatalf("%v: %v", flags, err)
		}
		for _, line := range []string{"HGNC (CC0) https://example.test/hgnc", "Open Targets https://example.test/ot"} {
			if !strings.Contains(errOut, line) {
				t.Errorf("%v: attribution %q missing from stderr %q", flags, line, errOut)
			}
		}
		// Redirected stdout (the test's is never a terminal) carries the credit in every table
		// and CSV mode, whatever --quiet and --no-footer say.
		for _, line := range []string{"# Source: HGNC (CC0) https://example.test/hgnc", "# Source: Open Targets https://example.test/ot"} {
			if !strings.Contains(out, line) {
				t.Errorf("%v: credit %q missing from stdout %q", flags, line, out)
			}
		}
		if strings.Contains(out+errOut, "licensable") {
			t.Errorf("%v: a tier word was printed", flags)
		}
		for _, f := range flags {
			if f == "--quiet" || f == "--no-footer" {
				if strings.Contains(errOut, "as of") || strings.Contains(errOut, "https://example.test/biomarkers") || strings.Contains(errOut, "showing") {
					t.Errorf("%v: non-attribution footer lines survived: %q", flags, errOut)
				}
			}
		}
	}
	// --json carries the attribution in the body and prints no footer.
	e := loggedIn(t)
	e.api = serve(page1("", "2", "exact"))
	out, errOut, err := e.split("biomarkers", "list", "--json")
	if err != nil || !strings.Contains(out, `"attribution"`) || errOut != "" {
		t.Fatalf("json: %v stdout=%q stderr=%q", err, out, errOut)
	}
}

func TestAllFollowsTheCursorWithOneHeaderAndCountsRows(t *testing.T) {
	e := loggedIn(t)
	var cursors []string
	e.api = func(w http.ResponseWriter, r *http.Request) {
		c := r.URL.Query().Get("cursor")
		cursors = append(cursors, c)
		if c == "" {
			serve(page1("c1", "3", "exact"))(w, r)
			return
		}
		serve(lastPage)(w, r)
	}
	out, errOut, err := e.split("biomarkers", "list", "--all", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cursors, "|") != "|c1" {
		t.Fatalf("cursors %v", cursors)
	}
	if n := strings.Count(out, "Symbol\t"); n != 1 {
		t.Fatalf("%d headers in %q", n, out)
	}
	if !strings.Contains(out, "BRAF") || !strings.Contains(out, "EGFR") {
		t.Fatalf("rows missing: %q", out)
	}
	if !strings.Contains(errOut, "fetched 3 rows; total 3 (exact)") {
		t.Fatalf("footer %q", errOut)
	}
	// the flag set is restored: a later plain call sends no cursor
	if _, err := e.po("biomarkers", "list", "--json"); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationChangedMidWalkRestartsOnce(t *testing.T) {
	e := loggedIn(t)
	var cursors []string
	conflicts := 0
	e.api = func(w http.ResponseWriter, r *http.Request) {
		c := r.URL.Query().Get("cursor")
		cursors = append(cursors, c)
		switch {
		case c == "":
			serve(page1("c1", "3", "exact"))(w, r)
		case conflicts == 0:
			conflicts++
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"generation_changed"}`))
		default:
			serve(lastPage)(w, r)
		}
	}
	out, errOut, err := e.split("biomarkers", "list", "--all")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if strings.Join(cursors, "|") != "|c1||c1" {
		t.Fatalf("cursors %v", cursors)
	}
	if strings.Count(out, "EGFR") != 1 {
		t.Fatalf("rows from the abandoned walk were kept: %q", out)
	}
	if !strings.Contains(errOut, "restarted once") || !strings.Contains(errOut, "fetched 3 rows") {
		t.Fatalf("footer %q", errOut)
	}
}

func TestGenerationChangedTwiceIsPartialExitNine(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			serve(page1("c1", "3", "exact"))(w, r)
			return
		}
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"generation_changed"}`))
	}
	out, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial {
		t.Fatalf("exit %d (%v)", apierr.ExitCode(err), err)
	}
	if !strings.Contains(out, "EGFR") || !strings.Contains(errOut, "partial: fetched 2 rows, then stopped (generation_changed twice)") {
		t.Fatalf("out %q err %q", out, errOut)
	}
	if !strings.Contains(errOut, "HGNC (CC0)") {
		t.Fatal("a partial result lost its attribution")
	}
}

func TestGenerationChangedOutsideAllIsUnavailable(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"generation_changed"}`))
	}
	out, _, err := e.split("biomarkers", "list", "--cursor", "stale")
	if apierr.ExitCode(err) != apierr.ExitUnavail || out != "" {
		t.Fatalf("exit %d out %q", apierr.ExitCode(err), out)
	}
}

func TestAnErrorAfterSomePagesIsPartial(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			serve(page1("c1", "3", "exact"))(w, r)
			return
		}
		w.WriteHeader(503)
	}
	out, _, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(out, "EGFR") {
		t.Fatalf("exit %d out %q", apierr.ExitCode(err), out)
	}
}

func TestUnbuiltIsNotAnEmptyTable(t *testing.T) {
	for _, flags := range [][]string{{}, {"--csv"}, {"--json"}} {
		e := loggedIn(t)
		e.api = serve(`{"error":"unbuilt","unbuilt_relations":["serving.a","serving.b"],"shape":{"kind":"table"},"rows":[],"message":"Not answered"}`)
		out, errOut, err := e.split(append([]string{"biomarkers", "list"}, flags...)...)
		if apierr.ExitCode(err) != apierr.ExitNotAnswered {
			t.Fatalf("%v: exit %d", flags, apierr.ExitCode(err))
		}
		if err.Error() != "not answered: serving.a, serving.b" {
			t.Fatalf("message %q", err)
		}
		if len(flags) == 1 && flags[0] == "--json" {
			if !strings.Contains(out, "unbuilt_relations") {
				t.Fatalf("json body not printed: %q", out)
			}
		} else if out != "" || errOut != "Error: not answered: serving.a, serving.b\n" {
			t.Fatalf("%v: output despite not answered: %q %q", flags, out, errOut)
		}
	}
}

func TestNotApplicablePrintsTheServerMessageWithItsOwnCode(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"error":"not_applicable","message":"applies to a gene or a protein variant, not to this kind of biomarker"}`)
	out, _, err := e.split("biomarkers", "get", "EGFR", "relations")
	if apierr.ExitCode(err) != apierr.ExitNotAnswered || out != "" {
		t.Fatalf("exit %d out %q", apierr.ExitCode(err), out)
	}
	if !strings.Contains(err.Error(), "applies to a gene or a protein variant") {
		t.Fatalf("message %q", err)
	}
	if apierr.ExitNotAnswered == apierr.ExitUnexpected {
		t.Fatal("code is not distinct")
	}
}

func TestNotEntitledPrintsCreditAndLicenseNotTheTier(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"not_entitled","tier":"prohibited","license":"CC BY-NC","attribution":"Data from Example Source under CC BY-NC"}`))
	}
	out, _, err := e.split("biomarkers", "list")
	if apierr.ExitCode(err) != apierr.ExitForbidden || out != "" {
		t.Fatalf("exit %d out %q", apierr.ExitCode(err), out)
	}
	for _, s := range []string{"license: CC BY-NC", "attribution: Data from Example Source under CC BY-NC"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("missing %q in %q", s, err)
		}
	}
	if strings.Contains(err.Error(), "prohibited") {
		t.Fatalf("tier word printed: %q", err)
	}
}

func TestChartAndKpiRenderThroughTheTablePath(t *testing.T) {
	for _, kind := range []string{"chart", "kpi", "attributes"} {
		e := loggedIn(t)
		e.api = serve(`{"page":"biomarkers","id":null,"section":"s","heading":"S","lens":null,"web_url":"https://example.test/s","shape":{"kind":"` + kind + `"},"columns":[{"key":"year","label":"Year","type":"integer"},{"key":"v","label":"Value","type":"number"}],"rows":[{"year":2024,"v":1.5}],"attribution":[],"build_state":{"built":true,"generation":"g1","last_loaded":null}}`)
		out, errOut, err := e.split("biomarkers", "list")
		if err != nil || out != "Year\tValue\n2024\t1.5\n" || strings.Contains(errOut, "[") {
			t.Fatalf("%s: %v %q %q", kind, err, out, errOut)
		}
	}
}

func TestGetPrintsIdentityThenTheSectionsIndexIncludingUnavailable(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"page":"biomarkers","id":"EGFR",
"identity":{"columns":[{"key":"key","label":"Field","type":"string"}],"rows":[{"key":"symbol","label":"Symbol","value":"EGFR"},{"key":"n","label":"Aliases","value":4},{"key":"x","label":"Empty","value":null}]},
"sections":[{"key":"relations","heading":"Relations","available":true,"kind":"table"},{"key":"assays","heading":"Assays","available":false,"kind":"table"}],
"attribution":[{"name":"HGNC","license":"CC0","href":"https://example.test/hgnc"}],
"web_url":"https://example.test/biomarkers/EGFR",
"build_state":{"built":true,"generation":"g7","last_loaded":null},
"resolution_confidence":{"match_tier":"exact","verdict":"accepted"}}`)
	out, errOut, err := e.split("biomarkers", "get", "EGFR")
	if err != nil {
		t.Fatal(err)
	}
	want := "field\tvalue\nSymbol\tEGFR\nAliases\t4\nEmpty\t\n\nsection\theading\tavailable\nrelations\tRelations\tyes\nassays\tAssays\tunavailable\n# Source: HGNC (CC0) https://example.test/hgnc\n"
	if out != want {
		t.Fatalf("%q want %q", out, want)
	}
	// last_loaded is null: the generation label is the as-of statement
	for _, s := range []string{"HGNC (CC0)", "as of generation g7 (profile local)", "resolved: exact (accepted)", "https://example.test/biomarkers/EGFR"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("footer missing %q in %q", s, errOut)
		}
	}
	csvOut, _, err := e.split("biomarkers", "get", "EGFR", "--csv")
	if err != nil || csvOut != "key,value\nsymbol,EGFR\nn,4\nx,\n# Source: HGNC (CC0) https://example.test/hgnc\n" {
		t.Fatalf("%v %q", err, csvOut)
	}
}

func TestOutputFlagConflictsAreUsageErrors(t *testing.T) {
	for _, flags := range [][]string{{"--json", "--csv"}, {"--json", "--all"}} {
		e := loggedIn(t)
		_, _, err := e.split(append([]string{"biomarkers", "list"}, flags...)...)
		if apierr.ExitCode(err) != apierr.ExitUsage {
			t.Errorf("%v: exit %d", flags, apierr.ExitCode(err))
		}
		if len(e.seen) != 0 {
			t.Errorf("%v: a request was made", flags)
		}
	}
}

func TestANonEnvelopeBodyIsNotRenderedAsATable(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(rawBody)
	out, _, err := e.split("biomarkers", "list")
	if err == nil || out != "" || apierr.ExitCode(err) != apierr.ExitUnexpected {
		t.Fatalf("%v %q", err, out)
	}
	e.api = serve(`[1,2]`)
	if _, _, err = e.split("biomarkers", "list"); err == nil {
		t.Fatal("array body accepted")
	}
}

func TestCursorAndLimitPassThrough(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("", "2", "exact"))
	if _, _, err := e.split("biomarkers", "list", "--limit", "2", "--cursor", "abc"); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://x" + e.lastSeen(t))
	if u.Query().Get("cursor") != "abc" || u.Query().Get("limit") != "2" {
		t.Fatalf("%s", e.lastSeen(t))
	}
}

func TestCSVCreditsFollowTheRows(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("", "2", "exact"))
	out, _, err := e.split("biomarkers", "list", "--csv", "--quiet", "--no-footer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "\n# Source: HGNC (CC0) https://example.test/hgnc\n# Source: Open Targets https://example.test/ot\n") {
		t.Fatalf("credits are not the trailing lines: %q", out)
	}
}

func TestAllStopsWhenACursorRepeats(t *testing.T) {
	e := loggedIn(t)
	calls := 0
	e.api = func(w http.ResponseWriter, r *http.Request) {
		calls++
		serve(page1("c1", "99", "exact"))(w, r)
	}
	out, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial {
		t.Fatalf("exit %d err %v", apierr.ExitCode(err), err)
	}
	if calls != 2 {
		t.Fatalf("%d calls, want 2 (first page, then c1 once)", calls)
	}
	if !strings.Contains(errOut, "partial: fetched 4 rows") || !strings.Contains(errOut, "already fetched (c1)") {
		t.Fatalf("stderr %q", errOut)
	}
	if !strings.Contains(out, "# Source: HGNC") {
		t.Fatalf("stdout %q", out)
	}
}

func TestAllStopsAtThePageCeiling(t *testing.T) {
	old := pages.MaxPages
	pages.MaxPages = 3
	defer func() { pages.MaxPages = old }()
	e := loggedIn(t)
	calls := 0
	e.api = func(w http.ResponseWriter, r *http.Request) {
		calls++
		serve(page1(fmt.Sprintf("c%d", calls), "99", "exact"))(w, r)
	}
	_, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial {
		t.Fatalf("exit %d err %v", apierr.ExitCode(err), err)
	}
	if calls != 3 {
		t.Fatalf("%d calls, want 3", calls)
	}
	if !strings.Contains(errOut, "partial: fetched 6 rows") || !strings.Contains(errOut, "3 page limit") {
		t.Fatalf("stderr %q", errOut)
	}
}

func TestAllRepeatedCursorWithEscapesLeavesStderrClean(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1(`c\u001b[2J\u0007`, "99", "exact"))
	_, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "already fetched (c)") {
		t.Fatalf("exit %d err %q", apierr.ExitCode(err), err)
	}
	if strings.ContainsAny(errOut+err.Error(), "\x1b\x07") {
		t.Fatalf("control text reached stderr: %q / %q", errOut, err)
	}
}
