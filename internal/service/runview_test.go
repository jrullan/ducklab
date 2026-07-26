package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC-32 / I7: the candidates payload must not contain who wrote what.
//
// Hiding the mapping in the UI is not enough — anything in the API can be
// rendered, screenshotted or logged. The assertion is on the serialised
// payload, because that is what actually leaves the engine.
func TestCandidatePayloadContainsNoAuthorship(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-tour", "done")
	s.RecoverRuns(context.Background())

	candDir := filepath.Join(s.RunDir("r-tour"), "candidates")
	if err := os.MkdirAll(candDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(candDir, "A.patch"), []byte("--- a/x\n+++ b/x\n+written by pato-local"), 0o644)
	os.WriteFile(filepath.Join(candDir, "B.patch"), []byte("--- a/y\n+++ b/y\n+other"), 0o644)

	cands, err := s.RunCandidates(context.Background(), "r-tour")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}

	payload, err := json.Marshal(cands)
	if err != nil {
		t.Fatal(err)
	}
	// The struct must have no author-shaped field at all.
	for _, forbidden := range []string{"duckling", "author", "provider", "model", "roster"} {
		if strings.Contains(strings.ToLower(string(payload)), `"`+forbidden+`"`) {
			t.Errorf("candidate payload exposes %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), `"label":"A"`) {
		t.Errorf("labels missing from payload: %s", payload)
	}
}

func TestCandidatesAreOrderedByLabel(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-order", "done")
	s.RecoverRuns(context.Background())

	candDir := filepath.Join(s.RunDir("r-order"), "candidates")
	os.MkdirAll(candDir, 0o755)
	for _, l := range []string{"C", "A", "B"} {
		os.WriteFile(filepath.Join(candDir, l+".patch"), []byte("+"+l), 0o644)
	}
	cands, _ := s.RunCandidates(context.Background(), "r-order")
	for i, want := range []string{"A", "B", "C"} {
		if cands[i].Label != want {
			t.Errorf("candidate %d = %q, want %q", i, cands[i].Label, want)
		}
	}
}

// A non-tournament run simply has no candidates; that is not an error.
func TestCandidatesEmptyForNonTournamentRun(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-solo", "done")
	s.RecoverRuns(context.Background())

	cands, err := s.RunCandidates(context.Background(), "r-solo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("got %d candidates for a solo run", len(cands))
	}
}

// The Run view opens before the diff exists; that must render as empty, not
// as an error the UI has to special-case.
func TestRunArtefactsMissingAreEmptyNotErrors(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-fresh", "running")
	s.RecoverRuns(context.Background())

	for name, fn := range map[string]func() (string, error){
		"diff":       func() (string, error) { return s.RunDiff(context.Background(), "r-fresh") },
		"transcript": func() (string, error) { return s.RunTranscript(context.Background(), "r-fresh") },
		"verify":     func() (string, error) { return s.RunVerify(context.Background(), "r-fresh", 100) },
	} {
		got, err := fn()
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
		if got != "" {
			t.Errorf("%s: got %q, want empty", name, got)
		}
	}
}

func TestRunArtefactsUnknownRunIsAnError(t *testing.T) {
	s := newTestService(t)
	if _, err := s.RunDiff(context.Background(), "r-nope"); err == nil {
		t.Error("unknown run returned a diff")
	}
	if _, err := s.RunCandidates(context.Background(), "r-nope"); err == nil {
		t.Error("unknown run returned candidates")
	}
}

func TestRunVerifyTailsOutput(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	writeRun(t, entry.Path, projectID, "r-log", "done")
	s.RecoverRuns(context.Background())

	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	os.WriteFile(filepath.Join(s.RunDir("r-log"), "verify.log"), []byte(strings.Join(lines, "\n")), 0o644)

	out, err := s.RunVerify(context.Background(), "r-log", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "\n") + 1; got != 10 {
		t.Errorf("tail returned %d lines, want 10", got)
	}
}
