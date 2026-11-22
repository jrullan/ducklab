package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// B-556: the flow report reads the project's runs, their event logs and the
// bug history through the service — one paused run and one reported bug.
func TestFlowReportReadsRunsEventsAndBugs(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "the export loses its last row", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	w, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-20261101-120000-flow", ProjectID: id, Stage: "build", TaskID: "T-001",
		Status: "done", Verdict: "PASSED", StartedAt: "2026-11-01T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("turn_start", nil)
	w.AppendEvent("turn_end", nil)
	w.AppendEvent("human_needed", map[string]interface{}{"kind": "gate"})
	w.AppendEvent("accept_started", nil)
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	f, err := s.FlowReport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Runs != 1 || f.Bugs.Reported != 1 || len(f.Waits) != 1 || f.Waits[0].Kind != "gate" || f.Tasks.Built != 1 {
		t.Fatalf("flow = %+v", f)
	}
	if _, err := s.FlowReport(context.Background(), "no-such-project"); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("an unknown project: err = %v, want ErrUnknownProject (the route's 404)", err)
	}
}
