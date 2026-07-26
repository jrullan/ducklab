package conv

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func sampleTranscript() *Transcript {
	tr := &Transcript{}
	tr.Add(Entry{Round: 1, Index: 0, Role: config.RoleImplementer, Duckling: "pato-local",
		Text: "I changed auth.go because the token check was inverted."})
	tr.Add(Entry{Round: 1, Index: 1, Role: config.RoleReviewer, Duckling: "pato-nube",
		Text: `{"verdict":"request-changes","findings":[]}`})
	return tr
}

func TestRenderShowsDucklingIdsWhenNotAnonymised(t *testing.T) {
	got := sampleTranscript().Render(false, "")
	if !strings.Contains(got, "pato-local") || !strings.Contains(got, "pato-nube") {
		t.Errorf("ids missing from a non-anonymised transcript:\n%s", got)
	}
}

// I7: an anonymised render must not leak identity, and must drop the author's
// own reasoning so a reviewer cannot adopt it.
func TestAnonymisedRenderHidesIdentityAndAuthorReasoning(t *testing.T) {
	got := sampleTranscript().Render(true, config.RoleImplementer)

	for _, id := range []string{"pato-local", "pato-nube"} {
		if strings.Contains(got, id) {
			t.Errorf("anonymised transcript leaked duckling id %q:\n%s", id, got)
		}
	}
	if strings.Contains(got, "because the token check was inverted") {
		t.Errorf("the author's reasoning survived anonymisation:\n%s", got)
	}
}

func TestLabelsAreStableWithinAConversation(t *testing.T) {
	tr := &Transcript{}
	first := tr.Label("pato-local")
	if tr.Label("pato-local") != first {
		t.Error("label changed for the same duckling")
	}
	second := tr.Label("pato-nube")
	if second == first {
		t.Error("two ducklings got the same label")
	}
	if first != "A" || second != "B" {
		t.Errorf("labels = %q, %q; want A, B by first appearance", first, second)
	}
}

func TestRenderEmptyTranscriptIsEmpty(t *testing.T) {
	if got := (&Transcript{}).Render(false, ""); got != "" {
		t.Errorf("empty transcript rendered %q", got)
	}
}

// Candidate labels must not encode who produced them, or a judge learns a
// positional convention across runs (05 §4.3).
func TestCandidateLabelsFollowContentNotDucklingOrder(t *testing.T) {
	a := Candidate{Duckling: "pato-local", Diff: "--- a/x.go\n+++ b/x.go\n+first"}
	b := Candidate{Duckling: "pato-nube", Diff: "--- a/y.go\n+++ b/y.go\n+second"}

	one := AnonymiseCandidates([]Candidate{a, b})
	two := AnonymiseCandidates([]Candidate{b, a}) // same content, reversed input

	label := func(cs []Candidate, id config.DucklingID) string {
		for _, c := range cs {
			if c.Duckling == id {
				return c.Label
			}
		}
		return ""
	}
	if label(one, "pato-local") != label(two, "pato-local") {
		t.Error("a candidate's label depends on input order; the judge could learn a positional convention")
	}
	if label(one, "pato-local") == label(one, "pato-nube") {
		t.Error("two candidates share a label")
	}
}

func TestRenderCandidatesNeverNamesTheAuthor(t *testing.T) {
	cands := AnonymiseCandidates([]Candidate{
		{Duckling: "pato-local", Diff: "+a", Gate: "green"},
		{Duckling: "pato-nube", Diff: "+b", Gate: "red", GateLog: "FAIL TestX"},
	})
	got := RenderCandidates(cands)
	for _, id := range []string{"pato-local", "pato-nube"} {
		if strings.Contains(got, id) {
			t.Errorf("candidate rendering leaked %q:\n%s", id, got)
		}
	}
	if !strings.Contains(got, "Candidate A") || !strings.Contains(got, "Candidate B") {
		t.Errorf("labels missing:\n%s", got)
	}
	if !strings.Contains(got, "PASSED") || !strings.Contains(got, "FAILED") {
		t.Errorf("gate results must be shown to the judge:\n%s", got)
	}
}

func TestGreenCandidates(t *testing.T) {
	cands := []Candidate{{Label: "A", Gate: "green"}, {Label: "B", Gate: "red"}, {Label: "C", Gate: "green"}}
	green := GreenCandidates(cands)
	if len(green) != 2 {
		t.Fatalf("got %d green, want 2", len(green))
	}
}

func TestFindCandidate(t *testing.T) {
	cands := []Candidate{{Label: "A"}, {Label: "B"}}
	if FindCandidate(cands, "B") == nil {
		t.Error("existing label not found")
	}
	if FindCandidate(cands, "Z") != nil {
		t.Error("missing label returned a candidate")
	}
}

// AC-17: round 2's implementer prompt must carry round 1's findings.
func TestRenderFindingsProducesActionableLines(t *testing.T) {
	got := RenderFindings([]Finding{
		{Severity: "major", File: "auth.go", Line: 88, Issue: "nil deref when the token is expired", Fix: "guard before deref"},
		{Severity: "minor", File: "auth.go", Issue: "unused import", Fix: "remove it"},
	})
	if !strings.Contains(got, "[major] auth.go:88") {
		t.Errorf("missing anchored finding:\n%s", got)
	}
	if !strings.Contains(got, "guard before deref") {
		t.Errorf("missing suggested fix:\n%s", got)
	}
	if !strings.Contains(got, "[minor] auth.go —") {
		t.Errorf("a finding without a line should still render:\n%s", got)
	}
}

func TestRenderFindingsEmptyIsEmpty(t *testing.T) {
	if got := RenderFindings(nil); got != "" {
		t.Errorf("no findings rendered %q", got)
	}
}
