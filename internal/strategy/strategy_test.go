package strategy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
)

// fakeSource returns queued replies, satisfying source.Client without a network.
type fakeSource struct {
	name       string
	replies    []string
	i          int
	lastPrompt string
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Complete(_ context.Context, msgs []source.Message, _ source.Options) (source.Result, error) {
	if len(msgs) > 0 {
		f.lastPrompt = msgs[len(msgs)-1].Content
	}
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
		Repo: repo, Gate: prim.GateFromCmd(testCmd),
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
	if out.Resolution != "solo" {
		t.Errorf("resolution = %q", out.Resolution)
	}
	// Collaborative/baseline modes never crown a "winner".
	if out.Winner != "" {
		t.Errorf("solo should not set a Winner, got %q", out.Winner)
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

func TestSoloUnverified(t *testing.T) {
	repo := gitRepo(t)
	solver := &fakeSource{name: "solo", replies: []string{"=== FILE: sol.txt ===\nsolved\n"}}
	// empty gate command -> no automated verification
	env := newEnv(t, repo, "", solver, solver, solver)
	out, err := Solo{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "UNVERIFIED" {
		t.Fatalf("no-gate solo should be UNVERIFIED, got %+v", out)
	}
	if out.TestsPass {
		t.Error("UNVERIFIED must not claim tests passed")
	}
}

func TestDriverUnverified(t *testing.T) {
	repo := gitRepo(t)
	driver := &fakeSource{name: "drv", replies: []string{
		"=== FILE: main.txt ===\nfixed\n",
	}}
	observer := &fakeSource{name: "obs", replies: []string{
		"Analysis: the change is coherent with the task and touches only what was asked.\n" +
			"Tests: none available; judged by inspection.\nVerdict: APPROVED",
	}}
	env := newEnv(t, repo, "", driver, observer, observer) // B = observer
	out, err := Driver{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "UNVERIFIED" {
		t.Fatalf("no-gate driver approval should be UNVERIFIED, got %+v", out)
	}
}

func TestDriverApproved(t *testing.T) {
	repo := gitRepo(t)
	driver := &fakeSource{name: "drv", replies: []string{
		"=== FILE: main.txt ===\nfixed\n",
	}}
	observer := &fakeSource{name: "obs", replies: []string{
		"Analysis: the change replaces base with fixed and matches the task fully.\n" +
			"Tests: they pass and cover the change.\nVerdict: APPROVED",
	}}
	env := newEnv(t, repo, "true", driver, observer, observer) // B = observer
	out, err := Driver{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "HUMAN_GATE" || out.Resolution != "approved_round_1" {
		t.Fatalf("driver outcome = %+v", out)
	}
	// Regression: the observer must be shown a NON-empty diff (against base, not
	// the scratch branch). Its prompt must contain the driver's actual change.
	if !contains(observer.lastPrompt, "fixed") {
		t.Errorf("observer prompt lacked the change diff (self-diff bug?):\n%s", observer.lastPrompt)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

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

func TestPlanExecutedGreen(t *testing.T) {
	repo := gitRepo(t)
	// Round 1 planner hands off a plan; reviewer observes; round 2 planner
	// stands by the plan (ends planning); then executes with SEARCH/REPLACE.
	planner := &fakeSource{name: "A", replies: []string{
		"A→B: here is my plan.\n1. Change base to fixed in main.txt\nThoughts?",
		"B, I appreciate the notes but I'm keeping my plan because it already meets the requirement.",
		"=== FILE: main.txt ===\nfixed\n",
	}}
	reviewer := &fakeSource{name: "B", replies: []string{
		"A, the plan looks solid. One observation: confirm the exact string. I think it's ready to execute.",
		"1. Plan vs execution: all steps done.\n2. Requirement: satisfied.\n3. Verdict: APPROVED",
	}}
	env := newEnv(t, repo, "true", planner, reviewer, reviewer) // B = reviewer
	out, err := Plan{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "HUMAN_GATE" || out.Resolution != "plan_executed" {
		t.Fatalf("plan outcome = %+v", out)
	}
	if out.Winner != "" {
		t.Errorf("plan is collaborative — no winner, got %q", out.Winner)
	}
}

func TestPlanUnverified(t *testing.T) {
	repo := gitRepo(t)
	planner := &fakeSource{name: "A", replies: []string{
		"A→B plan.\n1. Change base to fixed",
		"I'm keeping my plan because it is correct.",
		"=== FILE: main.txt ===\nfixed\n",
	}}
	reviewer := &fakeSource{name: "B", replies: []string{
		"A, the plan is promising. I think it's ready to execute.",
		"Plan vs execution: complete. Requirement: met. Verdict: APPROVED",
	}}
	env := newEnv(t, repo, "", planner, reviewer, reviewer) // B = reviewer, no gate
	out, err := Plan{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "UNVERIFIED" {
		t.Fatalf("no-gate plan should be UNVERIFIED, got %+v", out)
	}
}

func TestPlanExecuteRetriesUntilComplete(t *testing.T) {
	repo := gitRepo(t)
	// Planning ends fast (stand by), then execution is INCOMPLETE on round 1 and
	// completed on round 2 after the reviewer's feedback.
	planner := &fakeSource{name: "A", replies: []string{
		"A→B plan.\n1. do X\n2. do Y",
		"I'm keeping my plan because it is correct.",
		"=== FILE: main.txt ===\npartial\n",  // round 1 execution (incomplete)
		"=== FILE: main.txt ===\ncomplete\n", // round 2 execution (fixed)
	}}
	reviewer := &fakeSource{name: "B", replies: []string{
		"A, the plan is ready to execute.",                                         // planning review
		"1. Plan vs execution: step 2 (do Y) was NOT done. ISSUES: finish step 2.", // round 1 check → reject
		"1. Plan vs execution: all steps done.\nVerdict: APPROVED",                 // round 2 check → approve
	}}
	env := newEnv(t, repo, "", planner, reviewer, reviewer) // no gate
	out, err := Plan{}.Run(env)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "UNVERIFIED" {
		t.Fatalf("expected UNVERIFIED after retry completes the plan, got %+v", out)
	}
	// two execution attempts happened (reviewer approved only the second)
	if planner.i != 4 {
		t.Errorf("expected 4 planner calls (2 handoff + 2 execute), got %d", planner.i)
	}
}

func TestDriverRespectsMaxRounds(t *testing.T) {
	repo := gitRepo(t)
	driver := &fakeSource{name: "drv", replies: []string{
		"=== FILE: main.txt ===\n```search\nbase\n```\n```replace\nfixed\n```\n",
	}}
	// observer never approves → the loop is bounded only by MaxRounds
	observer := &fakeSource{name: "obs", replies: []string{
		"Analysis: still not quite right.\nTests: n/a.\nVerdict: CORRECTIONS: keep going",
	}}
	env := newEnv(t, repo, "", driver, observer, observer)
	env.MaxRounds = 1 // one round only
	out, _ := Driver{}.Run(env)
	if out.State != "ESCALATED" {
		t.Fatalf("expected escalation after 1 round, got %+v", out)
	}
	// exactly one DRIVE and one OBSERVE call happened
	if driver.i != 1 || observer.i != 1 {
		t.Errorf("MaxRounds=1 should be 1 drive + 1 observe, got drive=%d observe=%d", driver.i, observer.i)
	}
}

func TestEnvRoundsDefault(t *testing.T) {
	if (Env{}).rounds() != DefaultRounds {
		t.Errorf("unset MaxRounds should default to %d", DefaultRounds)
	}
	if (Env{MaxRounds: 9}).rounds() != 9 {
		t.Error("MaxRounds should override the default")
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
