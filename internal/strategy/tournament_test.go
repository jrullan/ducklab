package strategy

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
	"github.com/jrullan/ducklab/internal/vcs"
)

// fakeWorkspace records its lifecycle so leaks are detectable.
type fakeWorkspace struct {
	label  string
	patch  string
	closed *int32
}

func (w *fakeWorkspace) Root() string           { return "/fake/" + w.label }
func (w *fakeWorkspace) Patch() (string, error) { return w.patch, nil }
func (w *fakeWorkspace) Close() error           { atomic.AddInt32(w.closed, 1); return nil }

type tourFixture struct {
	closed   int32
	applied  []string
	gates    map[string]string // workspace root -> gate
	patches  []string
	judgeOut *agent.Choice
	started  int32
	peak     int32
	inFlight int32
}

func (f *tourFixture) params(t *testing.T) *TournamentParams {
	t.Helper()
	p := &TournamentParams{
		ExecuteParams: ExecuteParams{
			Prompt: "Task T-001: make TestAdd pass.",
			Roster: map[config.Role]config.DucklingID{
				config.RoleImplementer: "pato-local",
				config.RoleJudge:       "pato-nube",
			},
		},
		Contestants: len(f.patches),
		Ducklings:   []config.DucklingID{"pato-local", "pato-nube"},
		NewWorkspace: func(ctx context.Context, label string) (Workspace, error) {
			idx := int(label[1] - '0')
			return &fakeWorkspace{label: label, patch: f.patches[idx], closed: &f.closed}, nil
		},
		GateIn: func(ctx context.Context, root string) (string, string, error) {
			return f.gates[root], "", nil
		},
		Apply: func(patch string) error {
			f.applied = append(f.applied, patch)
			return nil
		},
	}
	p.Runner = func(ctx context.Context, turn *Turn, d config.DucklingID, prompt string, belt []string, tc TurnContext) (*agent.Outcome, error) {
		if turn.Role == config.RoleJudge {
			return &agent.Outcome{Text: "judged", Parsed: f.judgeOut}, nil
		}
		// Track concurrency for AC-19.
		cur := atomic.AddInt32(&f.inFlight, 1)
		for {
			peak := atomic.LoadInt32(&f.peak)
			if cur <= peak || atomic.CompareAndSwapInt32(&f.peak, peak, cur) {
				break
			}
		}
		atomic.AddInt32(&f.started, 1)
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&f.inFlight, -1)
		return &agent.Outcome{Text: "edited"}, nil
	}
	return p
}

