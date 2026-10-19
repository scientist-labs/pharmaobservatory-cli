package pages

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/gen/tree"
	sdk "github.com/scientist-labs/pharmaobservatory-sdks/go"
)

const sdkModule = "github.com/scientist-labs/pharmaobservatory-sdks/go"

// handMapped are the document's non-page operations, each mapped by hand in internal/cmd. A new
// non-page operation in a bumped SDK fails TestNonPageOperationsAreHandMapped until it is mapped
// there and added here.
var handMapped = []string{
	"GET /api/v1/whoami",              // po whoami
	"POST /api/v1/query",              // po query
	"GET /api/v1/results/{result_id}", // po query --result
}

func realModel(t *testing.T) *tree.Model {
	t.Helper()
	b, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}\t{{.Version}}", sdkModule).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	dir, version, _ := strings.Cut(strings.TrimSpace(string(b)), "\t")
	m, err := tree.Build(dir, sdkModule, version)
	if err != nil {
		t.Fatal(err)
	}
	if version != SDKVersion {
		t.Fatalf("pinned SDK is %s but the generated tree is from %s: run go generate ./...", version, SDKVersion)
	}
	return m
}

func ref(o tree.Op) OpRef { return OpRef{Page: o.Page, Kind: o.Kind, Key: o.Key, Path: o.Path} }

func TestGeneratedTreeCoversEveryPageOperationInThePinnedSpec(t *testing.T) {
	m := realModel(t)
	if len(m.Ops) == 0 {
		t.Fatal("the pinned spec has no page operations")
	}
	want := map[OpRef]bool{}
	for _, o := range m.Ops {
		want[ref(o)] = true
	}
	got := map[OpRef]bool{}
	for _, o := range Operations {
		got[o] = true
	}
	for r := range want {
		if !got[r] {
			t.Errorf("in the spec but not generated: %+v (run go generate ./...)", r)
		}
	}
	for r := range got {
		if !want[r] {
			t.Errorf("generated but not in the spec: %+v (run go generate ./...)", r)
		}
	}
	if strings.Join(Unmapped, ",") != strings.Join(m.Unmapped, ",") {
		t.Errorf("Unmapped %v differs from the spec's %v", Unmapped, m.Unmapped)
	}
}

func TestNonPageOperationsAreHandMapped(t *testing.T) {
	if err := tree.CheckAllowlist(Unmapped, handMapped); err != nil {
		t.Fatal(err)
	}
}

func TestEveryOperationIsReachableInTheCobraTree(t *testing.T) {
	root := &cobra.Command{Use: "po"}
	root.AddCommand(Build(&Env{})...)
	for _, o := range Operations {
		page := tree.Kebab(o.Page)
		var path []string
		switch o.Kind {
		case tree.KindList:
			path = []string{page, "list"}
		case tree.KindGet:
			path = []string{page, "get"}
		case tree.KindIDSection:
			get, _, err := root.Find([]string{page, "get"})
			if err != nil || !strings.Contains(get.Long, "  "+tree.Kebab(o.Key)+" ") {
				t.Errorf("%+v: not listed in get help (%v)", o, err)
			}
			continue
		case tree.KindSection:
			path = []string{page, tree.Kebab(o.Key)}
			if tree.PageReserved[tree.Kebab(o.Key)] {
				path = []string{page, "section", tree.Kebab(o.Key)}
			}
		}
		if tree.PageEscaped(o.Page) {
			path = append([]string{"page"}, path...)
		}
		c, rest, err := root.Find(path)
		if err != nil || len(rest) != 0 || c.Name() != path[len(path)-1] {
			t.Errorf("%+v: not reachable as %v (%v)", o, path, err)
		}
	}
}

