package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// The autopilot's whole discipline is knowing which steps are not its to
// take. A fresh project's guide says "describe what you want to build" — a
// human gate — so the loop must idle ON, saying what it needs, launching
// nothing.
func TestTheAutopilotIdlesAtAHumanGate(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")

	st, err := s.AutopilotSet(context.Background(), projectID, true, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !st.On || st.MaxTasks != 5 {
		t.Fatalf("enable = %+v", st)
	}

	// advance runs async with a settle delay; wait for it to conclude.
	deadline := time.Now().Add(3 * time.Second)
	for {
		st = s.AutopilotStatus(projectID)
		if strings.HasPrefix(st.LastAction, "needs you:") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.On {
		t.Errorf("the loop switched off at a human gate instead of idling: %+v", st)
	}
	if !strings.Contains(st.LastAction, "needs you") {
		t.Errorf("last action = %q, want the human gate named", st.LastAction)
	}
	if st.Started != 0 {
		t.Errorf("started %d runs at a human gate", st.Started)
	}
}

// One failure earns one retry; the second consecutive failure is a pattern,
// and patterns are handed to a person: the loop switches off wearing the
// reason.
func TestTwoConsecutiveFailuresStopTheLoop(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{ID: "r-x", ProjectID: projectID}
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); !st.On {
		t.Fatalf("one failure switched the loop off: %+v", st)
	}
	s.autopilotOnFail(run)
	st := s.AutopilotStatus(projectID)
	if st.On {
		t.Fatalf("two consecutive failures left the loop on: %+v", st)
	}
	if !strings.Contains(st.StoppedReason, "consecutive failures") {
		t.Errorf("stopped reason = %q, want the failure pattern named", st.StoppedReason)
	}

	// An accept in between resets the count: fail, accept, fail is weather
	// twice, not a pattern.
	if _, err := s.AutopilotSet(context.Background(), projectID, true, 5); err != nil {
		t.Fatal(err)
	}
	s.autopilotOnFail(run)
	s.autopilotOnAccept(run)
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); !st.On {
		t.Errorf("an interleaved accept did not reset the failure count: %+v", st)
	}
}

// Off is the default and off means OFF: with the autopilot never enabled,
// the settle hooks are no-ops — guarded behavior is byte-identical with the
// autopilot compiled in.
func TestTheHooksAreInertWhenOff(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")

	run := &runlog.Run{ID: "r-x", ProjectID: projectID}
	s.autopilotOnAccept(run)
	s.autopilotOnFail(run)
	if st := s.AutopilotStatus(projectID); st.On || st.ConsecutiveFails != 0 || st.StoppedReason != "" {
		t.Errorf("hooks moved state while off: %+v", st)
	}
}
