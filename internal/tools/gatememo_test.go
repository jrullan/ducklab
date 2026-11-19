package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/verify"
)

// memoFixture is a project whose gate counts its own executions in a file
// outside the tree identity, and whose tree identity the test sets.
func memoFixture(t *testing.T, gate string) (*ExecContext, func() int, *string) {
	t.Helper()
	root := t.TempDir()
	counter := filepath.Join(t.TempDir(), "runs")
	tree := "tree-1"
	prev := treeIdentity
	treeIdentity = func(string) (string, error) { return tree, nil }
	t.Cleanup(func() { treeIdentity = prev })
	ectx := &ExecContext{
		ProjectRoot: root,
		Verify:      config.Verify{Mode: "custom", Custom: "echo run >> " + counter + "; " + gate, TimeoutS: 30},
		GateMemo:    NewGateMemo(),
	}
	count := func() int {
		b, _ := os.ReadFile(counter)
		return strings.Count(string(b), "run")
	}
	return ectx, count, &tree
}

func runIt(t *testing.T, ctx context.Context, ectx *ExecContext) *verify.Result {
	t.Helper()
	res, err := RunVerify(ctx, ectx, ectx.ProjectRoot, ectx.Verify, verify.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// B-545: the reviewer's verify_run re-ran the implementer's on a tree the
// reviewer never touched, and the final gate re-ran the round gate a second
// after it. A green result on the same command and the same tree is reused,
// and says so.
func TestAGreenGateOnTheSameTreeIsReused(t *testing.T) {
	ectx, count, _ := memoFixture(t, "true")
	ctx := context.Background()
	runIt(t, ctx, ectx)
	second := runIt(t, ctx, ectx)
	if count() != 1 {
		t.Fatalf("the gate ran %d times on an unchanged tree, want 1", count())
	}
	if !verify.IsGreen(second) || !strings.Contains(second.Output, "[reused: this exact tree") {
		t.Errorf("the reused result does not say it was reused: %q", second.Output)
	}
}

// A changed tree, a red result, and the deciding round gate always run.
func TestTheMemoNeverStandsInForADecision(t *testing.T) {
	ctx := context.Background()

	ectx, count, tree := memoFixture(t, "true")
	runIt(t, ctx, ectx)
	*tree = "tree-2"
	runIt(t, ctx, ectx)
	if count() != 2 {
		t.Errorf("a changed tree reused the old result (%d runs)", count())
	}

	runIt(t, FreshGate(ctx), ectx)
	if count() != 3 {
		t.Errorf("the round gate reused a result instead of deciding (%d runs)", count())
	}
	// And what it found is what the next caller reuses.
	runIt(t, ctx, ectx)
	if count() != 3 {
		t.Errorf("the round gate's green was not recorded (%d runs)", count())
	}

	red, redCount, _ := memoFixture(t, "false")
	runIt(t, ctx, red)
	runIt(t, ctx, red)
	if redCount() != 2 {
		t.Errorf("a red result was reused (%d runs)", redCount())
	}
}

// A gate that changes the tree it judged leaves nothing to reuse: the tree it
// passed no longer exists.
func TestAGateThatChangesTheTreeIsNotRecorded(t *testing.T) {
	ectx, count, tree := memoFixture(t, "true")
	calls := 0
	treeIdentity = func(string) (string, error) {
		calls++
		return *tree + strings.Repeat("'", calls), nil // differs on every call
	}
	ctx := context.Background()
	runIt(t, ctx, ectx)
	runIt(t, ctx, ectx)
	if count() != 2 {
		t.Errorf("a result from a tree the gate itself changed was reused (%d runs)", count())
	}
}

// verify_run through the memo: the reviewer's call on the implementer's green
// tree comes back at once, green, and still closes the turn's tools.
func TestVerifyRunReusesTheImplementersGreen(t *testing.T) {
	ectx, count, _ := memoFixture(t, "true")
	tool := &VerifyRun{}
	if _, err := tool.Execute(context.Background(), ectx, nil); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), ectx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count() != 1 || res.IsError || !res.EndTurn || !strings.Contains(res.Content, "[reused") {
		t.Fatalf("second verify_run: runs=%d err=%v endturn=%v\n%s", count(), res.IsError, res.EndTurn, res.Content)
	}
	// Without a memo every call runs, as before.
	ectx.GateMemo = nil
	if _, err := tool.Execute(context.Background(), ectx, nil); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Errorf("a run without a memo reused a result (%d runs)", count())
	}
}

