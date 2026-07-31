package service

import (
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func rosterOf(impl, rev config.DucklingID) map[config.Role]config.DucklingID {
	return map[config.Role]config.DucklingID{
		config.RoleImplementer: impl,
		config.RoleReviewer:    rev,
	}
}

// Only tournament and split read req.Ducklings. A person who chose a duckling
// for a solo run in the desktop got the project roster's implementer instead,
// with nothing on screen to say the pick was ignored — the picker was sitting
// right there offering a choice that did nothing.
func TestASoloRunUsesTheDucklingYouPicked(t *testing.T) {
	roster := rosterOf("pato-atom", "pato-local")
	assignChosenDucklings(roster, "solo", []string{"pato-sonnet"})
	if got := roster[config.RoleImplementer]; got != "pato-sonnet" {
		t.Errorf("implementer = %q, want the picked pato-sonnet", got)
	}
}

// Positional, matching split: first implements, second reviews.
func TestAPairRunAssignsBothSidesInOrder(t *testing.T) {
	roster := rosterOf("pato-atom", "pato-atom")
	assignChosenDucklings(roster, "pair", []string{"pato-sonnet", "pato-local"})
	if roster[config.RoleImplementer] != "pato-sonnet" || roster[config.RoleReviewer] != "pato-local" {
		t.Errorf("roster = %v", roster)
	}
}

// One pick for a pair sets the implementer and leaves the reviewer alone: the
// second model exists to be decorrelated, and collapsing it onto the pick would
// make pair look like it works while doing nothing.
func TestOnePickForAPairLeavesTheReviewerAlone(t *testing.T) {
	roster := rosterOf("pato-atom", "pato-local")
	assignChosenDucklings(roster, "pair", []string{"pato-sonnet"})
	if roster[config.RoleImplementer] != "pato-sonnet" {
		t.Errorf("implementer = %q", roster[config.RoleImplementer])
	}
	if roster[config.RoleReviewer] != "pato-local" {
		t.Errorf("reviewer = %q, want the roster's untouched pato-local", roster[config.RoleReviewer])
	}
}

// Tournament and split take the list whole and assign it themselves; writing
// the roster here would fight that.
func TestTournamentAndSplitKeepTheirOwnAssignment(t *testing.T) {
	for _, mode := range []string{"tournament", "split"} {
		roster := rosterOf("pato-atom", "pato-local")
		assignChosenDucklings(roster, mode, []string{"pato-sonnet", "pato-local"})
		if roster[config.RoleImplementer] != "pato-atom" {
			t.Errorf("%s: the roster was overwritten: %v", mode, roster)
		}
	}
}

// A pick can create the collision the warning exists for, and the warning was
// decided before the pick was applied.
func TestAPickThatPutsOneDucklingOnBothSidesStillWarns(t *testing.T) {
	roster := rosterOf("pato-atom", "pato-sonnet")
	assignChosenDucklings(roster, "pair", []string{"pato-sonnet"})
	if w := bothSidesWarning(roster); w == "" {
		t.Error("picking the reviewer as the implementer produced no warning")
	}
}

// And it can remove one: a warning that outlived its cause reads as a defect
// that is not there.
func TestAPickThatSeparatesTheSidesClearsTheWarning(t *testing.T) {
	roster := rosterOf("pato-atom", "pato-atom")
	if bothSidesWarning(roster) == "" {
		t.Fatal("the collision itself is not detected")
	}
	assignChosenDucklings(roster, "pair", []string{"pato-sonnet"})
	if w := bothSidesWarning(roster); w != "" {
		t.Errorf("the warning survived the fix: %q", w)
	}
}
