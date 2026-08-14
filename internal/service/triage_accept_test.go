package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Accepting a triage did nothing at all.
//
// acceptRun knows two things: promote a document, or commit a diff. A triage is
// neither, so it fell through to the code path below, staged nothing, found the
// tree clean and reported success. Accept and Reject were the same button, the
// classification was discarded with a green tick, and the bug stayed open with
// its updated_at untouched.
func TestAcceptingATriageAppliesIt(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "normal",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.BugTriage(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)

	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}

	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 1 {
		t.Fatalf("got %d bugs", len(bugs))
	}
	if bugs[0].Status != "triaged" {
		t.Errorf("status = %q, want triaged — the accept was discarded", bugs[0].Status)
	}
	// The triager's judgement, not the reporter's guess.
	if bugs[0].Severity != "critical" {
		t.Errorf("severity = %q, want the triager's critical", bugs[0].Severity)
	}
}

// Agreeing with a classification is not the same decision as committing to fix
// it, and a triage that silently filled the board would take that away.
func TestAcceptingATriageDoesNotCreateATask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	before, _ := s.TaskList(context.Background(), id)
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x", Severity: "low"}); err != nil {
		t.Fatal(err)
	}
	run, _ := s.BugTriage(context.Background(), id, "")
	_, _ = s.waitForRun(context.Background(), run.ID)
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}

	after, _ := s.TaskList(context.Background(), id)
	if len(after) != len(before) {
		t.Errorf("accepting a triage created %d task(s); promoting is a separate act",
			len(after)-len(before))
	}
	// And the bug is now ready for that act.
	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status != "triaged" {
		t.Errorf("status = %q", bugs[0].Status)
	}
}

// The desktop's store flips a run's status only on what the stream says —
// human_needed, error, run_end. The triage accept set Status="done" in the
// state file and returned without a word, so the rail showed the triage
// "running" forever and the Bugs board never refetched the verdicts it had
// just applied. Ending a run and saying so are the same act.
func TestAcceptingATriageSaysRunEndOnTheStream(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "normal",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.BugTriage(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.waitForRun(context.Background(), run.ID)
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}

	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range detail.Events {
		if e.Type == "run_end" {
			found = true
		}
	}
	if !found {
		t.Error("accepting a triage emitted no run_end — the desktop never hears it finish")
	}
}