// AC-19: contestants run concurrently, and every workspace is released.
func TestTournamentRunsContestantsConcurrentlyAndClosesWorkspaces(t *testing.T) {
	f := &tourFixture{
		patches: []string{"+a", "+b"},
		gates:   map[string]string{"/fake/c0": "green", "/fake/c1": "red"},
	}
	p := f.params(t)

	res, err := ExecuteTournament(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if f.peak < 2 {
		t.Errorf("peak concurrency = %d; contestants ran sequentially", f.peak)
	}
	if got := atomic.LoadInt32(&f.closed); got != 2 {
		t.Errorf("%d workspaces closed, want 2 — a leaked worktree breaks the next run", got)
	}
	if res.Resolution != ResolutionShortCircuit {
		t.Errorf("resolution = %q, want %q", res.Resolution, ResolutionShortCircuit)
	}
}

// AC-20 / I8: exactly one green candidate short-circuits and is applied
// byte-identically, with no judge call at all.
func TestSingleGreenShortCircuitsAndAppliesVerbatim(t *testing.T) {
	const winning = "--- a/add.go\n+++ b/add.go\n@@\n-return a - b\n+return a + b\n"
	f := &tourFixture{
		patches: []string{winning, "+broken"},
		gates:   map[string]string{"/fake/c0": "green", "/fake/c1": "red"},
		// If the judge is consulted the test fails on the resolution below.
		judgeOut: &agent.Choice{Choice: "B", Reason: "should never be used"},
	}
	res, err := ExecuteTournament(context.Background(), f.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution != ResolutionShortCircuit {
		t.Fatalf("resolution = %q, want short_circuit (the judge must not be consulted)", res.Resolution)
	}
	if len(f.applied) != 1 {
		t.Fatalf("applied %d patches, want 1", len(f.applied))
	}
	if f.applied[0] != winning {
		t.Errorf("applied patch is not byte-identical to the green candidate (I8):\ngot:  %q\nwant: %q",
			f.applied[0], winning)
	}
}

func TestSeveralGreenGoesToTheJudge(t *testing.T) {
	f := &tourFixture{
		patches:  []string{"+a", "+b"},
		gates:    map[string]string{"/fake/c0": "green", "/fake/c1": "green"},
		judgeOut: &agent.Choice{Choice: "A", Reason: "smaller change"},
	}
	res, err := ExecuteTournament(context.Background(), f.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution != ResolutionJudgePick {
		t.Errorf("resolution = %q, want judge_pick", res.Resolution)
	}
	if res.Winner != "A" {
		t.Errorf("winner = %q, want A", res.Winner)
	}
	if len(f.applied) != 1 {
		t.Errorf("applied %d patches, want 1", len(f.applied))
	}
}

// All red and the judge refuses: nothing is applied and the candidates are kept.
func TestAllRedAndJudgeRefusesAppliesNothing(t *testing.T) {
	f := &tourFixture{
		patches:  []string{"+a", "+b"},
		gates:    map[string]string{"/fake/c0": "red", "/fake/c1": "red"},
		judgeOut: &agent.Choice{Choice: "none", Reason: "both ignore the task"},
	}
	res, err := ExecuteTournament(context.Background(), f.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution != ResolutionNoWinner {
		t.Errorf("resolution = %q, want no_winner", res.Resolution)
	}
	if len(f.applied) != 0 {
		t.Errorf("applied %d patches despite no winner", len(f.applied))
	}
	if len(res.Candidates) != 2 {
		t.Errorf("candidates were discarded; they must be kept for inspection")
	}
}

func TestAllRedButJudgePicksIsRecordedDistinctly(t *testing.T) {
	f := &tourFixture{
		patches:  []string{"+a", "+b"},
		gates:    map[string]string{"/fake/c0": "red", "/fake/c1": "red"},
		judgeOut: &agent.Choice{Choice: "A", Reason: "closest to the task"},
	}
	res, err := ExecuteTournament(context.Background(), f.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution != ResolutionJudgePickRed {
		t.Errorf("resolution = %q, want judge_pick_red — picking a red candidate must not be reported as a clean win", res.Resolution)
	}
}

// A judge naming a candidate that is not on the table must not throw away a
// green solution.
func TestJudgeNamingUnavailableCandidateStillAppliesGreen(t *testing.T) {
	f := &tourFixture{
		patches:  []string{"+a", "+b"},
		gates:    map[string]string{"/fake/c0": "green", "/fake/c1": "green"},
		judgeOut: &agent.Choice{Choice: "Z", Reason: "confused"},
	}
	res, err := ExecuteTournament(context.Background(), f.params(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.applied) != 1 {
		t.Fatalf("a green candidate was discarded over a malformed judge choice")
	}
	if res.Resolution != ResolutionJudgePick {
		t.Errorf("resolution = %q", res.Resolution)
	}
}

// The judge's prompt must never name a duckling (I7).
func TestJudgePromptIsAnonymous(t *testing.T) {
	f := &tourFixture{
		patches:  []string{"+a", "+b"},
		gates:    map[string]string{"/fake/c0": "green", "/fake/c1": "green"},
		judgeOut: &agent.Choice{Choice: "A", Reason: "x"},
	}
	p := f.params(t)
	var judgePrompt string
	inner := p.Runner
	p.Runner = func(ctx context.Context, turn *Turn, d config.DucklingID, prompt string, belt []string, tc TurnContext) (*agent.Outcome, error) {
		if turn.Role == config.RoleJudge {
			judgePrompt = prompt
		}
		return inner(ctx, turn, d, prompt, belt, tc)
	}
	if _, err := ExecuteTournament(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if judgePrompt == "" {
		t.Fatal("judge never ran")
	}
	for _, id := range []string{"pato-local", "pato-nube"} {
		if strings.Contains(judgePrompt, id) {
			t.Errorf("judge prompt leaked duckling id %q:\n%s", id, judgePrompt)
		}
	}
	if !strings.Contains(judgePrompt, "Candidate A") {
		t.Errorf("judge prompt is missing anonymised labels:\n%s", judgePrompt)
	}
}

// One contestant failing must not sink the run if another produced a winner.
func TestOneContestantFailureDoesNotSinkTheRun(t *testing.T) {
	f := &tourFixture{
		patches: []string{"+a", "+b"},
		gates:   map[string]string{"/fake/c0": "green"},
	}
	p := f.params(t)
	p.NewWorkspace = func(ctx context.Context, label string) (Workspace, error) {
		if label == "c1" {
			return nil, context.DeadlineExceeded
		}
		return &fakeWorkspace{label: label, patch: f.patches[0], closed: &f.closed}, nil
	}
	res, err := ExecuteTournament(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution != ResolutionShortCircuit {
		t.Errorf("resolution = %q; a surviving green candidate should still win", res.Resolution)
	}
}

func TestTournamentRequiresWorkspaceAndApply(t *testing.T) {
	if _, err := ExecuteTournament(context.Background(), &TournamentParams{}); err == nil {
		t.Error("missing NewWorkspace accepted")
	}
	if _, err := ExecuteTournament(context.Background(), &TournamentParams{
		NewWorkspace: func(context.Context, string) (Workspace, error) { return nil, nil },
	}); err == nil {
		t.Error("missing Apply accepted")
	}
}

// --- real git worktrees (AC-19) ---------------------------------------------

func newGitRepo(t *testing.T) (*vcs.Git, string) {
	t.Helper()
	dir := t.TempDir()
	g := vcs.New(dir)
	if err := g.Init(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package x\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	g.AddAll()
	if _, err := g.Commit("init"); err != nil {
		t.Skipf("git commit unavailable: %v", err)
	}
	return g, dir
}

func TestGitWorkspaceIsolatesAndCapturesAPatch(t *testing.T) {
	g, dir := newGitRepo(t)
	scratch := t.TempDir()
	factory := NewGitWorkspaceFactory(dir, scratch, "r-test")

	ws, err := factory(context.Background(), "c0")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	defer ws.Close()

	// Edit inside the workspace only.
	target := filepath.Join(ws.Root(), "add.go")
	os.WriteFile(target, []byte("package x\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)

	patch, err := ws.Patch()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+func Add(a, b int) int { return a + b }") {
		t.Errorf("patch does not capture the edit:\n%s", patch)
	}
	// The main tree must be untouched: that is what isolation means.
	main, _ := os.ReadFile(filepath.Join(dir, "add.go"))
	if strings.Contains(string(main), "a + b") {
		t.Error("the contestant's edit leaked into the main working tree")
	}
	_ = g
}

func TestGitWorkspacesAreIndependent(t *testing.T) {
	_, dir := newGitRepo(t)
	scratch := t.TempDir()
	factory := NewGitWorkspaceFactory(dir, scratch, "r-test")

	var wss []Workspace
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, err := factory(context.Background(), string(rune('c'+i)))
			if err != nil {
				t.Errorf("workspace %d: %v", i, err)
				return
			}
			mu.Lock()
			wss = append(wss, ws)
			mu.Unlock()
			os.WriteFile(filepath.Join(ws.Root(), "add.go"),
				[]byte("package x\n\n// contestant "+string(rune('0'+i))+"\n"), 0o644)
		}(i)
	}
	wg.Wait()
	if len(wss) != 2 {
		t.Fatalf("created %d workspaces", len(wss))
	}
	defer func() {
		for _, ws := range wss {
			ws.Close()
		}
	}()

	p0, _ := wss[0].Patch()
	p1, _ := wss[1].Patch()
	if p0 == p1 {
		t.Error("two workspaces produced the same patch; they are not isolated")
	}
}

func TestReapWorktreesCleansUpAfterADeadEngine(t *testing.T) {
	g, dir := newGitRepo(t)
	scratch := t.TempDir()
	factory := NewGitWorkspaceFactory(dir, scratch, "r-dead")

	ws, err := factory(context.Background(), "c0")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	root := ws.Root()
	// Engine dies here: no Close is ever called.

	if err := ReapWorktrees(dir, scratch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("orphaned worktree directory survived the reaper: %s", root)
	}
	list, _ := g.WorktreeList()
	for _, w := range list {
		if strings.Contains(w, "r-dead") {
			t.Errorf("orphaned worktree record survived the reaper: %v", list)
		}
	}
}

func TestWorkspaceCloseIsIdempotent(t *testing.T) {
	_, dir := newGitRepo(t)
	scratch := t.TempDir()
	ws, err := NewGitWorkspaceFactory(dir, scratch, "r-idem")(context.Background(), "c0")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ws.Close()
	ws.Close() // must not panic or error differently
}

func TestAnonymiseCandidatesUsedByTournament(t *testing.T) {
	// Guards the coupling: tournament relies on conv for labelling.
	cands := conv.AnonymiseCandidates([]conv.Candidate{{Diff: "+x"}, {Diff: "+y"}})
	if cands[0].Label != "A" || cands[1].Label != "B" {
		t.Errorf("labels = %q, %q", cands[0].Label, cands[1].Label)
	}
}

// A contestant must work in its own worktree. The runner had no way to be told
// where that was, so every contestant edited the shared tree: their patches
// came back empty, the judge correctly found nothing to choose between, and
// the work still landed in the repository with no one having picked it.
//
// Measured on the first tournament this project ever ran: both candidate
// patches were zero bytes, the resolution was no_winner, and add.go in the
// main tree was fixed anyway.
func TestEachContestantWorksInItsOwnWorkspace(t *testing.T) {
	var mu sync.Mutex
	var roots []string

	p := &TournamentParams{
		ExecuteParams: ExecuteParams{Prompt: "fix it"},
		Contestants:   2,
		Ducklings:     []config.DucklingID{"pato-uno", "pato-dos"},
		NewWorkspace: func(_ context.Context, label string) (Workspace, error) {
			var closed int32
			return &fakeWorkspace{label: label, patch: "--- a\n+++ b\n@@ -1 +1 @@\n+fixed\n", closed: &closed}, nil
		},
		GateIn: func(context.Context, string) (string, string, error) { return "green", "", nil },
		Apply:  func(string) error { return nil },
	}
	p.Runner = func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, tc TurnContext) (*agent.Outcome, error) {
		mu.Lock()
		roots = append(roots, tc.Root)
		mu.Unlock()
		if turn.Role == config.RoleJudge {
			return &agent.Outcome{Parsed: &agent.Choice{Choice: "A", Reason: "first"}}, nil
		}
		return &agent.Outcome{Text: "done"}, nil
	}

	if _, err := ExecuteTournament(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	sort.Strings(roots)
	// Two contestants and a judge. The judge arbitrates over patches and has
	// no tree of its own.
	var contestantRoots []string
	for _, r := range roots {
		if r != "" {
			contestantRoots = append(contestantRoots, r)
		}
	}
	if len(contestantRoots) != 2 {
		t.Fatalf("contestants given a workspace: %d, want 2 (roots=%q)", len(contestantRoots), roots)
	}
	if contestantRoots[0] == contestantRoots[1] {
		t.Errorf("both contestants were pointed at %q; they must not share a tree", contestantRoots[0])
	}
}
