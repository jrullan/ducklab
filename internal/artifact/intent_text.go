package artifact

import (
	"regexp"
	"strings"
)

// Intent text fences (review of #175). intent.md interleaves engine-written
// structure — the **Run:** header, "### Revision N — run" headings — with
// words a person (or a reviewer) wrote: the brief, a revision's note, the
// findings carried over. Read as plain Markdown, those words could forge the
// structure: a brief containing "### Requirements" swallowed the rest of the
// entry in the desktop, a line "### Revision 2 — r-x" read as chain metadata,
// a "**Revisions:**" or "**Outcome:**" line overrode the header, and a
// "## INT-009 — …" line split the journal.
//
// The engine therefore fences every user-written body between two HTML
// comments it alone writes, and neutralizes any line inside the body that
// could be read as such a comment. Parsers treat fenced lines as opaque text:
// no headings, no fields. The comments are invisible in rendered Markdown.
const (
	intentTextOpenPrefix = "<!-- ducklab:text "
	intentTextClose      = "<!-- /ducklab:text -->"
)

// intentMarkerLike matches a line that could be read as a ducklab comment,
// however spaced or cased. Inside user text its "<" is written as "&lt;",
// which renders identically and never parses as a fence.
var intentMarkerLike = regexp.MustCompile(`(?i)^(\s*)<(!--\s*/?\s*ducklab\s*:)`)

// fenceIntentText wraps user-written text of one kind (brief, note,
// findings) in the engine's fence.
func fenceIntentText(kind, text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = intentMarkerLike.ReplaceAllString(line, "${1}&lt;${2}")
	}
	return intentTextOpenPrefix + kind + " -->\n" + strings.Join(lines, "\n") + "\n" + intentTextClose
}

func isIntentTextOpen(line string) bool {
	return strings.HasPrefix(line, intentTextOpenPrefix) && strings.HasSuffix(strings.TrimRight(line, " \t\r"), "-->")
}

func isIntentTextClose(line string) bool {
	return strings.TrimRight(line, " \t\r") == intentTextClose
}

// intentFence tracks whether a line sits inside a fenced body.
type intentFence struct{ inside bool }

// opaque reports whether line is fenced text (the fence lines included), and
// advances the state.
func (f *intentFence) opaque(line string) bool {
	switch {
	case f.inside && isIntentTextClose(line):
		f.inside = false
		return true
	case f.inside:
		return true
	case isIntentTextOpen(line):
		f.inside = true
		return true
	}
	return false
}

// withoutIntentText is the engine-written part of an intent body: the lines
// fields may be read from. Bodies written before the fences keep their old
// meaning — every line is read.
func withoutIntentText(body string) string {
	if !strings.Contains(body, intentTextOpenPrefix) {
		return body
	}
	var out []string
	var fence intentFence
	for _, line := range strings.Split(body, "\n") {
		if !fence.opaque(line) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
