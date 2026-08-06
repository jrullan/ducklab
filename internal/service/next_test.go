package service

import (
	"context"
	"slices"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The next-actions contract: the engine states what is legal, clients render
// buttons from lists. Every client surface used to encode these rules by hand,
// and every hand-encoded rule was wrong at least once in the first real
// project — Accept offered on a run it could not apply to, a remove the engine
// refused after the click, a state with no button at all.
func TestWhatARunOffersMatchesItsState(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  runlog.Run
		want []string
	}{
		{"running", runlog.Run{Status: "running"}, []string{"abort"}},
		{"queued", runlog.Run{Status: "queued"}, []string{"abort"}},
		{"paused at a green gate", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "PASSED", Stage: "build"},
			[]string{"accept", "reject"}},
		// A FAILED verdict has nothing to accept.
		{"paused at a red gate", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "FAILED", Stage: "build"},
			[]string{"reject"}},
		// Only a document can be sent back with a note; "almost" for code is a
		// new run.
		{"a stage's gate", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED", Stage: "spec"},
			[]string{"accept", "request_changes", "reject"}},
		{"a question", runlog.Run{Status: "paused", PendingKind: "question"}, []string{"answer", "abort"}},
		// The states RunResume accepts, and nothing else.
		{"paused by a restart", runlog.Run{Status: "paused", PendingKind: "engine_restart"}, []string{"resume", "abort"}},
		{"paused by a shutdown", runlog.Run{Status: "paused", PendingKind: "engine_shutdown"}, []string{"resume", "abort"}},
		// Endings offer nothing: relaunch is an action on the task.
		{"done", runlog.Run{Status: "done", Accepted: true}, nil},
		{"failed", runlog.Run{Status: "failed"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runNext(&tc.run); !slices.Equal(got, tc.want) {
				t.Errorf("next = %v, want %v", got, tc.want)
			}
		})
	}
}

// The list must arrive recomputed, not replayed from disk: a stale stored copy
// that disagreed with the rules would be worse than none.
func TestNextIsRecomputedOnEveryRead(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", PendingKind: "gate", Verdict: "PASSED",
		// A lie on disk, as if the rules had changed since this was written.
		Next:      []string{"self_destruct"},
		StartedAt: "2026-07-31T10:00:00Z",
	}
	w, _ := runlog.NewWriter(dir, run)
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got.Run.Next, "self_destruct") {
		t.Fatalf("a stale stored list was served: %v", got.Run.Next)
	}
	if !slices.Contains(got.Run.Next, "accept") {
		t.Errorf("next = %v, want accept offered at a green gate", got.Run.Next)
	}

	list, err := s.RunList(context.Background(), RunFilter{ProjectID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(list[0].Next, "accept") {
		t.Errorf("RunList did not recompute: %v", list[0].Next)
	}
}

// Parity with the guards that act: what a task offers is what the endpoints
// will allow, because both read the same facts.
func TestWhatATaskOffersMatchesTheGuards(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    string
		gateMode  string
		removable bool
		want      []string
	}{
		// TDD is the front door: with no committed test, the definition of
		// done comes first, and the order IS the workflow clients render.
		{"fresh under a tests gate", "todo", "tests", true, []string{"test_first", "run", "remove"}},
		// Test-first is only offered where a test changes something the gate
		// can see.
		{"fresh under a build gate", "todo", "build", true, []string{"run", "remove"}},
		{"blocked but runnable", "blocked", "build", true, []string{"run", "remove"}},
		// TaskRemove's own guard, reflected: a pinned task never offers remove.
		{"pinned by an accepted run", "todo", "tests", false, []string{"test_first", "run"}},
		{"being worked on", "in_progress", "tests", true, nil},
		{"awaiting a decision", "review", "tests", true, nil},
		{"done", "accepted", "tests", false, []string{"review", "run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := taskNextActions(tc.status, tc.gateMode, tc.removable, false, false)
			if !slices.Equal(got, tc.want) {
				t.Errorf("next = %v, want %v", got, tc.want)
			}
		})
	}

	// Once the failing test is committed, building it is the front door.
	if got := taskNextActions("todo", "tests", true, false, true); !slices.Equal(got, []string{"run", "test_first", "remove"}) {
		t.Errorf("test-ready next = %v, want run first", got)
	}
	// A failed run retries by building, not by writing another test.
	if got := taskNextActions("blocked", "tests", true, false, false); !slices.Equal(got, []string{"run", "test_first", "remove"}) {
		t.Errorf("retry next = %v, want run first", got)
	}
}

// And through the real read path: the task a run pinned stops offering remove.
func TestAPinnedTaskStopsOfferingRemove(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	writeRun(t, dir, id, "r-1", "paused")
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if tk.ID == "T-001" && slices.Contains(tk.Next, "remove") {
			t.Errorf("T-001 has an open run and still offers remove: %v", tk.Next)
		}
		if tk.ID == "T-002" && !slices.Contains(tk.Next, "remove") {
			t.Errorf("T-002 has no runs and does not offer remove: %v", tk.Next)
		}
	}
}
