package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

func TestProjectUpdateDoesNotRecoverRunsFromOtherProjects(t *testing.T) {
	s := newTestService(t)
	first, err := s.ProjectInit(context.Background(), InitRequest{
		Path: t.TempDir(), Name: "Edited", GitInit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.ProjectInit(context.Background(), InitRequest{
		Path: t.TempDir(), Name: "Unrelated", GitInit: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// This run exists only on the unrelated project's disk. RecoverRuns would
	// discover it and rewrite queued -> paused, which makes a config PATCH on
	// the first project observably mutate the second project.
	writeRun(t, other.Path, other.ID, "r-unrelated-queued", "queued")
	if _, err := s.RunGet(context.Background(), "r-unrelated-queued"); err == nil {
		t.Fatal("fixture run unexpectedly existed in memory before the update")
	}

	updated, err := s.ProjectUpdate(context.Background(), first.ID, map[string]string{
		"autonomy": "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Autonomy != "auto" || updated.Config.Autonomy != config.AutonomyAuto {
		t.Fatalf("updated project = %+v", updated)
	}
	if _, err := s.RunGet(context.Background(), "r-unrelated-queued"); err == nil {
		t.Fatal("updating one project recovered an unrelated project's run")
	}
	diskRun, err := runlog.ReadState(filepath.Join(other.Path, ".ducklab", "runs", "r-unrelated-queued"))
	if err != nil {
		t.Fatal(err)
	}
	if diskRun.Status != "queued" {
		t.Fatalf("unrelated run status = %q, want queued", diskRun.Status)
	}
}

func TestProjectUpdateInvalidatesOnlyItsCachedProjectConfig(t *testing.T) {
	s := newTestService(t)
	project, err := s.ProjectInit(context.Background(), InitRequest{
		Path: t.TempDir(), Name: "Cached", GitInit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.remoteProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.cfg.Autonomy == config.AutonomyAuto {
		t.Fatal("fixture unexpectedly starts in auto autonomy")
	}
	if _, err := s.ProjectUpdate(context.Background(), project.ID, map[string]string{"autonomy": "auto"}); err != nil {
		t.Fatal(err)
	}
	after, err := s.remoteProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after == before || after.cfg.Autonomy != config.AutonomyAuto {
		t.Fatalf("project cache was not refreshed: before=%p after=%p config=%+v", before, after, after.cfg)
	}
}
