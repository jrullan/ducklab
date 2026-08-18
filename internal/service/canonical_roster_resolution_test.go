package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// Global mode seats fill an unpinned role, while a project pin replaces that
// role's whole list. Roles with neither inherit the ordinary global fallback;
// callers need the exact provenance to explain each result.
func TestRosterGetResolvesModeSeatsProjectPinsAndGlobalFallbackWithProvenance(t *testing.T) {
	s := writableService(t, "global-implementer", "global-reviewer", "fallback")
	projectID, dir := projectWithConfig(t, s, "canonical-roster")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings: map[string][]string{
			"pair": {"global-implementer", "global-reviewer"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	project, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	project.Roster[config.RoleReviewer] = "project-reviewer"
	if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), project); err != nil {
		t.Fatal(err)
	}

	view, err := s.RosterGet(context.Background(), projectID, "pair")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RosterEntry{}
	for _, entry := range view.Entries {
		got[entry.Role] = entry
	}
	for role, want := range map[string]RosterEntry{
		"implementer": {Duckling: "global-implementer", Source: "global mode seat"},
		"reviewer":    {Duckling: "project-reviewer", Source: "project pin"},
		"triager":     {Duckling: "", Source: "unseated"},
		"scribe":      {Duckling: "", Source: "unseated"},
	} {
		entry, ok := got[role]
		if !ok {
			t.Errorf("%s missing from roster", role)
			continue
		}
		if entry.Duckling != want.Duckling || entry.Source != want.Source {
			t.Errorf("%s = %+v, want duckling %q from %q", role, entry, want.Duckling, want.Source)
		}
	}
}
