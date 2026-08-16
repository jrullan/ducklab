package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A failed run showed FAILED and nothing else.
//
// The reason went to an `error` event, and no client rendered it — the
// desktop's timeline handles tool calls and policy violations only. Finding out
// why a run died meant opening events.jsonl by hand, which is what it took to
// diagnose a reasoning loop and a split refusal in the same week.
//
// Some of these messages exist to be acted on: split refuses a decomposition
// with the exact file two subtasks both claimed.
func TestAFailedRunSaysWhyItFailed(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	const why = `"index.html" is claimed by both "Solver" and "Renderer"; ` +
		`each file may have exactly one owner`
	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Status: "failed", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	// Written the way failRun writes it, for a run that predates the field.
	w.AppendEvent("error", map[string]interface{}{"error": why})
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Failure != why {
		t.Errorf("failure = %q, want the ownership conflict", got.Run.Failure)
	}
}

// The reason belongs to failed runs. A paused run is waiting, not broken, and a
// red banner on one would read as a failure that never happened.
func TestASucceedingRunCarriesNoFailure(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-2", ProjectID: id, TaskID: "T-001", Status: "paused", Verdict: "PASSED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.AppendEvent("error", map[string]interface{}{"error": "a tool call failed and was retried"})
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Failure != "" {
		t.Errorf("a paused run reports a failure: %q", got.Run.Failure)
	}
}

// A run that failed while handling an earlier error died of the second one.
func TestTheLastErrorIsTheReason(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	run := &runlog.Run{
		ID: "r-3", ProjectID: id, TaskID: "T-001", Status: "failed", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, _ := runlog.NewWriter(dir, run)
	w.AppendEvent("error", map[string]interface{}{"error": "first"})
	w.AppendEvent("error", map[string]interface{}{"error": "second"})
	w.Close()
	s.RecoverRuns(context.Background())

	got, _ := s.RunGet(context.Background(), "r-3")
	if got.Run.Failure != "second" {
		t.Errorf("failure = %q, want the last error", got.Run.Failure)
	}
}
