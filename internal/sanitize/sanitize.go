// Package sanitize is the one place server-supplied text is made safe for a terminal sink
// (stderr messages, table cells, credit lines, CSV cells). Nothing else in the CLI strips controls.
package sanitize

import (
	"regexp"
	"strings"
	"unicode"
)

// ansiSeq matches whole ANSI sequences (CSI and OSC, in ESC and C1 forms, plus two-byte escapes),
// not just the ESC byte.
var ansiSeq = regexp.MustCompile(`(?s)(?:\x1b|\x{9b})\[[0-?]*[ -/]*[@-~]|(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}]*(?:\x07|\x1b\\|\x{9c})?|\x1b[@-_]`)

// invisible reports the characters that render as nothing or reorder what is shown: the bidi
// embeddings, overrides and isolates (U+202A-202E, U+2066-2069) and the zero-width characters
// (U+200B-200D, U+FEFF). They are removed everywhere.
func invisible(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) ||
		(r >= 0x200B && r <= 0x200D) || r == 0xFEFF
}

// strip removes ANSI sequences, invisible characters and every control character except the runes
// in keep. Characters in space are replaced by one space instead.
func strip(s, keep, space string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(space, r):
			return ' '
		case strings.ContainsRune(keep, r):
			return r
		case invisible(r), unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// Line makes s safe as exactly one terminal line: CR, LF, tab, U+2028 and U+2029 become single
// spaces (CRLF becomes one); ANSI sequences, other C0/C1 controls (ESC, DEL included), bidi
// controls and zero-width characters are removed.
func Line(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strip(s, "", "\n\r\t  ")
}

// CSVCell keeps legitimate line breaks (newlines, and U+2028/U+2029, are valid inside a quoted
// RFC 4180 cell) and removes ANSI sequences, bidi controls, zero-width characters and every other
// control character.
func CSVCell(s string) string { return strip(s, "\n\r\t  ", "") }
