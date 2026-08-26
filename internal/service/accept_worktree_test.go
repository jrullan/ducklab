package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

// Acceptance must reproduce from the detached checkout, where this fake tool
// exists only when verify.link_deps explicitly borrows it from the live tree.
func TestAcceptWorktreeCleanCheckoutLinksDeclaredDependency(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, dir, `link_deps = ["tools/fake"]
mode = "custom"
custom = "test -f tools/fake/bin/runner"`)
	gitProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "tools", "fake", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "fake", "bin", "runner"), []byte("tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-accept-linked-dep")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatalf("accept did not reproduce the gate with its declared dependency: %v", err)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.GateReproduced == nil || !detail.Run.GateReproduced.Green {
		t.Fatal("acceptance gate was not green from the clean checkout")
	}
}

// A linked dependency can be staged by an earlier add operation despite the
// exclusion pathspec. Acceptance must unstage it before rebasing a moved
// default branch, while leaving it out of the landed commit.
func TestAcceptWorktreeRebasesWithLinkedDependencyUnstaged(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, dir, `link_deps = ["tools/fake"]
mode = "custom"
custom = "test -f tools/fake/runtime"`)
	git := gitProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "tools", "fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "fake", "runtime"), []byte("runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-rebase-linked-dep")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate the ordinary AddAll that first staged the link. stageRun must
	// remove this entry from the index, not merely omit it from its next add.
	if err := vcs.New(run.WorktreePath).AddAll(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), []byte("default moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.Add("default.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("default moved"); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept rebased worktree with linked dependency: %v", err)
	}
	diff, err := git.ShowCommit(result.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "tools/fake") {
		t.Fatalf("landed commit contains linked dependency: %s", diff)
	}
}

