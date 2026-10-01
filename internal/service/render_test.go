package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestCaptureRenderRunsCommandAndAttachesArtifacts(t *testing.T) {
	root := t.TempDir()
	run := &runlog.Run{ID: "render-run", ProjectID: "demo"}
	writer, err := runlog.NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	contract := config.RenderContract{
		Command:   "mkdir -p captures && printf '\\211PNG\\r\\n\\032\\n' > captures/one.png",
		Artifacts: "captures/*.png",
		TimeoutS:  5,
	}
	result, err := captureRender(context.Background(), root, contract, writer, run.ID, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Captures) != 1 || result.Captures[0] != "one.png" {
		t.Fatalf("captures = %#v", result.Captures)
	}
	data, err := os.ReadFile(filepath.Join(writer.RunDir(), "captures", "one.png"))
	if err != nil || string(data) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("attached capture = %q, err=%v", data, err)
	}
}

func TestCaptureRenderWritesOutputOutsideRunCheckout(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	result, err := captureRender(context.Background(), root, config.RenderContract{
		Command:   "mkdir -p \"$DUCKLAB_RENDER_OUTPUT\" && printf '\\211PNG\\r\\n\\032\\n' > \"$DUCKLAB_RENDER_OUTPUT/one.png\"",
		Artifacts: ".ducklab-render-captures/*.png", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Captures) != 1 || result.Captures[0] != "one.png" {
		t.Fatalf("captures = %#v", result.Captures)
	}
	if _, err := os.Stat(filepath.Join(root, ".ducklab-render-captures")); !os.IsNotExist(err) {
		t.Fatalf("render output remains inside the run checkout: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(writer.RunDir(), "captures", "one.png"))
	if err != nil || string(data) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("attached capture = %q, err=%v", data, err)
	}
}

func TestCaptureRenderAttachesArtifactsAfterDirtyExit(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	result, err := captureRender(context.Background(), root, config.RenderContract{
		Command:   "mkdir -p captures && printf '\\211PNG\\r\\n\\032\\n' > captures/dirty.png && exit 1",
		Artifacts: "captures/*.png", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err == nil {
		t.Fatal("dirty render did not report its exit")
	}
	if len(result.Captures) != 1 || result.Captures[0] != "dirty.png" {
		t.Fatalf("captures = %#v", result.Captures)
	}
}

func TestCaptureRenderRejectsNonPNG(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	_, err = captureRender(context.Background(), root, config.RenderContract{Command: "mkdir -p captures && printf not-png > captures/bad.png", Artifacts: "captures/*.png", TimeoutS: 5}, writer, "render-run", "demo")
	if err == nil {
		t.Fatal("invalid PNG did not fail")
	}
}

func TestCaptureRenderReportsMissingArtifacts(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	_, err = captureRender(context.Background(), root, config.RenderContract{
		Command: "true", Artifacts: "missing/*.png", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err == nil {
		t.Fatal("missing artifacts did not fail")
	}
}

func TestCaptureRenderTreatsAliveAtTimeoutAsSuccessfulSmoke(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	result, err := captureRender(context.Background(), root, config.RenderContract{
		Command: "printf 'warming up\\n' >&2; sleep 10", TimeoutS: 1,
	}, writer, "render-run", "demo")
	if err != nil {
		t.Fatalf("long-running GUI smoke failed: %v", err)
	}
	if len(result.Captures) != 0 {
		t.Fatalf("smoke captures = %v, want none", result.Captures)
	}
	if !strings.Contains(result.Note, "stayed alive for 1s") || !strings.Contains(result.Note, "warming up") {
		t.Fatalf("smoke note = %q, want liveness and collected output", result.Note)
	}
}

func TestCaptureRenderReportsSmokeCrashBeforeTimeout(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "render-run", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	_, err = captureRender(context.Background(), root, config.RenderContract{
		Command: "printf 'startup crash\\n' >&2; exit 7", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err == nil || !strings.Contains(err.Error(), "startup crash") {
		t.Fatalf("early crash error = %v", err)
	}
}

func TestConfiguredRunCommandIsAnExecutableProductSmoke(t *testing.T) {
	root := t.TempDir()
	writer, err := runlog.NewWriter(root, &runlog.Run{ID: "product-smoke", ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	note, err := smokeRunCommand(context.Background(), root, "printf 'started product\\n'", "run.smoke", "exit", 8, "product-smoke", "demo")
	if err != nil || note != "run.smoke met expectation exit: exited successfully" {
		t.Fatalf("successful product smoke = %q, %v", note, err)
	}
	if _, err := smokeRunCommand(context.Background(), root, "printf 'startup failed\\n' >&2; exit 9", "run.smoke", "exit", 8, "product-smoke", "demo"); err == nil || !strings.Contains(err.Error(), "run.smoke expected exit: startup failed") {
		t.Fatalf("crashing product smoke = %v", err)
	}
}

func TestProductSmokeConfigPrefersExplicitSmokeAndDefaultsTheWindow(t *testing.T) {
	command, source, expectation, timeoutS := productSmokeConfig(config.RunApp{Command: "./ui", Smoke: "./ui --headless"})
	if command != "./ui --headless" || source != "run.smoke" || expectation != "exit" || timeoutS != 3 {
		t.Fatalf("explicit smoke = %q, %q, %q, %d", command, source, expectation, timeoutS)
	}
	command, source, expectation, timeoutS = productSmokeConfig(config.RunApp{Command: "./ui", SmokeTimeoutS: 9})
	if command != "./ui" || source != "run.command" || expectation != "live" || timeoutS != 9 {
		t.Fatalf("command fallback = %q, %q, %q, %d", command, source, expectation, timeoutS)
	}
}

func TestProductSmokeNoteUsesTheConfiguredObservationWindow(t *testing.T) {
	note, err := smokeRunCommand(context.Background(), t.TempDir(), "printf 'warming up\\n'; sleep 30", "run.smoke", "live", 1, "product-smoke", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "run.smoke met expectation live: stayed alive for 1s") || !strings.Contains(note, "warming up") {
		t.Fatalf("configured smoke note = %q", note)
	}
}

func TestLiveProductSmokeRejectsCleanEarlyExit(t *testing.T) {
	_, err := smokeRunCommand(context.Background(), t.TempDir(), "printf 'gui could not open display\\n'; exit 0", "run.command", "live", 2, "product-smoke", "demo")
	if err == nil {
		t.Fatal("interactive command exited immediately with status 0 but passed liveness")
	}
	for _, want := range []string{"expected live", "exited early with status 0", "gui could not open display"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestExitProductSmokeRejectsLongLivedProcess(t *testing.T) {
	_, err := smokeRunCommand(context.Background(), t.TempDir(), "sleep 30", "run.smoke", "exit", 1, "product-smoke", "demo")
	if err == nil || !strings.Contains(err.Error(), "expected exit within 1s but stayed alive") {
		t.Fatalf("exit expectation = %v", err)
	}
}

func TestProductSmokeExpectationCanOverrideDerivedDefault(t *testing.T) {
	_, _, expectation, _ := productSmokeConfig(config.RunApp{Command: "./ui", SmokeExpect: "exit"})
	if expectation != "exit" {
		t.Fatalf("explicit expectation = %q", expectation)
	}
	_, _, expectation, _ = productSmokeConfig(config.RunApp{Smoke: "./check", URL: "http://localhost", SmokeExpect: "live"})
	if expectation != "live" {
		t.Fatalf("explicit expectation = %q", expectation)
	}
}

// B-466: a project whose project.toml carries the empty [render] table that
// SaveProject writes runs no render step. It used to start [run].command and
// wait 120 s at every final gate ("render smoke stayed alive for 120s").
func TestAnEmptyRenderTableRunsNoRenderStep(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	marker := filepath.Join(t.TempDir(), "launched")
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"run.command": "touch '" + marker + "' && sleep 30",
		"run.smoke":   "true",
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".ducklab", "project.toml"))
	if !strings.Contains(string(data), "[render]") {
		t.Log("project.toml no longer carries an empty [render]")
	}
	native := true
	for did, duck := range s.cfg.Ducklings {
		duck.Caps.NativeTools = &native
		s.cfg.Ducklings[did] = duck
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		var message provider.Message
		finish := provider.FinishStop
		if call == 1 {
			tc := provider.ToolCall{ID: "call-1", Type: "function"}
			tc.Function.Name, tc.Function.Arguments = "fs_write", `{"path":"index.html","content":"<p>calc</p>\n"}`
			message.ToolCalls = []provider.ToolCall{tc}
			finish = provider.FinishToolCalls
		} else {
			message.Content = "Done."
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: finish}}}
	}
	start := time.Now()
	r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.waitForRun(context.Background(), r.ID); err != nil && !strings.Contains(err.Error(), "waiting for a human") {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the run took %s: a render step waited on [run].command", took)
	}
	detail, err := s.RunGet(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range detail.Events {
		if e.Type == "render" {
			t.Fatalf("an empty [render] produced a render event: %+v", e.Data)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("[run].command was launched by the gate")
	}
}
