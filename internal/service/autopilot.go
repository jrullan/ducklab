package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// The autopilot: a scripted human driving the guide.
//
// It owns no planning brain of its own. Each time a run settles it asks
// ProjectNext — the same introspection the novice's rail renders — and acts
// only when the first step is one the buttons could take mechanically:
// test-first or build. Every other step (answer a question, approve a spec,
// triage bugs, fix a broken config) is a human gate, and the autopilot's
// whole discipline is knowing it is not one. It launches through the same
// service methods the buttons call, through the same queue, with
// origin:"autopilot" on every run it starts (docs/autopilot-plan.md).
//
// Stop rails, because an unattended loop's failure mode is conviction:
//   - MaxTasks caps how many runs one activation may start.
//   - Two consecutive failures switch it off. A retry can absorb weather;
//     a second failure is a pattern, and patterns need a person.
//   - It never lifts a money cap, never crosses UNVERIFIED, and inherits
//     the dissent guard: those pauses simply block the loop until a human
//     unblocks them, and the loop resumes on their accept.
//
// State is in-memory: an engine restart lands the autopilot OFF. That is a
// feature — an unattended loop should not survive the process nobody is
// watching restart.
type autopilotState struct {
	On               bool   `json:"on"`
	MaxTasks         int    `json:"max_tasks"`
	Started          int    `json:"started"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	// LastAction is what the autopilot last did or why it is idle — the
	// rail renders this verbatim.
	LastAction string `json:"last_action,omitempty"`
	// StoppedReason survives the off switch so the rail can say WHY it
	// stopped rather than showing a silently cleared toggle.
	StoppedReason string `json:"stopped_reason,omitempty"`
	// The pending retry's context: which task failed and why. The note is
	// what turns a retry into a second attempt instead of a repetition —
	// a blind relaunch of a deterministic failure reproduces it exactly.
	retryTask string
	retryNote string
}

const (
	autopilotDefaultMaxTasks = 10
	autopilotDefaultMaxFails = 2
)

// autopilotConfigMaxTasks is the configured activation cap.
func (s *Service) autopilotConfigMaxTasks() int {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if n := s.cfg.Defaults.AutopilotMaxTasks; n > 0 {
		return n
	}
	return autopilotDefaultMaxTasks
}

// autopilotConfigMaxFails is how many consecutive failures stop the loop.
func (s *Service) autopilotConfigMaxFails() int {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if n := s.cfg.Defaults.AutopilotMaxFails; n > 0 {
		return n
	}
	return autopilotDefaultMaxFails
}

// AutopilotStatus reports the state for one project, zero-valued when never
// enabled.
func (s *Service) AutopilotStatus(projectID string) autopilotState {
	s.apMu.Lock()
	defer s.apMu.Unlock()
	if st, ok := s.autopilots[projectID]; ok {
		return *st
	}
	return autopilotState{}
}

// AutopilotSet turns the loop on or off. Enabling resets the counters and
// immediately advances; disabling leaves in-flight runs alone — they carry
// their own autonomy and will settle normally, they just will not be followed.
func (s *Service) AutopilotSet(ctx context.Context, projectID string, on bool, maxTasks int) (autopilotState, error) {
	if _, err := s.registry.Get(projectID); err != nil {
		return autopilotState{}, err
	}
	s.apMu.Lock()
	if s.autopilots == nil {
		s.autopilots = map[string]*autopilotState{}
	}
	st, ok := s.autopilots[projectID]
	if !ok {
		st = &autopilotState{}
		s.autopilots[projectID] = st
	}
	st.On = on
	st.StoppedReason = ""
	if on {
		if maxTasks <= 0 {
			maxTasks = s.autopilotConfigMaxTasks()
		}
		st.MaxTasks = maxTasks
		st.Started = 0
		st.ConsecutiveFails = 0
		st.LastAction = "starting"
	} else {
		st.LastAction = "switched off"
	}
	out := *st
	s.apMu.Unlock()
	if on {
		go s.autopilotAdvance(projectID)
	}
	return out, nil
}

// autopilotOn reports whether the loop is live for a project.
func (s *Service) autopilotOn(projectID string) bool {
	s.apMu.Lock()
	defer s.apMu.Unlock()
	st, ok := s.autopilots[projectID]
	return ok && st.On
}

// autopilotStop switches the loop off with a reason the rail will show.
func (s *Service) autopilotStop(projectID, reason string) {
	s.apMu.Lock()
	defer s.apMu.Unlock()
	if st, ok := s.autopilots[projectID]; ok {
		st.On = false
		st.StoppedReason = reason
		st.LastAction = ""
	}
}

// autopilotOnAccept: a settled decision refuels the loop — including a
// HUMAN accept on a paused run, which is exactly how a blocked loop resumes.
func (s *Service) autopilotOnAccept(run *runlog.Run) {
	if run == nil || !s.autopilotOn(run.ProjectID) {
		return
	}
	s.apMu.Lock()
	if st, ok := s.autopilots[run.ProjectID]; ok {
		st.ConsecutiveFails = 0
	}
	s.apMu.Unlock()
	go s.autopilotAdvance(run.ProjectID)
}

// autopilotOnFail: one failure earns one retry — the guide will re-suggest
// the same task, and weather deserves a second attempt. A second consecutive
// failure is a pattern, and the loop hands the pattern to a person.
func (s *Service) autopilotOnFail(run *runlog.Run) {
	if run == nil || !s.autopilotOn(run.ProjectID) {
		return
	}
	s.apMu.Lock()
	st := s.autopilots[run.ProjectID]
	st.ConsecutiveFails++
	fails := st.ConsecutiveFails
	if run.TaskID != "" {
		reason := strings.TrimSpace(run.Failure)
		if reason == "" {
			reason = "see the run record"
		}
		st.retryTask = run.TaskID
		st.retryNote = fmt.Sprintf(
			"Previous attempt (%s) failed: %s. Address that cause — do not repeat the same approach unchanged.",
			run.ID, reason)
	}
	s.apMu.Unlock()
	if fails >= s.autopilotConfigMaxFails() {
		s.autopilotStop(run.ProjectID,
			fmt.Sprintf("stopped after %d consecutive failures (last: %s) — the loop needs you", fails, run.ID))
		return
	}
	go s.autopilotAdvance(run.ProjectID)
}

// autopilotAdvance takes the guide's first step, if it is mechanical.
func (s *Service) autopilotAdvance(projectID string) {
	// Let the settling run finish its writes before reading project state.
	time.Sleep(200 * time.Millisecond)
	if !s.autopilotOn(projectID) {
		return
	}
	// One run at a time: if the project is already busy, the next settle
	// hook will call again. The queue would serialize anyway; not launching
	// keeps the record free of a phantom queue.
	if s.projectBusy(projectID) {
		s.autopilotNote(projectID, "waiting for the active run")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	steps, err := s.ProjectNext(ctx, projectID)
	if err != nil || len(steps) == 0 {
		s.autopilotNote(projectID, "guide unavailable")
		return
	}
	first := steps[0]

	s.apMu.Lock()
	st := s.autopilots[projectID]
	if st == nil || !st.On {
		s.apMu.Unlock()
		return
	}
	if first.ID == "build" || first.ID == "test-first" || first.ID == "triage" {
		if st.Started >= st.MaxTasks {
			s.apMu.Unlock()
			s.autopilotStop(projectID,
				fmt.Sprintf("task cap reached (%d) — re-enable to continue", st.MaxTasks))
			return
		}
		st.Started++
	}
	s.apMu.Unlock()

	// The retry note, when the guide re-suggests the task that just failed.
	// Read-and-clear either way: a note for one task must not haunt another.
	s.apMu.Lock()
	note := ""
	if st2 := s.autopilots[projectID]; st2 != nil {
		if st2.retryTask == first.Ref {
			note = st2.retryNote
		}
		st2.retryTask, st2.retryNote = "", ""
	}
	s.apMu.Unlock()

	switch first.ID {
	case "brief":
		s.autopilotStop(projectID, "every task is done — nothing left to drive")
	case "build":
		_, err := s.RunStart(ctx, projectID, RunRequest{
			TaskID: first.Ref, Autonomy: "yolo", Origin: "autopilot", Note: note,
		})
		s.autopilotResult(projectID, "build "+first.Ref, err)
	case "test-first":
		_, err := s.TestStart(ctx, projectID, TestFirstRequest{
			TaskID: first.Ref, ThenBuild: true, Origin: "autopilot", Note: note,
			Build: RunRequest{Autonomy: "yolo", Origin: "autopilot"},
		})
		s.autopilotResult(projectID, "test-first "+first.Ref, err)
	case "triage":
		// Only when the project's own autonomy lets the classifications
		// apply themselves — under guarded the run would pause at its gate
		// and the loop would have manufactured an inbox item, not progress.
		// Duplicate proposals still pause regardless (bugs.go): closing a
		// report stays a person's call.
		if entry, eerr := s.registry.Get(projectID); eerr == nil {
			if a := s.triageAutonomy(entry.Path); a == "auto" || a == "yolo" {
				_, err := s.BugTriage(ctx, projectID, "")
				s.autopilotResult(projectID, "triage the open bugs", err)
				return
			}
		}
		s.autopilotNote(projectID, "needs you: "+first.Action)
	default:
		// A human gate: paused run, document approval, triage, broken
		// config. The loop idles ON — the guide is already telling the
		// person what it needs, and their action refuels the loop.
		s.autopilotNote(projectID, "needs you: "+first.Action)
	}
}

func (s *Service) autopilotNote(projectID, note string) {
	s.apMu.Lock()
	defer s.apMu.Unlock()
	if st, ok := s.autopilots[projectID]; ok {
		st.LastAction = note
	}
}

func (s *Service) autopilotResult(projectID, action string, err error) {
	if err != nil {
		s.autopilotStop(projectID, fmt.Sprintf("could not start %s: %v", action, err))
		return
	}
	s.autopilotNote(projectID, "started "+action)
}

// projectBusy reports whether any run for the project is still in motion.
func (s *Service) projectBusy(projectID string) bool {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	for _, rs := range s.runs {
		if rs.run.ProjectID == projectID &&
			(rs.run.Status == "running" || rs.run.Status == "queued") {
			return true
		}
	}
	return false
}
