package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// pausedWorktreeRun creates the same persisted gate state that RunAccept sees,
// with candidate work deliberately written only in its isolated checkout.
func pausedWorktreeRun(t *testing.T, s *Service, id, dir, runID string) (*runlog.Run, *vcs.Git) {
	t.Helper()
	git := vcs.New(dir)
	run := &runlog.Run{ID: runID, ProjectID: id, TaskID: "T-001", Stage: "build", Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := s.createRunWorktree(run, dir); err != nil {
		t.Fatal(err)
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run, git
}

func TestAcceptWorktreeReceiptNamesRebasedSHA(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	base := mustHead(t, git)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-rebased-receipt")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workGit := vcs.New(run.WorktreePath)
	if err := workGit.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := workGit.Commit("run candidate"); err != nil {
		t.Fatal(err)
	}
	preRebase := mustHead(t, workGit)
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("default moved"); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.CommitSHA == base || result.CommitSHA == preRebase || result.CommitSHA == "" {
		t.Fatalf("accepted sha = %q, want rebased commit rather than base %s or pre-rebase %s", result.CommitSHA, base, preRebase)
	}
	if got := mustHead(t, git); got != result.CommitSHA {
		t.Fatalf("default HEAD = %s, receipt = %s", got, result.CommitSHA)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.CommitSHA != result.CommitSHA {
		t.Fatalf("recorded sha = %s, want %s", detail.Run.CommitSHA, result.CommitSHA)
	}
}

func TestAcceptWorktreeFastPathDoesNotRebase(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-fast-path")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.GateReproduced == nil {
		t.Fatal("acceptance gate was not recorded")
	}
	gates := 0
	for _, event := range detail.Events {
		if event.Type == "gate_reproduced" {
			gates++
		}
	}
	if gates != 1 {
		t.Fatalf("fast path ran %d acceptance gates, want exactly one", gates)
	}
	// A fast-path commit has the recorded base directly as its parent; a rebase
	// would be both unnecessary and observably change that parent relationship.
	parent, err := vcs.New(dir).ParentSHA(detail.Run.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if parent != run.BaseSHA {
		t.Fatalf("parent = %s, want original base %s", parent, run.BaseSHA)
	}
}

func TestAcceptWorktreeRedAfterRebasePausesWithoutLanding(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "custom", "verify.custom": "cat value; test \"$(cat value)\" = good"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value"), []byte("good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("gate fixture"); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-red-rebase")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "value"), []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("break semantic contract"); err != nil {
		t.Fatal(err)
	}
	defaultSHA := mustHead(t, git)
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err == nil {
		t.Fatal("red rebased gate accepted")
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
		t.Fatalf("state = %s/%s", detail.Run.Status, detail.Run.PendingKind)
	}
	if mustHead(t, git) != defaultSHA {
		t.Fatal("red rebased commit landed on default")
	}
	if !strings.Contains(detail.Run.PendingData["detail"].(string), run.BaseSHA[:7]) || !strings.Contains(detail.Run.PendingData["detail"].(string), defaultSHA[:7]) {
		t.Fatalf("divergence missing from %q", detail.Run.PendingData["detail"])
	}
	if detail.Run.PendingData["output"] == "" {
		t.Fatal("red gate output was not attached")
	}
}

func TestAcceptWorktreeConflictPausesCleanly(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-conflict")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "index.html"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("default conflict"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err == nil {
		t.Fatal("textual conflict accepted")
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, ok := detail.Run.PendingData["conflicting_files"].([]string)
	if !ok || len(files) != 1 || files[0] != "index.html" {
		t.Fatalf("conflicts = %#v", detail.Run.PendingData["conflicting_files"])
	}
	if got := detail.Run.Next; len(got) != 2 || got[0] != "resolve_by_hand" || got[1] != "reject" {
		t.Fatalf("next = %v", got)
	}
	for _, name := range []string{"REBASE_HEAD", "MERGE_HEAD"} {
		out, err := vcs.New(run.WorktreePath).GitPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(strings.TrimSpace(out)); !os.IsNotExist(err) {
			t.Fatalf("stale %s remains: %v", name, err)
		}
	}
}

func TestAcceptWorktreeAdvancesCleanDefaultCheckout(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-update-clean-checkout")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "accepted.txt"), []byte("accepted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustHead(t, git); got != result.CommitSHA {
		t.Fatalf("default checkout HEAD = %s, accepted SHA = %s", got, result.CommitSHA)
	}
	got, err := os.ReadFile(filepath.Join(dir, "accepted.txt"))
	if err != nil {
		t.Fatalf("clean default checkout did not receive accepted file: %v", err)
	}
	if string(got) != "accepted work\n" {
		t.Fatalf("clean default checkout file = %q, want accepted work", got)
	}
	if clean, err := git.IsClean(); err != nil || !clean {
		t.Fatalf("default checkout is dirty after sync: clean=%v err=%v", clean, err)
	}
}

func TestAcceptWorktreeLeavesDirtyTouchedCheckoutBehindWithWarning(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-leave-dirty-checkout")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "shared.txt"), []byte("accepted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This is a candidate-touched path, so accepting must not overwrite it.
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("person's local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	wantWarning := "main advanced to " + result.CommitSHA + "; your checkout is behind and was left untouched"
	if got := acceptResultWarning(t, result); got != wantWarning {
		t.Fatalf("accept warning = %q, want %q", got, wantWarning)
	}
	got, err := os.ReadFile(filepath.Join(dir, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "person's local work\n" {
		t.Fatalf("dirty checkout was changed to %q", got)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Warning != wantWarning {
		t.Fatalf("run warning = %q, want %q", detail.Run.Warning, wantWarning)
	}
	found := false
	for _, event := range detail.Events {
		if event.Type == "warning" && event.Data["detail"] == wantWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("warning event %q was not recorded", wantWarning)
	}
}

// acceptResultWarning reads the public JSON contract so this test remains
// compilable until AcceptResult grows the required warning field.
func acceptResultWarning(t *testing.T, result *AcceptResult) string {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload["warning"]
}

func TestRejectWorktreeLeavesPersonTreeUnchanged(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	before := snapshotFixtureTree(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-reject-worktree")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("only isolated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.RunReject(context.Background(), run.ID, "no"); err != nil {
		t.Fatal(err)
	}
	assertFixtureTreeEqual(t, before, snapshotFixtureTree(t, dir))
	if _, err := os.Lstat(run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
}
