package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The guide, localized: a bug's card must answer "where is this and what is
// next" without sending the person to another view. Before this the card
// said "Run it from the tasks board" — a pointer without a door.
func TestABugsJourneyWalksItsLadderAndNamesTheDoor(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "the edge label lags", Body: "drag a vertex"}); err != nil {
		t.Fatal(err)
	}

	// Open: the door is triage; the ladder is at its first rung.
	j, err := s.NextFor(context.Background(), id, "B-001")
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.ID != "triage" {
		t.Fatalf("open bug door = %+v, want triage", j.Door)
	}
	if j.Rungs[0].State != "current" || j.Rungs[1].State != "next" {
		t.Fatalf("open rungs = %+v", j.Rungs)
	}

	// Triaged: the door is promote.
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "task_title": "Recompute the edge label on drag",
	}}); err != nil {
		t.Fatal(err)
	}
	j, err = s.NextFor(context.Background(), id, "B-001")
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.ID != "promote" {
		t.Fatalf("triaged bug door = %+v, want promote", j.Door)
	}
	if j.Rungs[0].State != "done" || j.Rungs[1].State != "current" {
		t.Fatalf("triaged rungs = %+v", j.Rungs)
	}

	// In progress: the door is the TASK's door, shown on the bug — the
	// person does not travel to the board to learn that the next act is to
	// write the test or build.
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	j, err = s.NextFor(context.Background(), id, "B-001")
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.Kind != "task" || j.Door.Ref != taskID {
		t.Fatalf("in-progress bug door = %+v, want the task %s's door", j.Door, taskID)
	}
	if !strings.HasPrefix(j.Door.Reason, taskID+": ") {
		t.Fatalf("the delegated door does not name its task: %q", j.Door.Reason)
	}
}

// A task with a committed failing test has ONE obvious next act, and the
// door says so in words — not "Build it" beside "Test first" as peers.
func TestATestReadyTasksDoorIsTheBuildOfItsCommittedTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "tests", "verify.tests": "true"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("no tasks: %v", err)
	}
	taskID := tasks[0].ID

	// Before any test: a tests-gated task's front door is test-first.
	j, err := s.NextFor(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.ID != "test-first" {
		t.Fatalf("fresh task door = %+v, want test-first", j.Door)
	}

	// An accepted test-first run: the door is the build, named for the test.
	run := &runlog.Run{
		ID: "r-red", ProjectID: id, TaskID: taskID, Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "abc123",
		StartedAt: "2026-08-06T15:14:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	j, err = s.NextFor(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.ID != "build" || !strings.Contains(j.Door.Action, "committed test") {
		t.Fatalf("test-ready door = %+v, want the build of the committed test", j.Door)
	}
	var test, build string
	for _, r := range j.Rungs {
		switch r.ID {
		case "test":
			test = r.State
		case "build":
			build = r.State
		}
	}
	if test != "done" || build != "current" {
		t.Fatalf("rungs test=%s build=%s, want done/current: %+v", test, build, j.Rungs)
	}
}

// Accepted work's next act is shipping: the release door leads, and the
// redo stays behind it.
func TestAnAcceptedTasksDoorIsTheRelease(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("no tasks: %v", err)
	}
	run := &runlog.Run{
		ID: "r-built", ProjectID: id, TaskID: tasks[0].ID, Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: "def456",
		StartedAt: "2026-08-06T16:00:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	j, err := s.NextFor(context.Background(), id, tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Door == nil || j.Door.ID != "release" {
		t.Fatalf("accepted task door = %+v, want release", j.Door)
	}
	if j.Rungs[len(j.Rungs)-1].State != "current" {
		t.Fatalf("accepted rung is not current: %+v", j.Rungs)
	}
}

// Unknown refs are refused with the shape named, not with an empty journey.
func TestAJourneyForAnUnknownRefIsRefused(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.NextFor(context.Background(), id, "X-9"); err == nil || !strings.Contains(err.Error(), "B-001") {
		t.Fatalf("err = %v, want a refusal naming the ref shapes", err)
	}
}
