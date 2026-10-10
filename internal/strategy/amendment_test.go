package strategy

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

// B-518: a justification is protocol. It is read in its common spellings and
// removed before anything stores the reply.
func TestRemovalJustificationIsParsedAndStripped(t *testing.T) {
	reply := "Removal authorized (req-008): \"Remove the real-scalar error example\"\n" +
		"**Removal authorized (REQ-009):** “drop the confirmation step”\n\n" +
		"## REQ-008 — Fractions\n\nBody stays.\n"
	got := ParseRemovalJustifications(reply)
	if len(got) != 2 || got[0].Section != "REQ-008" || got[0].Quote != "Remove the real-scalar error example" ||
		got[1].Section != "REQ-009" || got[1].Quote != "drop the confirmation step" {
		t.Fatalf("justifications = %+v", got)
	}
	stripped := StripRemovalJustifications(reply)
	if strings.Contains(stripped, "Removal authorized") || !strings.Contains(stripped, "## REQ-008 — Fractions\n\nBody stays.") {
		t.Fatalf("stripped reply = %q", stripped)
	}
	// Prose that merely mentions the phrase inside a section is not protocol.
	inline := "## REQ-001 — X\n\nNo removal authorized (REQ-001) here.\n"
	if len(ParseRemovalJustifications(inline)) != 0 || StripRemovalJustifications(inline) != inline {
		t.Fatalf("an inline mention was treated as a justification")
	}
}

// The scheduler records the rounds it spent, on every exit, so a stage can
// give a post-composition repair only what is left.
func TestExecuteScriptRecordsRoundsUsed(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", nil)
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — A\n\nold"},
		verdictOutcome("request-changes", agent.Finding{Severity: "major", Issue: "x", Fix: "y"}),
		&agent.Outcome{Text: "## REQ-001 — A\n\nnew"},
		verdictOutcome("approve"),
	)
	if _, err := ExecuteScript(context.Background(), script, params); err != nil {
		t.Fatal(err)
	}
	if script.RoundsUsed != 2 {
		t.Fatalf("RoundsUsed = %d, want 2", script.RoundsUsed)
	}
}

// Without a guard nothing changes: no removal check, no delta.
func TestNoAmendmentGuardMeansNoRemovalCheck(t *testing.T) {
	rec := &recorder{}
	var kinds []string
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — A\n\nbody"},
		verdictOutcome("approve"),
	)
	params.OnEvent = func(kind string, _ map[string]interface{}) { kinds = append(kinds, kind) }
	if _, err := ExecuteScript(context.Background(), CouncilScript("REQ", nil), params); err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds {
		if k == "structure_check" || strings.HasPrefix(k, "removal_") {
			t.Fatalf("unexpected %s without an amendment guard", k)
		}
	}
	if strings.Contains(strings.Join(rec.prompts, "\n"), "Amendment delta") {
		t.Fatalf("a first draft's critic was shown an amendment delta")
	}
}

// The final document review (rounds exhausted on request-changes) gets the
// same delta and the same blocking removals as the in-loop critics.
func TestFinalDocumentReviewCarriesTheAmendmentContext(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", nil)
	script.MaxRounds = 1
	script.Amendment = &AmendmentGuard{
		Delta: func(string) string { return "## Amendment delta — DELTA-MARKER" },
		Removals: func(string, []RemovalJustification) RemovalReport {
			return RemovalReport{Findings: []string{"REQ-001: approved content absent from the candidate: \"gone\""}}
		},
	}
	params := councilParams(rec,
		&agent.Outcome{Text: "## REQ-001 — A\n\nv1"},
		&agent.Outcome{Text: "## REQ-001 — A\n\nv1"}, // the one removal repair, unchanged
		verdictOutcome("approve"),                    // lowered
		&agent.Outcome{Text: "## REQ-001 — A\n\nv1"}, // revision, still removing
		verdictOutcome("approve"),                    // final review, lowered
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	final := rec.prompts[len(rec.prompts)-1]
	if !strings.Contains(final, "DELTA-MARKER") || !strings.Contains(final, "Engine-detected unauthorized removals — blocking") {
		t.Fatalf("final review prompt lacks the amendment context:\n%s", final)
	}
	if res.State.Verdict != "request-changes" {
		t.Fatalf("final verdict = %q, want the unresolved removal to block", res.State.Verdict)
	}
}

// The repair allowance is per round: a round-2 revision that removes content
// again gets its own repair turn, not the exhausted budget of round 1.
func TestRemovalRepairAllowanceResetsEachRound(t *testing.T) {
	rec := &recorder{}
	script := CouncilScript("REQ", nil)
	script.Amendment = &AmendmentGuard{
		Removals: func(candidate string, _ []RemovalJustification) RemovalReport {
			if strings.Contains(candidate, "bad") {
				return RemovalReport{Findings: []string{"REQ-001: approved content absent from the candidate: " + candidate}}
			}
			return RemovalReport{}
		},
	}
	draft := func(s string) *agent.Outcome { return &agent.Outcome{Text: "## REQ-001 — A\n\n" + s} }
	params := councilParams(rec,
		draft("bad1"), draft("bad2"), draft("bad3"), // round 1: two repairs, then unresolved
		verdictOutcome("approve"),    // lowered
		draft("bad4"),                // round 1 revision: allowance spent, unresolved
		verdictOutcome("approve"),    // round 2, lowered
		draft("bad5"), draft("good"), // round 2 revision: its own repair
		verdictOutcome("approve"), // round 3
	)
	res, err := ExecuteScript(context.Background(), script, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 3 || res.State.Verdict != "approve" || !strings.Contains(res.Text, "good") {
		t.Fatalf("rounds=%d verdict=%s text=%q, want round 2's repaired revision approved in round 3", res.Rounds, res.State.Verdict, res.Text)
	}
}
