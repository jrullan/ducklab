package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// verify_run must run the gate that decides, not a guess.
//
// It ran "go test ./..." hardcoded while the project's gate was "go build".
// A model called it, was told exit 0, and stopped — then the real gate failed
// on work the model had been told was fine. Found on a real run: three rounds
// of a duckling insisting it was done against a gate it had never actually
// executed.
func TestVerifyRunUsesTheProjectsOwnGate(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{
		ProjectRoot: root,
		ShellPolicy: config.ShellPolicy{Mode: "free", TimeoutS: 30},
		Verify:      config.Verify{Mode: "custom", Custom: "echo ran-the-real-gate", TimeoutS: 30},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("verify_run failed: %s", res.Content)
	}
	if !res.EndTurn || !strings.Contains(res.Content, "implementation tools are now closed") {
		t.Fatalf("a green gate did not close the implementation turn: %+v", res)
	}
	if !strings.Contains(res.Content, "ran-the-real-gate") {
		t.Errorf("verify_run did not run the project's gate: %q", res.Content)
	}
	if strings.Contains(res.Content, "go test ./...") {
		t.Errorf("verify_run still runs its hardcoded command: %q", res.Content)
	}
}

func TestGreenVerifyClosesMutationsForTheRestOfTheReply(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{
		ProjectRoot: root,
		ShellPolicy: config.ShellPolicy{Mode: "free", TimeoutS: 30},
		Verify:      config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30},
	}
	registry := NewRegistry()
	verified, err := registry.Execute(context.Background(), ectx, "verify_run", json.RawMessage(`{}`))
	if err != nil || verified.IsError || !verified.EndTurn || !ectx.ToolsClosed {
		t.Fatalf("green verify did not close tools: result=%+v err=%v closed=%v", verified, err, ectx.ToolsClosed)
	}
	written, err := registry.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"after-green.txt","content":"regression"}`))
	if err != nil || !written.IsError || !written.EndTurn {
		t.Fatalf("post-green mutation was not refused: result=%+v err=%v", written, err)
	}
	if _, err := os.Stat(filepath.Join(root, "after-green.txt")); !os.IsNotExist(err) {
		t.Fatalf("post-green mutation changed the tree: stat err=%v", err)
	}
}

func TestMutationRemainsUnverifiedUntilACompleteGreenGate(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{
		ProjectRoot: root,
		Verify:      config.Verify{Mode: "custom", Custom: "test -f changed.txt", TimeoutS: 30},
	}
	registry := NewRegistry()
	if _, err := registry.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"changed.txt","content":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if !ectx.MutationUnverified {
		t.Fatal("successful mutation did not require verification")
	}
	verified, err := registry.Execute(context.Background(), ectx, "verify_run", json.RawMessage(`{}`))
	if err != nil || verified.IsError || ectx.MutationUnverified {
		t.Fatalf("green verification did not clear mutation state: result=%+v err=%v pending=%v", verified, err, ectx.MutationUnverified)
	}
}

// A failing gate must come back as an error result, or a model reads a red
// gate as a green one.
func TestVerifyRunReportsAFailingGateAsAnError(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot: t.TempDir(),
		ShellPolicy: config.ShellPolicy{Mode: "free", TimeoutS: 30},
		Verify:      config.Verify{Mode: "custom", Custom: "exit 3", TimeoutS: 30},
	}
	res, _ := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if !res.IsError {
		t.Errorf("a gate that exited 3 was reported as success: %q", res.Content)
	}
}

func TestVerifyRunIncludesTheTaskVerificationThatDecidesTheRound(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:      t.TempDir(),
		TaskVerification: "echo task-gate-failed; exit 7",
		Verify:           config.Verify{Mode: "custom", Custom: "echo project-gate-ran", TimeoutS: 30},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "task-gate-failed") {
		t.Fatalf("task verification was not exposed as the deciding red gate: %s", res.Content)
	}
	if strings.Contains(res.Content, "project-gate-ran") {
		t.Fatalf("project gate ran after the task gate had already failed: %s", res.Content)
	}
}

func TestVerifyRunRunsTaskThenProjectWhenBothAreGreen(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:      t.TempDir(),
		TaskVerification: "echo task-gate-ran",
		Verify:           config.Verify{Mode: "custom", Custom: "echo project-gate-ran", TimeoutS: 30},
	}
	res, _ := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if res.IsError || !strings.Contains(res.Content, "task-gate-ran") || !strings.Contains(res.Content, "project-gate-ran") {
		t.Fatalf("composite gate result = %s", res.Content)
	}
}

func TestVerifyRunPutsBlockingProjectFailureBeforeSuccessfulTaskEvidence(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:      t.TempDir(),
		TaskVerification: "echo task-success-marker",
		Verify:           config.Verify{Mode: "custom", Custom: "echo project-failure-marker; exit 2", TimeoutS: 30},
	}
	res, _ := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if !res.IsError || !strings.HasPrefix(res.Content, "blocking project verification:") {
		t.Fatalf("project failure was not prefixed:\n%s", res.Content)
	}
	if strings.Index(res.Content, "project-failure-marker") > strings.Index(res.Content, "task-success-marker") {
		t.Fatalf("successful task evidence still precedes the blocker:\n%s", res.Content)
	}
}

func TestVerifyRunRejectsAGreenBuildThatDoesNotExerciseNewProductionSources(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:        t.TempDir(),
		Verify:             config.Verify{Mode: "custom", Custom: "echo 'ninja: no work to do.'", TimeoutS: 30},
		ActiveCapabilities: []string{"meson"},
		WorkspaceDiff: func() (string, error) {
			return "diff --git a/src/ui/overlay.c b/src/ui/overlay.c\nnew file mode 100644\n--- /dev/null\n+++ b/src/ui/overlay.c\n", nil
		},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "capability coverage [meson/build-integration, required]") || !strings.Contains(res.Content, "src/ui/overlay.c") {
		t.Fatalf("unintegrated production source survived verify_run:\n%s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "blocking capability coverage:") {
		t.Fatalf("blocking coverage did not lead the result:\n%s", res.Content)
	}
}

func TestVerifyRunRejectsAcceptedDependencySourceMissingFromMesonGraph(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "build", "compile_commands.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{
		ProjectRoot:        root,
		TaskVerification:   "true",
		Verify:             config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30},
		ActiveCapabilities: []string{"meson"},
		BuildGraphFiles:    []string{"src/core/capture_core.c"},
		WorkspaceDiff:      func() (string, error) { return "", nil },
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "src/core/capture_core.c") ||
		!strings.Contains(res.Content, "accepted dependency source files") {
		t.Fatalf("missing accepted dependency source survived verify_run:\n%s", res.Content)
	}
}

func TestVerifyRunExecutesAcceptanceProbesInOrder(t *testing.T) {
	root := t.TempDir()
	ectx := &ExecContext{
		ProjectRoot:          root,
		TaskVerification:     "true",
		TaskAcceptanceProbes: []string{"printf slice-one", "printf slice-two; exit 7", "printf must-not-run"},
		Verify:               config.Verify{Mode: "custom", Custom: "printf project-must-not-run", TimeoutS: 30},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "acceptance probe 2") ||
		!strings.Contains(res.Content, "slice-two") || strings.Contains(res.Content, "must-not-run") {
		t.Fatalf("acceptance probes did not block at the first red slice:\n%s", res.Content)
	}
}

func TestVerifyRunRejectsMissingDeclaredProducedFileBeforeCommands(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot:       t.TempDir(),
		TaskVerification:  "echo task-command-must-not-run",
		TaskProducedFiles: []string{"src/backend/required.h"},
		Verify:            config.Verify{Mode: "custom", Custom: "echo project-command-must-not-run", TimeoutS: 30},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "missing declared Produces files: src/backend/required.h") {
		t.Fatalf("missing produced file did not make the gate red: %s", res.Content)
	}
	if strings.Contains(res.Content, "must-not-run") {
		t.Fatalf("commands ran despite a broken artifact contract: %s", res.Content)
	}
}

func TestBareNativeSyntaxGateReportsCompilerWarningsWithoutChangingTheContract(t *testing.T) {
	root := t.TempDir()
	source := `int main(void) { int unused = 0; return 0; }`
	if err := os.WriteFile(filepath.Join(root, "warning.c"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{
		ProjectRoot:      root,
		TaskVerification: "cc -fsyntax-only warning.c",
		Verify:           config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30},
		Capabilities:     config.Capabilities{Auto: true},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "capability diagnostic [c-native/compiler warnings, diagnostic]") || !strings.Contains(res.Content, "-Werror") {
		t.Fatalf("warning-only native diagnostic changed the gate or was absent:\n%s", res.Content)
	}
}

func TestNativeWarningsCanBeADeclaredRequiredCapability(t *testing.T) {
	root := t.TempDir()
	source := `int main(void) { int unused = 0; return 0; }`
	if err := os.WriteFile(filepath.Join(root, "warning.c"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{
		ProjectRoot:      root,
		TaskVerification: "cc -fsyntax-only warning.c",
		Verify:           config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30},
		Capabilities: config.Capabilities{Auto: true, Policy: map[string]string{
			"c-native.warnings": "required",
		}},
	}
	res, _ := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content, "required") {
		t.Fatalf("declared required capability did not block:\n%s", res.Content)
	}
}

func TestVerifyRunIncludesRequiredSourceInspection(t *testing.T) {
	root := t.TempDir()
	source := `
