package service

import (
	"strings"
	"testing"
)

// Request changes on v0.6.3 produced the whole release twice: the addendum
// showed the scribe the entire prior file, the scribe returned a whole
// document, and Render wrapped it again (B-116). The extractor is the belt,
// the prose-only addendum the suspenders; this pins both.
func TestExtractReleaseProseSurvivesAWholeDocumentReply(t *testing.T) {
	reply := "---\nkind: release\nversion: v0.6.3\nsince: v0.6.2\ntasks: 2\n---\n\n" +
		"# v0.6.3\n\n" +
		"**Queued runs:** the real story.\n\n**Settings:** the cap is editable.\n\n" +
		"## What shipped\n\n### M-001\n\n- **T-096** something (`abc1234`)\n"
	got := extractReleaseProse(reply)
	for _, banned := range []string{"---", "kind: release", "# v0.6.3", "What shipped", "abc1234"} {
		if strings.Contains(got, banned) {
			t.Errorf("prose still carries scaffolding %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"the real story", "the cap is editable"} {
		if !strings.Contains(got, want) {
			t.Errorf("prose lost its content %q", want)
		}
	}

	// A reply that already IS prose passes through whole.
	if got := extractReleaseProse("**Queued runs:** honest queueing.\n"); got != "**Queued runs:** honest queueing." {
		t.Errorf("plain prose was mangled: %q", got)
	}
}

func TestRevisionAddendumCarriesProseOnlyAndSaysSo(t *testing.T) {
	prior := "---\nkind: release\nversion: v0.6.3\n---\n\n# v0.6.3\n\nThe prose.\n\n## What shipped\n\n- T-096\n"
	got := revisionAddendum("fix the heading", prior)
	if strings.Contains(got, "kind: release") || strings.Contains(got, "What shipped") {
		t.Error("the addendum still shows the scribe the scaffolding")
	}
	if !strings.Contains(got, "The prose.") || !strings.Contains(got, "ONLY the prose") {
		t.Errorf("the addendum lost the prose or the contract:\n%s", got)
	}
}
