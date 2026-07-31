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

// A zero reads as "no limit" to a person clearing a field, but the tracker
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
