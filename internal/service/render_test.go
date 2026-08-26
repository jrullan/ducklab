package service

import (
	"context"
	"os"
	"path/filepath"
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
	captures, err := captureRender(context.Background(), root, contract, writer, run.ID, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || captures[0] != "one.png" {
		t.Fatalf("captures = %#v", captures)
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

	captures, err := captureRender(context.Background(), root, config.RenderContract{
		Command:   "mkdir -p \"$DUCKLAB_RENDER_OUTPUT\" && printf '\\211PNG\\r\\n\\032\\n' > \"$DUCKLAB_RENDER_OUTPUT/one.png\"",
		Artifacts: ".ducklab-render-captures/*.png", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || captures[0] != "one.png" {
		t.Fatalf("captures = %#v", captures)
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
	captures, err := captureRender(context.Background(), root, config.RenderContract{
		Command:   "mkdir -p captures && printf '\\211PNG\\r\\n\\032\\n' > captures/dirty.png && exit 1",
		Artifacts: "captures/*.png", TimeoutS: 5,
	}, writer, "render-run", "demo")
	if err == nil {
		t.Fatal("dirty render did not report its exit")
	}
	if len(captures) != 1 || captures[0] != "dirty.png" {
		t.Fatalf("captures = %#v", captures)
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
