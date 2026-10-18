package pages

import (
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/gen/tree"
)

const sdkModule = "github.com/scientist-labs/pharmaobservatory-sdks/go"

// handMapped are the document's non-page operations, each mapped by hand in internal/cmd. A new
// non-page operation in a bumped SDK fails TestNonPageOperationsAreHandMapped until it is mapped
// there and added here.
var handMapped = []string{"GET /api/v1/whoami"} // po whoami

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
		var path []string
		switch o.Kind {
		case tree.KindList:
			path = []string{o.Page, "list"}
		case tree.KindGet:
			path = []string{o.Page, "get"}
		case tree.KindIDSection:
			get, _, err := root.Find([]string{o.Page, "get"})
			if err != nil || !strings.Contains(get.Long, "  "+o.Key+" ") {
				t.Errorf("%+v: not listed in get help (%v)", o, err)
			}
			continue
		case tree.KindSection:
			path = []string{o.Page, o.Key}
			if tree.PageReserved[o.Key] {
				path = []string{o.Page, "section", o.Key}
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
	for _, p := range allPages() {
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
		sort.Strings(have)
		sort.Strings(want)
		if strings.Join(have, ",") != strings.Join(want, ",") {
			t.Errorf("%s: flags %v want %v", o.Path, have, want)
		}
	}
	// the ticket's flags
	for _, c := range []struct{ page, kind, flag string }{
		{"biomarkers", tree.KindList, "kind"}, {"biomarkers", tree.KindList, "limit"},
		{"biomarkers", tree.KindList, "cursor"}, {"cell-lines", tree.KindSection, "species"},
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
