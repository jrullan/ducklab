package service

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/budget"
)

// The run budget was invisible and immutable: it came from the engine's config
// and no client could read it, so a run that hit the ceiling failed with a
// number nobody had chosen and nobody could raise.
func TestTheDefaultBudgetCanBeReadAndChanged(t *testing.T) {
	s := writableService(t, "pato-uno")

	before := s.BudgetDefaults()
	if before.MaxTokens <= 0 {
		t.Fatalf("no default budget to read: %+v", before)
	}

	v := before
	v.MaxTokens = 1_500_000
	if err := s.BudgetDefaultsSet(v); err != nil {
		t.Fatal(err)
	}
	if got := s.BudgetDefaults().MaxTokens; got != 1_500_000 {
		t.Errorf("max_tokens = %d", got)
	}
}

// The defaults PUT is a merge, not a replace: a form that does not know a
// field must not be able to destroy it. A settings page that sends only the
// fields it renders must thus leave every other budget value untouched.
func TestAFieldOmittedFromTheUpdateKeepsItsCurrentValue(t *testing.T) {
	s := writableService(t, "pato-uno")
	before := s.BudgetDefaults()
	if before.WallclockEscalationMultiplier <= 0 {
		t.Fatalf("no escalation multiplier to preserve: %+v", before)
	}

	// Send an update that knows nothing about the multiplier.
	saved, err := s.BudgetUpdateSet(BudgetUpdate{MaxTokens: int64Ptr(1_500_000)})
	if err != nil {
		t.Fatal(err)
	}
	if saved.MaxTokens != 1_500_000 {
		t.Errorf("max_tokens = %d, want the updated 1.5M", saved.MaxTokens)
	}
	if saved.WallclockEscalationMultiplier != before.WallclockEscalationMultiplier {
		t.Errorf("multiplier = %v, want the preserved %v", saved.WallclockEscalationMultiplier, before.WallclockEscalationMultiplier)
	}
	for _, c := range []struct {
		name string
		got  float64
		want float64
	}{
		{"max_usd", saved.MaxUSD, before.MaxUSD},
		{"max_turns", float64(saved.MaxTurns), float64(before.MaxTurns)},
		{"max_wallclock_s", float64(saved.MaxWallclockS), float64(before.MaxWallclockS)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want the preserved %v", c.name, c.got, c.want)
		}
	}
	// And the persisted value matches what the update reported back.
	if got := s.BudgetDefaults().MaxTokens; got != 1_500_000 {
		t.Errorf("persisted max_tokens = %d", got)
	}
	if got := s.BudgetDefaults().WallclockEscalationMultiplier; got != before.WallclockEscalationMultiplier {
		t.Errorf("persisted multiplier = %v, want the preserved %v", got, before.WallclockEscalationMultiplier)
	}
}

// An update that explicitly supplies the multiplier still goes through the
// same >0 validation as every other write: omission preserves, but a sent zero
// must not quietly disable the safeguard.
func TestAnExplicitZeroMultiplierInAnUpdateIsStillRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	saved, err := s.BudgetUpdateSet(BudgetUpdate{WallclockEscalationMultiplier: float64Ptr(0)})
	if err == nil {
		t.Fatalf("an update with an explicit zero multiplier was accepted: %+v", saved)
	}
	if !strings.Contains(err.Error(), "wallclock_escalation_multiplier") {
		t.Errorf("the error does not name the field: %v", err)
	}
	if s.BudgetDefaults().WallclockEscalationMultiplier == 0 {
		t.Error("the rejected zero was written anyway")
	}
}

func int64Ptr(v int64) *int64 { return &v }

func float64Ptr(v float64) *float64 { return &v }

// A == reads as "no limit" to a person clearing a field, but the tracker
// treats it as a ceiling of zero and every run would fail before its first call.
func TestAZeroLimitIsRefused(t *testing.T) {
	s := writableService(t, "pato-uno")
	v := s.BudgetDefaults()
	v.MaxTurns = 0
	err := s.BudgetDefaultsSet(v)
	if err == nil {
		t.Fatal("a budget with a zero limit was accepted")
	}
	if !strings.Contains(err.Error(), "max_turns") {
		t.Errorf("the error does not name the field: %v", err)
	}
	// And nothing was written.
	if s.BudgetDefaults().MaxTurns == 0 {
		t.Error("the rejected value was saved anyway")
	}
}

// A run request carrying only a token ceiling used to replace the whole budget,
// so the other three limits became zero and the run failed before its first
// call — the opposite of what asking for more budget means.
func TestARequestRaisingOneLimitKeepsTheOthers(t *testing.T) {
	defaults := budget.Budget{MaxUSD: 2, MaxTokens: 400_000, MaxTurns: 24, MaxWallclockS: 3600}
	req := &budget.Budget{MaxTokens: 1_500_000}

	got := mergeBudget(defaults, req)
	if got.MaxTokens != 1_500_000 {
		t.Errorf("max_tokens = %d, want the requested 1.5M", got.MaxTokens)
	}
	for _, c := range []struct {
		name string
		got  float64
		want float64
	}{
		{"max_usd", got.MaxUSD, 2},
		{"max_turns", float64(got.MaxTurns), 24},
		{"max_wallclock_s", float64(got.MaxWallclockS), 3600},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want the default %v", c.name, c.got, c.want)
		}
	}
}
