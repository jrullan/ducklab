package service

import (
	"strings"
	"testing"
)

// The round counts lived inside the scripts — pair three, council two,
// tournament one — so changing how many times a reviewer got to push back meant
// editing Go and rebuilding.
func TestRoundsPerModeCanBeSet(t *testing.T) {
	s := writableService(t, "pato-uno")

	before := s.ModeDefaults()
	if before.ScriptRounds["pair"] != 3 {
		t.Fatalf("the script's own count is not reported: %+v", before.ScriptRounds)
	}
	if got := s.roundsFor("pair", 0); got != 0 {
		t.Errorf("unset should leave the script alone, got %d", got)
	}

	if err := s.ModeDefaultsSet(ModeDefaultsView{
		Rounds: map[string]int{"pair": 5}, AgentMaxTurns: 24,
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.roundsFor("pair", 0); got != 5 {
		t.Errorf("roundsFor(pair) = %d, want the configured 5", got)
	}
	// A run that asked for a count outranks the default.
	if got := s.roundsFor("pair", 2); got != 2 {
		t.Errorf("the request was overridden: %d", got)
	}
	// And a mode nobody configured still defers to its script.
	if got := s.roundsFor("tournament", 0); got != 0 {
		t.Errorf("tournament = %d, want 0", got)
	}
}

// Zero is how a mode says "use the script's own count", so it must round-trip
// as absent rather than as a mode that runs no rounds at all.
func TestZeroRoundsMeansTheScriptDecides(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		Rounds: map[string]int{"pair": 0}, AgentMaxTurns: 24,
	}); err != nil {
		t.Fatal(err)
	}
	if _, stored := s.ModeDefaults().Rounds["pair"]; stored {
		t.Error("zero was stored as a real count")
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	err := s.ModeDefaultsSet(ModeDefaultsView{Rounds: map[string]int{"duet": 2}, AgentMaxTurns: 24})
	if err == nil || !strings.Contains(err.Error(), "duet") {
		t.Errorf("err = %v", err)
	}
}

// A run that can take fifty passes over every participant is a run nobody is
// supervising, and a zero-turn cap would stop every run before its first call.
func TestTheLimitsAreBounded(t *testing.T) {
	s := writableService(t, "pato-uno")
	if err := s.ModeDefaultsSet(ModeDefaultsView{Rounds: map[string]int{"pair": 50}, AgentMaxTurns: 24}); err == nil {
		t.Error("fifty rounds was accepted")
	}
	if err := s.ModeDefaultsSet(ModeDefaultsView{AgentMaxTurns: 0}); err == nil {
		t.Error("a zero per-turn cap was accepted")
	}
}
