package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/bug"
)

// The guide, localized.
//
// ProjectNext computes the project's next lawful step for Now and for the
// autopilot. Each record — a bug, a task — is its own state machine, and the
// person looking at one deserves the same answer at that point of context:
// where this thing is on its ladder, and which door is next. Before this,
// the bug card said "run it from the tasks board" (a pointer without a door)
// and the task card said "failing test committed — build it to make it pass"
// as prose, with the build launcher three peers away and unlabeled. The
// person who owns the product could not find the door.

// JourneyRung is one step on an entity's ladder.
type JourneyRung struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State: done | current | next | later | exit. "exit" marks a terminal
	// side-state (duplicate, wontfix) the ladder left through.
	State string `json:"state"`
	// At and Actor come from the audit trail for done rungs.
	At    string `json:"at,omitempty"`
	Actor string `json:"actor,omitempty"`
}

// Journey is an entity's position and its doors.
type Journey struct {
	Ref   string        `json:"ref"`
	Kind  string        `json:"kind"`
	Rungs []JourneyRung `json:"rungs"`
	// Steps are the lawful next actions, primary first; Door is Steps[0]
	// repeated for clients that render one button.
	Steps []NextStep `json:"steps"`
	Door  *NextStep  `json:"door,omitempty"`
}

// NextFor answers "where is this, and what is next?" for one bug or task.
func (s *Service) NextFor(ctx context.Context, projectID, ref string) (*Journey, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "B-"):
		return s.bugJourney(ctx, projectID, ref)
	case strings.HasPrefix(ref, "T-"):
		return s.taskJourney(ctx, projectID, ref)
	}
	return nil, fmt.Errorf("no journey for %q: refs are bug ids (B-001) or task ids (T-001)", ref)
}

var bugLadder = []struct{ id, label string }{
	{"open", "reported"},
	{"triaged", "triaged"},
	{"in_progress", "task"},
	{"fixed", "fixed"},
	{"verified", "verified"},
}

func (s *Service) bugJourney(ctx context.Context, projectID, id string) (*Journey, error) {
	bugs, err := s.BugList(ctx, projectID, false)
	if err != nil {
		return nil, err
	}
	var b *bug.Bug
	for i := range bugs {
		if bugs[i].ID == id {
			b = &bugs[i]
			break
		}
	}
	if b == nil {
		return nil, fmt.Errorf("no bug %s", id)
	}
	j := &Journey{Ref: id, Kind: "bug"}

	// Rungs: everything the trail says was reached is done; the status is
	// current; the rest lies ahead. A side exit collapses the ladder.
	reached := map[string]bug.AuditEntry{}
	for _, h := range b.History {
		reached[h.To] = h
	}
	status := string(b.Status)
	if status == string(bug.Duplicate) || status == string(bug.WontFix) {
		for _, r := range bugLadder {
			state := "later"
			if h, ok := reached[r.id]; ok || r.id == "open" {
				state = "done"
				if ok {
					j.Rungs = append(j.Rungs, JourneyRung{ID: r.id, Label: r.label, State: state, At: h.TS, Actor: h.Actor})
					continue
				}
			}
			j.Rungs = append(j.Rungs, JourneyRung{ID: r.id, Label: r.label, State: state})
		}
		j.Rungs = append(j.Rungs, JourneyRung{ID: status, Label: strings.ReplaceAll(status, "_", " "), State: "exit"})
		return j, nil
	}
	passed := true
	for _, r := range bugLadder {
		rung := JourneyRung{ID: r.id, Label: r.label}
		switch {
		case r.id == status:
			rung.State = "current"
			passed = false
		case passed:
			rung.State = "done"
			if h, ok := reached[r.id]; ok {
				rung.At, rung.Actor = h.TS, h.Actor
			}
		default:
			rung.State = "later"
		}
		j.Rungs = append(j.Rungs, rung)
	}
	// Mark the immediate next rung so a rail can light two things: where you
	// are and where the door leads.
	for i := range j.Rungs {
		if j.Rungs[i].State == "current" && i+1 < len(j.Rungs) && j.Rungs[i+1].State == "later" {
			j.Rungs[i+1].State = "next"
			break
		}
	}

	switch b.Status {
	case bug.Open:
		j.Steps = []NextStep{{ID: "triage", Kind: "bug", Ref: id,
			Action: "Triage this bug",
			Reason: "classify it — severity, duplicates, promotability — before it becomes work"}}
	case bug.Triaged:
		j.Steps = []NextStep{{ID: "promote", Kind: "bug", Ref: id,
			Action: "Make it a task",
			Reason: "it is classified and waiting for a decision; promote births its task (or park it)"}}
	case bug.InProgress:
		if b.TaskID != "" {
			// The door moved to the task: show the task's own door here, so
			// the person does not travel to the board to find out what is
			// next for the bug they are looking at.
			tj, err := s.taskJourney(ctx, projectID, b.TaskID)
			if err == nil && len(tj.Steps) > 0 {
				for _, st := range tj.Steps {
					st.Reason = b.TaskID + ": " + st.Reason
					j.Steps = append(j.Steps, st)
				}
			}
		}
	case bug.Fixed:
		j.Steps = []NextStep{{ID: "verify-bug", Kind: "bug", Ref: id,
			Action: "Verify the fix — try what the report describes",
			Reason: "the fixing task landed; only a person can say the report is answered"}}
	}
	if len(j.Steps) > 0 {
		d := j.Steps[0]
		j.Door = &d
	}
	return j, nil
}

