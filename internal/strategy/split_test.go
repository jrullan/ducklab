package strategy

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

func decomp(subtasks ...agent.Subtask) *agent.Decomposition {
	return &agent.Decomposition{Subtasks: subtasks}
}

func TestOwnershipAcceptsDisjointFiles(t *testing.T) {
	got, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "api", Files: []string{"src/api.go", "src/api_test.go"}},
		agent.Subtask{Title: "store", Files: []string{"src/store.go"}},
	))
	if err != nil {
		t.Fatalf("a disjoint decomposition was rejected: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 2 || got[1][0] != "src/store.go" {
		t.Errorf("ownership = %v", got)
	}
}

// The whole mode rests on this. Phase 4 copies each subtask's files out of its
// worktree, which is only safe because no two subtasks touched the same file.
func TestOwnershipRejectsATwiceClaimedFile(t *testing.T) {
	_, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "api", Files: []string{"src/api.go", "src/shared.go"}},
		agent.Subtask{Title: "store", Files: []string{"src/shared.go"}},
	))
	if err == nil {
		t.Fatal("two subtasks were allowed to claim the same file")
	}
	// The architect gets one retry, which is only useful if it is told what
	// was wrong and by whom.
	for _, want := range []string{"src/shared.go", "api", "store"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict does not name %q: %v", want, err)
		}
	}
}

// The same file spelled two ways is still the same file.
func TestOwnershipSeesThroughPathSpelling(t *testing.T) {
	_, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "a", Files: []string{"src/api.go"}},
		agent.Subtask{Title: "b", Files: []string{"./src/../src/api.go"}},
	))
	if err == nil {
		t.Error("the same file written two ways was treated as two files")
	}
}

// Checked here rather than trusted to the tool path jail: phase 4 copies these
// paths afterwards, and a copy driven by an unchecked "../" escapes long after
// any tool had a say.
func TestOwnershipRefusesPathsOutsideTheRepo(t *testing.T) {
	for _, bad := range []string{"../secrets.env", "/etc/passwd", "src/../../x.go", ".", ""} {
		if _, err := ValidateOwnership(decomp(
			agent.Subtask{Title: "a", Files: []string{bad}},
			agent.Subtask{Title: "b", Files: []string{"ok.go"}},
		)); err == nil {
			t.Errorf("%q was accepted as an owned file", bad)
		}
	}
}

func TestOwnershipRejectsAnEmptyDecomposition(t *testing.T) {
	if _, err := ValidateOwnership(nil); err == nil {
		t.Error("a nil decomposition was accepted")
	}
	if _, err := ValidateOwnership(decomp()); err == nil {
		t.Error("a decomposition with no subtasks was accepted")
	}
}

// Integration is a copy, not a merge, and that is the entire point of the
// mode: a weak model asked to reconcile whole files destroys code that was
// working. Disjoint ownership turns the reconciliation into a file copy that
// no model takes part in.
func TestIntegrateCopiesFromTheOwningWorkspaceOnly(t *testing.T) {
	var moves []string
	copier := func(from, to string) (bool, error) {
		moves = append(moves, from+" -> "+to)
		return true, nil
	}
	written, err := Integrate("/repo",
		[][]string{{"src/api.go"}, {"src/store.go"}},
		[]string{"/ws/a", "/ws/b"},
		copier,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Errorf("written = %v", written)
	}
	want := []string{
		"/ws/a/src/api.go -> /repo/src/api.go",
		"/ws/b/src/store.go -> /repo/src/store.go",
	}
	for i, w := range want {
		if moves[i] != w {
			t.Errorf("copy %d = %q, want %q", i, moves[i], w)
		}
	}
}

// Deciding a file was unnecessary is a legitimate outcome. Treating its
// absence as an error would fail a run that succeeded, and deleting the
// target copy would silently discard whatever was there before.
func TestIntegrateToleratesAFileNeverCreated(t *testing.T) {
	copier := func(from, to string) (bool, error) {
		return !strings.HasSuffix(from, "unused.go"), nil
	}
	written, err := Integrate("/repo",
		[][]string{{"src/api.go", "src/unused.go"}},
		[]string{"/ws/a"},
		copier,
	)
	if err != nil {
		t.Fatalf("a claimed-but-uncreated file failed the integration: %v", err)
	}
	if len(written) != 1 || written[0] != "src/api.go" {
		t.Errorf("written = %v, want only the file that exists", written)
	}
}

// A mismatch means the caller lost track of which workspace belongs to which
// subtask, and copying anything at that point would put a subtask's work under
// another's name.
func TestIntegrateRefusesMismatchedWorkspaces(t *testing.T) {
	_, err := Integrate("/repo", [][]string{{"a.go"}, {"b.go"}}, []string{"/ws/a"},
		func(string, string) (bool, error) { return true, nil })
	if err == nil {
		t.Error("integration ran with fewer workspaces than ownership lists")
	}
}

