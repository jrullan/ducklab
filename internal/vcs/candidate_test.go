package vcs

import (
	"os"
	"path/filepath"
	"testing"
)

// B-545: the accept's clean-checkout reproduction can run while the gate
// waits only if the commit accept will make is known beforehand. The
// candidate is that commit — built without touching the real index, the
// working tree or a ref — and the real accept's commit has its tree.
func TestACandidateCommitPredictsTheAcceptsTree(t *testing.T) {
	g, dir := newRepo(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "changed\n")
	write("src/app.js", "new file\n")
	write("node_modules/dep/index.js", "a dependency\n") // excluded, as linked deps are
	head, _ := g.HeadSHA()
	stagedBefore, _ := g.StagedPaths()

	sha, tree, err := g.CandidateCommit("ducklab-pre-reproduction", "node_modules")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := g.TreeOf(sha); got != tree {
		t.Fatalf("candidate %s has tree %s, reported %s", sha, got, tree)
	}
	// Nothing moved: not HEAD, not the index, not the files.
	if now, _ := g.HeadSHA(); now != head {
		t.Fatalf("HEAD moved to %s", now)
	}
	if stagedAfter, _ := g.StagedPaths(); len(stagedAfter) != len(stagedBefore) {
		t.Fatalf("the real index changed: %v → %v", stagedBefore, stagedAfter)
	}

	// The real accept: stage as accept stages, then commit.
	if err := g.AddAllExcluding("node_modules"); err != nil {
		t.Fatal(err)
	}
	real, err := g.Commit("accept")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := g.TreeOf(real); got != tree {
		t.Fatalf("the accept committed tree %s; the candidate predicted %s", got, tree)
	}
}
