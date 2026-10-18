package output

import (
	"regexp"
	"strings"
	"unicode"
)

// A local sanitizer for server-supplied text. It should be unified with apierr.Sanitize once
// that lands on main (#13); the semantics here are a superset: whole ANSI sequences go, not just
// the ESC byte, and Line also collapses whitespace.
var ansiSeq = regexp.MustCompile(`(?s)(?:\x1b|\x{9b})\[[0-?]*[ -/]*[@-~]|(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}]*(?:\x07|\x1b\\|\x{9c})?|\x1b[@-_]`)

// stripControls removes ANSI sequences and every control character except the ones keep names.
func stripControls(s string, keep string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !strings.ContainsRune(keep, r) {
			return -1
		}
		return r
	}, s)
}

// Line makes s safe as exactly one terminal line: CR, LF and tabs become single spaces, ANSI
// sequences and all other C0/C1 controls (ESC, DEL included) are removed.
func Line(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return stripControls(s, "")
}

// CSVCell keeps legitimate whitespace (newlines are valid inside a quoted RFC 4180 cell) and
// removes ESC sequences and every other control character.
func CSVCell(s string) string { return stripControls(s, "\n\r\t") }
