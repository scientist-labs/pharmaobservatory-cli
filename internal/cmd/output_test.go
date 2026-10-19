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

// page1 is the first page of a dataset read in the published shape: columns named, rows keyed by
// column name, total and next_cursor at the top level. total is a JSON number or null (a grouped
// read has no total). The withheld tier and the restriction block carry a tier word that must never
// be printed.
func page1(cursorNext, total string) string {
	next := "null"
	if cursorNext != "" {
		next = `"` + cursorNext + `"`
	}
	return `{"dataset":"biomarkers","grain":"one row per biomarker","object_type":null,"id":null,"scope":null,
"columns":[{"name":"symbol","type":"text","unit":null,"description":"The gene symbol."},{"name":"note","type":"text"},{"name":"n","type":"integer"}],
"rows":[{"symbol":"EGFR","note":"has, comma","n":3},{"symbol":"KRAS","note":"say \"hi\"","n":null}],
"total":` + total + `,"next_cursor":` + next + `,"sort":["symbol"],"filters":[],"group_by":[],"measures":[],
"attribution":[{"name":"HGNC","license":"CC0","href":"https://example.test/hgnc"},{"name":"Open Targets","license":null,"href":"https://example.test/ot"}],
"source_credit":"Source: HGNC (CC0); Open Targets","withheld_tiers":["licensable"],"withheld_columns":[],
"freshness":{"source":"hgnc","last_loaded":"2026-10-17T06:00:00Z"},"build":{"generation":"g7","fingerprint":"f"},
"build_state":{"built":true,"generation":"g7","last_loaded":"2026-10-17T06:00:00Z"},
"restriction":{"tier":"licensable"}}`
}

const lastPage = `{"dataset":"biomarkers","grain":"one row per biomarker",
"columns":[{"name":"symbol","type":"text"},{"name":"note","type":"text"},{"name":"n","type":"integer"}],
"rows":[{"symbol":"BRAF","note":"","n":1}],"total":3,"next_cursor":null,
"attribution":[{"name":"HGNC","license":"CC0","href":"https://example.test/hgnc"}],
"build_state":{"built":true,"generation":"g7","last_loaded":"2026-10-17T06:00:00Z"}}`

