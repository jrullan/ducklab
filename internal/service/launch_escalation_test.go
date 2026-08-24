package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

func TestLaunchEscalationWarnsAfterTwoTaskStageFailures(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", TaskID: "T-132", Stage: "build", Roster: map[string]string{"implementer": "luna"}}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w}
	s.runs = map[string]*runState{
		"r-first":  {run: &runlog.Run{ID: "r-first", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "FAILED"}},
		"r-second": {run: &runlog.Run{ID: "r-second", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "ABORTED"}},
		current.ID: rs,
	}

	s.emitLaunchEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" && event.Data["point"] == "launch" {
			return
		}
	}
	t.Fatal("two prior failed/aborted runs did not emit a launch escalation")
}

func TestLaunchEscalationDoesNotWarnAfterOneFailure(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	current := &runlog.Run{ID: "r-current", ProjectID: "p", TaskID: "T-132", Stage: "build", Roster: map[string]string{"implementer": "luna"}}
	w, err := runlog.NewWriter(dir, current)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rs := &runState{run: current, writer: w}
	s.runs = map[string]*runState{
		"r-first":  {run: &runlog.Run{ID: "r-first", ProjectID: "p", TaskID: "T-132", Stage: "build", Verdict: "FAILED"}},
		current.ID: rs,
	}

	s.emitLaunchEscalation(rs)
	events, err := runlog.ReadEvents(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "escalation_suggestion" {
			t.Fatal("one prior failure emitted a launch escalation")
		}
	}
}
