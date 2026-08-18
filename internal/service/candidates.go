package service

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
)

// Seat suggestions.
//
// One ranking rule, in the engine; every client (desktop board, MCP roster
// get) shows what it returns and never re-ranks. What the rule ORDERS BY is
// the person's: a list of criteria per role, from a catalog the engine
// names. One developer wants cost first, another the coding index; both are
// right for themselves, and the engine ships a default that says what it
// would do if nobody chose.

// Candidate is a non-binding recommendation for a roster seat.
type Candidate struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// Criterion is one thing a seat's suggestions can be ordered by.
type Criterion struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Higher is better ("desc") or lower is better ("asc"). Fixed per
	// criterion: nobody wants the WORST pass rate first.
	Direction string `json:"direction"`
	// Where the number comes from, so the person choosing knows what they
	// are ordering by.
	Source string `json:"source"`
}

// CriterionCatalog is every criterion a role's list may name, in a stable
// display order.
func CriterionCatalog() []Criterion {
	return []Criterion{
		{Key: "coding_index", Label: "coding index", Direction: "desc", Source: "external index (Artificial Analysis via OpenRouter, or declared)"},
		{Key: "intelligence_index", Label: "intelligence index", Direction: "desc", Source: "external index"},
		{Key: "agentic_index", Label: "agentic index", Direction: "desc", Source: "external index"},
		{Key: "pass_rate", Label: "pass rate in this seat", Direction: "desc", Source: "your runs, this role only"},
		{Key: "pass_rate_overall", Label: "pass rate overall", Direction: "desc", Source: "your runs, every role"},
		{Key: "cost_per_run", Label: "cost per run", Direction: "asc", Source: "your runs (local models: not comparable)"},
		{Key: "wallclock", Label: "wallclock per run", Direction: "asc", Source: "your runs"},
		{Key: "bench", Label: "bench score", Direction: "desc", Source: "your bench suites"},
		{Key: "input_cost", Label: "input price", Direction: "asc", Source: "list price per Mtok (local models: not comparable)"},
		{Key: "output_cost", Label: "output price", Direction: "asc", Source: "list price per Mtok (local models: not comparable)"},
		{Key: "context", Label: "context window", Direction: "desc", Source: "declared caps"},
	}
}

// DefaultCriteria is what the engine orders by when nobody chose. Roles
// absent here (triager, scribe, human) are not ranked by evidence.
func DefaultCriteria() map[string][]string {
	return map[string][]string{
		"implementer": {"coding_index", "pass_rate", "cost_per_run"},
		"architect":   {"coding_index", "pass_rate", "cost_per_run"},
		"reviewer":    {"pass_rate", "intelligence_index", "cost_per_run"},
		"judge":       {"pass_rate", "intelligence_index", "cost_per_run"},
		"advisor":     {"cost_per_run", "wallclock", "agentic_index"},
	}
}

// CriteriaFor is the effective list for a role: the configured one when the
// role is present in config (an empty list turns suggestions off), else the
// default.
func CriteriaFor(cfg map[string][]string, role string) []string {
	if cfg != nil {
		if list, ok := cfg[role]; ok {
			return list
		}
	}
	return DefaultCriteria()[role]
}

// ValidateCriteria refuses unknown keys and unknown roles.
func ValidateCriteria(cfg map[string][]string) error {
	known := map[string]bool{}
	for _, c := range CriterionCatalog() {
		known[c.Key] = true
	}
	for role, list := range cfg {
		if err := config.ValidateRole(config.Role(role)); err != nil {
			return fmt.Errorf("candidate criteria: %w", err)
		}
		for _, k := range list {
			if !known[k] {
				return fmt.Errorf("candidate criteria for %s: unknown criterion %q; next: use one of the catalog keys", role, k)
			}
		}
	}
	return nil
}

// minRuns is the evidence a measured criterion needs before it counts at
// all: two runs say almost nothing, and "unknown sorts last" must not turn
// them into a winner by default.
const minRuns = 3

