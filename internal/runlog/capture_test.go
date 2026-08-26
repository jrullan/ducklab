package runlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCaptureStoresBytesAndRejectsPaths(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, &Run{ID: "r1", ProjectID: "p", StartedAt: "now"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.WriteCapture("scene.png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".ducklab", "runs", "r1", "captures", "scene.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "png" {
		t.Fatalf("got %q", got)
	}
	if err := w.WriteCapture("../secret", []byte("x")); err == nil {
		t.Fatal("path escape accepted")
	}
}