func TestAcceptWorktreeNamesUndeclaredLinkedDependency(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, dir, `mode = "custom"
custom = "test -f tools/fake/bin/runner"`)
	gitProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "tools", "fake", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "fake", "bin", "runner"), []byte("tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-accept-missing-dep")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "run.txt"), []byte("run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.RunAccept(context.Background(), run.ID, "")
	if err == nil {
		t.Fatal("accept unexpectedly reproduced without the undeclared dependency")
	}
	if !strings.Contains(err.Error(), "tools/fake/") || !strings.Contains(err.Error(), "verify.link_deps") {
		t.Fatalf("accept error = %q, want the missing dependency and verify.link_deps guidance", err)
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

// A paused test-first run must retain its isolated checkout until its human
// gate is decided. The accepted assertion-red test then lands on the default
// branch, and only then is the worktree removed.
func TestAcceptPausedTestFirstRetainsWorktreeAndLandsRedTest(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"verify.mode": "tests", "verify.tests": "false",
	}); err != nil {
		t.Fatal(err)
	}
	git := gitProject(t, dir)
	run := &runlog.Run{
		ID: "r-paused-test-first", ProjectID: id, TaskID: "T-001", Stage: "test",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		PendingData: map[string]interface{}{"kind": "test_first", "retain_worktree": true},
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.createRunWorktree(run, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "red_test.txt"), []byte("red test\n"), 0o644); err != nil {
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
	if _, err := os.Stat(run.WorktreePath); err != nil {
		t.Fatalf("paused test-first worktree was removed: %v", err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept paused assertion-red test: %v", err)
	}
	if got := mustHead(t, git); got != result.CommitSHA {
		t.Fatalf("default HEAD = %s, accepted test commit = %s", got, result.CommitSHA)
	}
	if diff, err := git.ShowCommit(result.CommitSHA); err != nil || !strings.Contains(diff, "red_test.txt") {
		t.Fatalf("accepted red test is absent: diff=%q err=%v", diff, err)
	}
	if _, err := os.Stat(run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree remains after accepting test-first run: %v", err)
	}
}

func TestAcceptChainedTestStaysOnRunBranchAndExcludesLinkedDeps(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	appendVerifyPreparation(t, dir, `link_deps = ["tools/fake"]
mode = "tests"
tests = "false"`)
	git := gitProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "tools", "fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "fake", "runtime"), []byte("runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustHead(t, git)
	run := &runlog.Run{ID: "r-chained-red", ProjectID: id, TaskID: "T-001", Stage: "test", Status: "paused", Verdict: "PASSED", PendingKind: "gate", StartedAt: time.Now().UTC().Format(time.RFC3339), ChainBuild: map[string]interface{}{"task_id": "T-001", "mode": "solo"}}
	if err := s.createRunWorktree(run, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "red_test.txt"), []byte("red test\n"), 0o644); err != nil {
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
	result, err := s.RunAccept(context.Background(), run.ID, "chained: the test landed red")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustHead(t, git); got != before {
		t.Fatalf("default HEAD = %s, want unchanged %s", got, before)
	}
	if _, err := os.Stat(run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("chained test worktree remains after acceptance: %v", err)
	}
	diff, err := git.ShowCommit(result.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "tools/fake") {
		t.Fatalf("chain commit contains linked dependency: %s", diff)
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

// A failed rebase happens after the run commit exists. Once its blocker is
// cleared, retrying must reuse that tagged, clean HEAD instead of attempting an
// empty commit before it can rebase or land.
func TestAcceptWorktreeRetryReusesCommitAfterFailedRebase(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-retry-reuse-commit")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "index.html"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.Add("index.html"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("default conflict"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err == nil {
		t.Fatal("conflicting rebase accepted")
	}
	workGit := vcs.New(run.WorktreePath)
	firstCommit := mustHead(t, workGit)
	if has, err := workGit.HeadHasTrailer("Ducklab-Run", run.ID); err != nil || !has {
		t.Fatalf("failed accept did not leave its run commit: has=%v err=%v", has, err)
	}
	cmd := exec.Command("git", "reset", "--hard", run.BaseSHA)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clear rebase blocker: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), []byte("default moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.Add("default.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("cleared blocker with unrelated default change"); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("retry after clearing rebase blocker: %v", err)
	}
	if result.CommitSHA == firstCommit {
		t.Fatal("retry did not rebase the existing run commit onto the moved default")
	}
	if has, err := vcs.New(dir).HeadHasTrailer("Ducklab-Run", run.ID); err != nil || !has {
		t.Fatalf("landed rebased commit is not the reused run commit: has=%v err=%v", has, err)
	}
	if got := mustHead(t, git); got != result.CommitSHA {
		t.Fatalf("default HEAD = %s, want landed rebased commit %s", got, result.CommitSHA)
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
	if clean, err := git.PathsAreClean([]string{"accepted.txt"}); err != nil || !clean {
		t.Fatalf("accepted path is dirty after sync: clean=%v err=%v", clean, err)
	}
}

func TestAcceptWorktreeRacedCheckoutAdvancesWithWarning(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-raced-checkout")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "raced.txt"), []byte("accepted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A concurrent pull holds this lock while it mutates the checkout. Creating
	// it directly makes the race deterministic while leaving read-only checks
	// available to acceptance.
	lockPath := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept raced checkout: %v", err)
	}
	if result.Warning == "" || !strings.Contains(result.Warning, "raced the landing") || !strings.Contains(result.Warning, "run git status") {
		t.Fatalf("accept warning = %q, want raced-landing guidance", result.Warning)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status != "done" {
		t.Fatalf("run status = %q, want done", detail.Run.Status)
	}
	if detail.Run.Warning != result.Warning {
		t.Fatalf("run warning = %q, result warning = %q", detail.Run.Warning, result.Warning)
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
	gotWarning := acceptResultWarning(t, result)
	if !strings.Contains(gotWarning, wantWarning) {
		t.Fatalf("accept warning = %q, want checkout state %q", gotWarning, wantWarning)
	}
	for _, risk := range []string{"commit from this tree", "revert landed work", "builds", "stale sources"} {
		if !strings.Contains(strings.ToLower(gotWarning), risk) {
			t.Errorf("accept warning = %q, want risk %q", gotWarning, risk)
		}
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
