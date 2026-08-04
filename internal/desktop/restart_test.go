package desktop

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeCtl struct {
	active   []string
	actErr   error
	shutErr  error
	shutdown bool
}

func (f *fakeCtl) ActiveRuns() ([]string, error) { return f.active, f.actErr }
func (f *fakeCtl) Shutdown() error               { f.shutdown = true; return f.shutErr }

// A restart cuts running work off mid-call. The person clicking a version
// banner is fixing plumbing, not choosing to abandon a run.
func TestRestartRefusesWhileRunsAreGoing(t *testing.T) {
	ctl := &fakeCtl{active: []string{"r-1", "r-2"}}
	_, err := Restart(ctl, "/usr/bin/true", time.Second)
	if err == nil {
		t.Fatal("restarted over live runs")
	}
	if !strings.Contains(err.Error(), "r-1") {
		t.Errorf("the refusal does not name the runs: %v", err)
	}
	if ctl.shutdown {
		t.Error("the engine was shut down despite the refusal")
	}
}

// An engine that cannot even answer is not a reason to refuse: restarting
// past a hung process is half the point of having the button.
func TestRestartProceedsPastAnUnreachableEngine(t *testing.T) {
	ctl := &fakeCtl{actErr: errors.New("connection refused"), shutErr: errors.New("connection refused")}
	// The spawn will fail on a nonexistent binary, but the point is proved by
	// getting THERE rather than stopping at the dead engine.
	_, err := Restart(ctl, "/nonexistent/ducklab-engine", time.Second)
	if err == nil || strings.Contains(err.Error(), "connection refused") {
		t.Errorf("the dead engine blocked the restart: %v", err)
	}
	if !ctl.shutdown {
		t.Error("shutdown was never even attempted")
	}
}

func TestRestartNeedsABinary(t *testing.T) {
	if _, err := Restart(&fakeCtl{shutErr: errors.New("down")}, "", time.Second); err == nil {
		t.Error("a restart with no binary to start was allowed")
	}
}
