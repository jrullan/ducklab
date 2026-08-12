package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The exact sequence a real project produced, to the second: a bug triaged
// twice (a double-launch left a second gate pending), promoted into a task,
// and then the STALE gate accepted — which regressed it from in_progress to
// triaged, because Move(InProgress, Triaged) is a legal transition and the
// apply relied on Move to refuse. The task's accept then looked for
// in_progress, found triaged, and closed nothing, silently.
func TestAStaleTriageCannotUndoAPromotion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "Angle in red vertex does not allow changing", Severity: "normal",
	}); err != nil {
		t.Fatal(err)
	}
	// First triage, accepted; then promoted.
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}

	// The second, stale gate — accepted after the promotion.
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "critical", "component": "angle editing",
	}}); err != nil {
		t.Fatal(err)
	}

	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status != "in_progress" {
		t.Errorf("status = %q — the stale triage undid the promotion", bugs[0].Status)
	}
	// Its words still update; its place in the loop is not triage's to take.
	if bugs[0].Severity != "critical" {
		t.Errorf("severity = %q, want the newer classification", bugs[0].Severity)
	}
}

// And even when something HAS knocked the report backwards, the task's accept
// walks it to fixed from wherever it stands instead of demanding in_progress
// exactly and skipping in silence.
func TestTheClosureWalksFromWhereverTheBugStands(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	// Simulate the historical damage: knocked back after promotion.
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}

	fixed, err := s.BugFixedByTask(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if fixed != "B-001" {
		t.Fatalf("nothing was closed: %q", fixed)
	}
	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status != "fixed" {
		t.Errorf("status = %q, want fixed", bugs[0].Status)
	}
}