func serve(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// pharma runs a command and returns stdout, stderr and the error.
func (e *env) split(args ...string) (string, string, error) {
	e.errBuf = &bytes.Buffer{}
	out, err := e.pharma(args...)
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

func TestTableFromColumnsIsTabSeparatedWithNamesAndFooterOnStderr(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("c1", "12431"))
	out, errOut, err := e.split("biomarkers", "list")
	if err != nil {
		t.Fatal(err)
	}
	want := "symbol\tnote\tn\nEGFR\thas, comma\t3\nKRAS\tsay \"hi\"\t\n# Source: HGNC (CC0) https://example.test/hgnc\n# Source: Open Targets https://example.test/ot\n"
	if out != want {
		t.Fatalf("stdout %q want %q", out, want)
	}
	for _, s := range []string{
		"HGNC (CC0) https://example.test/hgnc\n",
		"Open Targets https://example.test/ot\n",
		"as of 2026-10-17T06:00:00Z (profile local)\n",
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
	e.api = serve(page1("", "2"))
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

func TestAReadWithNoTotalSaysOnlyWhatItShows(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("c1", "null"))
	_, errOut, err := e.split("biomarkers", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "showing 2; next: --cursor c1") || strings.Contains(errOut, "showing 2 of") {
		t.Fatalf("%q", errOut)
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
		e.api = serve(page1("", "2"))
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
				if strings.Contains(errOut, "as of") || strings.Contains(errOut, "showing") {
					t.Errorf("%v: non-attribution footer lines survived: %q", flags, errOut)
				}
			}
		}
	}
	// --json carries the attribution in the body and prints no footer.
	e := loggedIn(t)
	e.api = serve(page1("", "2"))
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
			serve(page1("c1", "3"))(w, r)
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
	if n := strings.Count(out, "symbol\t"); n != 1 {
		t.Fatalf("%d headers in %q", n, out)
	}
	if !strings.Contains(out, "BRAF") || !strings.Contains(out, "EGFR") {
		t.Fatalf("rows missing: %q", out)
	}
	if !strings.Contains(errOut, "fetched 3 rows; total 3 (exact)") {
		t.Fatalf("footer %q", errOut)
	}
	// the flag set is restored: a later plain call sends no cursor
	if _, err := e.pharma("biomarkers", "list", "--json"); err != nil {
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
			serve(page1("c1", "3"))(w, r)
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
			serve(page1("c1", "3"))(w, r)
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
			serve(page1("c1", "3"))(w, r)
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
	out, _, err := e.split("biomarkers", "get", "EGFR", "biomarker-evidence")
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

func TestAGroupedReadRendersItsOutputColumns(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"dataset":"biomarkers","grain":"g","columns":[{"name":"biomarker_kind","type":"text"},{"name":"count","type":"bigint"}],` +
		`"rows":[{"biomarker_kind":"gene","count":12431},{"biomarker_kind":"protein_variant","count":7}],"total":null,"next_cursor":null,` +
		`"group_by":["biomarker_kind"],"measures":["count"],"attribution":[],"source_credit":"Source: US FDA","build_state":{"built":true,"generation":"g1","last_loaded":null}}`)
	out, errOut, err := e.split("biomarkers", "list", "--group-by", "biomarker_kind", "--measure", "count")
	if err != nil || out != "biomarker_kind\tcount\ngene\t12431\nprotein_variant\t7\n# Source: US FDA\n" {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	if got := e.lastSeen(t); got != "/api/v1/biomarkers?group_by=biomarker_kind&measure=count" {
		t.Fatalf("request: %s", got)
	}
	if !strings.Contains(errOut, "US FDA\n") || !strings.Contains(errOut, "showing 2\n") {
		t.Fatalf("the source_credit is the credit when no attribution list is sent: %q", errOut)
	}
}

func TestWithheldColumnsAreNamedButTheTierNever(t *testing.T) {
	e := loggedIn(t)
	body := strings.Replace(page1("", "2"), `"withheld_columns":[]`, `"withheld_columns":["ticker","source_link"]`, 1)
	e.api = serve(body)
	for _, flags := range [][]string{{}, {"--quiet"}, {"--csv"}} {
		out, errOut, err := e.split(append([]string{"biomarkers", "list"}, flags...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(errOut, "withheld columns: ticker, source_link\n") {
			t.Errorf("%v: %q", flags, errOut)
		}
		if strings.Contains(out+errOut, "licensable") {
			t.Errorf("%v: the tier was printed", flags)
		}
	}
}

func TestFilterReachesEveryPageOfAWalk(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			serve(page1("c1", "3"))(w, r)
			return
		}
		serve(lastPage)(w, r)
	}
	if _, _, err := e.split("biomarkers", "list", "--filter", "biomarker_kind=gene", "--filter", "n.gt=2", "--all", "--limit", "2"); err != nil {
		t.Fatal(err)
	}
	if len(e.seen) != 2 || e.seen[0] != "/api/v1/biomarkers?limit=2&biomarker_kind=gene&n.gt=2" || e.seen[1] != "/api/v1/biomarkers?cursor=c1&limit=2&biomarker_kind=gene&n.gt=2" {
		t.Fatalf("requests: %v", e.seen)
	}
	n := len(e.seen)
	if _, _, err := e.split("biomarkers", "list", "--filter", "limit=5"); apierr.ExitCode(err) != apierr.ExitUsage || len(e.seen) != n {
		t.Fatalf("a reserved key must be refused locally: %v", err)
	}
}

// recordBody is GET /api/v1/{object}/{id} in the published shape: the primary row, attribute rows
// folded in by dataset, the datasets reachable from the record, and a redirect from a retired id.
const recordBody = `{"object_type":"sites","id":"site-2",
"record":{"site_id":"site-2","site_name":"General Hospital","n_trials":4,"city":null},
"attributes":{"site_hospitals":{"ccn":"123456","bed_count":300},"site_other":null},
"datasets":[{"name":"site_trials","title":"Trials at the site","tier":"licensable","entitled":true,"row_count":null,"href":"/api/v1/sites/site-2/site_trials"},
{"name":"site_investigators","title":"Investigators","tier":null,"entitled":false,"row_count":null,"href":"/api/v1/sites/site-2/site_investigators"}],
"attribution":[{"name":"ClinicalTrials.gov via AACT, CTTI","license":"CC BY","href":"https://example.test/aact"}],
"source_credit":"Source: ClinicalTrials.gov via AACT, CTTI (CC BY)",
"build_state":{"built":true,"generation":"g7","last_loaded":null},"restriction":{"tier":"licensable"},
"redirect":{"from":"site-1","to":"site-2"}}`

func TestGetPrintsTheRecordThenTheDatasetsIndex(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(recordBody)
	out, errOut, err := e.split("sites", "get", "site-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "field\tvalue\nsite_id\tsite-2\nsite_name\tGeneral Hospital\nn_trials\t4\ncity\t\nsite_hospitals.ccn\t123456\nsite_hospitals.bed_count\t300\n" +
		"\ndataset\ttitle\tentitled\nsite-trials\tTrials at the site\tyes\nsite-investigators\tInvestigators\tno\n# Source: ClinicalTrials.gov via AACT, CTTI (CC BY) https://example.test/aact\n"
	if out != want {
		t.Fatalf("%q\nwant\n%q", out, want)
	}
	for _, s := range []string{"ClinicalTrials.gov via AACT, CTTI (CC BY)", "redirected: site-1 is now site-2", "as of generation g7 (profile local)"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("footer missing %q in %q", s, errOut)
		}
	}
	if strings.Contains(out+errOut, "licensable") {
		t.Fatal("a tier was printed")
	}
	if e.lastSeen(t) != "/api/v1/sites/site-1" {
		t.Fatal(e.lastSeen(t))
	}
	_, quietErr, _ := e.split("sites", "get", "site-1", "--quiet")
	if !strings.Contains(quietErr, "redirected: site-1 is now site-2") || strings.Contains(quietErr, "as of") {
		t.Fatalf("the redirect survives --quiet, the as-of does not: %q", quietErr)
	}
	csvOut, _, err := e.split("sites", "get", "site-1", "--csv")
	if err != nil || !strings.HasPrefix(csvOut, "key,value\nsite_id,site-2\nsite_name,General Hospital\nn_trials,4\ncity,\nsite_hospitals.ccn,123456\nsite_hospitals.bed_count,300\n# Source: ") {
		t.Fatalf("%v %q", err, csvOut)
	}
}

func TestDatasetsCatalogListAndEntry(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"datasets":[{"name":"sites","title":"Sites","description":"d","grain":"one row per site","tier":"licensable","column_count":19,"object_types":["sites"],"source_credit":"Source: x"}]}`)
	out, errOut, err := e.split("datasets", "list")
	if err != nil || out != "name\ttitle\tgrain\tobject_types\tcolumn_count\nsites\tSites\tone row per site\t[\"sites\"]\t19\n" || strings.Contains(errOut, "licensable") {
		t.Fatalf("%v %q %q", err, out, errOut)
	}
	if e.lastSeen(t) != "/api/v1/datasets" {
		t.Fatal(e.lastSeen(t))
	}
	e.api = serve(`{"name":"site_trials","title":"Trials at a site","description":"One row per site and trial.","grain":"one row per site and trial","tier":"licensable",` +
		`"columns":[{"name":"site_id","type":"text","unit":null,"description":"The site.","tier":"licensable"},{"name":"enrollment","type":"integer","unit":"participants","description":"Enrollment."}],` +
		`"attribution":[{"name":"AACT","license":"CC BY","href":"h"}]}`)
	out, errOut, err = e.split("datasets", "get", "site_trials")
	if err != nil {
		t.Fatal(err)
	}
	want := "field\tvalue\nname\tsite_trials\ntitle\tTrials at a site\ndescription\tOne row per site and trial.\ngrain\tone row per site and trial\n\n" +
		"column\ttype\tunit\tdescription\nsite_id\ttext\t\tThe site.\nenrollment\tinteger\tparticipants\tEnrollment.\n# Source: AACT (CC BY) h\n"
	if out != want || strings.Contains(out+errOut, "licensable") {
		t.Fatalf("%q\nwant\n%q", out, want)
	}
	if e.lastSeen(t) != "/api/v1/datasets/site_trials" {
		t.Fatal(e.lastSeen(t))
	}
}

