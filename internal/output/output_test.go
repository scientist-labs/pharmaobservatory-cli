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

func hostileEnvelope(t *testing.T) *Envelope {
	t.Helper()
	body := `{"columns":[{"key":"id","label":"ID"}],"rows":[{"id":"real1"},{"id":"real2"}],` +
		`"attribution":[{"name":"HGNC\nEVIL,row,injected\n# Source: fake\r\u001b[31mred","license":null,"href":""}],` +
		`"web_url":"https://x/\u001b]0;pwn\u0007a\nb"}`
	e, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestHostileCreditInjectsNoCSVRowAndNoEscape(t *testing.T) {
	e := hostileEnvelope(t)
	var b bytes.Buffer
	g := FromEnvelope(e)
	if err := WriteCSV(&b, Keys(e), g.Rows); err != nil {
		t.Fatal(err)
	}
	WriteCredits(&b, e)
	out := b.String()
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("ESC reached output: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "id" || lines[1] != "real1" || lines[2] != "real2" ||
		!strings.HasPrefix(lines[3], "# Source: HGNC EVIL,row,injected # Source: fake") {
		t.Fatalf("unexpected lines %q", lines)
	}
	if n := strings.Count(out, "# Source:"); n != 2 {
		t.Fatalf("want 1 real credit line plus the quoted text, got %d in %q", n, out)
	}
	var f bytes.Buffer
	WriteFooter(&f, e, FooterOpts{})
	if strings.ContainsRune(f.String(), 0x1b) || strings.Contains(f.String(), "\r") {
		t.Fatalf("footer: %q", f.String())
	}
	if got := len(strings.Split(strings.TrimRight(f.String(), "\n"), "\n")); got != 2 {
		t.Fatalf("footer should be credit + url, got %d lines: %q", got, f.String())
	}
}

func TestHostileNotApplicableMessageIsOneCleanLine(t *testing.T) {
	e := &Envelope{Error: "not_applicable", Message: "no\nfake: line\u001b[2J\u0007done"}
	err := e.State()
	if err == nil || err.Error() != "not applicable: no fake: line"+"done" {
		t.Fatalf("%v", err)
	}
}

func TestHostileCellsCleanInTableAndCSV(t *testing.T) {
	g := Grid{Header: []string{"a"}, Rows: [][]string{{"x\u001b[31my\nz\u009b1m"}}}
	for _, w := range []int{0, 40} {
		var b bytes.Buffer
		_ = WriteTable(&b, g, w)
		if strings.ContainsRune(b.String(), 0x1b) || strings.Contains(b.String(), "z\n") || !strings.Contains(b.String(), "xy z") {
			t.Fatalf("width %d: %q", w, b.String())
		}
	}
	var b bytes.Buffer
	_ = WriteCSV(&b, g.Header, [][]string{{"p\nq\u001b[0m\u0007"}})
	if b.String() != "a\n\"p\nq\"\n" {
		t.Fatalf("csv keeps legit newline, drops controls: %q", b.String())
	}
}
