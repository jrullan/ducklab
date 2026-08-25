package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

func chatStartProject(t *testing.T, s *Service, name string) (string, string) {
	t.Helper()
	id, dir := projectWithConfig(t, s, name)
	cfg := config.DefaultProject(id, name)
	cfg.Verify.Mode = "tests"
	cfg.Verify.Tests = "gh pr list"
	if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

func chatEvents(t *testing.T, dir, runID string) []*runlog.Event {
	t.Helper()
	events, err := runlog.ReadEvents(filepath.Join(dir, ".ducklab", "runs", runID))
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestChatStartEmitsConfigAmendmentForDoctorFinding(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := chatStartProject(t, s, "finding")
	if err := os.MkdirAll(filepath.Join(dir, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend", "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := s.ChatStart(context.Background(), projectID, ChatStartRequest{
		Duckling: "pato-uno", Message: "what should I fix?",
	})
	if err != nil {
		t.Fatal(err)
	}

	var amendments []*runlog.Event
	for _, event := range chatEvents(t, dir, run.ID) {
		if event.Type == "config_amendment" {
			amendments = append(amendments, event)
		}
	}
	if len(amendments) != 1 {
		t.Fatalf("config_amendment events = %d, want 1", len(amendments))
	}
	data := amendments[0].Data
	for key, want := range map[string]string{
		"key": "verify.link_deps",
		"old": "",
		"new": "frontend/node_modules",
		"why": "frontend is present but its node_modules is not linked into acceptance checkouts",
	} {
		if got := data[key]; got != want {
			t.Errorf("amendment %s = %v, want %q", key, got, want)
		}
	}
}

func TestChatStartEmitsNoConfigAmendmentForCleanProject(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := chatStartProject(t, s, "clean")

	run, err := s.ChatStart(context.Background(), projectID, ChatStartRequest{
		Duckling: "pato-uno", Message: "does everything look right?",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range chatEvents(t, dir, run.ID) {
		if event.Type == "config_amendment" {
			t.Fatalf("clean chat emitted config_amendment: %+v", event.Data)
		}
	}
}
