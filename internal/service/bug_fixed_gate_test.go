package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/store"
)

// The fixed gate is "every portion landed", not "a proposal exists". A bug
// the triager read but a person then fixed by hand — no task promoted — has
// no portion in flight and must be allowed to become fixed (B-286,
// 2026-08-29: stranded in in_progress by exactly this).
func TestABugWithAProposalButNoPromotedTaskCanBeFixed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "the reviewer dribbles one class over rounds", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := db.GetBug("B-001")
	if err != nil {
		t.Fatal(err)
	}
	rec.Proposal = `{"tasks":[{"title":"state invariants first"}]}`
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, to := range []string{"triaged", "in_progress", "fixed"} {
		if _, err := s.BugMove(context.Background(), id, "B-001", to, "human"); err != nil {
			t.Fatalf("move to %s: %v", to, err)
		}
	}
}

// With a portion actually in flight the gate still holds: a promoted task
// that has not been accepted blocks fixed and says so.
func TestABugWithAnUnlandedPromotedTaskCannotBeFixed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "the header forgets the name", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := db.GetBug("B-001")
	if err != nil {
		t.Fatal(err)
	}
	rec.Proposal = `{"tasks":[{"title":"fix the header"}]}`
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = s.BugMove(context.Background(), id, "B-001", "fixed", "human")
	if err == nil || !strings.Contains(err.Error(), "proposed task(s) T-003 (todo) are accepted") {
		t.Fatalf("err = %v, want the unlanded-portion refusal", err)
	}
}

// Reopening starts a fresh attempt. Historical trace edges remain useful
// provenance, but an abandoned sibling from the old promotion must neither
// block the new fix nor masquerade as current work (B-428).
func TestAReopenedBugIgnoresStaleTasksFromThePreviousPromotion(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "the first fix did not hold", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := db.GetBug("B-001")
	rec.Status = "fixed"
	rec.TaskID = "T-001"
	rec.Proposal = `[{"title":"old split"}]`
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{ID: "T-001", Title: "old landed half", Status: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{ID: "T-002", Title: "old abandoned half", Status: "todo"}); err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"T-001", "T-002"} {
		if err := db.AddTrace("bug", "B-001", "task", taskID); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	if _, err := s.BugMove(context.Background(), id, "B-001", "in_progress", "human"); err != nil {
		t.Fatal(err)
	}
	db, err = s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ = db.GetBug("B-001")
	rec.Status = "in_progress"
	rec.TaskID = "T-003"
	rec.Proposal = `[{"title":"new fix"}]`
	if err := db.UpdateBug(rec); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(&store.Task{ID: "T-003", Title: "new fix", Status: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTrace("bug", "B-001", "task", "T-003"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := s.BugMove(context.Background(), id, "B-001", "fixed", "human"); err != nil {
		t.Fatalf("stale task T-002 blocked the current fix: %v", err)
	}
}
