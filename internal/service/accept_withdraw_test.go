package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

// B-069. An accept commits the run's diff and then reproduces the gate from a
// clean checkout of that commit. When the reproduction was red, the commit
// stayed on the branch and the run became undecidable: accept re-verified
// the same sha (red forever), reject refused because "a commit landed since
// the run began" — the run's own. Nothing may land that did not reproduce:
// the failed accept takes its own commit back, keeps the diff in the tree,
// and both decisions stay open.
func TestAnUnreproducibleAcceptCommitIsWithdrawn(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)
	// A gate that is green in the live tree and red from a clean checkout:
	// it looks for a file the tree has but git ignores — the shape of every
	// "works on my tree" failure, and of the T-068 arch violation the test
	// cache hid in-run.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("ignore local-only"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local-only"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "tests", "verify.tests": "test -f local-only"}); err != nil {
		t.Fatal(err)
	}
	baseline := mustHead(t, g)
	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-red-repro", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate", Autonomy: "yolo",
		TreeSnapshot: snap, TreeSnapshotHead: baseline, StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The run's work, uncommitted, as the strategy leaves it for the gate.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("the run's change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = s.RunAccept(context.Background(), "r-red-repro", "")
	if err == nil || !strings.Contains(err.Error(), "failed its gate from a clean checkout") {
		t.Fatalf("accept error = %v, want the clean-checkout failure", err)
	}
	// Nothing landed: HEAD is where the run began, and the diff is still in
	// the tree for the person to look at.
	if head := mustHead(t, g); head != baseline {
		t.Fatalf("HEAD = %s after a failed accept, want the baseline %s — the unreproducible commit stayed on the branch", head[:8], baseline[:8])
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(got) != "the run's change\n" {
		t.Fatalf("the run's diff was lost with the withdrawn commit: %q", got)
	}
	detail, err := s.RunGet(context.Background(), "r-red-repro")
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := false
	for _, e := range detail.Events {
		if e.Type == "commit_withdrawn" {
			withdrawn = true
		}
	}
	if !withdrawn {
		t.Error("the withdrawal is not on the record")
	}
	if !contains(detail.Run.Next, "accept") || !contains(detail.Run.Next, "reject") {
		t.Fatalf("next = %v; both decisions must stay open", detail.Run.Next)
	}

	// And reject now works: no foreign commit stands between HEAD and the
	// snapshot, so the tree restores to the run's start.
	if err := s.RunReject(context.Background(), "r-red-repro", "did not reproduce"); err != nil {
		t.Fatalf("reject after a withdrawn accept: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(got) != "original\n" {
		t.Errorf("reject did not restore the tree: %q", got)
	}
}

// The other way out: the person fixes the tree by hand and accepts again.
// The second accept is a NEW commit, verified again — green this time.
func TestAFixedTreeAcceptsAfterAWithdrawnCommit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("ignore local-only"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local-only"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"verify.mode": "tests", "verify.tests": "test -f local-only"}); err != nil {
		t.Fatal(err)
	}
	baseline := mustHead(t, g)
	run := &runlog.Run{
		ID: "r-fix-then-accept", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		TreeSnapshotHead: baseline, StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("the run's change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), "r-fix-then-accept", ""); err == nil {
		t.Fatal("first accept reproduced green from a checkout that lacks local-only")
	}
	// The fix by hand: stop ignoring the file the gate needs, so the commit
	// carries it.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), "r-fix-then-accept", ""); err != nil {
		t.Fatalf("second accept: %v", err)
	}
	detail, err := s.RunGet(context.Background(), "r-fix-then-accept")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Run.Accepted || detail.Run.CommitSHA == "" || detail.Run.CommitSHA == baseline {
		t.Fatalf("run = accepted %v sha %s", detail.Run.Accepted, detail.Run.CommitSHA)
	}
	if head := mustHead(t, g); head != detail.Run.CommitSHA {
		t.Errorf("HEAD %s is not the accepted commit %s", head[:8], detail.Run.CommitSHA[:8])
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
