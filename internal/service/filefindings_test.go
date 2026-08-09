package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

// An approval "with two minor findings" found real work; the approval means
// "not worth blocking this run", not "not worth remembering". Filing turns
// the transcript's findings into bug reports with provenance — and refuses
// to do it twice, because two clicks must not mean duplicate reports.
func TestFilingFindingsMakesBugsWithProvenanceOnce(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-ff", ProjectID: p.ID, TaskID: "T-028", Stage: "build", Mode: "pair",
		Status: "done", Verdict: "PASSED", Accepted: true,
		StartedAt: "2026-08-09T18:00:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("message", map[string]interface{}{
		"round": 1, "turn": 1, "role": "reviewer", "duckling": "luna",
		"content": "early round", "verdict": "request-changes",
		"findings": []interface{}{map[string]interface{}{"issue": "stale early finding"}},
	})
	w.AppendEvent("message", map[string]interface{}{
		"round": 2, "turn": 1, "role": "reviewer", "duckling": "luna",
		"content": "ok", "verdict": "approve",
		"findings": []interface{}{
			map[string]interface{}{
				"severity": "minor", "file": "app.py", "line": float64(42),
				"issue": "week boundary off by one", "fix": "use ISO weeks",
			},
		},
	})
	w.Close()
	s.RecoverRuns(context.Background())

	bugs, err := s.RunFileFindings(context.Background(), "r-ff")
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 1 {
		t.Fatalf("filed %d bugs, want 1 — only the LAST verdict's findings count", len(bugs))
	}
	b := bugs[0]
	if b.Title != "week boundary off by one" {
		t.Errorf("title = %q", b.Title)
	}
	if b.Severity != "low" {
		t.Errorf("severity = %q, want low (reviewer said minor)", b.Severity)
	}
	if b.Source != "review" || b.Reporter != "luna" {
		t.Errorf("provenance lost: source=%q reporter=%q", b.Source, b.Reporter)
	}
	for _, want := range []string{"app.py:42", "use ISO weeks", "r-ff", "T-028"} {
		if !strings.Contains(b.Body, want) {
			t.Errorf("body missing %q:\n%s", want, b.Body)
		}
	}

	// The second click is refused, naming what already exists.
	_, err = s.RunFileFindings(context.Background(), "r-ff")
	if err == nil || !strings.Contains(err.Error(), "already filed") {
		t.Errorf("a second filing was not refused: %v", err)
	}

	// And the bugs are really in the project's list.
	list, err := s.BugList(context.Background(), p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("bug list has %d, want 1", len(list))
	}
}
