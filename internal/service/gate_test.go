package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func TestOnDemandGateIncludesConfiguredProductSmoke(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: t.TempDir(), Name: "smoke", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "custom", "verify.custom": "true", "run.command": "printf 'product crashed\\n' >&2; exit 7",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.GateRun(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Green || result.ExitCode == 0 || !strings.Contains(result.Output, "blocking product smoke") || !strings.Contains(result.Output, "product crashed") {
		t.Fatalf("gate did not include the red product smoke: %+v", result)
	}
}

func TestOnDemandGateUsesSmokeInsteadOfLaunchingTheInteractiveCommand(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: t.TempDir(), Name: "headless-smoke", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectUpdate(context.Background(), p.ID, map[string]string{
		"verify.mode": "custom", "verify.custom": "true",
		"run.command": "printf 'interactive command must not run\\n' >&2; exit 19",
		"run.smoke":   "printf 'headless smoke ran\\n'", "run.smoke_timeout_s": "7",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.GateRun(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Green || !strings.Contains(result.Output, "run.smoke met expectation exit: exited successfully") || strings.Contains(result.Output, "interactive command must not run") {
		t.Fatalf("gate did not use the explicit headless smoke: %+v", result)
	}
}

// A project with no gate produces UNVERIFIED forever and says nothing about
// it. The note is what turns "why does nothing ever pass" into one line at the
// end of the first run.
func TestARunWithNoGateSaysSoAndSaysWhatToDo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := gateAdvice(root, config.Verify{Mode: "none"})
	if !strings.Contains(got, "UNVERIFIED") {
		t.Errorf("the note does not say what the consequence is: %q", got)
	}
	if !strings.Contains(got, "go build ./...") {
		t.Errorf("the note does not name the gate that is now available: %q", got)
	}
	if !strings.Contains(got, "gate --adopt") {
		t.Errorf("the note does not say how to fix it: %q", got)
	}
}

// An empty folder has nothing to detect, and pretending otherwise would send
// someone chasing a command that does not exist.
func TestTheNoteIsHonestWhenThereIsNothingToDetect(t *testing.T) {
	got := gateAdvice(t.TempDir(), config.Verify{Mode: "none"})
	if !strings.Contains(got, "UNVERIFIED") {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "adopt") {
		t.Errorf("offered to adopt a gate that does not exist: %q", got)
	}
}

// A project that already has a gate must not be nagged.
func TestNoNoteWhenAGateIsConfigured(t *testing.T) {
	if got := gateAdvice(t.TempDir(), config.Verify{Mode: "tests", Tests: "go test ./..."}); got != "" {
		t.Errorf("got %q, want silence", got)
	}
}

func TestGateCommandReadsTheModeInForce(t *testing.T) {
	for _, c := range []struct {
		v    config.Verify
		want string
	}{
		{config.Verify{Mode: "tests", Tests: "go test ./...", Build: "go build ./..."}, "go test ./..."},
		{config.Verify{Mode: "build", Tests: "go test ./...", Build: "go build ./..."}, "go build ./..."},
		{config.Verify{Mode: "lint", Lint: "golangci-lint run"}, "golangci-lint run"},
		{config.Verify{Mode: "none", Tests: "go test ./..."}, ""},
	} {
		if got := gateCommandFor(c.v); got != c.want {
			t.Errorf("mode %q: got %q, want %q", c.v.Mode, got, c.want)
		}
	}
}
