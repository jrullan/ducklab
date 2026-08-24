package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"

	"github.com/jrullan/ducklab/internal/artifact"
)

// serviceWithDucklings builds a service with named ducklings configured.
func serviceWithDucklings(t *testing.T, ids ...string) *Service {
	t.Helper()
	isolate(t)
	cfg := config.DefaultGlobal()
	cfg.Providers = map[config.ProviderID]config.Provider{
		"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"},
	}
	cfg.Ducklings = map[config.DucklingID]config.Duckling{}
	for i, id := range ids {
		cfg.Ducklings[config.DucklingID(id)] = config.Duckling{
			Provider: "fake", Model: "m-" + id,
			Cost: config.Cost{OutputPerMTok: float64(i)},
		}
	}
	s, err := New(cfg, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// projectWithConfig creates a registered project with a real project.toml.
func projectWithConfig(t *testing.T, s *Service, name string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".ducklab", "runs"), 0o755)
	id, err := s.registry.Register(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Save()

	cfg := config.DefaultProject(id, name)
	if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

// The roster must report what will actually be used, including roles the
// project never declared — otherwise it looks emptier than the runs behave.
func TestRosterSuggestUsesDocumentAcceptanceAndBuildVerdicts(t *testing.T) {
	s := serviceWithDucklings(t, "scribe", "implementer")
	projectID, _ := projectWithConfig(t, s, "suggestions")
	for i := range 7 {
		s.runs[fmt.Sprintf("release-%d", i)] = &runState{run: &runlog.Run{
			ID: fmt.Sprintf("release-%d", i), ProjectID: projectID,
			Stage: "release", Verdict: "UNVERIFIED", Accepted: true,
			Roster: map[string]string{"scribe": "scribe"},
		}}
	}
	// This revision replaced the preceding document, so it is neutral evidence.
	s.runs["superseded"] = &runState{run: &runlog.Run{
		ID: "superseded", ProjectID: projectID, Stage: "release",
		Verdict: "UNVERIFIED", Resolution: "superseded",
		Roster: map[string]string{"scribe": "scribe"},
	}}
	for i := range 8 {
		s.runs[fmt.Sprintf("build-%d", i)] = &runState{run: &runlog.Run{
			ID: fmt.Sprintf("build-%d", i), ProjectID: projectID,
			Stage: "build", Verdict: "FAILED", Accepted: true,
			Roster: map[string]string{"implementer": "implementer"},
		}}
	}

	suggestions, err := s.RosterSuggest(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, suggestion := range suggestions {
		switch suggestion.Role {
		case "scribe":
			if suggestion.Duckling != "scribe" || suggestion.Runs != 7 || suggestion.PassRate != 100 {
				t.Errorf("scribe suggestion = %+v, want 7 accepted releases at 100%%", suggestion)
			}
		case "implementer":
			if suggestion.Duckling != "implementer" || suggestion.Runs != 8 || suggestion.PassRate != 0 {
				t.Errorf("implementer suggestion = %+v, want 0/8 green gates", suggestion)
			}
		}
	}
}

func TestRosterGetShowsResolvedAssignmentsAndSource(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithConfig(t, s, "proj")

	view, err := s.RosterGet(context.Background(), projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) == 0 {
		t.Fatal("no roster entries")
	}
	// A blank installation: nobody seated anywhere. The engine picks a
	// duckling per role and says so — except the advisor, a duck nobody
	// asked for stays empty (B-063).
	for _, e := range view.Entries {
		if e.Role == "advisor" {
			if e.Duckling != "" || e.Source != "unseated" {
				t.Errorf("advisor must stay empty on a blank install, got %+v", e)
			}
			continue
		}
		if e.Duckling == "" {
			t.Errorf("role %q resolved to nothing", e.Role)
		}
		if e.Source != "engine picked (no seats configured)" {
			t.Errorf("role %q source = %q, want engine picked (no seats configured)", e.Role, e.Source)
		}
	}
}

func TestRosterGetReportsGlobalFallbackForProjectPin(t *testing.T) {
	s := serviceWithDucklings(t, "terra", "z-luna")
	projectID, dir := projectWithConfig(t, s, "ducklab")
	cfg, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Roster[config.RoleImplementer] = "z-luna"
	if err := writeProjectTOML(filepath.Join(dir, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}

	view, err := s.RosterGet(context.Background(), projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range view.Entries {
		if entry.Role == "implementer" {
			if entry.Duckling != "z-luna" || entry.Source != "project pin" || entry.Default != "z-luna" {
				t.Fatalf("implementer entry = %+v, want luna/project with terra default", entry)
			}
			return
		}
	}
	t.Fatal("implementer missing from roster")
}

func TestRosterSetPersistsAndIsReportedAsProjectSource(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, dir := projectWithConfig(t, s, "proj")

	view, err := s.RosterSet(context.Background(), projectID, "reviewer", "pato-dos")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range view.Entries {
		if e.Role == "reviewer" {
			found = true
			if e.Duckling != "pato-dos" {
				t.Errorf("reviewer = %q, want pato-dos", e.Duckling)
			}
			if e.Source != "project pin" {
				t.Errorf("source = %q, want project", e.Source)
			}
		}
	}
	if !found {
		t.Fatal("reviewer missing from the roster")
	}

	// It must be on disk, or it is lost on the next engine start.
	reloaded, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Roster[config.RoleReviewer] != "pato-dos" {
		t.Errorf("project.toml roster = %+v", reloaded.Roster)
	}
}

func TestRosterSetRejectsUnknownRoleAndDuckling(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, _ := projectWithConfig(t, s, "proj")

	if _, err := s.RosterSet(context.Background(), projectID, "wizard", "pato-uno"); err == nil {
		t.Error("unknown role accepted")
	}
	if _, err := s.RosterSet(context.Background(), projectID, "reviewer", "pato-fantasma"); err == nil {
		t.Error("unknown duckling accepted")
	}
}

// With one duckling, pair puts it on both sides. That is a legitimate
// experiment, but it must be surfaced rather than passing silently.
func TestRosterWarnsWhenOneDucklingPlaysBothSides(t *testing.T) {
	s := serviceWithDucklings(t, "pato-solo")
	projectID, _ := projectWithConfig(t, s, "proj")

	view, err := s.RosterGet(context.Background(), projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Warning == "" {
		t.Error("no warning when implementer and reviewer are the same duckling")
	}
}

func TestRosterNoWarningWithTwoDucklings(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithConfig(t, s, "proj")

	view, _ := s.RosterGet(context.Background(), projectID, "")
	if view.Warning != "" {
		t.Errorf("unexpected warning with two ducklings: %s", view.Warning)
	}
}

// suggest must rank from recorded evidence, and must be reproducible.
func TestRosterSuggestRanksByRecordedPassRate(t *testing.T) {
	s := serviceWithDucklings(t, "pato-bueno", "pato-malo")
	projectID, dir := projectWithConfig(t, s, "proj")

	// pato-bueno: 2/2 as implementer. pato-malo: 0/2.
	writeRunWithRoster(t, dir, projectID, "r-1", "PASSED", map[string]string{"implementer": "pato-bueno"})
	writeRunWithRoster(t, dir, projectID, "r-2", "PASSED", map[string]string{"implementer": "pato-bueno"})
	writeRunWithRoster(t, dir, projectID, "r-3", "FAILED", map[string]string{"implementer": "pato-malo"})
	writeRunWithRoster(t, dir, projectID, "r-4", "FAILED", map[string]string{"implementer": "pato-malo"})
	s.RecoverRuns(context.Background())

	sugg, err := s.RosterSuggest(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	var implementer *Suggestion
	for i := range sugg {
		if sugg[i].Role == "implementer" {
			implementer = &sugg[i]
		}
	}
	if implementer == nil {
		t.Fatal("no suggestion for implementer")
	}
	if implementer.Duckling != "pato-bueno" {
		t.Errorf("suggested %q, want pato-bueno (2/2 vs 0/2)", implementer.Duckling)
	}
	if implementer.Evidence == "" {
		t.Error("no evidence given for the recommendation")
	}
}

// A duckling with no history must not outrank one with a proven record.
func TestRosterSuggestPrefersEvidenceOverSilence(t *testing.T) {
	s := serviceWithDucklings(t, "pato-probado", "pato-nuevo")
	projectID, dir := projectWithConfig(t, s, "proj")

	writeRunWithRoster(t, dir, projectID, "r-1", "PASSED", map[string]string{"judge": "pato-probado"})
	s.RecoverRuns(context.Background())

	sugg, _ := s.RosterSuggest(context.Background(), projectID)
	for _, sg := range sugg {
		if sg.Role == "judge" && sg.Duckling != "pato-probado" {
			t.Errorf("judge = %q; a duckling with no runs outranked one with a record", sg.Duckling)
		}
	}
}

// Determinism: the same inputs must produce the same order every time.
func TestRosterSuggestIsReproducible(t *testing.T) {
	s := serviceWithDucklings(t, "pato-a", "pato-b", "pato-c")
	projectID, _ := projectWithConfig(t, s, "proj")

	first, err := s.RosterSuggest(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := s.RosterSuggest(context.Background(), projectID)
		if len(again) != len(first) {
			t.Fatalf("suggestion count changed: %d then %d", len(first), len(again))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("suggestion %d changed between identical calls: %+v vs %+v", j, first[j], again[j])
			}
		}
	}
}

func TestRosterApplyWritesEveryRole(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, dir := projectWithConfig(t, s, "proj")

	sugg, _ := s.RosterSuggest(context.Background(), projectID)
	if _, err := s.RosterApply(context.Background(), projectID, sugg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Roster) != len(sugg) {
		t.Errorf("wrote %d roles, suggested %d", len(reloaded.Roster), len(sugg))
	}
}

func writeRunWithRoster(t *testing.T, dir, projectID, id, verdict string, roster map[string]string) {
	t.Helper()
	run := &runlog.Run{
		ID: id, ProjectID: projectID, Mode: "solo", Status: "done",
		Verdict: verdict, Roster: roster,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
}

// Settings files the architect under DOCUMENTS — "architect · drafts", the
// council's first seat. A solo amendment used to read the TASKS' solo
// line-up instead: the person saved a new architect in the right place and
// the amendment's chip, and the run, kept the old one. Document stages seat
// from the documents group whatever their mode.
func TestASoloStageSeatsTheCouncilArchitect(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})

	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council": {"architect": {"pato-dos"}, "reviewer": {"pato-uno"}},
		"solo":    {"implementer": {"pato-uno"}},
	}

	view, err := s.RosterGet(context.Background(), id, "solo")
	if err != nil {
		t.Fatal(err)
	}
	arch := ""
	for _, e := range view.Entries {
		if e.Role == "architect" {
			arch = e.Duckling
		}
	}
	if arch != "pato-dos" {
		t.Errorf("solo stage architect = %q, want the documents seat pato-dos, not the tasks' solo pick", arch)
	}
}

// The chip is a door: a request naming its own seats overrides for THIS run
// alone — the team's saved seats stay untouched.
func TestAStageRequestSeatsItsOwnDucklings(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	_ = dir

	s.cfg.Defaults.ModeSeats = map[string]map[string][]string{
		"council": {"architect": {"pato-uno"}, "reviewer": {"pato-dos"}},
	}
	run, err := s.StageStart(context.Background(), id, StageRequest{
		Stage: "plan", Extend: "add a small thing", Ducklings: []string{"pato-dos"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The roster lands when the stage's executor picks the run up.
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	deadline := time.Now().Add(5 * time.Second)
	for len(rs.run.Roster) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rs.run.Roster["architect"] != "pato-dos" {
		t.Errorf("architect = %q, want the request's own pick pato-dos", rs.run.Roster["architect"])
	}
	s.RunAbort(context.Background(), run.ID)
	s.waitForRun(context.Background(), run.ID)
	// The saved seats did not move.
	if s.cfg.Defaults.ModeSeats["council"]["architect"][0] != "pato-uno" {
		t.Error("a per-run pick must never edit the saved seats")
	}
}