// passRateEstimate is what a pass rate is worth given how many runs it
// rests on: the lower bound of the 95% Wilson interval, in percent. 3 of 3
// is not "100%", it is "somewhere above 44%"; 166 of 198 is "above 78%".
// Ordering by the bound is what lets 198 runs at 84% outrank 3 at 100%
// without a hand-tuned threshold. The why line still shows the raw rate
// and the run count — the estimate ranks, the facts explain.
func passRateEstimate(ratePercent float64, runs int) float64 {
	if runs <= 0 {
		return 0
	}
	const z = 1.96
	n := float64(runs)
	p := ratePercent / 100
	den := 1 + z*z/n
	centre := p + z*z/(2*n)
	half := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	lb := (centre - half) / den
	if lb < 0 {
		lb = 0
	}
	return lb * 100
}

// criterionValue reads one criterion off a scorecard for a seat. ok=false
// means "no value" — unknown, not zero — and unknowns sort after knowns.
func criterionValue(key, role string, s Scorecard) (v float64, ok bool) {
	local := s.Locality == "local"
	inRole := func() (MeasuredEvidence, bool) {
		m, has := s.MeasuredByRole[role]
		return m, has && m.Runs >= minRuns
	}
	overall := s.Measured != nil && s.Measured.Runs >= minRuns
	switch key {
	case "coding_index":
		if s.Index != nil && s.Index.CodingScore > 0 {
			return s.Index.CodingScore, true
		}
	case "intelligence_index":
		if s.Index != nil && s.Index.IntelligenceScore > 0 {
			return s.Index.IntelligenceScore, true
		}
	case "agentic_index":
		if s.Index != nil && s.Index.AgenticScore > 0 {
			return s.Index.AgenticScore, true
		}
	case "pass_rate":
		if m, has := inRole(); has {
			return passRateEstimate(m.PassRate, m.Runs), true
		}
	case "pass_rate_overall":
		if overall {
			return passRateEstimate(s.Measured.PassRate, s.Measured.Runs), true
		}
	case "cost_per_run":
		// A local model's $0 is not a price, it is the absence of one; it
		// must not top every cost ordering by saying nothing.
		if !local && overall {
			return s.Measured.AvgCostPerRun, true
		}
	case "wallclock":
		if overall && s.Measured.AvgWallclock > 0 {
			return s.Measured.AvgWallclock, true
		}
	case "bench":
		best, has := 0.0, false
		for _, b := range s.Bench {
			if !has || b.Score > best {
				best, has = b.Score, true
			}
		}
		if has {
			return best, true
		}
	case "input_cost":
		if !local && (s.Cost.InputPerMTok > 0 || s.Cost.OutputPerMTok > 0) {
			return s.Cost.InputPerMTok, true
		}
	case "output_cost":
		if !local && (s.Cost.InputPerMTok > 0 || s.Cost.OutputPerMTok > 0) {
			return s.Cost.OutputPerMTok, true
		}
	case "context":
		if s.Caps.ContextTokens != nil && *s.Caps.ContextTokens > 0 {
			return float64(*s.Caps.ContextTokens), true
		}
	}
	return 0, false
}

func criterionPhrase(key, role string, s Scorecard, v float64) string {
	switch key {
	case "coding_index":
		return fmt.Sprintf("coding %.1f", v)
	case "intelligence_index":
		return fmt.Sprintf("intelligence %.1f", v)
	case "agentic_index":
		return fmt.Sprintf("agentic %.1f", v)
	case "pass_rate":
		m := s.MeasuredByRole[role]
		return fmt.Sprintf("%.0f%% over %d runs as %s", m.PassRate, m.Runs, role)
	case "pass_rate_overall":
		return fmt.Sprintf("%.0f%% over %d runs", s.Measured.PassRate, s.Measured.Runs)
	case "cost_per_run":
		return fmt.Sprintf("$%.2f/run", v)
	case "wallclock":
		return fmt.Sprintf("%.0fs/run", v)
	case "bench":
		return fmt.Sprintf("bench %.0f", v*100)
	case "input_cost":
		return fmt.Sprintf("$%g/Mtok in", v)
	case "output_cost":
		return fmt.Sprintf("$%g/Mtok out", v)
	case "context":
		if v >= 1_000_000 {
			return fmt.Sprintf("%.1fM ctx", v/1_000_000)
		}
		return fmt.Sprintf("%.0fk ctx", v/1000)
	}
	return ""
}

