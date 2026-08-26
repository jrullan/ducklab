package service

import (
	"fmt"
	"runtime"

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

// EngineDefaultsView exposes the global admission cap and the host context.
type EngineDefaultsView struct {
	MaxConcurrentRuns int `json:"max_concurrent_runs"`
	CPUCeiling        int `json:"cpu_ceiling"`
}

func (s *Service) EngineDefaults() EngineDefaultsView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return EngineDefaultsView{MaxConcurrentRuns: s.cfg.Engine.MaxConcurrentRuns, CPUCeiling: runtime.NumCPU()}
}

func (s *Service) EngineDefaultsSet(v EngineDefaultsView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	if v.MaxConcurrentRuns <= 0 {
		return fmt.Errorf("max_concurrent_runs must be greater than zero; got %d", v.MaxConcurrentRuns)
	}
	s.cfgMu.Lock()
	previous := s.cfg.Engine.MaxConcurrentRuns
	s.cfg.Engine.MaxConcurrentRuns = v.MaxConcurrentRuns
	if err := s.saveConfig(); err != nil {
		s.cfg.Engine.MaxConcurrentRuns = previous
		s.cfgMu.Unlock()
		return err
	}
	s.cfgMu.Unlock()
	// A cap raise changes admission without a run completing. Recheck the
	// waiting line now rather than leaving queued work asleep until the next
	// unrelated queue event. Small service test fixtures may not install a queue.
	if s.queue != nil {
		s.queue.poke(s)
	}
	return nil
}

// BudgetView is the default budget a run starts with.
type BudgetView struct {
	MaxUSD                        float64 `json:"max_usd"`
	MaxTokens                     int64   `json:"max_tokens"`
	MaxTurns                      int     `json:"max_turns"`
	MaxWallclockS                 int     `json:"max_wallclock_s"`
	WallclockEscalationMultiplier float64 `json:"wallclock_escalation_multiplier"`
}

// BudgetDefaults returns the budget every run starts with.
func (s *Service) BudgetDefaults() BudgetView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	b := s.cfg.Defaults.Budget
	return BudgetView{
		MaxUSD: b.MaxUSD, MaxTokens: b.MaxTokens,
		MaxTurns: b.MaxTurns, MaxWallclockS: b.MaxWallclockS,
		WallclockEscalationMultiplier: b.WallclockEscalationMultiplier,
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
		{"wallclock_escalation_multiplier", v.WallclockEscalationMultiplier},
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
		WallclockEscalationMultiplier: v.WallclockEscalationMultiplier,
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
func projectBudget(defaults budget.Budget, project config.Budget) budget.Budget {
	return mergeBudget(defaults, &budget.Budget{
		MaxUSD: project.MaxUSD, MaxTokens: project.MaxTokens,
		MaxTurns: project.MaxTurns, MaxWallclockS: project.MaxWallclockS,
	})
}

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
	Ducklings map[string][]string            `json:"ducklings"`
	ModeSeats map[string]map[string][]string `json:"mode_seats,omitempty"`
	RolePins  map[string][]string            `json:"role_pins,omitempty"`
	// RoleTurns caps the model calls one turn of a role may chain. Zero or
	// absent leaves the script's own cap alone.
	RoleTurns map[string]int `json:"role_turns"`
	// ScriptRoleTurns is what each role gets when nothing overrides it.
	ScriptRoleTurns map[string]int `json:"script_role_turns"`
	// Seats is how many ducklings each mode can seat, zero meaning as many as
	// are ticked. Reported so a client can stop a third box being ticked for a
	// two-chair mode instead of accepting a preference that will not run.
	Seats map[string]int `json:"seats"`
	// BuildMode and TestMode are what launchers open on. Empty means solo.
	BuildMode string `json:"build_mode,omitempty"`
	TestMode  string `json:"test_mode,omitempty"`
}

// ScriptRoleTurns are the caps the scripts themselves carry, so a client can
// show the real number rather than an empty box.
//
// A reviewer gets fewer than an implementer on purpose: reviewing is reading and
// giving a verdict, not iterating. A judge gets one — it chooses between
// finished candidates and has nothing to explore.
var ScriptRoleTurns = map[string]int{
	"implementer": 24, "reviewer": 8, "architect": 12,
	"judge": 1, "triager": 6, "advisor": 6, "scribe": 12,
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
		ModeSeats:       map[string]map[string][]string{},
		RolePins:        map[string][]string{},
		BuildMode:       s.cfg.Defaults.BuildMode,
		TestMode:        s.cfg.Defaults.TestMode,
	}
	for mode, n := range s.cfg.Defaults.Rounds {
		out.Rounds[mode] = n
	}
	for role, ids := range s.cfg.Defaults.RolePins {
		out.RolePins[role] = append([]string{}, ids...)
	}
	for mode, seats := range s.cfg.Defaults.ModeSeats {
		out.ModeSeats[mode] = seats
		var ids []string
		for _, role := range []string{"architect", "implementer", "reviewer", "judge", "advisor"} {
			ids = append(ids, seats[role]...)
		}
		out.Ducklings[mode] = ids
	}
	for role, n := range s.cfg.Defaults.RoleTurns {
		out.RoleTurns[role] = n
	}
	return out
}

