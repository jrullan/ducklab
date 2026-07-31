package service

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/strategy"
)

// The run budget was invisible and immutable.
//
// It came from cfg.Defaults.Budget and nothing in either client could read it,
// let alone change it. A run that hit the ceiling failed with a number nobody
// had chosen and nobody could raise — and the ceiling is easy to hit for a
// reason that is not obvious: every model call re-sends the whole conversation,
// so the prompt tokens are counted again each round. One real run spent 420k of
// its 436k on input and 16k on output.

// BudgetView is the default budget a run starts with.
type BudgetView struct {
	MaxUSD        float64 `json:"max_usd"`
	MaxTokens     int64   `json:"max_tokens"`
	MaxTurns      int     `json:"max_turns"`
	MaxWallclockS int     `json:"max_wallclock_s"`
}

// BudgetDefaults returns the budget every run starts with.
func (s *Service) BudgetDefaults() BudgetView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	b := s.cfg.Defaults.Budget
	return BudgetView{
		MaxUSD: b.MaxUSD, MaxTokens: b.MaxTokens,
		MaxTurns: b.MaxTurns, MaxWallclockS: b.MaxWallclockS,
	}
}

// BudgetDefaultsSet replaces the default budget and writes it back.
//
// Every limit must be positive. A zero would read as "no limit" to anyone
// looking at the file, but the tracker treats it as a ceiling of zero and every
// run would fail before its first call — the opposite of what a person clearing
// a field expects (I3: nothing unbounded, but nothing accidentally zero either).
func (s *Service) BudgetDefaultsSet(v BudgetView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		val  float64
	}{
		{"max_usd", v.MaxUSD},
		{"max_tokens", float64(v.MaxTokens)},
		{"max_turns", float64(v.MaxTurns)},
		{"max_wallclock_s", float64(v.MaxWallclockS)},
	} {
		if f.val <= 0 {
			return fmt.Errorf("budget %s must be greater than zero; got %v", f.name, f.val)
		}
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	previous := s.cfg.Defaults.Budget
	s.cfg.Defaults.Budget = config.Budget{
		MaxUSD: v.MaxUSD, MaxTokens: v.MaxTokens,
		MaxTurns: v.MaxTurns, MaxWallclockS: v.MaxWallclockS,
	}
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.Budget = previous
		return err
	}
	return nil
}

// mergeBudget layers a request's limits over the defaults, one field at a time.
//
// A request carrying a budget used to replace the whole thing, so a client
// raising only the token ceiling set the other three to zero — and the tracker
// reads zero as a ceiling of zero, so the run failed before its first call. That
// is the opposite of what asking for more budget means.
func mergeBudget(defaults budget.Budget, req *budget.Budget) budget.Budget {
	out := defaults
	if req == nil {
		return out
	}
	if req.MaxUSD > 0 {
		out.MaxUSD = req.MaxUSD
	}
	if req.MaxTokens > 0 {
		out.MaxTokens = req.MaxTokens
	}
	if req.MaxTurns > 0 {
		out.MaxTurns = req.MaxTurns
	}
	if req.MaxWallclockS > 0 {
		out.MaxWallclockS = req.MaxWallclockS
	}
	return out
}

// ModeDefaultsView is how many rounds each mode runs, and how many model calls one
// turn may chain.
//
// Two different limits that both used to be invisible. Rounds bound the
// conversation — how many times a reviewer gets to push back — and lived only
// inside the scripts. AgentMaxTurns bounds ONE participant's turn: the model
// calling tools, reading results, calling again. A run whose implementer works
// in circles is stopped by that, not by the round count, and neither could be
// seen or changed without editing Go.
type ModeDefaultsView struct {
	// Rounds per mode. Zero or absent leaves the script's own limit alone.
	Rounds map[string]int `json:"rounds"`
	// AgentMaxTurns caps the model calls a single turn may chain.
	AgentMaxTurns int `json:"agent_max_turns"`
	// ScriptRounds is what each mode does when nothing overrides it, so a client
	// can show the real number instead of an empty box.
	ScriptRounds map[string]int `json:"script_rounds"`
	// Ducklings is the line-up to use for each mode when a run does not name
	// one, in order. A combination that works is a finding, and re-ticking the
	// same boxes on every run is how a finding gets lost.
	Ducklings map[string][]string `json:"ducklings"`
	// RoleTurns caps the model calls one turn of a role may chain. Zero or
	// absent leaves the script's own cap alone.
	RoleTurns map[string]int `json:"role_turns"`
	// ScriptRoleTurns is what each role gets when nothing overrides it.
	ScriptRoleTurns map[string]int `json:"script_role_turns"`
	// Seats is how many ducklings each mode can seat, zero meaning as many as
	// are ticked. Reported so a client can stop a third box being ticked for a
	// two-chair mode instead of accepting a preference that will not run.
	Seats map[string]int `json:"seats"`
}

