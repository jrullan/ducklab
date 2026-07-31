package service

import "testing"

// A combination of models that works is a finding, and re-ticking the same boxes
// on every run is how a finding gets lost.
func TestAModesLineUpIsUsedWhenARunNamesNone(t *testing.T) {
	s := writableService(t, "pato-atom", "pato-sonnet")

	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"pair": {"pato-sonnet", "pato-atom"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Order is the whole point: pair takes the first as implementer and the
	// second as reviewer, and tournament and split assign positionally.
	got := s.ducklingsFor("pair", nil)
	if len(got) != 2 || got[0] != "pato-sonnet" || got[1] != "pato-atom" {
		t.Errorf("line-up = %v", got)
	}
	// A run that named its own outranks the preference.
	if got := s.ducklingsFor("pair", []string{"pato-atom"}); len(got) != 1 || got[0] != "pato-atom" {
		t.Errorf("the run's own choice was overridden: %v", got)
	}
	// And a mode with no preference leaves the roster to decide.
	if got := s.ducklingsFor("solo", nil); len(got) != 0 {
		t.Errorf("solo = %v, want nothing", got)
	}
}

// Keeping it would make the preference look saved and then quietly do nothing on
// the next run.
func TestALineUpNamingAMissingDucklingIsRefused(t *testing.T) {
	s := writableService(t, "pato-atom")
	err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"pair": {"pato-atom", "pato-ghost"}},
	})
	if err == nil {
		t.Fatal("a line-up naming a duckling that does not exist was accepted")
	}
	if got := s.ModeDefaults().Ducklings["pair"]; len(got) != 0 {
		t.Errorf("the rejected line-up was saved: %v", got)
	}
}

// The caller must not be able to mutate what the service holds.
func TestTheLineUpIsCopiedOut(t *testing.T) {
	s := writableService(t, "pato-atom", "pato-sonnet")
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"split": {"pato-atom", "pato-sonnet"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := s.ducklingsFor("split", nil)
	got[0] = "tampered"
	if again := s.ducklingsFor("split", nil); again[0] != "pato-atom" {
		t.Errorf("the stored line-up was mutated through a returned slice: %v", again)
	}
}