func TestEveryQueryParameterIsAFlag(t *testing.T) {
	m := realModel(t)
	byRef := map[OpRef]*Op{}
	pages := allPages()
	buildFrom(&Env{}, pages) // adds the hand-written --filter
	for _, p := range pages {
		add := func(kind string, o *Op) {
			if o != nil {
				byRef[OpRef{Page: p.Slug, Kind: kind, Key: o.Key, Path: o.Path}] = o
			}
		}
		add(tree.KindList, p.List)
		add(tree.KindGet, p.Get)
		for _, o := range p.IDSections {
			add(tree.KindIDSection, o)
		}
		for _, o := range p.Sections {
			add(tree.KindSection, o)
		}
	}
	for _, o := range m.Ops {
		g := byRef[ref(o)]
		if g == nil {
			t.Fatalf("no generated op for %+v", ref(o))
		}
		var have []string
		g.Flags.VisitAll(func(f *pflag.Flag) { have = append(have, f.Name) })
		var want []string
		for _, p := range o.Params {
			want = append(want, p.Flag)
		}
		if g.Flags.Lookup("cursor") != nil {
			want = append(want, "filter") // hand-written on every dataset read, see filterFlag
		}
		sort.Strings(have)
		sort.Strings(want)
		if strings.Join(have, ",") != strings.Join(want, ",") {
			t.Errorf("%s: flags %v want %v", o.Path, have, want)
		}
	}
	// the dataset paging and grouping parameters, and the hand-written filter
	for _, c := range []struct{ page, kind, flag string }{
		{"biomarkers", tree.KindList, "limit"}, {"biomarkers", tree.KindList, "cursor"},
		{"biomarkers", tree.KindList, "sort"}, {"biomarkers", tree.KindList, "group-by"},
		{"biomarkers", tree.KindList, "measure"}, {"biomarkers", tree.KindList, "filter"},
		{"companies", tree.KindIDSection, "scope"}, {"cell_lines", tree.KindList, "cursor"},
	} {
		found := false
		for r, o := range byRef {
			if r.Page == c.page && r.Kind == c.kind && o.Flags.Lookup(c.flag) != nil {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s lacks --%s", c.page, c.kind, c.flag)
		}
	}
}

func TestCollidingNamesAreRegisteredOnlyUnderTheEscapeCommands(t *testing.T) {
	mk := func(key string, escaped bool) *Op {
		return &Op{Key: key, Short: key, Path: "/p/" + key, Escaped: escaped, Flags: pflag.NewFlagSet(key, pflag.ContinueOnError)}
	}
	specs := []*PageSpec{
		{Slug: "login", Escaped: true, List: mk("", false)},
		{Slug: "widgets", Sections: []*Op{mk("help", true), mk("counts", false)}},
	}
	root := &cobra.Command{Use: "po"}
	root.AddCommand(buildFrom(&Env{}, specs)...)
	names := func(c *cobra.Command) string {
		var n []string
		for _, s := range c.Commands() {
			n = append(n, s.Name())
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}
	if got := names(root); got != "page,widgets" {
		t.Fatalf("top level %s: a colliding page slug must not be a top-level command", got)
	}
	page, _, _ := root.Find([]string{"page"})
	if names(page) != "login" {
		t.Fatalf("page: %s", names(page))
	}
	w, _, _ := root.Find([]string{"widgets"})
	if got := names(w); got != "counts,section" {
		t.Fatalf("widgets: %s: a colliding section key must live under section", got)
	}
	sec, _, _ := root.Find([]string{"widgets", "section"})
	if names(sec) != "help" {
		t.Fatalf("section: %s", names(sec))
	}
}

func TestDecodeWarningIsOneSanitizedLine(t *testing.T) {
	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&errBuf)
	w := &walker{cmd: cmd, o: &Op{Call: func(context.Context, *sdk.APIClient, string, *pflag.FlagSet) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[1,2]"))},
			errors.New("bad\x1b[31m\nforged line")
	}}, cl: &Client{}}
	if _, err := w.fetch(); err != nil {
		t.Fatal(err)
	}
	got := errBuf.String()
	if strings.Contains(got, "\x1b") || strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("not a single clean line: %q", got)
	}
}

func TestKebabCommandsAcceptTheAPINames(t *testing.T) {
	root := &cobra.Command{Use: "po"}
	root.AddCommand(Build(&Env{})...)
	for _, path := range [][]string{{"cell-lines", "list"}, {"cell_lines", "list"}, {"legal-entities", "get"}, {"datasets", "get"}, {"datasets", "list"}, {"sites", "get"}} {
		if c, rest, err := root.Find(path); err != nil || len(rest) != 0 || c.Name() != path[1] {
			t.Errorf("%v: not reachable (%v)", path, err)
		}
	}
	get, _, _ := root.Find([]string{"datasets", "get"})
	if get.Use != "get <name>" {
		t.Errorf("datasets get takes a name: %q", get.Use)
	}
	sg, _, _ := root.Find([]string{"companies", "get"})
	if !strings.Contains(sg.Long, "  sponsor-trials ") || sg.Use != "get <id> [dataset]" {
		t.Errorf("companies get: %q\n%s", sg.Use, sg.Long)
	}
}

func TestFilterFlagBuildsTheQueryAndRefusesReservedKeys(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	o := &Op{Flags: fs}
	fs.String("cursor", "", "")
	addFilterFlag(o)
	if err := fs.Parse([]string{"--filter", "kind=gene", "--filter", "kind=variant", "--filter", "n.gt=3"}); err != nil {
		t.Fatal(err)
	}
	q, err := filters(fs)
	if err != nil || q.Encode() != "kind=gene&kind=variant&n.gt=3" {
		t.Fatalf("%v %s", err, q.Encode())
	}
	for _, bad := range []string{"novalue", "=x", "limit=5", "group_by.ne=x"} {
		fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
		fs.String("cursor", "", "")
		addFilterFlag(&Op{Flags: fs})
		_ = fs.Parse([]string{"--filter", bad})
		if _, err := filters(fs); err == nil {
			t.Errorf("%q: want a usage error", bad)
		}
	}
	plain := pflag.NewFlagSet("y", pflag.ContinueOnError)
	addFilterFlag(&Op{Flags: plain})
	if plain.Lookup("filter") != nil {
		t.Error("an operation without --cursor (a record get) takes no --filter")
	}
}
