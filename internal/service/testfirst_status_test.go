package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// Accepting a test-first run commits a FAILING test — the definition of done,
// not the work. Labelled "build", the board read it as a finished task and
// offered "build again" for work that had never been built once; the person
// asked, reasonably, why nothing suggested it was not done yet.
func TestAnAcceptedTestFirstIsNotAFinishedTask(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Core\n\n### T-047 — Pick the stack\n\nDo it.\n",
	})
	run := &runlog.Run{
		ID: "r-test", ProjectID: id, TaskID: "T-047", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-08-06T03:00:12Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var tv *TaskView
	for i := range tasks {
		if tasks[i].ID == "T-047" {
			tv = &tasks[i]
		}
	}
	if tv == nil {
		t.Fatal("T-047 not listed")
	}
	if tv.Status != "todo" {
		t.Errorf("status = %q, want todo — the task was never built", tv.Status)
	}
	if !tv.TestReady {
		t.Error("the committed failing test is not surfaced")
	}
	offersRun := false
	for _, a := range tv.Next {
		if a == "run" {
			offersRun = true
		}
	}
	if !offersRun {
		t.Errorf("the buildable task offers no run: %v", tv.Next)
	}

	// The build lands and is accepted: the test no longer awaits anything,
	// and the card must stop saying it does.
	build := &runlog.Run{
		ID: "r-build", ProjectID: id, TaskID: "T-047", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-08-06T04:00:00Z",
	}
	bw, err := runlog.NewWriter(dir, build)
	if err != nil {
		t.Fatal(err)
	}
	bw.Close()
	s.RecoverRuns(context.Background())
	tasks, _ = s.TaskList(context.Background(), id)
	for i := range tasks {
		if tasks[i].ID == "T-047" {
			tv = &tasks[i]
		}
	}
	if tv.Status != "accepted" {
		t.Errorf("after the accepted build, status = %q, want accepted", tv.Status)
	}
	if tv.TestReady {
		t.Error("a satisfied test still claims to await its build")
	}
}
