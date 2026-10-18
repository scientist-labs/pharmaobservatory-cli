package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestWidthAwareTableTruncatesWithEllipsisAndNeverWraps(t *testing.T) {
	g := Grid{Header: []string{"Name", "Description"}, Rows: [][]string{{"a", strings.Repeat("x", 60)}, {"bb", "short"}}}
	var b bytes.Buffer
	if err := WriteTable(&b, g, 30); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if n := len([]rune(l)); n > 30 {
			t.Errorf("line of %d cells: %q", n, l)
		}
	}
	if !strings.Contains(b.String(), "…") {
		t.Fatalf("no ellipsis: %q", b.String())
	}
	if !strings.HasPrefix(b.String(), "Name  Description") {
		t.Fatalf("columns not aligned: %q", b.String())
	}
}

func TestTabSeparatedCellsNeverContainTabsOrNewlines(t *testing.T) {
	var b bytes.Buffer
	_ = WriteTable(&b, Grid{Header: []string{"a"}, Rows: [][]string{{"x\ty\nz"}}}, 0)
	if b.String() != "a\nx y z\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestThousandsAndCells(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 12431: "12,431", -1234567: "-1,234,567"} {
		if got := Thousands(in); got != want {
			t.Errorf("%d: %s", in, got)
		}
	}
	if Cell(true) != "true" || Cell(false) != "false" || Cell(nil) != "" || Cell([]any{1.0, "a"}) != `[1,"a"]` {
		t.Fatal("cells")
	}
}

func TestAttributionTextForm(t *testing.T) {
	e, err := Parse([]byte(`{"attribution":"Credit line"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := AttributionLines(e); len(got) != 1 || got[0] != "Credit line" {
		t.Fatalf("%v", got)
	}
}

func TestEmptyGridFit(t *testing.T) {
	if fit(nil, 10) != nil {
		t.Fatal("fit")
	}
	if trunc("abc", 1) != "a" {
		t.Fatal("trunc")
	}
}
