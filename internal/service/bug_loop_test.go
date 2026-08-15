package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The bug loop had an entrance and no exit.
//
// Promoting a report set its task id and moved it to in_progress. Then the task
// was built, reviewed and accepted — and nothing ever moved the bug again. It
// sat on the board as in_progress for good, while the work that answered it was
// committed.
func TestAcceptingATaskMovesItsBugOn(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "vertex drag never starts", Severity: "critical",
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
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	if taskID == "" {
		t.Fatalf("promote made no task: %v", out)
	}

	// The task's work landing is what answers the report.
	fixed, err := s.BugFixedByTask(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if fixed != "B-001" {
		t.Fatalf("no bug was moved on: %q", fixed)
	}

	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status != "fixed" {
		t.Errorf("status = %q, want fixed", bugs[0].Status)
	}
}

// Accepting the red test in a TDD chain records a promise, not the fix.
// Only the chained BUILD acceptance may move the promoted report to fixed.
func TestAcceptingAChainedTestFirstDoesNotFixItsBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "vertex drag never starts", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high",
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	if taskID == "" {
		t.Fatal("promotion created no task")
	}

	run := &runlog.Run{
		ID: "r-test-first", ProjectID: id, TaskID: taskID, Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		StartedAt: "2026-08-06T03:00:12Z",
		ChainBuild: map[string]interface{}{"task_id": taskID, "mode": "solo"},
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	if rs == nil {
		t.Fatal("test-first run was not recovered")
	}
	if _, err := s.ensureWriter(rs); err != nil {
		t.Fatal(err)
	}

	// This is the auto:tdd acceptance of the red test. Its chained build has
	// started, but no implementation has landed yet.
	if _, err := s.RunAcceptAs(context.Background(), run.ID, "chained: the test landed red", "auto:tdd"); err != nil {
		t.Fatal(err)
	}

	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 1 || bugs[0].Status != "in_progress" {
		t.Fatalf("after accepted test-first, bug status = %v, want in_progress", bugs)
	}
}

// "fixed", not "verified". The gate that passed may be a syntax check — this
// project accepted twenty-one tasks against one and the feature never worked.
// Verified is a person saying the report is actually answered, and that is the
// one judgement a run must not make for them (I2).
func TestAnAcceptedTaskDoesNotVerifyItsOwnBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x", Severity: "low"}); err != nil {
		t.Fatal(err)
	}
	run, _ := s.BugTriage(context.Background(), id, "")
	_, _ = s.waitForRun(context.Background(), run.ID)
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	out, _ := s.BugPromote(context.Background(), id, "B-001", "human")
	taskID, _ := out["task"].(string)
	if _, err := s.BugFixedByTask(context.Background(), id, taskID); err != nil {
		t.Fatal(err)
	}
	bugs, _ := s.BugList(context.Background(), id, false)
	if bugs[0].Status == "verified" {
		t.Error("a run verified its own fix")
	}
}

// A task nobody reported a bug about must not disturb anything.
func TestAnOrdinaryTaskTouchesNoBug(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	got, err := s.BugFixedByTask(context.Background(), id, "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("moved %q for a task with no report behind it", got)
	}
}
