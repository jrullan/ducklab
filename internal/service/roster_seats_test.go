package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// The roster's two scopes share one shape (B-068) and one honesty rule
// (B-063): a pin made on a mode's column pins that mode only; a role pin is
// mode-independent; a seat nobody filled stays empty; a launch with an empty
// required seat is refused with the seat named. These are the service-level
// tests T-062 owed (B-065).

func seatOf(t *testing.T, s *Service, projectID, mode, role string) RosterEntry {
	t.Helper()
	view, err := s.RosterGet(context.Background(), projectID, mode)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range view.Entries {
		if e.Role == role {
			return e
		}
	}
	t.Fatalf("no %s entry for %s", role, mode)
	return RosterEntry{}
}

func TestAProjectPinMadeForOneModePinsThatModeOnly(t *testing.T) {
	s := writableService(t, "luna", "terra", "glm52", "atom-local")
	projectID, dir := projectWithConfig(t, s, "per-mode")
	if err := s.ModeDefaultsSet(ModeDefaultsView{AgentMaxTurns: 24, Ducklings: map[string][]string{
		"solo": {"terra"}, "pair": {"luna", "glm52"},
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RosterSetManyMode(context.Background(), projectID, "solo", "implementer", []string{"atom-local"}); err != nil {
		t.Fatal(err)
	}
	solo := seatOf(t, s, projectID, "solo", "implementer")
	if solo.Duckling != "atom-local" || solo.Source != "project mode seat" {
		t.Errorf("solo implementer = %+v, want atom-local from project mode seat", solo)
	}
	if solo.Default != "terra" {
		t.Errorf("the overridden global value should be terra, got %q", solo.Default)
	}
	pair := seatOf(t, s, projectID, "pair", "implementer")
	if pair.Duckling != "luna" || pair.Source != "global mode seat" {
		t.Errorf("pair implementer must be untouched by solo's pin, got %+v", pair)
	}

	// The pin lives in project.toml under mode_seats, not as a role pin.
	proj, err := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := proj.ModeSeats["solo"]["implementer"]; len(got) != 1 || got[0] != "atom-local" {
		t.Errorf("project.toml mode_seats.solo.implementer = %v", got)
	}
	if proj.Roster["implementer"] != "" || len(proj.RosterSeats["implementer"]) != 0 {
		t.Errorf("a mode pin must not create a role pin: %v %v", proj.Roster, proj.RosterSeats)
	}

	// Unpinning on the mode's column removes that mode's seat and the seat
	// inherits Global again — for that mode only.
	if _, err := s.RosterUnpin(context.Background(), projectID, "solo", "implementer"); err != nil {
		t.Fatal(err)
	}
	if solo := seatOf(t, s, projectID, "solo", "implementer"); solo.Duckling != "terra" || solo.Source != "global mode seat" {
		t.Errorf("after unpin solo implementer = %+v, want terra inherited", solo)
	}
}

func TestARolePinIsModeIndependentAndAGlobalSetNeverTouchesTheProject(t *testing.T) {
	s := writableService(t, "luna", "terra", "k3")
	projectID, dir := projectWithConfig(t, s, "role-pin")
	// No mode: a role pin — every mode that seats the role gets it.
	if _, err := s.RosterSetManyMode(context.Background(), projectID, "", "advisor", []string{"k3"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"solo", "pair", "split"} {
		if e := seatOf(t, s, projectID, mode, "advisor"); e.Duckling != "k3" || e.Source != "project pin" {
			t.Errorf("%s advisor = %+v, want k3 from project pin", mode, e)
		}
	}
	before, _ := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if _, err := s.GlobalRosterSet(context.Background(), "pair", "implementer", []string{"luna"}); err != nil {
		t.Fatal(err)
	}
	after, _ := config.LoadProject(filepath.Join(dir, ".ducklab", "project.toml"))
	if len(after.ModeSeats) != len(before.ModeSeats) || len(after.Roster) != len(before.Roster) {
		t.Errorf("a global set changed project.toml: before %v/%v after %v/%v", before.ModeSeats, before.Roster, after.ModeSeats, after.Roster)
	}
	if e := seatOf(t, s, projectID, "pair", "implementer"); e.Duckling != "luna" || e.Source != "global mode seat" {
		t.Errorf("pair implementer = %+v, want luna from global mode seat", e)
	}
}

func TestRosterWritesRejectWithTheFieldNamed(t *testing.T) {
	s := writableService(t, "luna", "terra")
	projectID, _ := projectWithConfig(t, s, "validation")
	if _, err := s.RosterSetManyMode(context.Background(), projectID, "pair", "reviewer", []string{"nobody"}); err == nil || !strings.Contains(err.Error(), "nobody") || !strings.Contains(err.Error(), "next") {
		t.Errorf("unknown duckling not named with a next: %v", err)
	}
	if _, err := s.RosterSetManyMode(context.Background(), projectID, "pair", "chef", []string{"luna"}); err == nil || !strings.Contains(err.Error(), "role") {
		t.Errorf("invalid role not named: %v", err)
	}
	// Cardinality warns at write and refuses at launch: a split or a
	// tournament is assembled one seat at a time, so the first pin must land.
	view, err := s.RosterSetManyMode(context.Background(), projectID, "split", "implementer", []string{"luna"})
	if err != nil {
		t.Fatalf("the first worker must be accepted at write: %v", err)
	}
	if !strings.Contains(view.Warning, "two workers") {
		t.Errorf("the board must be told the launch will refuse: %q", view.Warning)
	}
	// Global writes follow the same rule: the first contestant lands, the
	// view warns, the second one clears the note.
	view, err = s.GlobalRosterSet(context.Background(), "tournament", "implementer", []string{"luna"})
	if err != nil {
		t.Fatalf("the first global contestant must be accepted at write: %v", err)
	}
	if !strings.Contains(view.Warning, "two contestants") {
		t.Errorf("global view must warn about the launch rule: %q", view.Warning)
	}
	view, err = s.GlobalRosterSet(context.Background(), "tournament", "implementer", []string{"luna", "terra"})
	if err != nil || strings.Contains(view.Warning, "two contestants") {
		t.Errorf("two contestants must clear the note: %v %q", err, view.Warning)
	}
}

func TestASeatNobodyFilledStaysEmptyOnceAnythingIsConfigured(t *testing.T) {
	s := writableService(t, "atom-local", "luna", "terra")
	projectID, _ := projectWithConfig(t, s, "no-advisor")
	if err := s.ModeDefaultsSet(ModeDefaultsView{AgentMaxTurns: 24, Ducklings: map[string][]string{"solo": {"terra"}}}); err != nil {
		t.Fatal(err)
	}
	// Seats are configured, the advisor is not: it must resolve to nobody —
	// not to atom-local, the alphabetically first duckling (B-063).
	if e := seatOf(t, s, projectID, "solo", "advisor"); e.Duckling != "" || e.Source != "unseated" {
		t.Errorf("solo advisor = %+v, want empty/unseated", e)
	}
	// And a required seat nobody filled refuses the launch, naming the seat.
	proj := &config.Project{}
	roster, _ := s.resolveRoster(proj, "pair")
	if err := unseatedRequired("pair", roster); err == nil || !strings.Contains(err.Error(), "implementer") || !strings.Contains(err.Error(), "Roster board") {
		t.Errorf("pair with no implementer seated must refuse naming the seat: %v", err)
	}
}

func TestABlankInstallStillRunsButNeverInventsAnAdvisor(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithConfig(t, s, "blank")
	impl := seatOf(t, s, projectID, "solo", "implementer")
	if impl.Duckling == "" || impl.Source != "engine picked (no seats configured)" {
		t.Errorf("blank install must still seat an implementer and say so: %+v", impl)
	}
	if adv := seatOf(t, s, projectID, "solo", "advisor"); adv.Duckling != "" {
		t.Errorf("a duck nobody asked for must stay empty: %+v", adv)
	}
}