// ScriptRoleTurns are the caps the scripts themselves carry, so a client can
// show the real number rather than an empty box.
//
// A reviewer gets fewer than an implementer on purpose: reviewing is reading and
// giving a verdict, not iterating. A judge gets one — it chooses between
// finished candidates and has nothing to explore.
var ScriptRoleTurns = map[string]int{
	"implementer": 24, "reviewer": 8, "architect": 12,
	"judge": 1, "triager": 6, "scribe": 12,
}

// ModeRounds are the counts the scripts themselves carry. Reported so a client
// never has to guess what "unset" means.
var ModeRounds = map[string]int{
	"solo": 3, "pair": 3, "tournament": 1, "council": 2, "split": 1,
}

// ModeSeats is how many ducklings each mode can seat. Zero means as many as
// are ticked: council seats one drafter and a critic per remaining duckling,
// tournament fields every candidate it is given, split spreads over the fleet.
// Solo is one model by definition, and pair is exactly an implementer and a
// reviewer. A line-up longer than the mode's chairs used to save fine and
// silently seat nobody past the limit, which reads as "my setting did not
// take".
var ModeSeats = map[string]int{
	"solo": 1, "pair": 2, "tournament": 0, "council": 0, "split": 0,
}

// ModeDefaults returns the per-mode round counts and the per-turn call cap.
func (s *Service) ModeDefaults() ModeDefaultsView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	out := ModeDefaultsView{
		Rounds:          map[string]int{},
		AgentMaxTurns:   s.cfg.Defaults.AgentMaxTurns,
		ScriptRounds:    ModeRounds,
		Ducklings:       map[string][]string{},
		RoleTurns:       map[string]int{},
		ScriptRoleTurns: ScriptRoleTurns,
		Seats:           ModeSeats,
	}
	for mode, n := range s.cfg.Defaults.Rounds {
		out.Rounds[mode] = n
	}
	for mode, ids := range s.cfg.Defaults.ModeDucklings {
		out.Ducklings[mode] = append([]string{}, ids...)
	}
	for role, n := range s.cfg.Defaults.RoleTurns {
		out.RoleTurns[role] = n
	}
	return out
}

