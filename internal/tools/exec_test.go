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
	if !strings.Contains(res.Content, "ran-the-real-gate") {
		t.Errorf("verify_run did not run the project's gate: %q", res.Content)
	}
	if strings.Contains(res.Content, "go test ./...") {
		t.Errorf("verify_run still runs its hardcoded command: %q", res.Content)
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

func TestBareNativeSyntaxGateReportsCompilerWarningsWithoutChangingTheContract(t *testing.T) {
	root := t.TempDir()
	source := `void takes_unsigned(unsigned int *value); int main(void) { int value = 0; takes_unsigned(&value); return 0; }`
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
	source := `void takes_unsigned(unsigned int *value); int main(void) { int value = 0; takes_unsigned(&value); return 0; }`
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