func TestDatasetSubcommandsTakeKebabOrAPINames(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("", "2"))
	for _, args := range [][]string{
		{"companies", "get", "c 1", "sponsor-trials", "--scope", "group", "--filter", "phase=PHASE3"},
		{"companies", "get", "c 1", "sponsor_trials", "--scope", "group", "--filter", "phase=PHASE3"},
	} {
		if _, _, err := e.split(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(e.seen) != 2 || e.seen[0] != "/api/v1/companies/c%201/sponsor_trials?scope=group&phase=PHASE3" || e.seen[1] != e.seen[0] {
		t.Fatalf("requests: %v", e.seen)
	}
	// legal entities have a single scope, so the document gives their datasets no --scope.
	if _, _, err := e.split("legal-entities", "get", "le1", "sponsor-trials", "--scope", "group"); apierr.ExitCode(err) != apierr.ExitUsage || len(e.seen) != 2 {
		t.Fatalf("%v %v", err, e.seen)
	}
	if _, _, err := e.split("cell-lines", "list"); err != nil || e.lastSeen(t) != "/api/v1/cell_lines" {
		t.Fatalf("%v %s", err, e.lastSeen(t))
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
	e.api = serve(page1("", "2"))
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
	e.api = serve(page1("", "2"))
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
		serve(page1("c1", "99"))(w, r)
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
		serve(page1(fmt.Sprintf("c%d", calls), "99"))(w, r)
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
	e.api = serve(page1(`c\u001b[2J\u0007`, "99"))
	_, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(err.Error(), "already fetched (c)") {
		t.Fatalf("exit %d err %q", apierr.ExitCode(err), err)
	}
	if strings.ContainsAny(errOut+err.Error(), "\x1b\x07") {
		t.Fatalf("control text reached stderr: %q / %q", errOut, err)
	}
}

// A record, attributes or attribute group that is not an object is reported by its JSON kind only:
// the server's text never reaches the error line.
func TestMalformedRecordShapeLeavesStderrClean(t *testing.T) {
	hostile := `"\u001b[31mRED\u001b]8;;http://x\u0007L\nFAKE: line\u0000\u202eEVIL"`
	for _, body := range []string{
		`{"record":` + hostile + `}`,
		`{"record":{"a":1},"attributes":` + hostile + `}`,
		`{"record":{"a":1},"attributes":{"g":` + hostile + `}}`,
	} {
		e := loggedIn(t)
		e.api = serve(body)
		_, errOut, err := e.split("sites", "get", "s1")
		if err == nil || !strings.Contains(err.Error(), "expected an object, got a string") {
			t.Fatalf("%s: %v", body, err)
		}
		all := errOut + err.Error()
		if strings.ContainsAny(all, "\x1b\x07\x00\u202e") || strings.Contains(all, "FAKE") || strings.Contains(all, "EVIL") {
			t.Fatalf("server text reached stderr: %q", all)
		}
	}
}

// One --all run fetches at most MaxPages pages in all: a generation restart does not reset the cap.
func TestPageCapHoldsAcrossAGenerationRestart(t *testing.T) {
	old := pages.MaxPages
	pages.MaxPages = 3
	defer func() { pages.MaxPages = old }()
	e := loggedIn(t)
	calls := 0
	e.api = func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error_code":"GENERATION_CHANGED","message":"rebuilt","docs":"d"}`))
			return
		}
		serve(page1(fmt.Sprintf("c%d", calls), "99"))(w, r)
	}
	_, errOut, err := e.split("biomarkers", "list", "--all")
	if apierr.ExitCode(err) != apierr.ExitPartial || !strings.Contains(errOut, "3 page limit") {
		t.Fatalf("exit %d err %v stderr %q", apierr.ExitCode(err), err, errOut)
	}
	if calls != 4 {
		t.Fatalf("%d calls, want 4 (page 1, the 409, then 2 more pages to reach the cap of 3)", calls)
	}
}

