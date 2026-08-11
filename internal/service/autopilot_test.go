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

// The loop's leash is configuration, not code: the activation cap and the
// failure tolerance come from defaults a person can edit, bounded so a typo
// cannot configure an unsupervised thousand-task loop.
func TestAutopilotDefaultsRoundTripAndBounds(t *testing.T) {
	s := writableService(t, "pato-uno")

	d := s.AutopilotDefaults()
	if d.MaxTasks != autopilotDefaultMaxTasks || d.MaxFails != autopilotDefaultMaxFails {
		t.Fatalf("built-ins = %+v", d)
	}

	if err := s.AutopilotDefaultsSet(AutopilotDefaultsView{MaxTasks: 3, MaxFails: 1, Autonomy: "auto"}); err != nil {
		t.Fatal(err)
	}
	d = s.AutopilotDefaults()
	if d.MaxTasks != 3 || d.MaxFails != 1 || d.Autonomy != "auto" {
		t.Errorf("after set = %+v", d)
	}
	if s.autopilotConfigMaxFails() != 1 {
		t.Error("the driver does not read the configured failure tolerance")
	}

	for _, bad := range []AutopilotDefaultsView{
		{MaxTasks: 0, MaxFails: 2, Autonomy: "guarded"},
		{MaxTasks: 1000, MaxFails: 2, Autonomy: "guarded"},
		{MaxTasks: 5, MaxFails: 0, Autonomy: "guarded"},
		{MaxTasks: 5, MaxFails: 2, Autonomy: "cowboy"},
	} {
		if err := s.AutopilotDefaultsSet(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// The default modes are a promise to every caller, not just the launcher
// that pre-fills them client-side: a run started with no mode — the CLI, the
// autopilot — gets the configured build mode, not a hardcoded solo. The
// autopilot's first production build ran solo past a config that said pair.
func TestAnEmptyModeTakesTheConfiguredDefault(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos")
	v := s.ModeDefaults()
	v.BuildMode = "pair"
	v.TestMode = "pair"
	if err := s.ModeDefaultsSet(v); err != nil {
		t.Fatal(err)
	}
	if got := s.testModeDefault(""); got != "pair" {
		t.Errorf("empty test mode resolved to %q, want the configured pair", got)
	}
	if got := s.testModeDefault("solo"); got != "solo" {
		t.Errorf("an explicit mode was overridden: %q", got)
	}
	s.cfgMu.RLock()
	buildDefault := s.cfg.Defaults.BuildMode
	s.cfgMu.RUnlock()
	if buildDefault != "pair" {
		t.Errorf("build default = %q", buildDefault)
	}
}
