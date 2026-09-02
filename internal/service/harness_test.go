package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestHarnessProfileIsResolvedPersistedAndEmittedOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname='fixture'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{ID: "r-profile", ProjectID: "fixture", Stage: "build", Mode: "pair", TaskID: "T-004"}
	writer, err := runlog.NewWriter(root, run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	rs := &runState{run: run, writer: writer}
	project := config.DefaultProject("fixture", "Fixture")
	project.Verify.Mode = "custom"
	project.Verify.Custom = "cargo test"

	first, err := ensureHarnessProfile(rs, root, project, "cc -fsyntax-only capture.c")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureHarnessProfile(rs, root, project, "this changed but a resume must not re-resolve")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, "rust (Cargo.toml)") || !strings.Contains(first, "cc -fsyntax-only capture.c") {
		t.Fatalf("capsule was not stable or complete:\n%s", first)
	}
	state, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", run.ID, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `"harness_profile"`) || !strings.Contains(string(state), `"c-native"`) {
		t.Fatalf("profile not persisted:\n%s", state)
	}
	events, err := os.ReadFile(filepath.Join(root, ".ducklab", "runs", run.ID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(events), `"type":"capabilities_resolved"`); got != 1 {
		t.Fatalf("capability event count = %d, want 1:\n%s", got, events)
	}
}