// The v0.1.4 object types and datasets: trials and conditions with their datasets, and the new
// company and biomarker datasets, all reachable, kebab or API spelling, with --filter and paging.
func TestTrialsConditionsAndTheNewDatasetsAreReachable(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(page1("", "2"))
	for args, want := range map[string]string{
		"trials get NCT01234567 trial-drugs --limit 5 --filter molecule_name=aspirin": "/api/v1/trials/NCT01234567/trial_drugs?limit=5&molecule_name=aspirin",
		"trials get NCT01234567 trial_delivery":                                       "/api/v1/trials/NCT01234567/trial_delivery",
		"trials list --filter phase=PHASE3 --sort -start_date":                        "/api/v1/trials?sort=-start_date&phase=PHASE3",
		"conditions get D001943 condition-trials --cursor c9":                         "/api/v1/conditions/D001943/condition_trials?cursor=c9",
		"companies get c1 company-assets":                                             "/api/v1/companies/c1/company_assets",
		"companies get c1 company-funding-events --scope group":                       "/api/v1/companies/c1/company_funding_events?scope=group",
		"companies get c1 company-funding-summary":                                    "/api/v1/companies/c1/company_funding_summary",
		"companies get c1 ownership-positions":                                        "/api/v1/companies/c1/ownership_positions",
		"biomarkers get ESR1 biomarker-validation":                                    "/api/v1/biomarkers/ESR1/biomarker_validation",
	} {
		if _, _, err := e.split(strings.Fields(args)...); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if got := e.lastSeen(t); got != want {
			t.Errorf("%s: request %s want %s", args, got, want)
		}
	}
}

