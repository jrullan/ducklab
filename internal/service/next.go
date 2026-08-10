package service

import (
	"github.com/jrullan/ducklab/internal/runlog"
)

// The next-actions contract: the engine states what is legal, clients render
// buttons from lists.
//
// This generalizes what bug.NextFrom already proved. Before it, every client
// surface encoded the loop's rules by hand — "paused at a gate means Accept",
// "todo means Build it", "failed means relaunch" — and every one of those
// rules was wrong at least once in the first real project: Accept offered on a
// triage it could not apply, a remove button that the engine refused after the
// click, a bug state with no button at all. An action the engine did not offer
// cannot render; one it offers cannot be missing. (docs/ux-evaluation.md §5.4)

// runNext lists what a person may legally do to a run, in the order a client
// should offer them.
//
// Derived on read, never persisted: the stored copy in state.json is ignored
// and overwritten, so it can never go stale against these rules.
func runNext(r *runlog.Run) []string {
	if r == nil {
		return nil
	}
	switch r.Status {
	case "running", "queued":
		return []string{"abort"}
	case "paused":
		switch r.PendingKind {
		case "question":
			return []string{"answer", "abort"}
		case "engine_restart", "engine_shutdown":
			// The states RunResume accepts, and nothing else: a human gate is
			// answered, not continued.
			return []string{"resume", "abort"}
		case "chat":
			// The conversation waits for the person's next message.
			return []string{"reply", "abort"}
		case "budget", "provider", "error":
			// Stopped by its own ceiling, a provider that went away, or any
			// error at all — work intact in the tree either way, because no
			// error may discard work automatically. Fix what needs fixing and
			// resume; or abort, which restores. Build and test know how to
			// re-enter their strategy; anything else relaunches instead.
			if r.Stage == "build" || r.Stage == "test" {
				return []string{"resume", "abort"}
			}
			return []string{"abort"}
		case "gate":
			var out []string
			// A FAILED verdict has nothing to accept; offering the button and
			// disabling it is the client's courtesy, offering the action is not
			// the engine's.
			if r.Verdict != "FAILED" {
				out = append(out, "accept")
			}
			// Only a document can be sent back with a note; code runs are
			// accepted or rejected, and "almost" for code is a new run.
			switch r.Stage {
			case "intake", "spec", "plan":
				out = append(out, "request_changes")
			}
			return append(out, "reject")
		}
		return []string{"abort"}
	default:
		// done and failed are endings for TASK runs — relaunching travels on
		// the task's own list. An accepted STAGE run is different: it is the
		// middle of one process. New requirements feed the spec, the spec
		// feeds the plan, and the person was made to leave the run view and
		// find the Documents screen to take a step the acceptance itself
		// implies. The engine states the next step; the view renders it in
		// place.
		if r.Accepted {
			switch r.Stage {
			case "intake":
				return []string{"run_spec"}
			case "spec":
				return []string{"run_plan"}
			}
		}
		return nil
	}
}

// taskNextActions lists what a person may legally start from a task.
//
// gateMode is the project's verify mode: writing a test first is only offered
// where a test changes something the gate can see. removable reflects
// TaskRemove's own guard — no accepted run, none still open — so the button
// and the refusal can never disagree. testFailed says the run that blocked
// the task was the TEST phase itself.
func taskNextActions(status, gateMode string, removable, depsWaiting, testReady, testFailed bool) []string {
	var out []string
	switch status {
	case "todo", "blocked":
		// Two different "blocked" live under one status, and they earn
		// different actions. A task whose last run failed is retryable — run
		// is the point. A task waiting on unaccepted dependencies is not:
		// offering run there let T-023 start and get ACCEPTED while T-022,
		// which it depended on, had never passed — the model invented the
		// thing it depended on, and the plan's ordering meant nothing.
		if !depsWaiting {
			// The order IS the workflow, and clients render it as given. On a
			// tests-gated task with no committed test, TDD is the front door:
			// write the failing definition of done first, then build against
			// it. The board used to show Test first at the bottom of the rail
			// — an afterthought placement for the step meant to come first.
			//
			// A blocked task earns the front door back when what failed was
			// the TEST phase: "retry by building" is right after a failed
			// build, and wrong about a test that never landed — an aborted
			// test-first left the person with no way to restart the chain
			// they had asked for.
			if gateMode == "tests" && !testReady && (status == "todo" || testFailed) {
				out = append(out, "test_first", "run")
			} else {
				out = append(out, "run")
				// A committed test awaiting its build is a promise, and a
				// promise has two exits: keep it (run, above) or withdraw it.
				// Without the second, a chain whose build kept failing held
				// the project's queue with git surgery as the only escape.
				if testReady {
					out = append(out, "retire_test")
				}
				if gateMode == "tests" {
					out = append(out, "test_first")
				}
			}
		}
		if removable {
			out = append(out, "remove")
		}
	case "accepted":
		// A decision can be regretted: reviewing reads the commit, building
		// again starts a new run against work that is already done.
		out = append(out, "review", "run")
	case "in_progress", "review":
		// The action lives on the run: watch it, abort it, or decide its gate.
	}
	return out
}
