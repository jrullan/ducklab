package service

import "testing"

// A Settings line-up is a saved choice, not an implicit override of the roster.
func TestAModesLineUpRequiresAnExplicitRunChoice(t *testing.T) {
	s := writableService(t, "pato-atom", "pato-sonnet")

	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"pair": {"pato-sonnet", "pato-atom"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Omitted seats leave the resolved roster in charge, even when Settings
	// remembers a different line-up for the mode.
	if got := s.ducklingsFor("pair", nil); len(got) != 0 {
		t.Errorf("omitted seats = %v, want roster", got)
	}
	// A Settings line-up becomes an override only when a caller explicitly sends it.
	if got := s.ducklingsFor("pair", []string{"pato-sonnet", "pato-atom"}); len(got) != 2 || got[0] != "pato-sonnet" || got[1] != "pato-atom" {
		t.Errorf("explicit line-up = %v", got)
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
	got := s.ducklingsFor("split", []string{"pato-atom", "pato-sonnet"})
	got[0] = "tampered"
	if again := s.ducklingsFor("split", []string{"pato-atom", "pato-sonnet"}); again[0] != "pato-atom" {
		t.Errorf("the explicit line-up was mutated through a returned slice: %v", again)
	}
}

// A line-up longer than the mode's chairs used to save fine and silently seat
// nobody past the limit — which reads later as "my setting did not take". The
// engine refuses it at save, and reports each mode's capacity so a client can
// stop the extra box being ticked at all.
func TestALineupCannotOutnumberTheModesSeats(t *testing.T) {
	s := writableService(t, "pato-uno", "pato-dos", "pato-tres")
	for mode, over := range map[string][]string{
		"solo": {"pato-uno", "pato-dos"},
		"pair": {"pato-uno", "pato-dos", "pato-tres"},
	} {
		err := s.ModeDefaultsSet(ModeDefaultsView{
			AgentMaxTurns: 24,
			Ducklings:     map[string][]string{mode: over},
		})
		if err == nil {
			t.Errorf("%s accepted %d ducklings", mode, len(over))
		}
	}
	// Council has no ceiling any more: one drafts, the rest critique.
	if err := s.ModeDefaultsSet(ModeDefaultsView{
		AgentMaxTurns: 24,
		Ducklings:     map[string][]string{"council": {"pato-uno", "pato-dos", "pato-tres"}},
	}); err != nil {
		t.Errorf("council refused three ducklings: %v", err)
	}
	if got := s.ModeDefaults().Seats; got["solo"] != 1 || got["pair"] != 2 || got["council"] != 0 {
		t.Errorf("seats reported as %v", got)
	}
}