// trial_delivery's has_enrollment_delta reaches the Go SDK only as an additional property (see the
// SDK's bin/go-spec-patch); the CLI renders rows from the response columns, so it is a column like
// any other, in the table and in CSV, and every cell is sanitized.
func TestTrialDeliveryRendersHasEnrollmentDeltaAndSanitizes(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"dataset":"trial_delivery","grain":"one row per trial","object_type":"trials","id":"NCT01234567",` +
		`"columns":[{"name":"trial_id","type":"text"},{"name":"enrollment_delta","type":"integer"},{"name":"has_enrollment_delta","type":"boolean"},{"name":"note","type":"text"}],` +
		`"rows":[{"trial_id":"NCT01234567","enrollment_delta":12,"has_enrollment_delta":true,"note":"a\u001b[31mb\u202ec"}],"total":1,"next_cursor":null,` +
		`"attribution":[{"name":"ClinicalTrials.gov via AACT, CTTI","license":"CC BY","href":"https://example.test/aact"}],"build_state":{"built":true,"generation":"g1","last_loaded":null}}`)
	out, errOut, err := e.split("trials", "get", "NCT01234567", "trial-delivery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "trial_id\tenrollment_delta\thas_enrollment_delta\tnote\nNCT01234567\t12\ttrue\tabc\n# Source: ClinicalTrials.gov") {
		t.Fatalf("%q", out)
	}
	if strings.ContainsAny(out+errOut, "\x1b\u202e") {
		t.Fatalf("control text reached the terminal: %q", out+errOut)
	}
	csv, _, err := e.split("trials", "get", "NCT01234567", "trial-delivery", "--csv")
	if err != nil || !strings.HasPrefix(csv, "trial_id,enrollment_delta,has_enrollment_delta,note\nNCT01234567,12,true,abc\n") {
		t.Fatalf("%v %q", err, csv)
	}
}

func TestConditionRecordRendersLikeEveryRecord(t *testing.T) {
	e := loggedIn(t)
	e.api = serve(`{"object_type":"conditions","id":"D001943","record":{"condition_id":"D001943","condition_name":"Breast Neoplasms","categories":["Neoplasms","Skin"],"n_trials":4021},` +
		`"attributes":{"condition_burden":{"dalys":123.5}},"datasets":[{"name":"condition_trials","title":"Trials","tier":null,"entitled":true,"row_count":null}],` +
		`"attribution":[{"name":"US National Library of Medicine, Medical Subject Headings (MeSH)","license":"Public domain","href":"https://example.test/mesh"}],` +
		`"build_state":{"built":true,"generation":"g1","last_loaded":null}}`)
	out, _, err := e.split("conditions", "get", "D001943")
	if err != nil {
		t.Fatal(err)
	}
	want := "field\tvalue\ncondition_id\tD001943\ncondition_name\tBreast Neoplasms\ncategories\t[\"Neoplasms\",\"Skin\"]\nn_trials\t4021\ncondition_burden.dalys\t123.5\n\ndataset\ttitle\tentitled\ncondition-trials\tTrials\tyes\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("%q\nwant prefix\n%q", out, want)
	}
}
