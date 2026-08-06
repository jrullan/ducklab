package tools

import (
	"context"
	"encoding/json"
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

// The description is where a model decides whether a tool is for it. The old
// one-liner — "this pauses the run until answered" — read as a deterrent, and
// the tool went unused while a model deliberated a question only the person
// could answer. The description must state when asking WINS, and where the
// line is.
func TestAskHumanSaysWhenAskingWins(t *testing.T) {
	d := (&AskHuman{}).Description()
	for _, want := range []string{"ONE precise question", "wrong guess costs the whole run", "never ask about those"} {
		if !strings.Contains(d, want) {
			t.Errorf("ask_human's description does not say %q", want)
		}
	}
}
