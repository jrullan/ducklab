package service

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A plan regeneration is not a task edit. Accepted work must therefore remain
// accepted when a new task is added, and every consumer of task state must make
// the same decision: board listing, project counts, launch guard, and the queue
// selection used by autopilot.
func TestTaskStatusSurvivesPlanRegenerationAndIsSharedByAllConsumers(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Core\n\n### T-001 — Existing work\n\nDo it.\n\n### T-002 — Recycled work\n\nOriginal body.\n",
	})
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

	for _, taskID := range []string{"T-001", "T-002"} {
		run := &runlog.Run{ID: "r-accepted-" + taskID, ProjectID: id, TaskID: taskID, Stage: "build",
			Status: "done", Verdict: "PASSED", Accepted: true,
			StartedAt: time.Now().UTC().Format(time.RFC3339)}
		w, err := runlog.NewWriter(dir, run)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	s.RecoverRuns(context.Background())

	// Regenerate the plan, preserving T-001's body while adding a fresh task.
	// This is intentionally a whole-plan write: it must not recycle the history
	// of every unchanged task.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte(
		"## M-001 — Core\n\n### T-001 — Existing work\n\nDo it.\n\n### T-002 — New work\n\nDo that.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, task := range tasks {
		statuses[task.ID] = task.Status
	}
	if statuses["T-001"] != "accepted" {
		t.Fatalf("board status after adding T-002 = %q, want accepted (all statuses: %v)", statuses["T-001"], statuses)
	}
	if statuses["T-002"] != "todo" {
		t.Fatalf("new task status = %q, want todo", statuses["T-002"])
	}

	st, err := s.ProjectStatus(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if st.TaskCounts["accepted"] != 1 || st.TaskCounts["todo"] != 1 {
		t.Fatalf("project counts = %v, want one accepted and one todo", st.TaskCounts)
	}

	// The launch guard must consume the same derived state as the board.
	if _, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"}); err == nil {
		t.Fatal("launch guard allowed the already-accepted unchanged task")
	} else if !strings.Contains(err.Error(), "already accepted") {
		t.Fatalf("launch refusal = %v, want accepted-state refusal", err)
	}

	// TaskNext is the mechanical queue selection used by autopilot: it must
	// skip the accepted task and choose the newly-added todo task.
	next, err := s.TaskNext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ID != "T-002" {
		var got string
		if next != nil {
			got = next.ID
		}
		t.Fatalf("queue next task = %q, want T-002", got)
	}
}