// splitFixture drives ExecuteSplit without models or a filesystem.
type splitFixture struct {
	decompositions []*agent.Decomposition // one per architect attempt
	architectCalls int
	subtaskRoots   []string
	copied         []string
	gates          []string // consumed in order
	gateCalls      int
	seamPrompts    []string
	mu             sync.Mutex
}

func (f *splitFixture) params() *SplitParams {
	p := &SplitParams{
		ExecuteParams: ExecuteParams{ProjectRoot: "/repo", Prompt: "build the thing"},
		NewWorkspace: func(_ context.Context, label string) (Workspace, error) {
			var closed int32
			return &fakeWorkspace{label: label, patch: "", closed: &closed}, nil
		},
		GateIn: func(context.Context, string) (string, string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			g := "green"
			if f.gateCalls < len(f.gates) {
				g = f.gates[f.gateCalls]
			}
			f.gateCalls++
			return g, "", nil
		},
		CopyFile: func(from, to string) (bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.copied = append(f.copied, from+" -> "+to)
			return true, nil
		},
	}
	p.Runner = func(_ context.Context, turn *Turn, _ config.DucklingID, prompt string, _ []string, tc TurnContext) (*agent.Outcome, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch turn.Role {
		case config.RoleArchitect:
			i := f.architectCalls
			f.architectCalls++
			if i < len(f.decompositions) {
				return &agent.Outcome{Parsed: f.decompositions[i]}, nil
			}
			return &agent.Outcome{Parsed: f.decompositions[len(f.decompositions)-1]}, nil
		case config.RoleImplementer:
			if tc.Root != "" {
				f.subtaskRoots = append(f.subtaskRoots, tc.Root)
			} else {
				f.seamPrompts = append(f.seamPrompts, prompt)
			}
			return &agent.Outcome{Text: "done"}, nil
		default:
			return &agent.Outcome{Parsed: &agent.Verdict{Verdict: "approve"}}, nil
		}
	}
	return p
}

var goodDecomp = &agent.Decomposition{Subtasks: []agent.Subtask{
	{Title: "api", Files: []string{"src/api.go"}},
	{Title: "store", Files: []string{"src/store.go"}},
}}

func TestSplitRunsEachSubtaskInItsOwnTreeThenIntegrates(t *testing.T) {
	f := &splitFixture{decompositions: []*agent.Decomposition{goodDecomp}}
	res, err := ExecuteSplit(context.Background(), f.params())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.subtaskRoots) != 2 || f.subtaskRoots[0] == f.subtaskRoots[1] {
		t.Errorf("subtask roots = %v; each piece needs its own tree", f.subtaskRoots)
	}
	if len(res.Integrated) != 2 {
		t.Errorf("integrated = %v, want both owned files", res.Integrated)
	}
	if res.Gate != "green" {
		t.Errorf("gate = %q", res.Gate)
	}
}

// The architect gets exactly one more attempt, and it must be told what
// clashed or the retry is a coin flip.
func TestSplitRetriesTheArchitectOnceWithTheConflictNamed(t *testing.T) {
	clashing := &agent.Decomposition{Subtasks: []agent.Subtask{
		{Title: "api", Files: []string{"src/shared.go"}},
		{Title: "store", Files: []string{"src/shared.go"}},
	}}
	f := &splitFixture{decompositions: []*agent.Decomposition{clashing, goodDecomp}}
	res, err := ExecuteSplit(context.Background(), f.params())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Retried {
		t.Error("the run does not record that the first decomposition was refused")
	}
	if f.architectCalls != 2 {
		t.Errorf("architect called %d times, want 2", f.architectCalls)
	}
}

// Refusing beats degrading: a task that will not decompose is a task for
// another mode, not a merge handed to a model.
func TestSplitRefusesRatherThanMergingWithAModel(t *testing.T) {
	clashing := &agent.Decomposition{Subtasks: []agent.Subtask{
		{Title: "api", Files: []string{"src/shared.go"}},
		{Title: "store", Files: []string{"src/shared.go"}},
	}}
	f := &splitFixture{decompositions: []*agent.Decomposition{clashing, clashing}}
	_, err := ExecuteSplit(context.Background(), f.params())
	if err == nil {
		t.Fatal("two clashing decompositions were accepted")
	}
	if !strings.Contains(err.Error(), "src/shared.go") {
		t.Errorf("the refusal does not name the clash: %v", err)
	}
	if len(f.copied) != 0 {
		t.Error("files were integrated despite the refusal")
	}
}

