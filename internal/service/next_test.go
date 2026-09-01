package service

import (
	"context"
	"slices"
	"strings"
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
		{"a stage blocked by final review", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "FAILED", Stage: "spec", PendingData: map[string]interface{}{"review_verdict": "request-changes"}},
			[]string{"request_changes", "reject"}},
		{"a stage blocked by candidate identity", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "FAILED", Stage: "spec", PendingData: map[string]interface{}{"proposal_identity_mismatch": true}},
			[]string{"request_changes", "reject"}},
		{"a release draft gate", runlog.Run{Status: "paused", PendingKind: "gate", Verdict: "UNVERIFIED", Stage: "release"},
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

func TestFinalReviewDissentCannotBeAcceptedThroughTheAPI(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindSpec: specDoc})
	entry, err := s.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: &runlog.Run{
		ID: "r-red-doc", ProjectID: id, Stage: "spec", Status: "paused", PendingKind: "gate", Verdict: "FAILED",
		PendingData: map[string]interface{}{"review_verdict": "request-changes"},
	}, projectPath: dir}
	if err := s.acceptRun(context.Background(), rs, entry, "", "human"); err == nil || !strings.Contains(err.Error(), "final reviewer requested changes") {
		t.Fatalf("accept error = %v, want final-review guard", err)
	}
}

func TestCandidateIdentityMismatchCannotBeAcceptedThroughTheAPI(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindSpec: specDoc})
	entry, err := s.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: &runlog.Run{
		ID: "r-mismatched-doc", ProjectID: id, Stage: "spec", Status: "paused", PendingKind: "gate", Verdict: "FAILED",
		PendingData: map[string]interface{}{"proposal_identity_mismatch": true},
	}, projectPath: dir}
	if err := s.acceptRun(context.Background(), rs, entry, "", "human"); err == nil || !strings.Contains(err.Error(), "differs from the persisted proposal") {
		t.Fatalf("accept error = %v, want candidate-identity guard", err)
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
			got := taskNextActions(tc.status, tc.gateMode, tc.removable, false, false, false, false)
			if !slices.Equal(got, tc.want) {
				t.Errorf("next = %v, want %v", got, tc.want)
			}
		})
	}

	// Once the failing test is committed, building it is the front door — and
	// withdrawing the promise stands right beside it, because a state that
	// can hold the project's queue owes the person both exits.
	if got := taskNextActions("todo", "tests", true, false, true, false, false); !slices.Equal(got, []string{"run", "retire_test", "test_first", "remove"}) {
		t.Errorf("test-ready next = %v, want run first then retire_test", got)
	}
	// A failed BUILD retries by building, not by writing another test.
	if got := taskNextActions("blocked", "tests", true, false, false, false, false); !slices.Equal(got, []string{"run", "test_first", "remove"}) {
		t.Errorf("retry next = %v, want run first", got)
	}
	// But a failed TEST retries the chain: the definition of done never
	// landed, so TDD is still the front door — an aborted test-first left
	// the person with no way to restart the test+build they had asked for.
	if got := taskNextActions("blocked", "tests", true, false, false, true, false); !slices.Equal(got, []string{"test_first", "run", "remove"}) {
		t.Errorf("failed-test retry next = %v, want test_first first", got)
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

// The document chain is ONE process: an accepted requirements run offers the
// spec, an accepted spec run offers the plan — in place, because leaving the
// run view to find the Documents screen is navigation the acceptance itself
// already implied.
func TestAnAcceptedStageRunOffersTheNextStage(t *testing.T) {
	for _, tc := range []struct {
		stage string
		want  []string
	}{
		{"intake", []string{"run_spec"}},
		{"spec", []string{"run_plan"}},
		{"plan", nil},  // tasks are next, and they live on the board
		{"build", nil}, // task runs still end plainly
	} {
		r := &runlog.Run{Stage: tc.stage, Status: "done", Accepted: true}
		if got := runNext(r); !slices.Equal(got, tc.want) {
			t.Errorf("%s accepted: next = %v, want %v", tc.stage, got, tc.want)
		}
	}
	// An unaccepted ending offers nothing new.
	r := &runlog.Run{Stage: "intake", Status: "failed"}
	if got := runNext(r); got != nil {
		t.Errorf("a failed intake offers %v", got)
	}
}

// The pause said "then resume: the run replays with the new settings" while
// the menu offered only abort — a rule written before document stages could
// re-enter through their persisted request. The promise and the menu now
// agree, for every stage that can keep it.
func TestAPausedDocumentStageOffersResume(t *testing.T) {
	for _, stage := range []string{"intake", "spec", "plan", "build", "test"} {
		r := &runlog.Run{Stage: stage, Status: "paused", PendingKind: "error"}
		next := runNext(r)
		if !slices.Contains(next, "resume") {
			t.Errorf("a paused %s offers %v — the resume its pause text promises is missing", stage, next)
		}
	}
	// A stage with no persisted-request machinery does not promise what it
	// cannot keep.
	r := &runlog.Run{Stage: "triage", Status: "paused", PendingKind: "error"}
	if slices.Contains(runNext(r), "resume") {
		t.Error("triage cannot re-enter; offering resume would be a button that only errors")
	}
}

// The triager judged the fix unverifiable by automated test — visual,
// cosmetic, config — so the front door is the build. test_first stays
// offered: the recommendation is one click to overrule, and the autopilot
// follows the first action like every client.
func TestBuildOnlyFlipsTheFrontDoor(t *testing.T) {
	got := taskNextActions("todo", "tests", true, false, false, false, true)
	if !slices.Equal(got, []string{"run", "test_first", "remove"}) {
		t.Errorf("build-only order = %v, want run first with test_first still offered", got)
	}
	// Without the recommendation the TDD chain keeps the front door.
	got = taskNextActions("todo", "tests", true, false, false, false, false)
	if got[0] != "test_first" {
		t.Errorf("default order lost the TDD front door: %v", got)
	}
}

// An accepted run whose on_accept publication failed keeps the acceptance but
// offers the push door as the retry — the commit is durable locally, it just
// never reached the remote. A successful publication stays a plain ending
// (B-266).
func TestAFailedPublicationOffersThePushDoor(t *testing.T) {
	failedReceipt := runlog.Run{
		Status: "done", Accepted: true, CommitSHA: "abc",
		RemoteReceipts: []map[string]interface{}{
			{"action": "push", "branch": "main", "status": "failed", "error": "dial tcp: connection refused"},
		},
	}
	if got := runNext(&failedReceipt); !slices.Equal(got, []string{"push"}) {
		t.Errorf("failed publication next = %v, want [push]", got)
	}
	// A warning alone (e.g. a persisted failure before receipts existed) is the
	// same state and the same door.
	warnOnly := runlog.Run{
		Status: "done", Accepted: true, CommitSHA: "abc",
		Warning: "committed as abc; push failed: remote unavailable",
	}
	if got := runNext(&warnOnly); !slices.Equal(got, []string{"push"}) {
		t.Errorf("warning-only failure next = %v, want [push]", got)
	}
	// A successful publication is a clean ending: no push door.
	ok := runlog.Run{
		Status: "done", Accepted: true, CommitSHA: "abc",
		RemoteReceipts: []map[string]interface{}{
			{"action": "push", "branch": "main", "status": "pushed"},
		},
	}
	if got := runNext(&ok); got != nil {
		t.Errorf("successful publication next = %v, want nil", got)
	}
}
