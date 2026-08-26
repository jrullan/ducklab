package service

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// T-001 — accepted days earlier — was launched test-first by an overnight
// operator working from a stale listing, and the launch went straight to a
// model. The board hides its buttons on a finished task, but the engine's own
// door was open: any client asking politely got fresh work against something
// committed, then an abort, then a stale failure haunting two views (the
// T-101/T-102 pathology, third occurrence). The engine now holds the door:
// a finished task is refused with the reason, and redo is the explicit
// consent that reopens it.
func TestAFinishedTaskRefusesALaunchWithoutRedo(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	// Runnable for real: the guard under test sits behind the git door.
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run := &runlog.Run{
		ID: "r-old", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.Close()
	s.RecoverRuns(context.Background())

	// The TDD chain — the door the overnight operator actually used.
	if _, err := s.TestStart(context.Background(), id, TestFirstRequest{TaskID: "T-001"}); err == nil {
		t.Fatal("test-first launched on an accepted task with no consent")
	} else if !strings.Contains(err.Error(), "already accepted") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	// The bare build door refuses the same way.
	if _, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"}); err == nil {
		t.Fatal("run launched on an accepted task with no consent")
	} else if !strings.Contains(err.Error(), "already accepted") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	// Redo is the consent: the same launch goes through. The desktop's
	// relaunch panel sends it after showing its caveat; an agent must be
	// told by a human to set it.
	r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo", Redo: true})
	if err != nil {
		t.Fatalf("redo did not reopen the door: %v", err)
	}
	// Started for real — abort it and wait for the goroutine to settle, or
	// its writes race the TempDir cleanup.
	s.RunAbort(context.Background(), r.ID)
	s.waitForRun(context.Background(), r.ID)

	// A task that was never accepted is untouched by the guard.
	if r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-002", Mode: "solo"}); err != nil {
		t.Errorf("an unfinished task was refused: %v", err)
	} else {
		s.RunAbort(context.Background(), r.ID)
		s.waitForRun(context.Background(), r.ID)
	}
}
