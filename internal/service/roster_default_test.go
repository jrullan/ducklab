package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func rosterEntry(t *testing.T, v *RosterView, role string) RosterEntry {
	t.Helper()
	for _, e := range v.Entries {
		if e.Role == role {
			return e
		}
	}
	t.Fatalf("no %s entry in %+v", role, v.Entries)
	return RosterEntry{}
}

// B-557: one duckling for every role the project does not pin. The project's
// own pins and mode seats are the exceptions; the global seats lose to it;
// the record names it as its own source.
func TestTheProjectDefaultSeatsEveryRoleThatIsNotPinned(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm", "luna")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{"pair": {"implementer": {"glm"}, "reviewer": {"glm"}}}
	id, _ := projectWithDocs(t, s, nil)

	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RosterSet(ctx, id, "scribe", "luna"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RosterSetManyMode(ctx, id, "pair", "advisor", []string{"glm"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.RosterGet(ctx, id, "pair")
	if err != nil {
		t.Fatal(err)
	}
	if v.ProjectDefault != "terra" {
		t.Errorf("project_default = %q", v.ProjectDefault)
	}
	for role, want := range map[string][2]string{
		"implementer": {"terra", "project default"}, // over the global pair seat
		"reviewer":    {"terra", "project default"},
		"triager":     {"terra", "project default"},
		"scribe":      {"luna", "project pin"},
		"advisor":     {"glm", "project mode seat"},
	} {
		e := rosterEntry(t, v, role)
		if e.Duckling != want[0] || e.Source != want[1] {
			t.Errorf("%s = %s (%s), want %s (%s)", role, e.Duckling, e.Source, want[0], want[1])
		}
	}
	// One model on both sides of the pair is exactly what a comparison has
	// to measure, so the warning stays.
	if !strings.Contains(v.Warning, "terra is on both sides of the pair") {
		t.Errorf("no both-sides warning: %q", v.Warning)
	}
}

// Set, persist, clear: the default lives in project.toml and unpinning the
// "default" role removes it, after which the global seats answer again.
func TestTheProjectDefaultPersistsAndClears(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{"solo": {"implementer": {"glm"}}}
	id, dir := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "common", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil || cfg.RosterDefault != "terra" {
		raw, _ := os.ReadFile(filepath.Join(dir, ".ducklab", "project.toml"))
		t.Fatalf("persisted default = %q (%v)\n%s", cfg.RosterDefault, err, raw)
	}
	v, err := s.RosterUnpin(ctx, id, "", RosterDefaultRole)
	if err != nil {
		t.Fatal(err)
	}
	if v.ProjectDefault != "" {
		t.Errorf("default still %q after unpin", v.ProjectDefault)
	}
	v, _ = s.RosterGet(ctx, id, "solo")
	if e := rosterEntry(t, v, "implementer"); e.Duckling != "glm" || e.Source != "global mode seat" {
		t.Errorf("after clearing: implementer = %s (%s)", e.Duckling, e.Source)
	}
}

// The default is one registered duckling for every mode.
func TestTheProjectDefaultRefusesWhatItCannotMean(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm")
	ctx := context.Background()
	id, _ := projectWithDocs(t, s, nil)
	for name, call := range map[string]func() error{
		"unknown duckling": func() error {
			_, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"nobody"})
			return err
		},
		"two ducklings": func() error {
			_, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra", "glm"})
			return err
		},
		"one mode": func() error {
			_, err := s.RosterSetManyMode(ctx, id, "pair", RosterDefaultRole, []string{"terra"})
			return err
		},
	} {
		if call() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A project default is a configured seat: the blank-install spreading (the
// engine picks a distinct duckling per role) must not run, so the seats the
// default does not fill — the judge — stay empty instead of being picked.
func TestAProjectDefaultStopsTheEngineFromPicking(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = nil
	s.cfg.Defaults.RolePins = nil
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"glm"}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.RosterGet(ctx, id, "tournament")
	if e := rosterEntry(t, v, "judge"); e.Duckling != "" || e.Source != "unseated" {
		t.Errorf("judge = %s (%s), want unseated: the engine picked despite a configured default", e.Duckling, e.Source)
	}
}

// Review of B-557: the default fills single seats only. The advisor and the
// judge keep their own configuration, and the multi-slot participant lists
// (council critics, split workers, tournament contestants) keep theirs —
// one default cannot give them distinct ducklings. The board's card lists
// (Ducklings) follow the same rule as the resolver.
func TestTheProjectDefaultLeavesAdvisorJudgeAndParticipantListsAlone(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm", "luna")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council":    {"reviewer": {"glm", "luna"}},
		"tournament": {"implementer": {"glm", "luna"}, "judge": {"luna"}},
		"pair":       {"advisor": {"luna"}},
	}
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	check := func(mode, role, duckling, source string, ids ...string) {
		t.Helper()
		v, err := s.RosterGet(ctx, id, mode)
		if err != nil {
			t.Fatal(err)
		}
		e := rosterEntry(t, v, role)
		if e.Duckling != duckling || e.Source != source || strings.Join(e.Ducklings, ",") != strings.Join(ids, ",") {
			t.Errorf("%s %s = %s (%s) %v, want %s (%s) %v", mode, role, e.Duckling, e.Source, e.Ducklings, duckling, source, ids)
		}
	}
	check("council", "reviewer", "glm", "global mode seat", "glm", "luna")
	check("council", "architect", "terra", "project default", "terra")
	check("tournament", "implementer", "glm", "global mode seat", "glm", "luna")
	check("tournament", "judge", "luna", "global mode seat", "luna")
	check("pair", "advisor", "luna", "global mode seat", "luna")
	check("pair", "implementer", "terra", "project default", "terra")
	check("solo", "implementer", "terra", "project default", "terra")
}

// A default naming a duckling that is no longer registered is ignored with a
// warning: the seats fall back to the global ones instead of every role
// resolving to a model that cannot run.
func TestAnUnregisteredProjectDefaultFallsBackWithAWarning(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{"solo": {"implementer": {"glm"}}}
	id, dir := projectWithDocs(t, s, nil)
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RosterDefault = "removed-model"
	if err := writeProjectTOML(path, cfg); err != nil {
		t.Fatal(err)
	}
	v, err := s.RosterGet(ctx, id, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if e := rosterEntry(t, v, "implementer"); e.Duckling != "glm" || e.Source != "global mode seat" {
		t.Errorf("implementer = %s (%s), want the global seat", e.Duckling, e.Source)
	}
	if !strings.Contains(v.Warning, "removed-model is not a registered duckling") {
		t.Errorf("no warning: %q", v.Warning)
	}
}

// A pinned seat's Default is what unpinning it gives — here the project
// default, not the global seat (second review of B-557: the tooltip named a
// fallback the unpin would not produce).
func TestAPinnedSeatsDefaultIsWhatUnpinningGives(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm", "luna")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{"solo": {"implementer": {"glm"}}}
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RosterSet(ctx, id, "implementer", "luna"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.RosterGet(ctx, id, "solo")
	if e := rosterEntry(t, v, "implementer"); e.Duckling != "luna" || e.Default != "terra" {
		t.Errorf("implementer = %s, default %q, want luna over the project default terra", e.Duckling, e.Default)
	}
}

// Second review of B-557: the default fills only the single seats a mode
// uses. A tournament's and a split's unused reviewer stay as configured (the
// tournament took implementer+reviewer as contestants), a solo stage's
// reviewer is left to the documents council seat instead of the drafter, and
// a council architect is not the default when the default is a critic.
func TestTheProjectDefaultFillsOnlySeatsTheModeUses(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm", "luna")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council": {"architect": {"luna"}, "reviewer": {"terra", "glm"}},
		"split":   {"implementer": {"glm", "luna"}},
	}
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	seat := func(mode, role string) RosterEntry {
		t.Helper()
		v, err := s.RosterGet(ctx, id, mode)
		if err != nil {
			t.Fatal(err)
		}
		return rosterEntry(t, v, role)
	}
	for _, c := range []struct{ mode, role string }{
		{"tournament", "reviewer"}, {"split", "reviewer"}, {"solo", "reviewer"},
	} {
		if e := seat(c.mode, c.role); e.Source == "project default" {
			t.Errorf("%s %s filled by the default", c.mode, c.role)
		}
	}
	if e := seat("split", "implementer"); strings.Join(e.Ducklings, ",") != "glm,luna" || e.Source == "project default" {
		t.Errorf("split workers = %v (%s), want the global list", e.Ducklings, e.Source)
	}
	if e := seat("council", "architect"); e.Duckling != "luna" {
		t.Errorf("council architect = %s (%s): the default is a critic and must not draft", e.Duckling, e.Source)
	}
	if e := seat("solo", "architect"); e.Duckling != "terra" || e.Source != "project default" {
		t.Errorf("solo architect = %s (%s)", e.Duckling, e.Source)
	}
}

// An unregistered default does not count as a configured seat: on a blank
// install the engine still picks instead of every launch refusing.
func TestAnUnregisteredDefaultKeepsBlankInstallPicking(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = nil
	s.cfg.Defaults.RolePins = nil
	id, dir := projectWithDocs(t, s, nil)
	path := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, _ := config.LoadProject(path)
	cfg.RosterDefault = "gone"
	if err := writeProjectTOML(path, cfg); err != nil {
		t.Fatal(err)
	}
	v, _ := s.RosterGet(ctx, id, "solo")
	if e := rosterEntry(t, v, "implementer"); e.Duckling == "" || e.Source != "engine picked (no seats configured)" {
		t.Errorf("implementer = %s (%s), want an engine pick", e.Duckling, e.Source)
	}
}

// The tournament races what the board shows: with no ducklings in the
// request, the tournament implementer list — not implementer + reviewer.
func TestATournamentRacesTheBoardsContestants(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "glm", "luna")
	ctx := context.Background()
	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{"tournament": {"implementer": {"glm", "luna"}, "judge": {"terra"}}}
	id, _ := projectWithDocs(t, s, nil)
	if _, err := s.RosterSetManyMode(ctx, id, "", RosterDefaultRole, []string{"terra"}); err != nil {
		t.Fatal(err)
	}
	projCfg, err := s.projectConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	roster, _ := s.resolveCanonicalRoster(projCfg, "tournament")
	if got := s.tournamentContestants(nil, projCfg, roster); len(got) != 2 || got[0] != "glm" || got[1] != "luna" {
		t.Errorf("contestants = %v, want the board's [glm luna]", got)
	}
	if got := s.tournamentContestants([]string{"terra", "luna"}, projCfg, roster); len(got) != 2 || got[0] != "terra" {
		t.Errorf("requested contestants not honoured: %v", got)
	}
}
