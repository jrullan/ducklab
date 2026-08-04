package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/daemon"
)

// EngineControl is the slice of the engine client a restart needs. An
// interface so the decision logic is testable without a process to kill.
type EngineControl interface {
	ActiveRuns() ([]string, error)
	Shutdown() error
}

// Restart stops the running engine and starts the installed binary in its
// place, returning the new connection details.
//
// Refused while runs are running or queued: a restart cuts them off mid-call,
// and the person clicking a version-skew banner is fixing plumbing, not
// choosing to abandon work. Paused runs survive by design (I9) and do not
// block. There is deliberately no force from the desktop — aborting work is
// the run's own Abort button, where the work is visible.
func Restart(ctl EngineControl, enginePath string, wait time.Duration) (*daemon.EngineInfo, error) {
	active, err := ctl.ActiveRuns()
	if err == nil && len(active) > 0 {
		return nil, fmt.Errorf("%d run(s) still going (%s) — wait for them or abort them first",
			len(active), strings.Join(active, ", "))
	}
	// An unreachable engine is not a reason to refuse: restarting past a hung
	// process is half the point of having the button.
	if err := ctl.Shutdown(); err == nil {
		if err := daemon.WaitGone(wait); err != nil {
			return nil, err
		}
	}
	if enginePath == "" {
		return nil, fmt.Errorf("no engine binary found to start")
	}
	if err := daemon.StartEngine(enginePath); err != nil {
		return nil, err
	}
	return daemon.WaitReady(wait)
}