// RankCandidates orders the scorecards for a seat by the given criteria and
// returns at most three, each with a why line built from the criteria that
// placed it. A duckling is eligible when it has a value for the FIRST
// criterion — the one the person put first is the one they mean; a
// duckling known only by a tie-breaker would win by default over an empty
// field, and a suggestion that rests on nothing is worse than none — and
// when its declared roles (if any) include the seat: "Not good for reviewer"
// written as roles=[implementer] is a statement the suggestion must respect.
// Comparison is lexicographic over the criteria; below the first, a
// duckling with no value sorts after every duckling with one. No eligible
// duckling → no candidates → the seat says nothing.
func RankCandidates(role string, scorecards []Scorecard, criteria []string) []Candidate {
	if len(criteria) == 0 {
		return []Candidate{}
	}
	dir := map[string]string{}
	for _, c := range CriterionCatalog() {
		dir[c.Key] = c.Direction
	}
	type ranked struct {
		s      Scorecard
		values []float64
		known  []bool
	}
	rows := make([]ranked, 0, len(scorecards))
	for _, s := range scorecards {
		if len(s.Roles) > 0 {
			fits := false
			for _, r := range s.Roles {
				if r == role {
					fits = true
				}
			}
			if !fits {
				continue
			}
		}
		r := ranked{s: s, values: make([]float64, len(criteria)), known: make([]bool, len(criteria))}
		for i, k := range criteria {
			v, ok := criterionValue(k, role, s)
			r.values[i], r.known[i] = v, ok
		}
		if r.known[0] {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		for k, key := range criteria {
			switch {
			case a.known[k] && !b.known[k]:
				return true
			case !a.known[k] && b.known[k]:
				return false
			case !a.known[k]:
				continue
			case a.values[k] == b.values[k]:
				continue
			case dir[key] == "asc":
				return a.values[k] < b.values[k]
			default:
				return a.values[k] > b.values[k]
			}
		}
		return a.s.ID < b.s.ID
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	out := make([]Candidate, len(rows))
	for i, r := range rows {
		var parts []string
		for k, key := range criteria {
			if r.known[k] {
				parts = append(parts, criterionPhrase(key, role, r.s, r.values[k]))
			}
		}
		out[i] = Candidate{ID: r.s.ID, Why: strings.Join(parts, " · ")}
	}
	return out
}

// candidatesFor ranks for a seat under the configured criteria.
func (s *Service) candidatesFor(role string, cards []Scorecard) []Candidate {
	s.cfgMu.RLock()
	criteria := CriteriaFor(s.cfg.Defaults.CandidateCriteria, role)
	s.cfgMu.RUnlock()
	return RankCandidates(role, cards, criteria)
}

// CandidateCriteriaView is the criteria as a client edits them: what is in
// effect per role, what the engine would do on its own, and the catalog to
// choose from.
type CandidateCriteriaView struct {
	// Criteria is the effective list per ranked role (configured or default).
	Criteria map[string][]string `json:"criteria"`
	// Configured names the roles the person has overridden; the rest are
	// defaults and can be told apart in the UI.
	Configured []string            `json:"configured"`
	Defaults   map[string][]string `json:"defaults"`
	Catalog    []Criterion         `json:"catalog"`
}

func (s *Service) CandidateCriteria() CandidateCriteriaView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	view := CandidateCriteriaView{Criteria: map[string][]string{}, Defaults: DefaultCriteria(), Catalog: CriterionCatalog(), Configured: []string{}}
	for role := range view.Defaults {
		view.Criteria[role] = append([]string{}, CriteriaFor(s.cfg.Defaults.CandidateCriteria, role)...)
	}
	for role, list := range s.cfg.Defaults.CandidateCriteria {
		view.Criteria[role] = append([]string{}, list...)
		view.Configured = append(view.Configured, role)
	}
	sort.Strings(view.Configured)
	return view
}

// CandidateCriteriaSet replaces the configured criteria. A role set equal to
// its default is stored anyway — the person said so — and a role omitted
// falls back to the default; to turn a role's suggestions off, send an
// empty list for it.
func (s *Service) CandidateCriteriaSet(criteria map[string][]string) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if err := ValidateCriteria(criteria); err != nil {
		return err
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	prev := s.cfg.Defaults.CandidateCriteria
	next := map[string][]string{}
	for role, list := range criteria {
		next[role] = append([]string{}, list...)
	}
	if len(next) == 0 {
		next = nil
	}
	s.cfg.Defaults.CandidateCriteria = next
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.CandidateCriteria = prev
		return err
	}
	return nil
}
