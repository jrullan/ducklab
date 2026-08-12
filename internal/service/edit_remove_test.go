package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A report could be moved, triaged and promoted but never edited, so a typo or a
// missing detail lived as long as the bug did — and the triager, and then the
// implementer, worked from it.
func TestAReportCanBeCorrected(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "drag broke", Body: "somethign is wrong", Severity: "low",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{
		Title:    "dragging a vertex does not update the left edge label",
		Body:     "In the default triangle, drag the top vertex.",
		Severity: "critical",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Title, "left edge label") || got.Severity != "critical" {
		t.Errorf("edit did not take: %+v", got)
	}
	// The loop's own fields are not a form's business: status, task and
	// duplicate have transitions, and letting an edit set them would put the
	// loop's rules in two places.
	if string(got.Status) != "open" {
		t.Errorf("status changed to %q", got.Status)
	}
}

func TestAnUnknownSeverityIsRefused(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Severity: "urgent"}); err == nil {
		t.Error("an invented severity was accepted")
	}
}

// Removing a promoted task must put its report back, or the bug sits in
// in_progress forever pointing at a task nobody can find.
func TestRemovingAPromotedTaskReturnsItsReport(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "drag broke"}); err != nil {
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

	res, err := s.TaskRemove(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if res["bug"] != "B-001" {
		t.Errorf("the report was not unlinked: %v", res)
	}
	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status != "triaged" || bugs[0].TaskID != "" {
		t.Errorf("bug = %+v", bugs[0])
	}
	// And it is gone from the document every reader parses, not just the
	// database.
	tasks, _ := s.TaskList(context.Background(), id)
	for _, tk := range tasks {
		if tk.ID == taskID {
			t.Errorf("%s is still in the plan", taskID)
		}
	}
	// Promotable again, which is the point.
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Errorf("the report cannot be promoted again: %v", err)
	}
}

// An accepted run's work is committed and traced to its task, so the task must
// stay. And a run still going would be orphaned mid-flight.
func TestAcceptedAndOpenRunsPinTheirTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.Close()
	s.RecoverRuns(context.Background())

	if _, err := s.TaskRemove(context.Background(), id, "T-001"); err == nil {
		t.Fatal("a task with committed work was removed")
	} else if !strings.Contains(err.Error(), "committed") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	if _, err := s.TaskRemove(context.Background(), id, "T-002"); err != nil {
		t.Errorf("T-002 has no runs at all and was refused: %v", err)
	}
}

// The workflow removal exists for: a bug promoted badly, run, and failed. The
// first guard refused ANY run — which made undoing a bad promotion impossible,
// because you learn it was bad by running it.
func TestAFailedRunDoesNotPinItsTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	writeRun(t, dir, id, "r-1", "failed")
	s.RecoverRuns(context.Background())

	if _, err := s.TaskRemove(context.Background(), id, "T-001"); err != nil {
		t.Fatalf("a failed run pinned its task: %v", err)
	}
	// History is history: the run record survives the task.
	if d, err := s.RunGet(context.Background(), "r-1"); err != nil || d.Run.TaskID != "T-001" {
		t.Errorf("the failed run's record was disturbed: %v", err)
	}
}

// A run paused at its gate is a decision nobody has made yet.
func TestAPausedRunPinsItsTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	writeRun(t, dir, id, "r-1", "paused")
	s.RecoverRuns(context.Background())

	_, err := s.TaskRemove(context.Background(), id, "T-001")
	if err == nil {
		t.Fatal("a task with an undecided run was removed")
	}
	if !strings.Contains(err.Error(), "abort or decide") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}
