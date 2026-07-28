package review

import (
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
)

var at = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func TestRenderRecordsTheVerdictAndWhereItCameFrom(t *testing.T) {
	got := Render(Record{
		TaskID: "T-001", Title: "Fix Add", Mode: "solo",
		RunID: "r-1", CommitSHA: "abc1234", Ducklings: []string{"pato-uno"},
		Verdict: &agent.Verdict{Verdict: "approve"},
		At:      at,
	})
	for _, want := range []string{
		"kind: review", "task: T-001", "verdict: approve",
		"run_id: r-1", "commit: abc1234", "mode: solo",
		"ducklings: [pato-uno]", "reviewed_at: 2026-07-28T12:00:00Z",
		"# Review — Fix Add", "No findings.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the review does not record %q:\n%s", want, got)
		}
	}
}

// The reader must meet the critical findings first, whatever order a model
// happened to emit them in. That order is not information.
func TestRenderOrdersFindingsBySeverity(t *testing.T) {
	got := Render(Record{
		TaskID: "T-001", At: at,
		Verdict: &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{
			{Severity: "minor", File: "z.go", Issue: "naming"},
			{Severity: "critical", File: "a.go", Line: 4, Issue: "nil deref", Fix: "guard it"},
			{Severity: "major", File: "m.go", Issue: "leak"},
		}},
	})
	ci, mi, ni := strings.Index(got, "nil deref"), strings.Index(got, "leak"), strings.Index(got, "naming")
	if !(ci < mi && mi < ni) {
		t.Errorf("findings are not ordered critical, major, minor:\n%s", got)
	}
	if !strings.Contains(got, "`a.go:4`") {
		t.Error("a finding with a line does not say where")
	}
	if !strings.Contains(got, "guard it") {
		t.Error("the suggested fix was dropped")
	}
}

// An unknown severity sorts last rather than first: inventing urgency for a
// word we do not recognise would push real critical findings down the page.
func TestRenderDoesNotPromoteAnUnknownSeverity(t *testing.T) {
	got := Render(Record{
		TaskID: "T-001", At: at,
		Verdict: &agent.Verdict{Verdict: "request-changes", Findings: []agent.Finding{
			{Severity: "spicy", File: "a.go", Issue: "unknown severity"},
			{Severity: "critical", File: "b.go", Issue: "real problem"},
		}},
	})
	if strings.Index(got, "real problem") > strings.Index(got, "unknown severity") {
		t.Errorf("an unrecognised severity outranked a critical one:\n%s", got)
	}
}

// Blocking with nothing to fix is a reviewer failing its job, and the record
// must not read like a considered rejection.
func TestRenderSaysWhenChangesWereRequestedWithNoFindings(t *testing.T) {
	got := Render(Record{
		TaskID: "T-001", At: at,
		Verdict: &agent.Verdict{Verdict: "request-changes"},
	})
	if !strings.Contains(got, "no findings given") {
		t.Errorf("a contentless rejection reads as a clean review:\n%s", got)
	}
}

// A review of a task nothing has reviewed yet must not claim a verdict.
func TestRenderIsHonestAboutHavingNoVerdict(t *testing.T) {
	got := Render(Record{TaskID: "T-001", At: at})
	if !strings.Contains(got, "verdict: unreviewed") {
		t.Errorf("a missing verdict was rendered as something:\n%s", got)
	}
}