int count_trailing_zeroes(unsigned long mask) { int n = 0; while (mask != 0 && (mask & 1) == 0) { n++; mask >>= 1; } return n; }
int count_one_bits(unsigned long mask) { int n = 0; while (mask & 1) { n++; mask >>= 1; } return n; }
int red_width(unsigned long red_mask) { int red_shift = count_trailing_zeroes(red_mask); return red_shift + count_one_bits(red_mask); }
`
	if err := os.WriteFile(filepath.Join(root, "capture.c"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{
		ProjectRoot:      root,
		TaskVerification: "cc -fsyntax-only $(pkg-config --cflags x11) capture.c",
		Verify:           config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30},
		Capabilities:     config.Capabilities{Auto: true},
	}
	res, err := (&VerifyRun{}).Execute(context.Background(), ectx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "x11-image/channel-mask-flow, required") || !strings.Contains(res.Content, "unshifted mask") {
		t.Fatalf("required source inspection did not make verify_run red:\n%s", res.Content)
	}
}

// The description is where a model decides whether a tool is for it. The old
// one-liner — "this pauses the run until answered" — read as a deterrent, and
// the tool went unused while a model deliberated a question only the person
// could answer. The description must state when asking WINS, and where the
// line is.
func TestAskHumanSaysWhenAskingWins(t *testing.T) {
	d := (&AskHuman{}).Description()
	for _, want := range []string{"ONE precise question", "needed outcome or decision", "not approval to run a shell command", "wrong guess costs the whole run", "never ask about those"} {
		if !strings.Contains(d, want) {
			t.Errorf("ask_human's description does not say %q", want)
		}
	}
	if strings.Contains(strings.ToLower(d), "ask the human for approval") {
		t.Errorf("ask_human's description must not promise shell-command approval: %q", d)
	}
}

func TestAskHumanResolvesDeterministicWorkspaceFactsWithoutPausing(t *testing.T) {
	ectx := &ExecContext{DeterministicAnswers: map[string]string{
		"project root": "use `.`; absolute root is `/work/neocapture`",
	}}
	res, err := (&AskHuman{}).Execute(context.Background(), ectx, json.RawMessage(`{
		"question":"What is the project root path?",
		"options":[".","/workspace/gnome-screenshot"]
	}`))
	if err != nil {
		t.Fatalf("deterministic question paused: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, "use `.`") {
		t.Fatalf("deterministic answer = %+v", res)
	}
	if ectx.Pending != nil {
		t.Fatalf("deterministic question created a human pending: %+v", ectx.Pending)
	}
}

// 45 verify_run calls, all red, 53 patches between them, 32KB of test output
// ballooning the context each time — 8.7M tokens on a datepicker default,
// ended only by the wallclock. An approach that has failed ten times straight
// is not converging; the gate brake says so instead of letting it burn.
func TestTheGateBrakeStopsARedSpiral(t *testing.T) {
	dir := t.TempDir()
	ectx := &ExecContext{
		ProjectRoot: dir,
		// A gate that always fails, cheaply.
		Verify: config.Verify{Mode: "custom", Custom: "exit 1", TimeoutS: 30},
	}
	tool := &VerifyRun{}

	for i := 1; i <= GateFailLimit; i++ {
		res, err := tool.Execute(context.Background(), ectx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("a failing gate reported success on attempt %d", i)
		}
		if i >= GateFailLimit-3 && !strings.Contains(res.Content, "gate brake") {
			t.Errorf("attempt %d near the limit does not warn: %.120s", i, res.Content)
		}
	}
	// Beyond the limit: refused, with orders to stop and explain.
	res, err := tool.Execute(context.Background(), ectx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "REFUSED") || !strings.Contains(res.Content, "not converging") {
		t.Errorf("the brake did not engage: %.200s", res.Content)
	}

	// One green resets the count: progress is progress.
	ectx.ConsecGateFails = 2
	ectx.Verify = config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30}
	if res, _ := tool.Execute(context.Background(), ectx, nil); res.IsError {
		t.Fatalf("a passing gate reported failure: %.120s", res.Content)
	}
	if ectx.ConsecGateFails != 0 {
		t.Errorf("a green gate must reset the streak, got %d", ectx.ConsecGateFails)
	}
}
