package sanitize

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	in := "bad\x1b[31m red\x1b]0;title\x07 \r\nnext\x7f\u009b end"
	if got := Line(in); got != "bad red  next end" {
		t.Fatalf("%q", got)
	}
	if Line("a\tb\nc\rd") != "a b c d" {
		t.Fatal("whitespace not collapsed")
	}
	if Line("plain text, accents é") != "plain text, accents é" {
		t.Fatal("printable text altered")
	}
}

func TestLineStripsUnicodeSeparatorsBidiAndZeroWidth(t *testing.T) {
	if got := Line("a\u2028b\u2029c"); got != "a b c" {
		t.Fatalf("separators: %q", got)
	}
	for _, r := range []rune{0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069, 0x200B, 0x200C, 0x200D, 0xFEFF} {
		if got := Line("a" + string(r) + "b"); got != "ab" {
			t.Errorf("U+%04X survived Line: %q", r, got)
		}
		if got := CSVCell("a" + string(r) + "b"); got != "ab" {
			t.Errorf("U+%04X survived CSVCell: %q", r, got)
		}
	}
	if strings.Contains(Line("fdp\u202Egnp.exe"), "\u202E") {
		t.Fatal("override survived")
	}
}

func TestCSVCell(t *testing.T) {
	if got := CSVCell("p\nq\u001b[0m\u0007\u2028r\u2029s"); got != "p\nq\u2028r\u2029s" {
		t.Fatalf("%q", got)
	}
	if got := CSVCell("a\tb\r\nc"); got != "a\tb\r\nc" {
		t.Fatalf("%q", got)
	}
}
