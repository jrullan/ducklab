package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	res, err := Run(context.Background(), dir, cfg)
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

	res, err := Run(context.Background(), dir, cfg)
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

	res, err := Run(context.Background(), dir, cfg)
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

	res, err := Run(context.Background(), dir, cfg)
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

// I3: nothing is unbounded. The gate ignored its own timeout_s, so a command
// that hangs — a test waiting on input, a dev server started by mistake — held
// the run open until a person noticed. It runs on every round of every run,
// which makes it the worst place for this.
func TestTheGateStopsAtItsTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("sleeps")
	}
	start := time.Now()
	res, err := Run(context.Background(), t.TempDir(), config.Verify{
		Mode: "custom", Custom: "sleep 30", TimeoutS: 1,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("a timed-out gate should be a result, not a transport error: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("waited %v for a 1s timeout", elapsed)
	}
	if res.ExitCode == 0 {
		t.Error("a gate that was killed reported success")
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Errorf("the output does not say why it stopped: %q", res.Output)
	}
	// A gate that ran out of time verified nothing, and calling that FAILED
	// would blame the code for our limit (05 §5.2).
	if got := Verdict(res); got != "UNVERIFIED" {
		t.Errorf("verdict = %s, want UNVERIFIED", got)
	}
}

// A gate that finishes inside its budget is untouched.
func TestAQuickGateIsUnaffected(t *testing.T) {
	res, err := Run(context.Background(), t.TempDir(), config.Verify{Mode: "custom", Custom: "true", TimeoutS: 30})
	if err != nil || res.ExitCode != 0 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

// The night of four pytest orphans: verify ran on context.Background, so an
// abort left the gate's process group running — holding the database
// connections that hung the NEXT run's suite. The run's own context rides in
// now: cancel returns promptly and leaves no survivors.
func TestAnAbortKillsTheGateAndItsChildren(t *testing.T) {
	marker := "verify_abort_repro_sleeper"
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := Run(ctx, t.TempDir(), config.Verify{
		Mode: "custom", Custom: "python3 -c 'import time; time.sleep(300) # " + marker + "'",
		TimeoutS: 600,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the abort took %s to land", took)
	}
	// Cancelled means UNVERIFIED, never a pass.
	if res.ExitCode == 0 {
		t.Errorf("a killed gate reported exit 0: %+v", res)
	}
	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", marker).Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Fatalf("the gate's child survived the abort: %s", s)
	}
}
