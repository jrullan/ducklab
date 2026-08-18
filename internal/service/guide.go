package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The project-level "what now?".
//
// Ducklab's cycle is a state machine with human gates, which means the
// question every new user asks — what do I do next? — always has a
// computable answer. Runs and bugs already speak their own Next (the loop's
// rules live in the engine, 5.4); this is the same principle one level up.
// It is guidance, not inventory: a handful of steps in the order the loop
// itself would take them, each pointing at an action that already exists.
//
// Deterministic on purpose. A guide computed from the real state cannot
// drift out of date the way a tutorial does, costs no tokens, and is the
// same introspection an autopilot will one day drive (docs/autopilot-plan.md
// — the novice's guide and the autopilot share this engine; one has a human
// at the gate).

// NextStep is one suggested action.
type NextStep struct {
	// ID is a stable slug for the KIND of step ("intake", "answer-run"…),
	// for clients that want icons or filtering. Not unique within a list.
	ID string `json:"id"`
	// Action says WHAT to do, in outcome language first — new users do not
	// share the harness's vocabulary yet, so "describe what you want to
	// build" leads and "intake" follows in parentheses.
	Action string `json:"action"`
	// Reason says WHY this is next, computed from the state that made it so.
	Reason string `json:"reason"`
	// Kind and Ref say WHERE: the object to act on ("run", "task", "bug",
	// "stage", "project") and its id, so a client can link the real button.
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

// projectSnapshot is everything nextSteps reads. Gathered from disk by
// ProjectNext, synthetic in tests — the guidance rules are pinned without a
// filesystem.
type projectSnapshot struct {
	HasRequirements    bool
	HasSpec            bool
	OpenSpecSections   int
	HasPlan            bool
	Tasks              []TaskView
	AcceptedUnreleased int
	UnreleasedBranches int
	Bugs               []bug.Bug
	// Paused runs, newest first.
	Paused []*runlog.Run
}

// nextSteps is the guide's whole brain: the loop's own order, stated.
func nextSteps(st projectSnapshot) []NextStep {
	var out []NextStep
	if st.AcceptedUnreleased == 0 {
		for _, t := range st.Tasks {
			if t.Status == "accepted" && t.Branch != "" && t.Branch != "main" {
				st.AcceptedUnreleased++
			}
		}
	}
	// A branch is required to classify accepted work as unreleased; accepted work
	// without provenance is retained as shipped-compatible legacy state.

	// 1. Work already paid for waits on one click. Nothing outranks it.
	for _, r := range st.Paused {
		out = append(out, pausedStep(r))
	}

	// 2. The document pipeline, until the project has a plan to build from.
	switch {
	case !st.HasRequirements:
		out = append(out, NextStep{
			ID:     "intake",
			Action: "Describe what you want to build (intake) — or adopt the existing code",
			Reason: "this project has no requirements yet; everything downstream grows from them",
			Kind:   "stage", Ref: "intake",
		})
		return out // nothing below is actionable without requirements
	case !st.HasSpec:
		out = append(out, NextStep{
			ID:     "spec",
			Action: "Turn the requirements into a spec",
			Reason: "requirements are approved but no spec exists to pin the contracts",
			Kind:   "stage", Ref: "spec",
		})
		return out
	case !st.HasPlan && st.OpenSpecSections > 0:
		out = append(out, NextStep{
			ID:     "plan",
			Action: "Plan the work — break the spec into tasks",
			Reason: fmt.Sprintf("%d spec section(s) are not yet built and no plan exists", st.OpenSpecSections),
			Kind:   "stage", Ref: "plan",
		})
		return out
	}

	// Accepted work is not shipped merely because its gate passed. Keep the
	// release door visible and promote it ahead of new work.
	if st.AcceptedUnreleased > 0 && nextBuildable(st.Tasks) == nil {
		out = append(out, NextStep{
			ID:     "release",
			Action: fmt.Sprintf("Cut a release — %d accepted task(s) await shipping", st.AcceptedUnreleased),
			Reason: fmt.Sprintf("%d accepted task(s) await a release", st.AcceptedUnreleased),
			Kind:   "release",
		})
	}

	// 3. The bug inbox: classify before starting new work.
	if n := countBugs(st.Bugs, bug.Open); n > 0 {
		out = append(out, NextStep{
			ID:     "triage",
			Action: "Triage the open bugs",
			Reason: fmt.Sprintf("%d bug(s) are open and unclassified", n),
			Kind:   "bug",
		})
	}
	if b := firstBug(st.Bugs, bug.Triaged); b != nil {
		out = append(out, NextStep{
			ID:     "promote",
			Action: fmt.Sprintf("Promote %s to a task, or park it", b.ID),
			Reason: "it is triaged and waiting for a decision",
			Kind:   "bug", Ref: b.ID,
		})
	}
	// A fixed bug is waiting for a person's verification. Reopen is the
	// deliberate alternative when that verification finds the fix insufficient.
	for _, b := range st.Bugs {
		if b.Status == bug.Fixed {
			out = append(out, NextStep{
				ID:     "verify-bug",
				Action: fmt.Sprintf("Verify %s — confirm the fix answers the report; reopen it if the problem remains", b.ID),
				Reason: "the fix is waiting for human verification",
				Kind:   "bug", Ref: b.ID,
			})
		}
	}
	// Accepted work can be deliberately reopened, but only with explicit redo
	// consent so a new run cannot silently repeat finished work.
	hasFixedBug := countBugs(st.Bugs, bug.Fixed) > 0
	for _, t := range st.Tasks {
		// Legacy accepted tasks without provenance are already treated as
		// shipped-compatible. A redo door is meaningful for work still tied to
		// an active fix/release context (or explicitly retained on a branch).
		if t.Status == "accepted" && (hasFixedBug || t.Branch != "") {
			out = append(out, NextStep{
				ID:     "reopen-task",
				Action: fmt.Sprintf("Reopen %s — redo the task with explicit consent", t.ID),
				Reason: "the task is accepted; starting it again would redo finished work, so pass redo and say why",
				Kind:   "task", Ref: t.ID,
			})
		}
	}

	// 4. The next buildable task — ONE, not the backlog: a guide that lists
	// everything startable is a board, and the board already exists.
	if t := nextBuildable(st.Tasks); t != nil {
		if t.TestReady {
			out = append(out, NextStep{
				ID:     "build",
				Action: fmt.Sprintf("Build %s — its failing test already defines done", t.ID),
				Reason: "a committed test is waiting for the code that makes it pass",
				Kind:   "task", Ref: t.ID,
			})
		} else if t.BuildOnly {
			// The triager judged this fix unverifiable by automated test:
			// the front door is the build, the honest reviewer is eyes.
			out = append(out, NextStep{
				ID:     "build",
				Action: fmt.Sprintf("Build %s — triage recommends no gate test", t.ID),
				Reason: "the fix verifies by eyes, not by test; test-first stays one click away",
				Kind:   "task", Ref: t.ID,
			})
		} else {
			out = append(out, NextStep{
				ID:     "test-first",
				Action: fmt.Sprintf("Start %s (test first, then build)", t.ID),
				Reason: "it is the next task whose dependencies are all accepted",
				Kind:   "task", Ref: t.ID,
			})
		}
	}

	// 4b. The amendment's toll, surfaced where it gets settled. After the
	// buildable work on purpose: build the change first, document it after.
	// Clicking through lands on the Cycle spec tab, where settling is one
	// button — the person never writes the maintenance prompt.
	if n := specDebtCount(st.Tasks); n > 0 {
		out = append(out, NextStep{
			ID:     "spec-debt",
			Action: fmt.Sprintf("Teach the spec what was built — %d task(s) wear spec-debt", n),
			Reason: "the plan grew without a redesign; the spec has not caught up",
			Kind:   "stage", Ref: "spec",
		})
	}

	// 5. Quiet project: everything accepted, inbox empty. Three doors, each
	// its own step with its own destination — one long sentence linking only
	// to intake made the amendment and the release read as decoration on the
	// brief. The autopilot still reads only the first: "brief" leading is
	// what tells it the project is done.
	if len(out) == 0 {
		out = append(out,
			NextStep{
				ID:     "brief",
				Action: "New feature — write a brief",
				Reason: "extends the requirements, then spec and plan follow",
				Kind:   "stage", Ref: "intake",
			},
			NextStep{
				ID:     "amend",
				Action: "Quick change — amend the plan",
				Reason: "one to three tasks, no redesign; uncovered work wears spec-debt",
				Kind:   "stage", Ref: "plan",
			},
			NextStep{
				ID:     "release",
				Action: fmt.Sprintf("Cut a release — %d accepted task(s) await shipping", st.AcceptedUnreleased),
				Reason: "everything accepted since the last one ships",
				Kind:   "release",
			},
		)
	}
	return out
}

func pausedStep(r *runlog.Run) NextStep {
	subject := strings.TrimSpace(r.Stage + " " + r.TaskID)
	switch r.PendingKind {
	case "question":
		return NextStep{
			ID:     "answer-run",
			Action: fmt.Sprintf("Answer the question the %s run asked", subject),
			Reason: "a model is paused waiting for your decision; the advisor has drafted an answer",
			Kind:   "run", Ref: r.ID,
		}
	case "chat":
		return NextStep{
			ID:     "chat",
			Action: "Reply to the consultant, or end the chat",
			Reason: "a conversation is waiting on you",
			Kind:   "run", Ref: r.ID,
		}
	case "gate":
		return NextStep{
			ID:     "decide-run",
			Action: fmt.Sprintf("Decide the %s run — accept or reject its result", subject),
			Reason: "the work is done and waiting at its gate",
			Kind:   "run", Ref: r.ID,
		}
	default: // budget, provider, error
		return NextStep{
			ID:     "resume-run",
			Action: fmt.Sprintf("Resume or abort the paused %s run", subject),
			Reason: fmt.Sprintf("it stopped on %s with its work preserved", orDefault(r.PendingKind, "an interruption")),
			Kind:   "run", Ref: r.ID,
		}
	}
}

// nextBuildable picks the one task to suggest: test-ready first (its
// definition of done already exists), then the first todo whose dependencies
// are all accepted.
func nextBuildable(tasks []TaskView) *TaskView {
	accepted := map[string]bool{}
	for _, t := range tasks {
		if t.Status == "accepted" {
			accepted[t.ID] = true
		}
	}
	startable := func(t TaskView) bool {
		for _, dep := range t.DependsOn {
			if !accepted[dep] {
				return false
			}
		}
		return true
	}
	for i := range tasks {
		if tasks[i].TestReady && tasks[i].Status != "accepted" && startable(tasks[i]) {
			return &tasks[i]
		}
	}
	for i := range tasks {
		if tasks[i].Status == "todo" && startable(tasks[i]) {
			return &tasks[i]
		}
	}
	return nil
}

func countBugs(bugs []bug.Bug, status bug.Status) int {
	n := 0
	for _, b := range bugs {
		if b.Status == status {
			n++
		}
	}
	return n
}

func firstBug(bugs []bug.Bug, status bug.Status) *bug.Bug {
	for i := range bugs {
		if bugs[i].Status == status {
			return &bugs[i]
		}
	}
	return nil
}

// ProjectNext gathers the project's real state and asks nextSteps.
func (s *Service) ProjectNext(ctx context.Context, projectID string) ([]NextStep, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}

	// The config tripwire, first and alone. A project.toml that does not
	// parse fails EVERY run at load — a duplicated key did exactly that,
	// and every relaunch died with the same one-line error the person had
	// to excavate per-run. Broken config outranks every other suggestion,
	// and nothing else the guide would say is trustworthy while it stands.
	tomlPath := filepath.Join(entry.Path, ".ducklab", "project.toml")
	if _, statErr := os.Stat(tomlPath); statErr == nil {
		if _, cfgErr := config.LoadProject(tomlPath); cfgErr != nil {
			return []NextStep{{
				ID:     "config",
				Action: "Fix .ducklab/project.toml — it does not parse, and every run will fail at load",
				Reason: cfgErr.Error(),
				Kind:   "project",
			}}, nil
		}
	}

	st := projectSnapshot{}

	if doc, lerr := artifact.Load(entry.Path, artifact.KindRequirements); lerr == nil && doc != nil && len(doc.Sections) > 0 {
		st.HasRequirements = true
	}
	if doc, lerr := artifact.Load(entry.Path, artifact.KindSpec); lerr == nil && doc != nil && len(doc.Sections) > 0 {
		st.HasSpec = true
		// Open = not excluded and not already as-built, the same reading
		// StageStart uses to refuse an empty plan.
		for _, sp := range doc.Sections {
			if strings.EqualFold(sp.Field("priority"), "wont") {
				continue
			}
			if v := strings.ToLower(strings.TrimSpace(sp.Field("as-built"))); v == "yes" || v == "true" {
				continue
			}
			st.OpenSpecSections++
		}
	}
	if doc, lerr := artifact.Load(entry.Path, artifact.KindPlan); lerr == nil && doc != nil && len(doc.Sections) > 0 {
		st.HasPlan = true
	}

	// Tolerant gathers: a missing plan makes TaskList error, and a project
	// with no tasks yet still deserves guidance about everything else.
	st.Tasks, _ = s.TaskList(ctx, projectID)
	if accepted, branches, countErr := s.acceptedUnreleased(ctx, projectID, entry.Path, st.Tasks); countErr == nil {
		st.AcceptedUnreleased = accepted
		st.UnreleasedBranches = branches
	}

	st.Bugs, _ = s.BugList(ctx, projectID, false)
	if runs, rerr := s.RunList(ctx, RunFilter{ProjectID: projectID}); rerr == nil {
		for _, r := range runs {
			if r.Status == "paused" {
				st.Paused = append(st.Paused, r)
			}
		}
	}
	return nextSteps(st), nil
}