// ModeDefaultsSet replaces them and writes the config back.
func (s *Service) ModeDefaultsSet(v ModeDefaultsView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if v.AgentMaxTurns <= 0 {
		return fmt.Errorf("agent_max_turns must be greater than zero; got %d", v.AgentMaxTurns)
	}
	for mode, n := range v.Rounds {
		if _, ok := ModeRounds[mode]; !ok {
			return fmt.Errorf("unknown mode %q", mode)
		}
		// Zero is how a mode says "use the script's own count", so only a
		// negative is wrong. A ceiling because a round is a full pass over every
		// participant, and a run that can take fifty of them is a run nobody is
		// supervising.
		if n < 0 || n > 20 {
			return fmt.Errorf("rounds for %q must be 0 (use the script default) to 20; got %d", mode, n)
		}
	}

	for role, n := range v.RoleTurns {
		if _, ok := ScriptRoleTurns[role]; !ok {
			return fmt.Errorf("unknown role %q", role)
		}
		// A cap of one lets a turn make a single call, which is right for a
		// judge and wrong for everyone else — but that is the caller's business.
		// Above forty a turn stops being bounded in any useful sense.
		if n < 0 || n > 40 {
			return fmt.Errorf("turns for %q must be 0 (use the script default) to 40; got %d", role, n)
		}
	}

	for mode, ids := range v.Ducklings {
		if _, ok := ModeRounds[mode]; !ok {
			return fmt.Errorf("unknown mode %q", mode)
		}
		if cap := ModeSeats[mode]; cap > 0 && len(ids) > cap {
			// Accepting the extra names would save a preference the run can
			// only ignore, which reads later as "my setting did not take".
			return fmt.Errorf("mode %q seats %d, and the line-up names %d", mode, cap, len(ids))
		}
		for _, id := range ids {
			if _, ok := s.cfg.Ducklings[config.DucklingID(id)]; !ok {
				// Silently keeping it would make the preference look saved and
				// then quietly do nothing on the next run.
				return fmt.Errorf("mode %q names duckling %q, which does not exist", mode, id)
			}
		}
	}

	for role, n := range v.RoleTurns {
		if _, ok := ScriptRoleTurns[role]; !ok {
			return fmt.Errorf("unknown role %q", role)
		}
		// A cap of one lets a turn make a single call, which is right for a
		// judge and wrong for everyone else — but that is the caller's business.
		// Above forty a turn stops being bounded in any useful sense.
		if n < 0 || n > 40 {
			return fmt.Errorf("turns for %q must be 0 (use the script default) to 40; got %d", role, n)
		}
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	prevRounds, prevTurns := s.cfg.Defaults.Rounds, s.cfg.Defaults.AgentMaxTurns
	prevDucklings, prevRoleTurns := s.cfg.Defaults.ModeDucklings, s.cfg.Defaults.RoleTurns
	rounds := map[string]int{}
	for mode, n := range v.Rounds {
		if n > 0 {
			rounds[mode] = n
		}
	}
	lineups := map[string][]string{}
	for mode, ids := range v.Ducklings {
		if len(ids) > 0 {
			lineups[mode] = append([]string{}, ids...)
		}
	}
	s.cfg.Defaults.Rounds = rounds
	s.cfg.Defaults.AgentMaxTurns = v.AgentMaxTurns
	roleTurns := map[string]int{}
	for role, n := range v.RoleTurns {
		if n > 0 {
			roleTurns[role] = n
		}
	}
	s.cfg.Defaults.ModeDucklings = lineups
	s.cfg.Defaults.RoleTurns = roleTurns
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.Rounds, s.cfg.Defaults.AgentMaxTurns = prevRounds, prevTurns
		s.cfg.Defaults.ModeDucklings, s.cfg.Defaults.RoleTurns = prevDucklings, prevRoleTurns
		return err
	}
	return nil
}

// ducklingsFor returns the line-up a run should use: the one it named, else the
// one configured for its mode, else none — which leaves the roster to decide.
func (s *Service) ducklingsFor(mode string, requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return append([]string{}, s.cfg.Defaults.ModeDucklings[mode]...)
}

// roundsFor returns how many rounds a mode should run: what the request asked
// for, else what the config says, else zero to let the script decide.
func (s *Service) roundsFor(mode string, requested int) int {
	if requested > 0 {
		return requested
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.Defaults.Rounds[mode]
}

// turnsFor returns the call cap for one role: the configured one, else the
// script's own.
func (s *Service) turnsFor(role string, scriptCap int) int {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if n := s.cfg.Defaults.RoleTurns[role]; n > 0 {
		return n
	}
	return scriptCap
}

// applyRoleTurns rewrites a script's per-role caps from the configuration.
//
// Done here rather than in the scripts because a script is a fixed shape and the
// caps are a preference. Walking the turns is what makes a setting apply to
// every mode at once instead of to whichever ones somebody remembered.
func (s *Service) applyRoleTurns(script *strategy.Script) *strategy.Script {
	if script == nil {
		return script
	}
	for i := range script.Turns {
		script.Turns[i].MaxTurns = s.turnsFor(string(script.Turns[i].Role), script.Turns[i].MaxTurns)
	}
	return script
}

// roleTurnCaps is the configured caps in the shape the strategies want.
func (s *Service) roleTurnCaps() map[config.Role]int {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	out := map[config.Role]int{}
	for role, n := range s.cfg.Defaults.RoleTurns {
		if n > 0 {
			out[config.Role(role)] = n
		}
	}
	return out
}
