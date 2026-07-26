package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectNoGateForPlainDocs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# docs\n")

	gate, cmd, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gate != GateNone {
		t.Errorf("gate = %q, want none (got cmd %q)", gate, cmd)
	}
}

func TestDetectGoProject(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n\ngo 1.24\n")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	gate, cmd, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gate != GateTests && gate != GateBuild {
		t.Errorf("gate = %q, want tests or build", gate)
	}
	if cmd == "" {
		t.Error("empty command for a Go project")
	}
}

// P3: a gate that could not run anything must never report success.
func TestVerdictNoneIsUnverifiedNotPassed(t *testing.T) {
	got := Verdict(&Result{Gate: GateNone, ExitCode: 0})
	if got != "UNVERIFIED" {
		t.Errorf("verdict = %q, want UNVERIFIED — a none gate must never read as PASSED", got)
	}
}

func TestVerdictGreenGateIsPassed(t *testing.T) {
	for _, gate := range []Gate{GateTests, GateBuild, GateLint} {
		if got := Verdict(&Result{Gate: gate, ExitCode: 0}); got != "PASSED" {
			t.Errorf("gate %q exit 0: verdict = %q, want PASSED", gate, got)
		}
	}
}

func TestVerdictRedGateIsFailed(t *testing.T) {
	if got := Verdict(&Result{Gate: GateTests, ExitCode: 1}); got != "FAILED" {
		t.Errorf("verdict = %q, want FAILED", got)
	}
}

func TestIsGreenAndIsRedAgreeWithVerdict(t *testing.T) {
	green := &Result{Gate: GateTests, ExitCode: 0}
	red := &Result{Gate: GateTests, ExitCode: 2}
	none := &Result{Gate: GateNone, ExitCode: 0}

	if !IsGreen(green) || IsRed(green) {
		t.Error("green result misclassified")
	}
	if IsGreen(red) || !IsRed(red) {
		t.Error("red result misclassified")
	}
	if IsGreen(none) {
		t.Error("a none gate reported green")
	}
}

func TestRunExecutesConfiguredCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Verify{Mode: "custom", Custom: "exit 0", TimeoutS: 30}

	res, err := Run(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
}

func TestRunCapturesNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Verify{Mode: "custom", Custom: "exit 3", TimeoutS: 30}

	res, err := Run(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", res.ExitCode)
	}
	if IsGreen(res) {
		t.Error("a non-zero exit reported green")
	}
}

// A missing binary means nothing was tested; that is UNVERIFIED, not FAILED.
// Reporting FAILED would blame the model for a broken toolchain.
func TestRunMissingBinaryIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Verify{Mode: "custom", Custom: "ducklab-no-such-binary-xyz", TimeoutS: 30}

	res, err := Run(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict := Verdict(res); verdict == "PASSED" {
		t.Error("a missing binary reported PASSED")
	}
}

func TestRunNoneModeSkipsExecution(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Verify{Mode: "none", TimeoutS: 30}

	res, err := Run(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Gate != GateNone {
		t.Errorf("gate = %q, want none", res.Gate)
	}
	if Verdict(res) != "UNVERIFIED" {
		t.Errorf("verdict = %q, want UNVERIFIED", Verdict(res))
	}
}