func validateModeCardinality(mode string, n int) error {
	switch mode {
	case "council":
		if n < 2 {
			return fmt.Errorf("council requires at least one critic")
		}
	case "split":
		if n < 2 {
			return fmt.Errorf("split requires at least two workers")
		}
	case "tournament":
		if n < 2 {
			return fmt.Errorf("tournament requires at least two contestants")
		}
	}
	return nil
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
		// The ceiling was 40 when a turn meant reviewing a diff, and above
		// forty THAT is a model in circles. Adoption changed the workload
		// class: a critic verifying a survey against a real codebase looks a
		// lot, legitimately — one spent all 40 on honest reads and searches.
		// The real resources are bounded by the run budget (tokens, money,
		// wallclock); this cap only guards against circling, so it is a wide
		// net, not a leash.
		if n < 0 || n > 200 {
			return fmt.Errorf("turns for %q must be 0 (use the script default) to 200; got %d", role, n)
		}
	}

	// The positional line-up (v.Ducklings) is the LEGACY form; when the
	// canonical mode_seats are present they are the truth and the line-up is
	// only a derived echo of them — one that now carries the advisor, so
	// pair's echo names three and tripped the old "pair seats 2" cap,
	// refusing EVERY global write from the board (Split, Tournament, Common
	// alike). Validate the line-up only when it is the form being written.
	lineupsAuthoritative := len(v.ModeSeats) == 0
	for mode, ids := range v.Ducklings {
		if !lineupsAuthoritative {
			break
		}
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

	for mode, roles := range v.ModeSeats {
		if _, ok := ModeRounds[mode]; !ok {
			return fmt.Errorf("unknown mode %q", mode)
		}
		count := 0
		for role, ids := range roles {
			if !validRole(config.Role(role)) || role == "human" {
				return fmt.Errorf("unknown roster role %q", role)
			}
			for _, id := range ids {
				if _, ok := s.cfg.Ducklings[config.DucklingID(id)]; !ok {
					return fmt.Errorf("mode %q names duckling %q, which does not exist", mode, id)
				}
			}
			if mode == "council" && role == "reviewer" {
				count += len(ids)
			}
			if mode == "split" && role == "implementer" {
				count += len(ids)
			}
			if mode == "tournament" && role == "implementer" {
				count += len(ids)
			}
		}
		// Cardinality is a LAUNCH rule, not a write rule — the same as for
		// project pins: a tournament is seated one contestant at a time, and
		// refusing the first because there is no second yet made the seat
		// impossible to fill from the board. The launch (RunStart) keeps the
		// hard check; GlobalRosterGet carries the "not runnable yet" note.
		_ = count
	}

	// The default modes launchers open on. Empty clears back to solo; a mode
	// that does not exist would open every launcher on nonsense.
	if v.BuildMode != "" {
		if _, ok := ModeRounds[v.BuildMode]; !ok {
			return fmt.Errorf("unknown build mode %q", v.BuildMode)
		}
	}
	if v.TestMode != "" && v.TestMode != "solo" && v.TestMode != "pair" {
		return fmt.Errorf("test mode must be solo or pair; got %q", v.TestMode)
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	prevRounds, prevTurns := s.cfg.Defaults.Rounds, s.cfg.Defaults.AgentMaxTurns
	prevModeSeats, prevRoleTurns := s.cfg.Defaults.ModeSeats, s.cfg.Defaults.RoleTurns
	prevRolePins := s.cfg.Defaults.RolePins
	prevBuildMode, prevTestMode := s.cfg.Defaults.BuildMode, s.cfg.Defaults.TestMode
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
	s.cfg.Defaults.ModeDucklings = nil
	// The roster is written by the Roster board (GlobalRosterSet), not by
	// this view. A Settings save that carries no seats and no line-up is
	// saying nothing about them — and used to be read as "replace with
	// nothing": every Settings save wiped every global seat and pin (the
	// board went blank the moment a role-turn cap was changed). Only a
	// request that names seats, or the legacy line-up, replaces them.
	if v.ModeSeats != nil || v.Ducklings != nil {
		seats := map[string]map[string][]string{}
		for mode, roles := range v.ModeSeats {
			seats[mode] = roles
		}
		if len(seats) == 0 {
			seats = config.LegacyModeSeats(lineups)
		}
		s.cfg.Defaults.ModeSeats = seats
	}
	if v.RolePins != nil {
		rolePins := map[string][]string{}
		for role, ids := range v.RolePins {
			rolePins[role] = append([]string{}, ids...)
		}
		s.cfg.Defaults.RolePins = rolePins
	}
	s.cfg.Defaults.RoleTurns = roleTurns
	s.cfg.Defaults.BuildMode = v.BuildMode
	s.cfg.Defaults.TestMode = v.TestMode
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.Rounds, s.cfg.Defaults.AgentMaxTurns = prevRounds, prevTurns
		s.cfg.Defaults.ModeSeats, s.cfg.Defaults.RoleTurns = prevModeSeats, prevRoleTurns
		s.cfg.Defaults.RolePins = prevRolePins
		s.cfg.Defaults.BuildMode, s.cfg.Defaults.TestMode = prevBuildMode, prevTestMode
		return err
	}
	return nil
}

