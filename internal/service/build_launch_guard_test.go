package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// runDirs lists the run records a project holds on disk.
func runDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".ducklab", "runs"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func liveRuns(s *Service, projectID string) int {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	n := 0
	for _, rs := range s.runs {
		if rs.run.ProjectID == projectID {
			n++
		}
	}
	return n
}

// B-517, from TI-36X: "Retry with this note" on the failed intake
// r-20261010-115519-tpfn sent POST /runs with no task, the intake's council
// mode and the redo note. The engine recorded r-20261010-120758-gjs4 (and two
// more), each FAILED within a second on `unknown mode "council"`. The request
// is now refused before any run exists, in words that say what to do.
func TestATasklessCouncilBuildIsRefusedBeforeAnyRunExists(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, dir := omittedModeProject(t, s, "")
	before := runDirs(t, dir)

	_, err := s.RunStart(context.Background(), projectID, RunRequest{
		TaskID: "", Mode: "council", Ducklings: []string{"pato-uno", "pato-dos"},
		Note: "Retry the task after addressing the failure.\n\nReviewer's blocking findings:\n- [major] REQ-008 invents behavior",
	})
	if err == nil {
		t.Fatal("a build with no task and a document-stage mode was started")
	}
	if !errors.Is(err, ErrLaunchRefused) {
		t.Errorf("the refusal is not marked as a refused launch: %v", err)
	}
	if !strings.Contains(err.Error(), "needs a task") || !strings.Contains(err.Error(), "revise that stage") {
		t.Errorf("the refusal does not say what is missing and what to do instead: %v", err)
	}
	if got := runDirs(t, dir); len(got) != len(before) {
		t.Errorf("a refused launch left run records on disk: %v", got)
	}
	if n := liveRuns(s, projectID); n != 0 {
		t.Errorf("a refused launch registered %d run(s)", n)
	}
}

// The task alone does not make it a build: a mode no build executes is
// refused too — from the request, and from a saved default naming one.
func TestABuildInADocumentStageModeIsRefused(t *testing.T) {
	for _, mode := range []string{"council", "sectioned", "human", "bogus"} {
		t.Run("request "+mode, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno", "pato-dos")
			projectID, dir := omittedModeProject(t, s, "")
			_, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: mode, DryRun: true})
			if err == nil || !errors.Is(err, ErrLaunchRefused) {
				t.Fatalf("mode %q: err = %v, want a refused launch", mode, err)
			}
			if !strings.Contains(err.Error(), "not a build mode") || !strings.Contains(err.Error(), "solo, pair, tournament, split") {
				t.Errorf("the refusal does not name the build modes: %v", err)
			}
			if got := runDirs(t, dir); len(got) != 0 {
				t.Errorf("a refused launch left run records on disk: %v", got)
			}
		})
	}
	t.Run("settings default", func(t *testing.T) {
		s := serviceWithDucklings(t, "pato-uno", "pato-dos")
		s.cfg.Defaults.BuildMode = "council"
		projectID, dir := omittedModeProject(t, s, "")
		_, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", DryRun: true})
		if err == nil || !errors.Is(err, ErrLaunchRefused) {
			t.Fatalf("err = %v, want a refused launch", err)
		}
		if !strings.Contains(err.Error(), "from settings") {
			t.Errorf("the refusal does not say the mode came from Settings: %v", err)
		}
		if got := runDirs(t, dir); len(got) != 0 {
			t.Errorf("a refused launch left run records on disk: %v", got)
		}
	})
	// Every build mode still passes the door (the dry run proves the record
	// is made).
	for _, mode := range BuildModes {
		t.Run("allowed "+mode, func(t *testing.T) {
			if err := checkBuildLaunch("T-001", mode, "request"); err != nil {
				t.Errorf("build mode %q refused: %v", mode, err)
			}
		})
	}
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := omittedModeProject(t, s, "")
	if _, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: "solo", DryRun: true}); err != nil {
		t.Errorf("a solo build of a real task was refused: %v", err)
	}
}

// Siblings: a test-first chain promises a build, so the promised build's
// mode is checked at the promise; a review takes only its own modes.
func TestSiblingLaunchesRefuseModesTheyCannotRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, dir := omittedModeProject(t, s, "")

	_, err := s.TestStart(context.Background(), projectID, TestFirstRequest{
		TaskID: "T-001", ThenBuild: true, Build: RunRequest{Mode: "council"},
	})
	if err == nil || !errors.Is(err, ErrLaunchRefused) || !strings.Contains(err.Error(), "not a build mode") {
		t.Errorf("test-first chain promising a council build: err = %v, want a refused launch", err)
	}
	_, err = s.ReviewStart(context.Background(), projectID, ReviewRequest{TaskID: "T-001", Mode: "tournament"})
	if err == nil || !errors.Is(err, ErrLaunchRefused) || !strings.Contains(err.Error(), "not a review mode") {
		t.Errorf("review in tournament mode: err = %v, want a refused launch", err)
	}
	if got := runDirs(t, dir); len(got) != 0 {
		t.Errorf("refused launches left run records on disk: %v", got)
	}
}

// The redo note on a document stage says what the retry is: a revision of
// the draft. "Retry the task" on the intake's note read as a build order.
func TestRedoNoteOnAStageRunLeadsWithARevision(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	run := &runlog.Run{
		ID: "r-intake", ProjectID: id, Stage: "intake", Mode: "council",
		Status: "paused", PendingKind: "gate", Verdict: "FAILED",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.AppendEvent("message", map[string]interface{}{
		"role": "reviewer", "verdict": "request-changes",
		"findings": []interface{}{map[string]interface{}{"severity": "major", "issue": "REQ-008 invents behavior"}},
	})
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.RunGet(context.Background(), "r-intake")
	if err != nil {
		t.Fatal(err)
	}
	note := got.Run.RedoNote
	if note == nil {
		t.Fatal("the failed intake carries no redo note")
	}
	if !strings.HasPrefix(note.Draft, "Revise the intake draft") || strings.Contains(note.Draft, "Retry the task") {
		t.Errorf("the intake's note reads as a task retry:\n%s", note.Draft)
	}
	for stage, want := range map[string]string{
		"spec": "Revise the spec draft", "plan": "Revise the plan draft", "release": "Revise the release draft",
		"build": "Retry the task", "test": "Retry the task",
	} {
		if got := redoLead(stage); !strings.HasPrefix(got, want) {
			t.Errorf("redoLead(%q) = %q, want prefix %q", stage, got, want)
		}
	}
}
