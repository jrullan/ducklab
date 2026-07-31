package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Every other run records what it did — build, review, release. Triage recorded
// "operate", the name of the whole loop.
//
// Runs are labelled by task id and fall back to the stage, so a triage run,
// which has no task, appeared in the list as "operate" with nothing to say what
// had actually run. And "operate" will only ever mean triage, because the other
// half of that loop — running the task a bug was promoted into — is an ordinary
// build with a task id of its own.
func TestATriageRunSaysItIsATriage(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "critical",
	}); err != nil {
		t.Fatal(err)
	}

	run, err := s.BugTriage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Stage != "triage" {
		t.Errorf("stage = %q, want triage", run.Stage)
	}
}
