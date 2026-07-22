// Package prim holds ducklab's shared primitives: text helpers, file-block and
// SEARCH/REPLACE parsing, portable shell/git, and the role prompts every
// strategy composes from. Nothing here talks to a model or a terminal — it is
// the deterministic core the orchestrator drives.
package prim

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts arbitrary text into a filesystem- and branch-safe slug:
// lowercase, every run of non-alphanumerics collapsed to a single hyphen, with
// no leading or trailing hyphens.
func Slugify(text string) string {
	text = strings.ToLower(text)
	text = nonSlug.ReplaceAllString(text, "-")
	return strings.Trim(text, "-")
}

// TruncateMiddle shortens text to at most maxLen runes, preserving the start
// and the end and marking the elision with "…". Structure tends to live at the
// head of a diff and tests at the tail, so keeping both ends beats a plain cut.
func TruncateMiddle(text string, maxLen int) string {
	if utf8.RuneCountInString(text) <= maxLen {
		return text
	}
	if maxLen <= 0 {
		return ""
	}
	r := []rune(text)
	half := maxLen / 2
	endLen := maxLen - half - 1
	start := string(r[:half])
	end := ""
	if endLen > 0 {
		end = string(r[len(r)-endLen:])
	}
	return start + "…" + end
}