// AutopilotDefaultsView is the loop's own knobs plus the global autonomy —
// the "sensible defaults" a person should be able to see and change without
// editing TOML: how far an unattended loop may go, how much failure it
// tolerates, and what autonomy a run gets when nothing names one.
type AutopilotDefaultsView struct {
	MaxTasks int    `json:"max_tasks"`
	MaxFails int    `json:"max_fails"`
	Autonomy string `json:"autonomy"`
}

// AutopilotDefaults reports them, built-ins filled in.
func (s *Service) AutopilotDefaults() AutopilotDefaultsView {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	v := AutopilotDefaultsView{
		MaxTasks: s.cfg.Defaults.AutopilotMaxTasks,
		MaxFails: s.cfg.Defaults.AutopilotMaxFails,
		Autonomy: string(s.cfg.Defaults.Autonomy),
	}
	if v.MaxTasks <= 0 {
		v.MaxTasks = autopilotDefaultMaxTasks
	}
	if v.MaxFails <= 0 {
		v.MaxFails = autopilotDefaultMaxFails
	}
	if v.Autonomy == "" {
		v.Autonomy = string(config.AutonomyGuarded)
	}
	return v
}

// AutopilotDefaultsSet replaces them and writes the config back.
func (s *Service) AutopilotDefaultsSet(v AutopilotDefaultsView) error {
	if err := s.canWriteConfig(); err != nil {
		return err
	}
	// Bounded on both ends: a cap of zero would mean an autopilot that can
	// start nothing, and a thousand-task activation is a loop nobody is
	// supervising (the same I3 reasoning as rounds).
	if v.MaxTasks < 1 || v.MaxTasks > 100 {
		return fmt.Errorf("autopilot max_tasks must be 1 to 100; got %d", v.MaxTasks)
	}
	if v.MaxFails < 1 || v.MaxFails > 10 {
		return fmt.Errorf("autopilot max_fails must be 1 to 10; got %d", v.MaxFails)
	}
	valid := false
	for _, a := range config.ValidAutonomies() {
		if string(a) == v.Autonomy {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("unknown autonomy %q", v.Autonomy)
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	prevTasks, prevFails := s.cfg.Defaults.AutopilotMaxTasks, s.cfg.Defaults.AutopilotMaxFails
	prevAutonomy := s.cfg.Defaults.Autonomy
	s.cfg.Defaults.AutopilotMaxTasks = v.MaxTasks
	s.cfg.Defaults.AutopilotMaxFails = v.MaxFails
	s.cfg.Defaults.Autonomy = config.Autonomy(v.Autonomy)
	if err := s.saveConfig(); err != nil {
		s.cfg.Defaults.AutopilotMaxTasks, s.cfg.Defaults.AutopilotMaxFails = prevTasks, prevFails
		s.cfg.Defaults.Autonomy = prevAutonomy
		return err
	}
	return nil
}

// ducklingsFor returns only explicit per-run seats. An omitted field means the
// resolved project/global roster decides; Settings line-ups remain preferences
// available to an operator who explicitly supplies them.
func (s *Service) ducklingsFor(_ string, requested []string) []string {
	return append([]string{}, requested...)
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

// applyRoleTurns rewrites a script's per-role caps from the configuration,
// then from the run's own override when it carries one.
//
// Done here rather than in the scripts because a script is a fixed shape and the
// caps are a preference. Walking the turns is what makes a setting apply to
// every mode at once instead of to whichever ones somebody remembered.
//
// The override used to ride only ExecuteParams.TurnCaps — which tournament
// and split read, and the script modes never did. So the per-run
// "calls/reply" was accepted, recorded, and silently ignored in exactly the
// modes most runs use. Human turns keep their cap: the override unblocks
// models, not people.
func (s *Service) applyRoleTurns(script *strategy.Script, override int) *strategy.Script {
	if script == nil {
		return script
	}
	for i := range script.Turns {
		script.Turns[i].MaxTurns = s.turnsFor(string(script.Turns[i].Role), script.Turns[i].MaxTurns)
		if override != 0 && script.Turns[i].Role != config.RoleHuman {
			script.Turns[i].MaxTurns = capOverride(override)
		}
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
