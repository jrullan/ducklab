package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// preFixture is a project with a counting gate, and a build run paused at its
// gate in an isolated worktree whose candidate makes the gate pass.
type preFixture struct {
	s       *Service
	id      string
	root    string
	git     *vcs.Git
	work    *vcs.Git
	rs      *runState
	counter string
}

func newPreFixture(t *testing.T, gate string) *preFixture {
	t.Helper()
	s := serviceWithDucklings(t, "pato-uno")
	projectID, root := projectWithDocs(t, s, nil)
	counter := filepath.Join(t.TempDir(), "reproductions")
	appendVerifyPreparation(t, root, `mode = "custom"
custom = "echo ran >> `+counter+`; `+gate+`"`)
	g := gitProject(t, root)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := g.WorktreeAdd(wt, "ducklab/T-001"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-pre", ProjectID: projectID, TaskID: "T-001", Stage: "build", Status: "paused",
		PendingKind: "gate", Verdict: "PASSED", WorktreePath: wt, StartedAt: "2026-10-29T01:00:00Z",
	}
	w, err := runlog.NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	rs := &runState{run: run, writer: w, runDir: w.RunDir(), projectPath: root}
	s.runsMu.Lock()
	s.runs[run.ID] = rs
	s.runsMu.Unlock()
	return &preFixture{s: s, id: projectID, root: root, git: g, work: vcs.New(wt), rs: rs, counter: counter}
}

func (f *preFixture) reproductions() int {
	b, _ := os.ReadFile(f.counter)
	return strings.Count(string(b), "ran")
}

// start begins the pre-reproduction and waits for it to finish.
func (f *preFixture) start(t *testing.T) {
	t.Helper()
	entry, err := f.s.registry.Get(f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.s.startPreReproduction(f.rs, entry)
	f.s.preRepro.mu.Lock()
	pre := f.s.preRepro.runs[f.rs.run.ID]
	f.s.preRepro.mu.Unlock()
	if pre == nil {
		t.Fatal("no pre-reproduction started")
	}
	select {
	case <-pre.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the pre-reproduction did not finish")
	}
}

// accept makes the commit accept makes: the worktree staged as stageRun
// stages it.
func (f *preFixture) accept(t *testing.T) string {
	t.Helper()
	if err := f.work.AddAllExcluding(landingExclusions(f.work.Root, f.root)...); err != nil {
		t.Fatal(err)
	}
	sha, err := f.work.Commit("accept")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// B-545: accept's clean-checkout reproduction ran after the decision — on
// T-020 another full minute. It now runs while the gate waits, and accept
// reuses it for the exact tree it commits: the gate runs once, not twice.
func TestAcceptReusesTheReproductionRunWhileTheGateWaited(t *testing.T) {
	f := newPreFixture(t, "test -f feature.txt")
	f.start(t)
	if f.reproductions() != 1 {
		t.Fatalf("the pre-reproduction ran the gate %d times", f.reproductions())
	}
	sha := f.accept(t)
	repro, ok := f.s.preReproduced(context.Background(), f.rs.run.ID, f.git, f.root, sha)
	if !ok || !repro.Green || !strings.Contains(repro.Output, "[reproduced from a clean checkout of this exact tree") {
		t.Fatalf("accept did not reuse the green pre-reproduction: ok=%v %+v", ok, repro)
	}
	if f.reproductions() != 1 {
		t.Errorf("accept ran the gate again (%d runs)", f.reproductions())
	}
}

// Anything but the exact tree, green, under the same gate config, and accept
// reproduces as before.
func TestAPreReproductionIsReusedOnlyForWhatItProved(t *testing.T) {
	ctx := context.Background()

	changed := newPreFixture(t, "test -f feature.txt")
	changed.start(t)
	if err := os.WriteFile(filepath.Join(changed.work.Root, "later.txt"), []byte("edited at the gate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := changed.s.preReproduced(ctx, changed.rs.run.ID, changed.git, changed.root, changed.accept(t)); ok {
		t.Error("a reproduction of a different tree was reused")
	}

	red := newPreFixture(t, "false")
	red.start(t)
	if _, ok := red.s.preReproduced(ctx, red.rs.run.ID, red.git, red.root, red.accept(t)); ok {
		t.Error("a red reproduction was reused")
	}

	reconfigured := newPreFixture(t, "test -f feature.txt")
	reconfigured.start(t)
	appendVerifyPreparation(t, reconfigured.root, `custom = "test -f feature.txt && test -f README.md"`)
	if _, ok := reconfigured.s.preReproduced(ctx, reconfigured.rs.run.ID, reconfigured.git, reconfigured.root, reconfigured.accept(t)); ok {
		t.Error("a reproduction under another gate config was reused")
	}

	old := newPreFixture(t, "test -f feature.txt")
	old.start(t)
	old.s.preRepro.mu.Lock()
	old.s.preRepro.runs[old.rs.run.ID].at = time.Now().Add(-preReproductionMaxAge - time.Minute)
	old.s.preRepro.mu.Unlock()
	if _, ok := old.s.preReproduced(ctx, old.rs.run.ID, old.git, old.root, old.accept(t)); ok {
		t.Error("a stale reproduction was reused")
	}
}

// Leaving the gate any other way ends it: a resumed run must not have a
// clean-checkout gate running beside its own.
func TestLeavingTheGateDropsThePreReproduction(t *testing.T) {
	f := newPreFixture(t, "test -f feature.txt")
	f.start(t)
	f.s.dropPreReproduction(f.rs.run.ID)
	if _, ok := f.s.preReproduced(context.Background(), f.rs.run.ID, f.git, f.root, f.accept(t)); ok {
		t.Error("a dropped pre-reproduction was reused")
	}
}

// Accept would rebase onto a main that moved: the tree it lands is not the
// worktree's, so nothing is pre-reproduced.
func TestNoPreReproductionWhenMainMoved(t *testing.T) {
	f := newPreFixture(t, "test -f feature.txt")
	if err := os.WriteFile(filepath.Join(f.root, "upstream.txt"), []byte("landed elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.git.AddPaths("upstream.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.git.Commit("upstream"); err != nil {
		t.Fatal(err)
	}
	entry, _ := f.s.registry.Get(f.id)
	f.s.startPreReproduction(f.rs, entry)
	f.s.preRepro.mu.Lock()
	pre := f.s.preRepro.runs[f.rs.run.ID]
	f.s.preRepro.mu.Unlock()
	if pre != nil {
		t.Fatal("a pre-reproduction started although accept will rebase")
	}
}