// Each piece verified alone, so a red integration is a seam. The pair is
// pointed at the seam rather than told to start over.
func TestSplitFixesSeamsWhenTheIntegrationIsRed(t *testing.T) {
	f := &splitFixture{
		decompositions: []*agent.Decomposition{goodDecomp},
		gates:          []string{"red", "green"},
	}
	res, err := ExecuteSplit(context.Background(), f.params())
	if err != nil {
		t.Fatal(err)
	}
	if res.SeamRoundsUsed != 1 {
		t.Errorf("seam rounds = %d, want 1", res.SeamRoundsUsed)
	}
	if res.Gate != "green" {
		t.Errorf("gate = %q after the seam round", res.Gate)
	}
	if len(f.seamPrompts) == 0 {
		t.Fatal("no seam round ran")
	}
	if !strings.Contains(f.seamPrompts[0], "src/api.go") ||
		!strings.Contains(f.seamPrompts[0], "Do not reimplement") {
		t.Errorf("the seam prompt does not point at the integration: %q", f.seamPrompts[0])
	}
}

// A red tree that stays red must stop, not grind.
func TestSplitStopsAfterItsSeamRounds(t *testing.T) {
	f := &splitFixture{
		decompositions: []*agent.Decomposition{goodDecomp},
		gates:          []string{"red", "red", "red", "red", "red"},
	}
	res, err := ExecuteSplit(context.Background(), f.params())
	if err != nil {
		t.Fatal(err)
	}
	if res.SeamRoundsUsed != DefaultSeamRounds {
		t.Errorf("seam rounds = %d, want %d", res.SeamRoundsUsed, DefaultSeamRounds)
	}
	if res.Gate != "red" {
		t.Errorf("gate = %q; a run that never went green must say so", res.Gate)
	}
}

// A run that stops to ask a person has not failed; it is waiting. The first
// real split run was marked FAILED because its architect asked a question, and
// the outcome carrying that question was dropped on the way out.
func TestSplitCarriesOutTheQuestionItStoppedOn(t *testing.T) {
	pending := &tools.PendingQuestion{ID: "q1", Question: "Which package?"}
	p := (&splitFixture{decompositions: []*agent.Decomposition{goodDecomp}}).params()
	p.Runner = func(_ context.Context, turn *Turn, _ config.DucklingID, _ string, _ []string, _ TurnContext) (*agent.Outcome, error) {
		if turn.Role == config.RoleArchitect {
			return &agent.Outcome{Pending: pending}, tools.ErrHumanNeeded
		}
		return &agent.Outcome{Text: "done"}, nil
	}

	res, err := ExecuteSplit(context.Background(), p)
	if err == nil {
		t.Fatal("the run continued past a question")
	}
	if res.Outcome == nil || res.Outcome.Pending == nil {
		t.Fatal("the question was dropped; the caller cannot turn it into a pause")
	}
	if res.Outcome.Pending.Question != "Which package?" {
		t.Errorf("question = %q", res.Outcome.Pending.Question)
	}
}

// A contract name is not an instruction: the model never sees it. The first
// real split run failed because the architect was handed the task prompt, a
// read-only toolbelt and a json:decomposition contract it had never been told
// about — it tried to do the work itself and ran out of turns.
func TestTheArchitectIsToldToDecompose(t *testing.T) {
	got := decomposePrompt("Add a math helper and a string helper.")
	for _, want := range []string{
		"Add a math helper", // the task survives
		"Do NOT implement",  // and is not what it should do
		"subtasks",
		"same file",    // the constraint that makes integration safe
		`{"subtasks":`, // the shape to answer in
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the decomposition prompt never mentions %q", want)
		}
	}
}

// Split exists for work beyond one model's reach (05 §4.5), so a person who
// knows which piece is the hard one should be able to put the stronger duckling
// on it. Fewer names than subtasks is not an error: the list runs out and the
// roster covers the rest.
func TestSubtasksCanGoToDifferentDucklings(t *testing.T) {
	var mu sync.Mutex
	assigned := map[string]config.DucklingID{}

	p := (&splitFixture{decompositions: []*agent.Decomposition{goodDecomp}}).params()
	p.Ducklings = []config.DucklingID{"pato-fuerte"}
	p.Roster = map[config.Role]config.DucklingID{config.RoleImplementer: "pato-normal"}
	p.Runner = func(_ context.Context, turn *Turn, d config.DucklingID, _ string, _ []string, tc TurnContext) (*agent.Outcome, error) {
		if turn.Role == config.RoleImplementer && tc.Root != "" {
			mu.Lock()
			assigned[tc.Root] = d
			mu.Unlock()
		}
		if turn.Role == config.RoleArchitect {
			return &agent.Outcome{Parsed: goodDecomp}, nil
		}
		return &agent.Outcome{Text: "done"}, nil
	}

	if _, err := ExecuteSplit(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var got []config.DucklingID
	for _, d := range assigned {
		got = append(got, d)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := []config.DucklingID{"pato-fuerte", "pato-normal"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("assignments = %v, want the named duckling first and the roster for the rest", got)
	}
}
