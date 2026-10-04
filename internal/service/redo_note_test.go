package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-485, from TI-36X r-20261002-190108-fxji (T-003, test-first): the run ended
// FAILED with "no test file was written, so nothing was specified" and the
// reviewer's critical finding, yet the retry note said neither — run.Failure
// is empty for a test-first verdict, so the note's "Failure:" part vanished —
// and the desktop called it "advisor-drafted by qwen38-max" although no
// advisor was consulted.
func TestRedoNoteLeadsWithWhyTheTestFirstRunFailed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	const detail = "no test file was written, so nothing was specified"
	const issue = "No test file was written; the diff is empty and the task requires creating tests/parser.test.mjs."
	run := &runlog.Run{
		ID: "r-tf", ProjectID: id, TaskID: "T-001", Stage: "test", Mode: "pair",
		Status: "done", Verdict: "FAILED",
		Roster:    map[string]string{"advisor": "qwen38-max", "reviewer": "glm52", "implementer": "atom-local"},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	// Written the way the real run's events.jsonl has them.
	w.AppendEvent("message", map[string]interface{}{
		"role": "reviewer", "duckling": "glm52", "verdict": "request-changes",
		"findings": []interface{}{
			map[string]interface{}{"severity": "critical", "file": "tests/parser.test.mjs", "issue": issue, "fix": "Create tests/parser.test.mjs."},
			map[string]interface{}{"severity": "minor", "file": "x", "issue": "a nit that blocks nothing"},
		},
	})
	w.AppendEvent("verdict", map[string]interface{}{"verdict": "FAILED", "detail": detail})
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-tf")
	if err != nil {
		t.Fatal(err)
	}
	note := got.Run.RedoNote
	if note == nil {
		t.Fatal("a FAILED test-first run carries no retry note")
	}
	if note.Origin != runlog.RedoOriginDucklab || note.Advisor != "" {
		t.Errorf("origin = %q, advisor = %q; the engine assembled this note, no advisor wrote it", note.Origin, note.Advisor)
	}
	if !strings.Contains(note.Reason, detail) || !strings.Contains(note.Reason, issue) {
		t.Errorf("reason lacks the verdict detail or the blocking finding:\n%s", note.Reason)
	}
	if strings.Contains(note.Reason, "a nit that blocks nothing") {
		t.Errorf("reason carries a minor finding as blocking:\n%s", note.Reason)
	}
	body := strings.TrimPrefix(note.Draft, "Retry the task after addressing the failure.\n\n")
	if !strings.HasPrefix(body, "Why it failed: "+detail) {
		t.Errorf("the draft does not lead with why the run failed:\n%s", note.Draft)
	}
	if !strings.Contains(note.Draft, issue) {
		t.Errorf("the draft lacks the reviewer's blocking finding:\n%s", note.Draft)
	}
}

// Only the reviewer's last verdict speaks: an objection an earlier round
// raised and a later round approved is not why the run failed.
func TestRedoReasonReadsOnlyTheLastReviewerVerdict(t *testing.T) {
	events := []*runlog.Event{
		{Type: "message", Data: map[string]interface{}{"verdict": "request-changes", "findings": []interface{}{
			map[string]interface{}{"severity": "critical", "issue": "round one objection"},
		}}},
		{Type: "message", Data: map[string]interface{}{"verdict": "approve", "findings": []interface{}{}}},
		{Type: "verdict", Data: map[string]interface{}{"verdict": "FAILED", "detail": "the gate is still green"}},
	}
	got := redoReason(&runlog.Run{Verdict: "FAILED"}, events)
	if strings.Contains(got, "round one objection") || !strings.Contains(got, "the gate is still green") {
		t.Errorf("reason = %q", got)
	}
}

// A failed run whose reason predates run.Failure gets it back from events in
// RunGet; the note drafted from the stored record used to miss it.
func TestRedoNoteCarriesTheFailureRecoveredFromEvents(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	const why = `"index.html" is claimed by both "Solver" and "Renderer"`
	run := &runlog.Run{
		ID: "r-old", ProjectID: id, TaskID: "T-001", Status: "failed", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("error", map[string]interface{}{"error": why})
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.RedoNote == nil || !strings.Contains(got.Run.RedoNote.Reason, why) || !strings.Contains(got.Run.RedoNote.Draft, why) {
		t.Fatalf("the note lacks the recovered failure: %+v", got.Run.RedoNote)
	}
}
