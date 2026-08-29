package conv

import (
	"strings"
	"testing"
)

// The implementer reads findings as rendered text. A class-level finding must
// arrive as the rule it is, not as an empty location.
func TestRenderFindingsNamesTheInvariantOfAClassFinding(t *testing.T) {
	out := RenderFindings([]Finding{
		{Severity: "major", File: "*", Invariant: "the published ref is the ref acceptance advances", Issue: "three sites pick their own branch", Fix: "define it once"},
		{Severity: "minor", File: "remote.go", Line: 12, Issue: "unused import", Fix: "remove it"},
	})
	for _, want := range []string{
		"[major] everywhere the invariant applies — three sites pick their own branch → define it once (invariant: the published ref is the ref acceptance advances)",
		"[minor] remote.go:12 — unused import → remove it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered findings lack %q:\n%s", want, out)
		}
	}
}
