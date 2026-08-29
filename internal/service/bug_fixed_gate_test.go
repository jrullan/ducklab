package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
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
	if err == nil || !strings.Contains(err.Error(), "every proposed task is accepted") {
		t.Fatalf("err = %v, want the unlanded-portion refusal", err)
	}
}