// Review of B-545: an implementer's green on tree T outlived the round
// gate's red on the same T (something outside the tree identity changed, or
// a flaky test), and the final gate reused the stale green. Only the latest
// run of a key speaks for it.
func TestARedRunErasesAnEarlierGreen(t *testing.T) {
	// The gate reads a marker outside the tree identity, as an ignored build
	// output or a flaky dependency would.
	marker := filepath.Join(t.TempDir(), "broken")
	ectx, count, _ := memoFixture(t, "[ ! -e "+marker+" ]")
	ctx := context.Background()
	runIt(t, ctx, ectx) // the implementer's green on T, recorded
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runIt(t, FreshGate(ctx), ectx); verify.IsGreen(res) {
		t.Fatal("the deciding run did not go red")
	}
	before := count()
	res := runIt(t, ctx, ectx)
	if verify.IsGreen(res) || strings.Contains(res.Output, "[reused") {
		t.Fatalf("a green that the latest run contradicted was reused: %q", res.Output)
	}
	if count() != before+1 {
		t.Errorf("the call after a red did not run the gate")
	}
}

// A reused result did not run: it carries no duration of its own.
func TestAReusedResultHasNoDuration(t *testing.T) {
	ectx, _, _ := memoFixture(t, "sleep 0.05")
	ctx := context.Background()
	runIt(t, ctx, ectx)
	if res := runIt(t, ctx, ectx); res.Duration != 0 {
		t.Errorf("a reused result reports %vs of a run that did not happen", res.Duration)
	}
}

// B-554: a green round gate seals the memo's epoch; anything that executes
// afterwards breaks the seal, a reused verify_run does not, and a round gate
// that is not green leaves nothing sealed.
func TestTheRoundGateSealBreaksOnAnyExecution(t *testing.T) {
	ctx := context.Background()
	ectx, count, _ := memoFixture(t, "true")
	if ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatal("a memo with no round gate reports a sealed green")
	}
	gate, _, err := RunVerificationGate(FreshGate(ctx), ectx)
	if err != nil || gate != "green" {
		t.Fatalf("round gate: %s %v", gate, err)
	}
	ectx.GateMemo.SealRoundGate(true)
	if !ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatal("nothing ran after the green round gate, yet the seal is broken")
	}

	// A verify_run on the same tree reuses the result: nothing executes.
	reg := NewRegistry()
	if _, err := reg.Execute(ctx, ectx, "verify_run", nil); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || !ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatalf("a reused verify_run broke the seal (runs=%d)", count())
	}

	// Any other tool runs something the tree identity may not see.
	ectx.ToolsClosed, ectx.ReadToolsClosed = false, false
	if _, err := reg.Execute(ctx, ectx, "shell", json.RawMessage(`{"cmd":"true"}`)); err != nil {
		t.Fatal(err)
	}
	if ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatal("a shell command after the round gate left the seal intact")
	}

	// A verification that really runs breaks it too.
	ectx.GateMemo.SealRoundGate(true)
	runIt(t, FreshGate(ctx), ectx)
	if ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatal("a verification that ran after the round gate left the seal intact")
	}

	// A round gate that is not green seals nothing.
	ectx.GateMemo.SealRoundGate(false)
	if ectx.GateMemo.UntouchedSinceGreenRoundGate() {
		t.Fatal("a red round gate sealed the epoch")
	}

	var none *GateMemo
	none.Touch()
	none.SealRoundGate(true)
	if none.UntouchedSinceGreenRoundGate() {
		t.Fatal("a nil memo reports a sealed green")
	}
}

// Review of B-554: the final gate decides reuse at each step. When an early
// step really runs (its key missed: the round gate recorded nothing for it),
// the steps after it run too, though the memo holds a green for them.
func TestAStepThatRunsInTheFinalGateMakesTheLaterStepsRun(t *testing.T) {
	ctx := context.Background()
	ectx, count, _ := memoFixture(t, "true")
	runIt(t, FreshGate(ctx), ectx) // the round gate's project verify, recorded
	ectx.GateMemo.SealRoundGate(true)

	final := ReuseWhileSealed(ctx)
	if res := runIt(t, final, ectx); !strings.Contains(res.Output, "[reused") || count() != 1 {
		t.Fatalf("an untouched seal did not reuse (runs=%d)", count())
	}
	// A step with no recorded green runs for real...
	other := ectx.Verify
	other.Custom = "true # task verification"
	if _, err := RunVerify(final, ectx, ectx.ProjectRoot, other, verify.Identity{}); err != nil {
		t.Fatal(err)
	}
	// ...and the project verify after it no longer reuses.
	if res := runIt(t, final, ectx); strings.Contains(res.Output, "[reused") || count() != 2 {
		t.Fatalf("a step after one that ran reused the memo (runs=%d): %q", count(), res.Output)
	}
}
