package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/strategy"
)

// A triager used all six of its turns calling tools, never answered, and its own
// failure message told the reader to raise the turn cap for that role. There was
// nowhere to raise it: the caps were literals in five files — council 12, pair
// 24 and 8, triage 6 — and Settings had only the global fallback, which no turn
// ever used because every turn sets its own.
func TestARolesTurnCapCanBeRaised(t *testing.T) {
	s := writableService(t, "pato-uno")

	if got := s.turnsFor("triager", ScriptRoleTurns["triager"]); got != 6 {
		t.Fatalf("the script's own cap is not the fallback: %d", got)
	}
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"triager": 20},
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.turnsFor("triager", 6); got != 20 {
		t.Errorf("cap = %d, want the configured 20", got)
	}
	// A role nobody configured keeps the script's own.
	if got := s.turnsFor("reviewer", 8); got != 8 {
		t.Errorf("reviewer = %d, want 8", got)
	}
}

// Walking a script reaches four modes; tournament and split build their turns
// themselves. A setting that applies to some modes is worse than none.
func TestTheCapReachesEveryMode(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"implementer": 30, "reviewer": 3},
	}); err != nil {
		t.Fatal(err)
	}

	// Script-driven modes.
	pair := s.applyRoleTurns(strategy.PairScript())
	for _, turn := range pair.Turns {
		want := map[config.Role]int{config.RoleImplementer: 30, config.RoleReviewer: 3}[turn.Role]
		if want != 0 && turn.MaxTurns != want {
			t.Errorf("pair %s = %d, want %d", turn.Role, turn.MaxTurns, want)
		}
	}

	// And the ones that build their own.
	caps := s.roleTurnCaps()
	if got := strategy.CapFor(caps, config.RoleImplementer, 24); got != 30 {
		t.Errorf("tournament/split implementer = %d, want 30", got)
	}
	if got := strategy.CapFor(caps, config.RoleJudge, 1); got != 1 {
		t.Errorf("an unconfigured role lost its own cap: %d", got)
	}
}

func TestAnUnknownRoleIsRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"inspector": 5},
	})
	if err == nil || !strings.Contains(err.Error(), "inspector") {
		t.Errorf("err = %v", err)
	}
}

// Above forty a turn stops being bounded in any useful sense (I3).
func TestTheCapIsBounded(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24, RoleTurns: map[string]int{"implementer": 500},
	}); err == nil {
		t.Error("500 calls in one turn was accepted")
	}
}
