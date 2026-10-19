package tree

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture copies the synthetic SDK module to a temp dir so a test can edit it.
func fixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	for _, f := range []string{"client.go", "api_widgets.go", "api/openapi.yaml"} {
		b, err := os.ReadFile(filepath.Join("testdata", "sdk", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func edit(t *testing.T, dir, file, old, repl string) {
	t.Helper()
	p := filepath.Join(dir, file)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	if err := os.WriteFile(p, bytes.Replace(b, []byte(old), []byte(repl), 1), 0o644); err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, dir string) *Model {
	t.Helper()
	m, err := Build(dir, "example.test/sdk", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func find(m *Model, kind, page, key string) *Op {
	for i := range m.Ops {
		o := &m.Ops[i]
		if o.Kind == kind && o.Page == page && o.Key == key {
			return o
		}
	}
	return nil
}

func TestFourMappingRules(t *testing.T) {
	m := build(t, fixture(t))
	for _, c := range []struct{ kind, page, key, path, method string }{
		{KindList, "widgets", "", "/api/v1/widgets", "WidgetsList"},
		{KindGet, "widgets", "", "/api/v1/widgets/{id}", "WidgetsGet"},
		{KindIDSection, "widgets", "parts", "/api/v1/widgets/{id}/parts", "WidgetsParts"},
		{KindSection, "widgets", "counts", "/api/v1/widgets/counts", "WidgetsCounts"},
	} {
		o := find(m, c.kind, c.page, c.key)
		if o == nil || o.Path != c.path || o.Method != c.method || o.Service != "WidgetsAPI" {
			t.Errorf("%s: got %+v", c.kind, o)
		}
	}
	if o := find(m, KindGet, "widgets", ""); !o.HasID || o.Summary != "One widget" {
		t.Errorf("get: %+v", o)
	}
	if o := find(m, KindList, "widgets", ""); o.HasID {
		t.Errorf("list takes no id")
	}
}

func TestParameterTypesAndCollisions(t *testing.T) {
	m := build(t, fixture(t))
	got := map[string]Param{}
	for _, p := range find(m, KindList, "widgets", "").Params {
		got[p.Name] = p
	}
	for name, want := range map[string]struct{ flag, define, getter, desc string }{
		"cursor": {"cursor", "String", "GetString", "Opaque cursor"},
		"limit":  {"limit", "Int32", "GetInt32", "query parameter limit"},
		"json":   {"param-json", "Bool", "GetBool", "query parameter json"}, // collides with the global --json
		"ratio":  {"ratio", "Float32", "GetFloat32", "query parameter ratio"},
	} {
		p := got[name]
		if p.Flag != want.flag || p.Define != want.define || p.Getter != want.getter || p.Desc != want.desc {
			t.Errorf("%s: %+v", name, p)
		}
	}
	if Kebab("safety_status") != "safety-status" {
		t.Error("kebab")
	}
}

func TestReservedNameCollisions(t *testing.T) {
	m := build(t, fixture(t))
	if o := find(m, KindSection, "widgets", "help"); o == nil || !o.Escaped {
		t.Errorf("a section key equal to a page-group command must be escaped: %+v", o)
	}
	if o := find(m, KindSection, "widgets", "counts"); o.Escaped {
		t.Error("an ordinary section key must not be escaped")
	}
	if !PageEscaped("login") || PageEscaped("widgets") {
		t.Error("page slug escaping")
	}
	files, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	var login string
	for _, f := range files {
		if f.Name == "zz_generated_login.go" {
			login = string(f.Data)
		}
	}
	if !strings.Contains(login, `Slug: "login", Command: "login", Escaped: true`) {
		t.Errorf("login page not escaped:\n%s", login)
	}
}

func TestUnmappedOperationFailsAllowlistUntilMapped(t *testing.T) {
	m := build(t, fixture(t))
	if len(m.Unmapped) != 1 || m.Unmapped[0] != "GET /api/v1/whoami" {
		t.Fatalf("unmapped: %v", m.Unmapped)
	}
	if err := CheckAllowlist(m.Unmapped, []string{"GET /api/v1/whoami"}); err != nil {
		t.Fatal(err)
	}
	err := CheckAllowlist(m.Unmapped, nil)
	if err == nil || !strings.Contains(err.Error(), "GET /api/v1/whoami") {
		t.Fatalf("an unmapped operation must fail the allowlist: %v", err)
	}
	if err := CheckAllowlist(nil, []string{"GET /api/v1/whoami"}); err == nil {
		t.Fatal("a stale allowlist entry must fail too")
	}
}

func TestDeterministicAndRemovedOperationDisappears(t *testing.T) {
	dir := fixture(t)
	a, err := Render(build(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Render(build(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatal("file sets differ")
	}
	for i := range a {
		if a[i].Name != b[i].Name || !bytes.Equal(a[i].Data, b[i].Data) {
			t.Fatalf("%s differs between runs", a[i].Name)
		}
	}
	index := func(fs []File) string {
		for _, f := range fs {
			if f.Name == "zz_generated_index.go" {
				return string(f.Data)
			}
		}
		return ""
	}
	if !strings.Contains(index(a), `"/api/v1/widgets/counts"`) || !strings.Contains(index(a), `v9.9.9`) {
		t.Fatal("index misses an operation or the SDK version")
	}
	edit(t, dir, "api/openapi.yaml", "  /api/v1/widgets/counts:\n    get:\n      operationId: widgets.counts\n      summary: Widget counts\n      tags: [widgets]\n", "")
	c, err := Render(build(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(index(c), "widgets/counts") {
		t.Fatal("a removed operation must leave the generated index")
	}
}

func TestJoinMismatchesBreakGeneration(t *testing.T) {
	for name, c := range map[string]struct{ file, old, repl, want string }{
		"operationId disagrees with the path": {"api/openapi.yaml", "operationId: widgets.parts", "operationId: widgets.pieces", "must end in .parts"},
		"list operationId":                    {"api/openapi.yaml", "operationId: widgets.list", "operationId: widgets.index", "must end in .list"},
		"missing in the SDK":                  {"api_widgets.go", `"/api/v1/widgets/counts"`, `"/api/v1/widgets/other"`, "SDK source has no such operation"},
		"extra SDK query parameter":           {"api/openapi.yaml", "      - {name: colour, in: query, schema: {type: string}}\n", "", "does not declare"},
		"type disagreement":                   {"api/openapi.yaml", "{name: limit, in: query, schema: {type: integer}}", "{name: limit, in: query, schema: {type: string}}", "string in the document but int32"},
		"bad parameter name":                  {"api/openapi.yaml", "{name: ratio,", "{name: Ratio,", "not a valid flag source"},
		"header parameter":                    {"api/openapi.yaml", "{name: ratio, in: query,", "{name: ratio, in: header,", "not supported"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fixture(t)
			edit(t, dir, c.file, c.old, c.repl)
			_, err := Build(dir, "m", "v")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestSpecParameterMissingFromSDK(t *testing.T) {
	dir := fixture(t)
	edit(t, dir, "api/openapi.yaml", "      - {name: colour, in: query, schema: {type: string}}\n", "      - {name: colour, in: query, schema: {type: string}}\n      - {name: shade, in: query, schema: {type: string}}\n")
	if _, err := Build(dir, "m", "v"); err == nil || !strings.Contains(err.Error(), `"shade" the SDK does not send`) {
		t.Fatalf("%v", err)
	}
}

func TestNonGetAndForeignPathsAreUnmapped(t *testing.T) {
	dir := fixture(t)
	edit(t, dir, "api/openapi.yaml", "  /api/v1/whoami:", "  /other/path:\n    get:\n      operationId: x.y\n      tags: [x]\n  /api/v1/widgets/{id}/deep/er:\n    get:\n      operationId: widgets.er\n      tags: [widgets]\n  /api/v1/whoami:")
	m := build(t, dir)
	want := "GET /api/v1/whoami,GET /api/v1/widgets/{id}/deep/er,GET /other/path"
	if got := strings.Join(m.Unmapped, ","); got != want {
		t.Fatalf("got %s", got)
	}
}

// An object type with an underscore (cell_lines) maps to its kebab command, and a get whose path
// parameter is not {id} (datasets/{name}) maps with that parameter as its positional argument.
func TestUnderscorePagesAndNamedPathParameter(t *testing.T) {
	dir := fixture(t)
	edit(t, dir, "api/openapi.yaml", "  /api/v1/whoami:", `  /api/v1/cell_lines/{name}:
    get:
      operationId: cell_lines.get
      tags: [cell_lines]
      parameters:
      - {name: name, in: path, required: true, schema: {type: string}}
  /api/v1/cell_lines/{name}/site_trials:
    get:
      operationId: cell_lines.site_trials
      tags: [cell_lines]
      parameters:
      - {name: name, in: path, required: true, schema: {type: string}}
  /api/v1/whoami:`)
	b, err := os.ReadFile(filepath.Join(dir, "api_widgets.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b) + `
type CellLinesAPIService service

type ApiCellLinesGetRequest struct {
	ctx        context.Context
	ApiService *CellLinesAPIService
	name       string
}

func (a *CellLinesAPIService) CellLinesGet(ctx context.Context, name string) ApiCellLinesGetRequest {
	return ApiCellLinesGetRequest{ApiService: a, ctx: ctx, name: name}
}

func (a *CellLinesAPIService) CellLinesGetExecute(r ApiCellLinesGetRequest) (*http.Response, error) {
	var localVarHTTPMethod = http.MethodGet
	localVarPath := localBasePath + "/api/v1/cell_lines/{name}"
	_ = localVarHTTPMethod
	_ = localVarPath
	return nil, nil
}

type ApiCellLinesSiteTrialsRequest struct {
	ctx        context.Context
	ApiService *CellLinesAPIService
	name       string
}

func (a *CellLinesAPIService) CellLinesSiteTrials(ctx context.Context, name string) ApiCellLinesSiteTrialsRequest {
	return ApiCellLinesSiteTrialsRequest{ApiService: a, ctx: ctx, name: name}
}

func (a *CellLinesAPIService) CellLinesSiteTrialsExecute(r ApiCellLinesSiteTrialsRequest) (*http.Response, error) {
	var localVarHTTPMethod = http.MethodGet
	localVarPath := localBasePath + "/api/v1/cell_lines/{name}/site_trials"
	_ = localVarHTTPMethod
	_ = localVarPath
	return nil, nil
}
`
	if err := os.WriteFile(filepath.Join(dir, "api_widgets.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	edit(t, dir, "client.go", "WidgetsAPI *WidgetsAPIService", "WidgetsAPI *WidgetsAPIService\n\tCellLinesAPI *CellLinesAPIService")
	m := build(t, dir)
	g := find(m, KindGet, "cell_lines", "")
	if g == nil || g.IDName != "name" || !g.HasID || g.Service != "CellLinesAPI" {
		t.Fatalf("get: %+v", g)
	}
	if d := find(m, KindIDSection, "cell_lines", "site_trials"); d == nil || d.IDName != "name" {
		t.Fatalf("dataset: %+v", d)
	}
	files, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	var page string
	for _, f := range files {
		if f.Name == "zz_generated_cell_lines.go" {
			page = string(f.Data)
		}
	}
	if !strings.Contains(page, `Slug: "cell_lines", Command: "cell-lines"`) || !strings.Contains(page, `IDName: "name"`) {
		t.Fatalf("cell_lines page:\n%s", page)
	}
}

// fakeOp is SDK source for one parameterless GET, shaped like the generated code parseSDK reads.
func fakeOp(service, method, path string, withID bool) string {
	sig, pos := "ctx context.Context", ""
	if withID {
		sig, pos = "ctx context.Context, id string", ", id: id"
	}
	return `
type Api` + method + `Request struct {
	ctx        context.Context
	ApiService *` + service + `
	id         string
}

func (a *` + service + `) ` + method + `(` + sig + `) Api` + method + `Request {
	return Api` + method + `Request{ApiService: a, ctx: ctx` + pos + `}
}

func (a *` + service + `) ` + method + `Execute(r Api` + method + `Request) (*http.Response, error) {
	var localVarHTTPMethod = http.MethodGet
	localVarPath := localBasePath + "` + path + `"
	_ = localVarHTTPMethod
	_ = localVarPath
	return nil, nil
}
`
}

func appendFile(t *testing.T, dir, file, src string) {
	t.Helper()
	p := filepath.Join(dir, file)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte(src)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Names that differ only by separators collide as a command (cell_lines and cell-lines) or as a Go
// identifier (x_1 and x1): the generator refuses them by name instead of emitting broken code.
func TestSeparatorCollisionsAreGeneratorErrors(t *testing.T) {
	t.Run("dataset keys", func(t *testing.T) {
		dir := fixture(t)
		edit(t, dir, "api/openapi.yaml", "  /api/v1/whoami:", `  /api/v1/widgets/{id}/pa_rts:
    get:
      operationId: widgets.pa_rts
      tags: [widgets]
      parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
  /api/v1/whoami:`)
		appendFile(t, dir, "api_widgets.go", fakeOp("WidgetsAPIService", "WidgetsPaRts", "/api/v1/widgets/{id}/pa_rts", true))
		_, err := Build(dir, "m", "v")
		if err == nil || !strings.Contains(err.Error(), "/api/v1/widgets/{id}/parts") || !strings.Contains(err.Error(), "/api/v1/widgets/{id}/pa_rts") {
			t.Fatalf("want an error naming both paths, got %v", err)
		}
	})
	t.Run("object types", func(t *testing.T) {
		dir := fixture(t)
		edit(t, dir, "api/openapi.yaml", "  /api/v1/whoami:", `  /api/v1/widget_s:
    get:
      operationId: widget_s.list
      tags: [widget_s]
  /api/v1/whoami:`)
		appendFile(t, dir, "api_widgets.go", "\ntype WidgetSAPIService service\n"+fakeOp("WidgetSAPIService", "WidgetSList", "/api/v1/widget_s", false))
		edit(t, dir, "client.go", "WidgetsAPI *WidgetsAPIService", "WidgetsAPI *WidgetsAPIService\n\tWidgetSAPI *WidgetSAPIService")
		_, err := Build(dir, "m", "v")
		if err == nil || !strings.Contains(err.Error(), `"widget_s"`) || !strings.Contains(err.Error(), `"widgets"`) {
			t.Fatalf("want an error naming both object types, got %v", err)
		}
	})
}
