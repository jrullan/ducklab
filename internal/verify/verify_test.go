package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestDetectPythonGateNamesAnInterpreterOnPATH(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.py", "print('hi')\n")

	gate, cmd, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gate != GateBuild {
		t.Fatalf("gate = %q, want build", gate)
	}
	interp := strings.Fields(cmd)[0]
	if _, err := exec.LookPath(interp); err != nil {
		t.Errorf("detected gate %q names %q, which is not on PATH", cmd, interp)
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

// A repository with both the Go service and the desktop must not silently
// measure only one language. The detected tests gate is the project's shared
// verification contract, so it must execute the frontend typecheck and Vitest
// suite as well as Go tests.
func TestDetectMixedGoFrontendProjectCoversFrontend(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/x\n\ngo 1.24\n")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	write(t, dir, "frontend/package.json", `{"scripts":{"test":"vitest run","typecheck":"tsc --noEmit"}}`)
	write(t, dir, "frontend/tsconfig.json", `{}`)

	gate, cmd, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gate != GateTests {
		t.Fatalf("gate = %q, want tests", gate)
	}
	for _, want := range []string{"go test ./...", "frontend", "tsc --noEmit", "vitest run"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("mixed-project gate %q does not include %q", cmd, want)
		}
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

func TestRunStampsProcessIdentity(t *testing.T) {
	res, err := Run(context.Background(), t.TempDir(), config.Verify{Mode: "custom", Custom: `printf '%s/%s' "$DUCKLAB_RUN_ID" "$DUCKLAB_PROJECT_ID"`, TimeoutS: 30}, Identity{RunID: "run-test", ProjectID: "project-test"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(res.Output), "run-test/project-test"; got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
}

// The acceptance checkout's [verify] setup uses the same Run path, so it must
// receive the run identity just like the gate command.
func TestRunSetupStampsProcessIdentity(t *testing.T) {
	res, err := Run(context.Background(), t.TempDir(), config.Verify{Mode: "custom", Custom: `printf '%s/%s' "$DUCKLAB_RUN_ID" "$DUCKLAB_PROJECT_ID"`, TimeoutS: 30}, Identity{RunID: "run-setup", ProjectID: "project-setup"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(res.Output), "run-setup/project-setup"; got != want {
		t.Fatalf("setup identity = %q, want %q", got, want)
	}
}

func TestRunConcurrentIdentitiesDoNotCross(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")}
	ids := []Identity{{RunID: "run-one", ProjectID: "project-one"}, {RunID: "run-two", ProjectID: "project-two"}}
	var wg sync.WaitGroup
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := fmt.Sprintf(`printf '%%s' "$DUCKLAB_RUN_ID" > %s`, paths[i])
			res, err := Run(context.Background(), dir, config.Verify{Mode: "custom", Custom: cmd, TimeoutS: 30}, ids[i])
			if err != nil || res.ExitCode != 0 {
				t.Errorf("run %d failed: %v (result %#v)", i, err, res)
			}
		}(i)
	}
	wg.Wait()
	for i, path := range paths {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != ids[i].RunID {
			t.Errorf("run %d wrote %q, want %q", i, got, ids[i].RunID)
		}
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

// This is the binary a gate spawns below. Keeping it in the test binary makes
// the assertion exercise the real child process environment, rather than a
// value passed through an in-process helper.
func TestVerifyRunStateEnvironmentHelper(t *testing.T) {
	if os.Getenv("VERIFY_CHILD") != "1" {
		t.Skip("helper")
	}
	for _, key := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
		"HOME", "AppData", "LocalAppData",
	} {
		fmt.Printf("VERIFY_STATE_ENV:%s=%s\n", key, os.Getenv(key))
	}
}

// Every process in a verification gate tree must see throwaway state paths.
// In particular, setting the harness paths in the parent must not let a gate
// child discover or mutate the live registry. Two invocations also need
// distinct paths: isolation is per verify_run, not merely per process tree.
func TestRunScrubsStateEnvironmentForSpawnedChildren(t *testing.T) {
	master := t.TempDir()
	keys := []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "HOME", "AppData", "LocalAppData"}
	for _, key := range keys {
		t.Setenv(key, filepath.Join(master, key))
	}

	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("VERIFY_CHILD=1 %q -test.v -test.run=TestVerifyRunStateEnvironmentHelper", binary)
	cfg := config.Verify{Mode: "custom", Custom: command, TimeoutS: 30}

	read := func() map[string]string {
		res, err := Run(context.Background(), t.TempDir(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != 0 {
			t.Fatalf("state helper gate failed: %s", res.Output)
		}
		got := make(map[string]string)
		for _, line := range strings.Split(res.Output, "\n") {
			if strings.HasPrefix(line, "VERIFY_STATE_ENV:") {
				parts := strings.SplitN(strings.TrimPrefix(line, "VERIFY_STATE_ENV:"), "=", 2)
				if len(parts) == 2 {
					got[parts[0]] = parts[1]
				}
			}
		}
		return got
	}
	first, second := read(), read()

	wantKeys := []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}
	if runtime.GOOS == "darwin" {
		wantKeys = append(wantKeys, "HOME")
	} else if runtime.GOOS == "windows" {
		wantKeys = append(wantKeys, "AppData", "LocalAppData")
	}
	for _, key := range wantKeys {
		for name, got := range map[string]string{"first": first[key], "second": second[key]} {
			if got == "" {
				t.Errorf("%s %s was empty", name, key)
			} else if strings.HasPrefix(got, master) {
				t.Errorf("%s %s leaked harness state path %q", name, key, got)
			}
		}
		if first[key] == second[key] {
			t.Errorf("%s was reused across verify_run calls: %q", key, first[key])
		}
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
	// This is an unrelated concurrent gate. Its sleeper must not be mistaken
	// for a gate's child while handling that cancellation reaped ours. Unique
	// per invocation so concurrent gates never match its processes. This marker
	// is the UNRELATED sleeper's; the gate under test gets its own below (the
	// B-168 pattern) so the pgrep check can never match another gate's sleeper.
	otherMarker := fmt.Sprintf("verify_abort_repro_unrelated_%d_%x", os.Getpid(), time.Now().UnixNano())
	other := exec.Command("python3", "-c", "import time; time.sleep(3) # "+otherMarker)
	if err := other.Start(); err != nil {
		t.Fatalf("start unrelated gate: %v", err)
	}
	defer func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	}()

	// A fresh unique marker for the gate under this invocation only.
	gateMarker := fmt.Sprintf("verify_abort_repro_sleeper_%d_%x", os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := Run(ctx, t.TempDir(), config.Verify{
		Mode: "custom", Custom: "python3 -c 'import time; time.sleep(300) # " + gateMarker + "'",
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
	// Poll until the children are gone, with a load-tolerant deadline instead of
	// a fixed settle sleep: pass as soon as no marker process remains, fail only
	// with the surviving pids if any are still alive at the deadline.
	deadline := time.Now().Add(5 * time.Second)
	survivors := ""
	for {
		out, _ := exec.Command("pgrep", "-f", gateMarker).Output()
		survivors = strings.TrimSpace(string(out))
		if survivors == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gate's child survived the abort: %s", survivors)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
