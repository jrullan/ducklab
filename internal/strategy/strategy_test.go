package strategy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
)

// fakeSource returns queued replies, satisfying source.Client without a network.
type fakeSource struct {
	name    string
	replies []string
	i       int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Complete(_ context.Context, _ []source.Message, _ source.Options) (source.Result, error) {
	r := ""
	if f.i < len(f.replies) {
		r = f.replies[f.i]
	} else if len(f.replies) > 0 {
		r = f.replies[len(f.replies)-1]
	}
	f.i++
	return source.Result{Content: r}, nil
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sh := func(c string) {
		if ok, out := prim.Shell(c, dir); !ok {
			t.Fatalf("%s: %s", c, out)
		}
	}
	sh("git init -q && git config user.email t@t.local && git config user.name tester")
	os.WriteFile(filepath.Join(dir, "main.txt"), []byte("base\n"), 0o644)
	sh("git add -A && git commit -q -m base")
	return dir
}

func newEnv(t *testing.T, repo, testCmd string, a, b, judge source.Client) Env {
	t.Helper()
	r, err := run.Open(filepath.Join(repo, "runs", "t"))
	if err != nil {
		t.Fatal(err)
	}
	return Env{
		Ctx: context.Background(), TaskID: "t", Requirement: "do the thing",
		Repo: repo, TestCmd: testCmd,
		Contestants: []source.Client{a, b}, Judge: judge, Run: r,
	}
}

func TestSoloGreen(t *testing.T) {
	repo := gitRepo(t)
	solver := &fakeSource{name: "solo", replies: []string{"=== FILE: sol.txt ===\nsolved\n"}}
	env := newEnv(t, repo, "true", solver, solver, solver)
	out, err := Solo{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "HUMAN_GATE" || !out.TestsPass {
		t.Fatalf("solo green outcome = %+v", out)
	}
	if out.Winner != "solo" {
		t.Errorf("winner = %q", out.Winner)
	}
}

func TestSoloRed(t *testing.T) {
	repo := gitRepo(t)
	solver := &fakeSource{name: "solo", replies: []string{"=== FILE: sol.txt ===\nsolved\n"}}
	env := newEnv(t, repo, "false", solver, solver, solver)
	out, _ := Solo{}.Run(env)
	if out.State != "ESCALATED" {
		t.Fatalf("solo red should escalate, got %+v", out)
	}
}

func TestDriverApproved(t *testing.T) {
	repo := gitRepo(t)
	driver := &fakeSource{name: "drv", replies: []string{
		"=== FILE: main.txt ===\n<<< SEARCH\nbase\n===\nfixed\n>>> REPLACE\n",
	}}
	observer := &fakeSource{name: "obs", replies: []string{
		"Analysis: the change replaces base with fixed and matches the task fully.\n" +
			"Tests: they pass and cover the change.\nVerdict: APPROVED",
	}}
	env := newEnv(t, repo, "true", driver, driver, observer)
	out, err := Driver{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "HUMAN_GATE" || out.Resolution != "approved_round_1" {
		t.Fatalf("driver outcome = %+v", out)
	}
}

func TestTournamentShortCircuit(t *testing.T) {
	repo := gitRepo(t)
	a := &fakeSource{name: "aa", replies: []string{"=== FILE: sol.txt ===\nfrom-a\n"}}
	b := &fakeSource{name: "bb", replies: []string{"=== FILE: sol.txt ===\nfrom-b\n"}}
	judge := &fakeSource{name: "jj", replies: []string{"Both fine.\n### DECISION: A"}}
	env := newEnv(t, repo, "true", a, b, judge)
	out, err := Tournament{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "HUMAN_GATE" || out.Resolution != "short_circuit" {
		t.Fatalf("tournament outcome = %+v", out)
	}
	if out.Winner != "aa" {
		t.Errorf("winner = %q, want aa", out.Winner)
	}
}

func TestTournamentJudgeOverride(t *testing.T) {
	repo := gitRepo(t)
	// A wins the vote but carries a blocking finding; B is clean+green -> override to B.
	a := &fakeSource{name: "aa", replies: []string{"=== FILE: sol.txt ===\nfrom-a\n"}}
	b := &fakeSource{name: "bb", replies: []string{"=== FILE: sol.txt ===\nfrom-b\n"}}
	judge := &fakeSource{name: "jj", replies: []string{
		"### SOLUTION A\nBLOCKING FINDING: deletes tests\n### SOLUTION B\nclean\nDECISION: A",
	}}
	env := newEnv(t, repo, "true", a, b, judge)
	out, err := Tournament{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.Winner != "bb" {
		t.Fatalf("expected override to bb, got %+v", out)
	}
}

func TestDirtyGuard(t *testing.T) {
	repo := gitRepo(t)
	os.WriteFile(filepath.Join(repo, "main.txt"), []byte("dirty\n"), 0o644)
	solver := &fakeSource{name: "solo", replies: []string{"=== FILE: sol.txt ===\nx\n"}}
	env := newEnv(t, repo, "true", solver, solver, solver)
	if _, err := (Solo{}).Run(env); err == nil {
		t.Fatal("expected dirty-tree guard to error")
	}
}