var taskLadder = []struct{ id, label string }{
	{"todo", "planned"},
	{"test", "test"},
	{"build", "build"},
	{"accepted", "accepted"},
}

func (s *Service) taskJourney(ctx context.Context, projectID, id string) (*Journey, error) {
	tasks, err := s.TaskList(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var t *TaskView
	for i := range tasks {
		if tasks[i].ID == id {
			t = &tasks[i]
			break
		}
	}
	if t == nil {
		return nil, fmt.Errorf("no task %s", id)
	}
	j := &Journey{Ref: id, Kind: "task"}

	// Which rung is current follows the same facts the board derives:
	// accepted is the end; a committed test or a live build sits at build;
	// a tests-gated task with nothing committed sits at test; otherwise at
	// planned. Test-first is optional, so "test" is skipped (never current)
	// when the task is being built without one.
	current := "todo"
	testDone := t.TestReady
	switch {
	case t.Status == "accepted":
		current = "accepted"
	case t.TestReady, t.Status == "in_progress", t.Status == "review":
		current = "build"
	case hasAction(t.Next, "test_first") && t.Next[0] == "test_first":
		current = "test"
	}
	passed := true
	for _, r := range taskLadder {
		rung := JourneyRung{ID: r.id, Label: r.label}
		switch {
		case r.id == current:
			rung.State = "current"
			passed = false
		case passed:
			rung.State = "done"
			if r.id == "test" && !testDone && current == "build" && t.Status != "accepted" {
				// Built without a committed test: the rung was not taken.
				rung.State = "skipped"
			}
		default:
			rung.State = "later"
		}
		j.Rungs = append(j.Rungs, rung)
	}
	for i := range j.Rungs {
		if j.Rungs[i].State == "current" && i+1 < len(j.Rungs) && j.Rungs[i+1].State == "later" {
			j.Rungs[i+1].State = "next"
			break
		}
	}

	// Doors come from the same list the board renders (taskNextActions),
	// worded for the moment. Primary first, as the engine ordered them.
	for _, a := range t.Next {
		switch a {
		case "test_first":
			j.Steps = append(j.Steps, NextStep{ID: "test-first", Kind: "task", Ref: id,
				Action: "Write the failing test first, then build",
				Reason: "tests-gated task with no committed test — the definition of done comes first"})
		case "run":
			if t.TestReady {
				j.Steps = append(j.Steps, NextStep{ID: "build", Kind: "task", Ref: id,
					Action: "Build the committed test — make it pass",
					Reason: "a failing test is committed and waiting for its build"})
			} else if t.Status == "accepted" {
				j.Steps = append(j.Steps, NextStep{ID: "build", Kind: "task", Ref: id,
					Action: "Build it again",
					Reason: "already accepted — a rerun needs a note saying what changed"})
			} else {
				j.Steps = append(j.Steps, NextStep{ID: "build", Kind: "task", Ref: id,
					Action: "Build it",
					Reason: "planned and unblocked"})
			}
		case "retire_test":
			j.Steps = append(j.Steps, NextStep{ID: "retire-test", Kind: "task", Ref: id,
				Action: "Withdraw the committed test",
				Reason: "the promise has two exits: keep it (build) or withdraw it"})
		}
	}
	if t.Status == "accepted" {
		// Accepted work's natural next act is shipping, not rebuilding: the
		// release door leads and "build it again" (a redo, which needs a
		// note) stays available behind it.
		j.Steps = append([]NextStep{{ID: "release", Kind: "release",
			Action: "Cut a release to ship it",
			Reason: "accepted work waits for a release to reach people"}}, j.Steps...)
	}
	if len(j.Steps) > 0 {
		d := j.Steps[0]
		j.Door = &d
	}
	return j, nil
}

func hasAction(list []string, a string) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}