// finalDissent reads a run's record for the last verdict any turn gave and
// reports it when it is not an approval — the engine-side twin of the
// desktop's reviewerDissent, because a check that only protects the person
// watching protects nobody under auto.
func finalDissent(runDir string) (verdict string, findings int, dissent bool) {
	events, err := runlog.ReadEvents(runDir)
	if err != nil {
		return "", 0, false
	}
	for _, e := range events {
		if e.Type != "message" {
			continue
		}
		v, ok := e.Data["verdict"].(string)
		if !ok || v == "" {
			continue
		}
		verdict = v
		findings = 0
		if fs, ok := e.Data["findings"].([]interface{}); ok {
			findings = len(fs)
		}
	}
	if verdict == "" {
		return "", 0, false
	}
	norm := strings.ReplaceAll(strings.ToLower(verdict), "_", "-")
	if norm == "approve" || norm == "approved" {
		return "", 0, false
	}
	return verdict, findings, true
}

// specDebtCount: how many tasks the spec has not caught up with.
// specDebtCount counts only DELIVERED debt: the guide's settle step says
// "teach the spec what was built", and an amendment task still todo is not
// built — it appears here once its build is accepted.
func specDebtCount(tasks []TaskView) int {
	n := 0
	for _, t := range tasks {
		if t.SpecDebt && t.Status == "accepted" {
			n++
		}
	}
	return n
}
