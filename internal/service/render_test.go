package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
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
