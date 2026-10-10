package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/report"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
	"github.com/jrullan/ducklab/internal/tools"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
)

// loopCache builds one agent.Loop per duckling, lazily.
//
// pair and tournament use several ducklings in one run, so a single loop is
// not enough. Loops are cached because building one probes capabilities, and
// probing once per turn would cost a request per turn.
//
// observedProvider is every agent loop's provider, so it sees every request a
// run makes — and every request that carries images: stage architect turns,
// build, test-first and review turns (taskVision, advisor consults included),
// the triager's screenshots and the consultant chat all reach a provider only
// through an agent.Loop built by buildLoop. That makes it the one place image
// evidence is recorded (B-515), instead of a hook at each call site that
// attaches images and could be missed by the next one.
type observedProvider struct {
	provider.Provider
	id       config.DucklingID
	registry *duckling.Registry
	// emit records a change of the duckling's recorded vision on the run
	// (vision_evidence), which is also what tells the desktop to refetch
	// the fleet. nil outside a run log.
	emit func(kind string, data map[string]interface{}) error
}

func (p observedProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	resp, err := p.Provider.Chat(ctx, req)
	p.observe(req, err)
	return resp, err
}

func (p observedProvider) ChatStream(ctx context.Context, req provider.ChatRequest, ch chan<- provider.Delta) (provider.ChatResponse, error) {
	resp, err := p.Provider.ChatStream(ctx, req, ch)
	p.observe(req, err)
	return resp, err
}

// observe folds one result into the duckling's health, and — when the request
// carried images — into its recorded vision.
func (p observedProvider) observe(req provider.ChatRequest, err error) {
	p.registry.RecordProviderResult(p.id, err)
	if !agent.RequestCarriesImages(req) {
		return
	}
	outcome, changed := p.registry.RecordImageEvidence(p.id, err)
	if !changed || p.emit == nil {
		return
	}
	data := map[string]interface{}{"duckling": string(p.id), "vision": outcome}
	if err != nil {
		data["error"] = err.Error()
	}
	_ = p.emit("vision_evidence", data)
}

type loopCache struct {
	svc         *Service
	tracker     *budget.Tracker
	writer      agent.RunLogWriter
	onDelta     func(*agent.Turn, string)
	onReasoning func(*agent.Turn, string)
	onToolCall  func(*agent.Turn, string, *agent.ToolCallRecord)
	// capLift, when set, lets the calls lift reach a reply already in
	// flight: the loop consults it before every model call.
	capLift func() bool
	// onRetry lands every transient provider failure on the record as it
	// happens — the alternative was up to twenty silent minutes.
	onRetry func(*agent.Turn, int, error)
	// onProviderStall identifies the local non-streaming watchdog separately
	// from a run wallclock expiry or an upstream HTTP error.
	onProviderStall func(*agent.Turn, time.Duration)
	// onCapNear says, in time to act, that a reply is on its last allowed
	// model call.
	onCapNear func(*agent.Turn, int, int)
	// onCall carries each model call's number against its cap, live.
	onCall func(*agent.Turn, int, int)
	// onToolStart says what just began running — the other half of a gate
	// command's fifteen legal minutes of silence.
	onToolStart      func(*agent.Turn, string, string, json.RawMessage)
	onRepetitionLoop func(*agent.Turn, string)
	mu               sync.Mutex
	loops            map[config.DucklingID]*agent.Loop
}

func (c *loopCache) get(ctx context.Context, id config.DucklingID) (*agent.Loop, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.loops[id]; ok {
		return l, nil
	}
	l, err := c.svc.buildLoop(ctx, id, c.tracker, c.writer)
	if err != nil {
		return nil, err
	}
	l.OnDelta = c.onDelta
	l.OnReasoning = c.onReasoning
	l.OnToolCall = c.onToolCall
	l.OnToolStart = c.onToolStart
	l.OnRepetitionLoop = c.onRepetitionLoop
	l.OnRetry = c.onRetry
	l.OnProviderStall = c.onProviderStall
	if writer, ok := c.writer.(*runLogAdapter); ok && writer != nil && writer.w != nil {
		l.OnRecovery = func(turn *agent.Turn, kind string, data map[string]interface{}) {
			event := make(map[string]interface{}, len(data)+4)
			for key, value := range data {
				event[key] = value
			}
			event["round"] = turn.Round
			event["turn"] = turn.Index
			event["role"] = string(turn.Role)
			event["duckling"] = string(id)
			writer.w.AppendEvent(kind, event)
		}
	}
	l.CapLift = c.capLift
	l.OnCapNear = c.onCapNear
	l.OnCall = c.onCall
	c.loops[id] = l
	return l, nil
}

// effectiveCaps is what a duckling can do, as the agent loop and every
// service one-shot must see it: declared capabilities over probing, plus the
// probed thinking control. One-shots used to build caps from the declaration
// alone, lost ThinkingControl, and suppressed reasoning with a local-server
// parameter OpenRouter ignores (B-479).
//
// probe is true for the agent loop, which may spend one probe request on an
// undeclared duckling. One-shots pass false and use the declaration plus what
// is already cached: an extra request per advice is not theirs to spend, and
// an unknown thinking control already makes oneShotCap assume reasoning.
func (s *Service) effectiveCaps(ctx context.Context, id config.DucklingID, probe bool) *duckling.Capabilities {
	// Declared capabilities win over probing: probing costs a request, and on
	// a local endpoint the declaration is usually more accurate anyway.
	caps := &duckling.Capabilities{NativeTools: false, ContextTokens: 32768}
	if cfg, ok := s.cfg.Ducklings[id]; ok && cfg.Caps.NativeTools != nil {
		caps.NativeTools = *cfg.Caps.NativeTools
	} else if cached, ok := s.ducklings.CachedCaps(id); ok {
		c := *cached // the declarations below must not write into the cache
		caps = &c
	} else if probe {
		if probed, err := s.ducklings.Probe(ctx, id); err == nil {
			c := *probed
			caps = &c
		}
	}
	// A declared context window wins on its own, not only beside a
	// native_tools declaration: the one-shot cap sizes itself from it, and a
	// probed or default 32K must not hide an 8K declaration (review of #134).
	if cfg, ok := s.cfg.Ducklings[id]; ok && cfg.Caps.ContextTokens != nil {
		caps.ContextTokens = *cfg.Caps.ContextTokens
	}
	if cfg, ok := s.cfg.Ducklings[id]; ok && cfg.Caps.Vision != nil {
		caps.Vision = *cfg.Caps.Vision
	}
	// Declared capabilities and observed endpoint behaviour are complementary.
	// A native_tools declaration must not hide a cached mandatory-reasoning
	// result from the request builder.
	if probed, ok := s.ducklings.CachedCaps(id); ok {
		caps.ThinkingControl = probed.ThinkingControl
		caps.ThinkingControlNote = probed.ThinkingControlNote
	}
	return caps
}

// buildLoop assembles the agent loop for one duckling.
func (s *Service) buildLoop(ctx context.Context, id config.DucklingID, tracker *budget.Tracker, writer agent.RunLogWriter) (*agent.Loop, error) {
	d, err := s.ducklings.Get(id)
	if err != nil {
		return nil, fmt.Errorf("duckling %q: %w", id, err)
	}
	p, err := s.ducklings.Provider(id)
	if err != nil {
		return nil, fmt.Errorf("duckling %q provider: %w", id, err)
	}
	observed := observedProvider{Provider: p, id: id, registry: s.ducklings}
	if adapter, ok := writer.(*runLogAdapter); ok && adapter != nil && adapter.w != nil {
		observed.emit = adapter.w.AppendEvent
	}
	p = observed

	caps := s.effectiveCaps(ctx, id, true)

	loop := &agent.Loop{
		Provider: p,
		Duckling: &agent.DucklingConfig{
			ID: id, Provider: d.Provider, Model: d.Model,
			Params: d.Params, Caps: duckling.ProviderCaps(caps), Cost: d.Cost,
		},
		Registry:              tools.NewRegistry(),
		Budget:                tracker,
		MaxTurns:              s.cfg.Defaults.AgentMaxTurns,
		RepairAttempts:        s.cfg.Defaults.RepairAttempts,
		ContractRepairTimeout: time.Duration(s.cfg.Defaults.HTTPTimeoutS) * time.Second,
		NonStreamingTimeout:   time.Duration(s.cfg.Defaults.HTTPTimeoutS) * time.Second,
		NarratedToolLimit:     s.cfg.Defaults.NarratedToolLimit,
		RunWriter:             writer,
		// A seat whose endpoint refused images — in this run or recorded
		// before it — gets none: the turn is told why instead of failing on
		// the same 400 again (B-515).
		SeesImages: func() bool { return s.seatCanSee(id) },
	}
	return loop, nil
}

// resolveRoster fills every role a mode needs.
//
// When a project declares no roster, roles are spread across the available
// ducklings rather than all defaulting to one. Assigning the same model to
// implementer and reviewer measures self-consistency, not review — the second
// model exists to be decorrelated, and silently collapsing it to the first
// would make pair look like it works while doing nothing (05 §3.2).
func (s *Service) resolveCanonicalRoster(projCfg *config.Project, mode string) (map[config.Role]config.DucklingID, map[config.Role]string) {
	out := map[config.Role]config.DucklingID{}
	sources := map[config.Role]string{}
	s.cfgMu.RLock()
	seats := s.cfg.Defaults.ModeSeats[mode]
	pins := s.cfg.Defaults.RolePins
	available := make(map[config.DucklingID]bool, len(s.cfg.Ducklings))
	autoAvailable := make(map[config.DucklingID]bool, len(s.cfg.Ducklings))
	for id := range s.cfg.Ducklings {
		available[id] = true
		if !s.ducklings.LastProbeFailed(id) {
			autoAvailable[id] = true
		}
	}
	s.cfgMu.RUnlock()
	firstAvailable := func(ids []config.DucklingID) config.DucklingID {
		for _, id := range ids {
			if available[id] {
				return id
			}
		}
		return ""
	}
	toIDs := func(in []string) []config.DucklingID {
		out := make([]config.DucklingID, len(in))
		for i, id := range in {
			out[i] = config.DucklingID(id)
		}
		return out
	}
	// Precedence per seat, and the record says which rung answered:
	// project mode seat → project role pin → global mode seat → global role
	// pin. A role nobody seated resolves to NOBODY. It used to resolve to the
	// alphabetically first registered duckling ("global role fallback"), so a
	// mode with no advisor got atom-local as its duck and a launch with no
	// implementer got whatever sorted first (B-063). Optional roles simply
	// stay empty — the duck skips its consult, ask_advisor says nobody is
	// seated; required roles are checked at launch by requiredSeatsFor, which
	// refuses with the seat named instead of guessing.
	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		// A project pin is honoured as written — it is the person's own
		// word; naming an unregistered duckling is a launch-time error with
		// the seat named, never a silent skip to the next rung.
		if projCfg != nil && projCfg.ModeSeats != nil {
			if ids := projCfg.ModeSeats[mode][string(role)]; len(ids) > 0 && ids[0] != "" {
				out[role], sources[role] = config.DucklingID(ids[0]), "project mode seat"
				continue
			}
		}
		if projCfg != nil {
			if ids := projCfg.RosterSeats[role]; len(ids) > 0 && ids[0] != "" {
				out[role], sources[role] = ids[0], "project pin"
				continue
			}
			if id := projCfg.Roster[role]; id != "" {
				out[role], sources[role] = id, "project pin"
				continue
			}
		}
		if id := firstAvailable(toIDs(seats[string(role)])); id != "" {
			out[role], sources[role] = id, "global mode seat"
			continue
		}
		// The documents' architect: a stage run in solo still drafts with the
		// council's architect, not with whoever implements tasks — the seat
		// the person configured for writing documents is that one. (This
		// used to hold only because the alphabet happened to agree.)
		if role == config.RoleArchitect && mode != "council" {
			s.cfgMu.RLock()
			council := s.cfg.Defaults.ModeSeats["council"]
			s.cfgMu.RUnlock()
			if id := firstAvailable(toIDs(council["architect"])); id != "" {
				out[role], sources[role] = id, "global mode seat (council)"
				continue
			}
		}
		if id := firstAvailable(toIDs(pins[string(role)])); id != "" {
			out[role], sources[role] = id, "global role fallback"
			continue
		}
		out[role], sources[role] = "", "unseated"
	}
	// A blank installation — two ducklings registered, no seat configured
	// anywhere — must still be able to run: the engine picks a distinct
	// duckling per role and says so on the record. The moment anything is
	// configured, it stops guessing: a configured install with no triager
	// gets "no triager seated" at launch, not the alphabet (B-063).
	if !s.anySeatConfigured(projCfg) {
		availableIDs := make([]config.DucklingID, 0, len(autoAvailable))
		for id := range autoAvailable {
			availableIDs = append(availableIDs, id)
		}
		sort.Slice(availableIDs, func(i, j int) bool { return availableIDs[i] < availableIDs[j] })
		for _, role := range config.ValidRoles() {
			// Every role but the advisor: a duck nobody asked for is the one
			// seat that must stay empty (it costs a turn and speaks into the
			// run), and the harness already knows what an empty duck means.
			if role == config.RoleHuman || role == config.RoleAdvisor || out[role] != "" {
				continue
			}
			for _, id := range availableIDs {
				used := false
				for _, assigned := range out {
					if assigned == id {
						used = true
						break
					}
				}
				if !used {
					out[role] = id
					break
				}
			}
			if out[role] == "" && len(availableIDs) > 0 {
				out[role] = availableIDs[0]
			}
			if out[role] != "" {
				sources[role] = "engine picked (no seats configured)"
			}
		}
	}
	return out, sources
}

// anySeatConfigured reports whether a person has seated anyone anywhere —
// global mode seats or role pins, project mode seats or role pins.
func (s *Service) anySeatConfigured(projCfg *config.Project) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	for _, seats := range s.cfg.Defaults.ModeSeats {
		for _, ids := range seats {
			if len(ids) > 0 {
				return true
			}
		}
	}
	for _, ids := range s.cfg.Defaults.RolePins {
		if len(ids) > 0 {
			return true
		}
	}
	if projCfg != nil {
		for _, seats := range projCfg.ModeSeats {
			for _, ids := range seats {
				if len(ids) > 0 {
					return true
				}
			}
		}
		for _, ids := range projCfg.RosterSeats {
			if len(ids) > 0 {
				return true
			}
		}
		for _, id := range projCfg.Roster {
			if id != "" {
				return true
			}
		}
	}
	return false
}

// requiredSeatsFor names the roles a mode cannot run without. Everything
// else — the advisor above all — is optional: absent means absent.
func requiredSeatsFor(mode string) []config.Role {
	switch mode {
	case "solo", "":
		return []config.Role{config.RoleImplementer}
	case "pair":
		return []config.Role{config.RoleImplementer, config.RoleReviewer}
	case "council":
		return []config.Role{config.RoleArchitect, config.RoleReviewer}
	case "split":
		return []config.Role{config.RoleArchitect, config.RoleImplementer, config.RoleReviewer}
	case "tournament":
		return []config.Role{config.RoleImplementer, config.RoleJudge}
	case "triage":
		return []config.Role{config.RoleTriager}
	case "release":
		return []config.Role{config.RoleScribe}
	}
	return nil
}

// unseatedRequired reports which required seats of a mode are empty, in a
// message that names the seat and the door that fills it.
func unseatedRequired(mode string, roster map[config.Role]config.DucklingID) error {
	var missing []string
	for _, role := range requiredSeatsFor(mode) {
		if roster[role] == "" {
			missing = append(missing, string(role))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("no %s seated for %s — assign one on the Roster board (or pass ducklings on the launch)", strings.Join(missing, ", "), mode)
}

// resolveRoster resolves every seat for a run of the given mode. The mode
// matters: it used to be resolved with mode "" at every launch, so the
// global per-mode seats never reached a run except through the desktop
// launcher's prefill (B-063).
func (s *Service) resolveRoster(projCfg *config.Project, mode string) (map[config.Role]config.DucklingID, string) {
	out, _ := s.resolveCanonicalRoster(projCfg, mode)
	return out, bothSidesWarning(out)
}

// bothSidesWarning reports a roster that puts one duckling on both sides.
//
// Recomputed after a run's picked ducklings are applied: the warning used to be
// produced inside resolveRoster, and a picker that assigned the implementer
// afterwards could create exactly this collision with the warning already
// decided against the old roster.
func bothSidesWarning(out map[config.Role]config.DucklingID) string {
	if out[config.RoleImplementer] == "" || out[config.RoleImplementer] != out[config.RoleReviewer] {
		return ""
	}
	return fmt.Sprintf(
		"%s is on both sides of the pair: this measures self-consistency, not review. "+
			"Configure a second duckling, or set [roster] reviewer in project.toml.",
		out[config.RoleImplementer])
}

// runnerFor returns a TurnRunner that picks the loop matching each turn's role.
func cacheRunID(ectx *tools.ExecContext) string {
	if ectx == nil {
		return ""
	}
	return ectx.RunID
}

func (s *Service) runnerFor(cache *loopCache, roster map[config.Role]config.DucklingID, ectx *tools.ExecContext) strategy.TurnRunner {
	return func(ctx context.Context, t *strategy.Turn, d config.DucklingID, prompt string, belt []string, tc strategy.TurnContext) (*agent.Outcome, error) {
		if d == "" {
			d = roster[t.Role]
		}
		loop, err := cache.get(ctx, d)
		if err != nil {
			return nil, err
		}
		// Each turn gets its own exec context so the role recorded with a tool
		// call is the role that actually made it.
		turnCtx := *ectx
		turnCtx.Role = t.Role
		turnCtx.Duckling = d
		turnCtx.SeatContextTokens = loop.Duckling.Caps.ContextTokens
		// A contestant works in its own worktree. Leaving the project root
		// here is what let every tournament contestant edit the shared tree.
		if tc.Root != "" {
			turnCtx.ProjectRoot = tc.Root
		}
		provider := string(loop.Duckling.Provider)
		if err := s.queue.acquireProvider(ctx, s, provider, cacheRunID(ectx)); err != nil {
			return nil, err
		}
		defer s.queue.releaseProvider(provider, cacheRunID(ectx))
		return agent.RunTurn(ctx, loop, t.AgentTurn(d, prompt, belt, tc.Round, tc.Index), &turnCtx)
	}
}

// humanNote renders the person's run-specific instruction as a prompt
// section. The task body was written before history happened; this is the
// channel for what only the person knows now — "address the reviewer's
// outstanding findings", most of all.
func humanNote(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return "\n\n## Note from the human\n\n" + note + "\n"
}

// uncappedTurns stands in for "no cap" on the per-reply call loop — the
// agent loop's own constant, shared so a lift resolved here and a lift
// applied mid-flight mean the same number.
const uncappedTurns = agent.UncappedTurns

type resolvedTurnCaps struct {
	Caps                 map[config.Role]int
	Sources              map[config.Role]string
	SmallSeatPairReserve int
}

func effectiveSmallSeatPairReserve(configured int) int {
	if configured > 0 {
		return configured
	}
	return config.DefaultSmallSeatPairReserve
}

// resolveTurnCaps is the single precedence rule for calls/reply. The returned
// maps stay sparse: a role absent from them keeps the script's designed cap.
// Build/test apply their phase portion to the implementer only (an empty phase
// value uses the global fallback); role and run overrides are progressively
// more specific and may name every role. A script may still impose a hard
// ceiling; strategy records that final clamp because only it knows the turn.
func (s *Service) resolveTurnCaps(phase string, override int) resolvedTurnCaps {
	return s.resolveTurnCapsFor(phase, override, "", false)
}

// resolveTurnCapsFor adds contextual defaults between the generic role layer
// and explicit run choices. Pair's small-seat reserve is deliberately not a
// ceiling: a launch override or live no-cap remains authoritative.
func (s *Service) resolveTurnCapsFor(phase string, override int, mode string, smallSeat bool) resolvedTurnCaps {
	s.cfgMu.RLock()
	global := s.cfg.Defaults.AgentMaxTurns
	phaseCap := s.cfg.Defaults.PhaseTurns[phase]
	pairReserve := effectiveSmallSeatPairReserve(s.cfg.Defaults.SmallSeatPairReserve)
	roleCaps := make(map[string]int, len(s.cfg.Defaults.RoleTurns))
	for role, n := range s.cfg.Defaults.RoleTurns {
		roleCaps[role] = n
	}
	s.cfgMu.RUnlock()
	if global <= 0 {
		global = 24
	}
	out := resolvedTurnCaps{Caps: map[config.Role]int{}, Sources: map[config.Role]string{}}
	if phase == "build" || phase == "test" {
		cap, source := global, "global default"
		if phaseCap > 0 {
			cap, source = phaseCap, phase+" default"
		}
		out.Caps[config.RoleImplementer] = cap
		out.Sources[config.RoleImplementer] = source
	}
	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		if n := roleCaps[string(role)]; n > 0 {
			out.Caps[role] = n
			out.Sources[role] = string(role) + " role default"
		}
	}
	if mode == "pair" && smallSeat {
		out.Caps[config.RoleImplementer] = pairReserve
		out.Sources[config.RoleImplementer] = "small-seat pair reserve (default)"
		out.SmallSeatPairReserve = pairReserve
	}
	if override != 0 {
		for _, role := range config.ValidRoles() {
			if role == config.RoleHuman {
				continue
			}
			out.Caps[role] = capOverride(override)
			if override < 0 {
				out.Sources[role] = "run no-cap"
			} else {
				out.Sources[role] = "run override"
			}
		}
	}
	return out
}

// resolveRosterTurnCaps is the execution boundary: capacity policy follows
// the roster already chosen for this run, never a second read of project
// defaults that can disagree with --ducklings or role-keyed seats.
func (s *Service) resolveRosterTurnCaps(phase string, override int, mode string, roster map[config.Role]config.DucklingID) (resolvedTurnCaps, int) {
	smallSeat := s.smallImplementerSeat(roster)
	resolved := s.resolveTurnCapsFor(phase, override, mode, smallSeat)
	if smallSeat && mode == "pair" {
		return resolved, resolved.SmallSeatPairReserve
	}
	return resolved, 0
}

// capOverride resolves a run's AgentTurns override: negative means no cap.
func capOverride(override int) int {
	if override < 0 {
		return uncappedTurns
	}
	return override
}

// modeContext carries everything a mode dispatch needs.
type modeContext struct {
	entry   *registry.ProjectEntry
	projCfg *config.Project
	rs      *runState
	ectx    *tools.ExecContext
	cache   *loopCache
	roster  map[config.Role]config.DucklingID
	req     RunRequest
}

// dispatchMode runs the requested duck mode.
func (s *Service) modeTurnMedian(mode, exclude string) float64 {
	s.runsMu.RLock()
	states := make(map[string]*runState, len(s.runs))
	for id, rs := range s.runs {
		states[id] = rs
	}
	s.runsMu.RUnlock()
	var turns []float64
	for id, rs := range states {
		if id == exclude || rs == nil || rs.run == nil {
			continue
		}
		current := rs.snapshotRun()
		if current.Mode != mode || current.Budget.Turns <= 0 {
			continue
		}
		turns = append(turns, float64(current.Budget.Turns))
	}
	if len(turns) == 0 {
		return 0
	}
	slices.Sort(turns)
	middle := len(turns) / 2
	if len(turns)%2 == 1 {
		return turns[middle]
	}
	return (turns[middle-1] + turns[middle]) / 2
}

func resumeTurn(run *runlog.Run) *strategy.ResumeTurn {
	if run == nil || run.InterruptedTurn == nil {
		return nil
	}
	t := run.InterruptedTurn
	findings := make([]conv.Finding, 0, len(t.Findings))
	for _, f := range t.Findings {
		findings = append(findings, conv.Finding{Severity: f.Severity, File: f.File, Line: f.Line, Issue: f.Issue, Fix: f.Fix, Invariant: f.Invariant})
	}
	return &strategy.ResumeTurn{Round: t.Round, Index: t.Index, Role: config.Role(t.Role), Notes: t.Notes, VerifiedAfterMutation: t.VerifiedAfterMutation, Looked: t.Looked, Findings: findings}
}

func interruptedTurnFromEvent(data map[string]interface{}) *runlog.InterruptedTurn {
	verified, _ := data["verified_after_mutation"].(bool)
	t := &runlog.InterruptedTurn{Round: intValue(data["round"]), Index: intValue(data["turn"]), Role: stringValueAny(data["role"]), Notes: stringValueAny(data["notes"]), VerifiedAfterMutation: verified, Looked: stringSliceAny(data["looked"])}
	if raw, err := json.Marshal(data["findings"]); err == nil {
		_ = json.Unmarshal(raw, &t.Findings)
	}
	return t
}

func stringValueAny(v interface{}) string {
	s, _ := v.(string)
	return s
}

// stringSliceAny reads a []string that may arrive as []interface{} from an
// event map.
func stringSliceAny(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func intValue(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func (s *Service) dispatchMode(ctx context.Context, mc *modeContext) error {
	cards, _ := s.Scorecards(ctx)
	currentSeat := string(mc.roster[config.RoleImplementer])
	escalationCandidates, currentFloor := escalationCandidatesFor(string(config.RoleImplementer), currentSeat, cards)
	root := mc.ectx.ProjectRoot
	smallSeat := s.smallImplementerSeat(mc.roster)
	turnCaps, pairReserve := s.resolveRosterTurnCaps("build", mc.req.AgentTurns, mc.rs.run.Mode, mc.roster)
	if smallSeat && mc.rs.run.Mode == "pair" && turnCaps.Caps[config.RoleImplementer] > pairReserve {
		mc.rs.writer.AppendEvent("warning", map[string]interface{}{
			"detail": fmt.Sprintf("%s for %s raises the small-seat pair reserve from %d to %d calls/reply; the independent reviewer's slot may starve before review begins",
				turnCaps.Sources[config.RoleImplementer], mc.roster[config.RoleImplementer], pairReserve, turnCaps.Caps[config.RoleImplementer]),
		})
	}
	deliverables := s.taskDeliverables(ctx, mc.rs.run.ProjectID, mc.req.TaskID)
	base := strategy.ExecuteParams{
		LiveToolEvents:       true,
		EscalationCandidates: escalationCandidates,
		CurrentLowerBound:    currentFloor,
		ModeMedian:           s.modeTurnMedian(mc.rs.run.Mode, mc.rs.run.ID),
		ResumeFrom:           resumeTurn(mc.rs.run),
		ProjectRoot:          root,
		TaskID:               mc.req.TaskID,
		// Task execution must carry the same resolved seat class as document
		// stages. Without this, small-seat strategy guards existed but build
		// pair runs silently received the large-seat behavior.
		SmallSeat:            smallSeat,
		SmallSeatPairReserve: pairReserve,
		// Answers the person already gave ride ON the prompt: a resumed run
		// replays from scratch, and a model that cannot see the decisions
		// re-asks them in new words forever.
		Prompt: s.buildTaskPrompt(ctx, mc.rs.run.ProjectID, mc.entry.Path, mc.req.TaskID) +
			oracleBrief(mc.ectx.OracleTests) + humanNote(mc.req.Note) + mc.rs.answeredDecisions(),
		// The task's bullets, numbered: the implementer's work contract
		// (strategy/deliverables.go). The plan's words, never the model's.
		Deliverables:       deliverables,
		ManualDeliverables: manualDeliverables(deliverables),
		ExecContext:        mc.ectx,
		// The request, then the configured default for this mode, then the
		// script's own count. The counts lived only in the scripts, so changing
		// how many times a reviewer got to push back meant editing Go.
		Rounds: s.roundsFor(mc.rs.run.Mode, mc.req.Rounds),
		Runner: s.runnerFor(mc.cache, mc.roster, mc.ectx),
		Roster: mc.roster,
		// So tournament and split, which build their own turns, honour the same
		// per-role caps as every other mode.
		TurnCaps:       turnCaps.Caps,
		TurnCapSources: turnCaps.Sources,
		Gate: func(ctx context.Context) (string, string, error) {
			mc.rs.recordGateRoot(root)
			gate, log, err := tools.RunVerificationGate(ctx, mc.ectx)
			if err != nil {
				// A build-system marker without its tool is not a gate error:
				// it is a toolchain the person has not installed, named on the
				// record so the next launch's pre-flight (toolchain.go) can ask.
				var missing *verify.MissingToolchain
				if errors.As(err, &missing) {
					mc.rs.writer.AppendEvent("toolchain_missing", map[string]interface{}{
						"tool": missing.Tool, "marker": missing.Marker,
						"detail": fmt.Sprintf("the tree has %s but %s is not installed — the gate cannot run; install %s to verify this work", missing.Marker, missing.Tool, missing.Tool),
					})
					return "none", "", nil
				}
				return "none", "", err
			}
			return gate, log, nil
		},
		Diff: func() (string, error) {
			return vcs.New(root).DiffExcluding(runDiffExclusions(mc.rs.run, root, mc.entry.Path)...)
		},
		InvariantFindings: func() ([]conv.Finding, error) {
			paths, err := vcs.New(root).WorkingChangedPaths(runDiffExclusions(mc.rs.run, root, mc.entry.Path)...)
			if err != nil {
				return nil, err
			}
			return taskCandidateInvariantFindings(root, mc.req.TaskID, paths), nil
		},
		AdvisorLaneConflicts: func(note string) []string {
			return advisorLaneConflicts(root, mc.req.TaskID, note)
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			mc.rs.writer.AppendEvent(kind, data)
			if kind == "turn_interrupted" {
				mc.rs.wmu.Lock()
				mc.rs.run.InterruptedTurn = interruptedTurnFromEvent(data)
				mc.rs.writer.WriteState()
				mc.rs.wmu.Unlock()
			} else if kind == "turn_end" && data["incomplete"] != true {
				// Keep a replayable checkpoint until a pending safe-point pause
				// has either landed or been ruled out.
				mc.rs.wmu.Lock()
				mc.rs.run.InterruptedTurn = interruptedTurnFromEvent(data)
				mc.rs.wmu.Unlock()
				if !s.pauseAtSafePoint(mc.rs) {
					mc.rs.wmu.Lock()
					mc.rs.run.InterruptedTurn = nil
					mc.rs.writer.WriteState()
					mc.rs.wmu.Unlock()
				}
			}
		},
	}

	// B-504 (TI-36X T-008): a task citing a REF-IMG is built and reviewed by
	// seats that are shown it — every implementer, reviewer, judge and
	// advisor turn (B-507), retries and resumes included, through the runner.
	// Solo and pair also render the candidate between turns whenever the tree
	// changed (B-505), and the slices citing a compared reference are
	// measured by that render rather than by the implementer's report
	// (B-506). Tournament and split contestants work in their own worktrees,
	// which the render command does not know about, so they get the
	// references only, and their slices stay the implementer's.
	vision := s.newTaskVision(ctx, mc.rs.run.ProjectID, mc.req.TaskID, []string{mc.entry.Path, root}, mc.roster,
		[]config.Role{config.RoleImplementer, config.RoleReviewer, config.RoleJudge, config.RoleAdvisor},
		func(kind string, data map[string]interface{}) { mc.rs.writer.AppendEvent(kind, data) })
	if m := mc.rs.run.Mode; m == "" || m == "solo" || m == "pair" {
		contract := effectiveRenderContract(mc.projCfg)
		if mc.projCfg.RenderConfigured && contract.Command != "" && len(contract.Compare) > 0 {
			vision = vision.withFeedback(mc.rs.runDir, base.Diff, func(ctx context.Context) (*runlog.VisualGate, []string, error) {
				rendered, err := captureRender(ctx, root, contract, mc.rs.writer, mc.rs.run.ID, mc.rs.run.ProjectID)
				if len(rendered.Captures) == 0 {
					if err == nil {
						err = fmt.Errorf("the render produced no captures")
					}
					return nil, nil, err
				}
				return runVisualGate(mc.entry.Path, contract, mc.rs.writer, rendered.Captures, root), rendered.Captures, nil
			})
			base.Visual = vision.visualCheck(deliverables, contract)
		}
	}
	base.Runner = vision.wrap(base.Runner, mc.roster)
	base.Gate = vision.wrapGate(base.Gate)

	switch mc.rs.run.Mode {
	case "", "solo":
		res, err := strategy.ExecuteScript(ctx, strategy.SoloScript(), &base)
		return pendingOrErr(res, err)

	case "pair":
		res, err := strategy.ExecuteScript(ctx, strategy.PairScript(), &base)
		return pendingOrErr(res, err)

	case "tournament":
		return s.runTournament(ctx, mc, base)

	case "split":
		return s.runSplit(ctx, mc, base)

	default:
		return fmt.Errorf("unknown mode %q (available: solo, pair, tournament, split)", mc.rs.run.Mode)
	}
}

func (s *Service) runTournament(ctx context.Context, mc *modeContext, base strategy.ExecuteParams) error {
	var contestants []config.DucklingID
	for _, id := range mc.req.Ducklings {
		contestants = append(contestants, config.DucklingID(id))
	}
	if len(contestants) < 2 {
		// Two contestants from the roster: implementer plus the next distinct
		// duckling. A tournament against yourself measures self-consistency,
		// not decorrelation, so it is worth being explicit about.
		contestants = distinctDucklings(mc.roster, s.cfg.Ducklings, 2)
	}

	scratch := filepath.Join(mc.entry.Path, ".ducklab", "worktrees")
	tp := &strategy.TournamentParams{
		ExecuteParams: base,
		Contestants:   len(contestants),
		Ducklings:     contestants,
		NewWorkspace:  strategy.NewGitWorkspaceFactory(mc.entry.Path, scratch, mc.rs.run.ID),
		GateIn: func(ctx context.Context, root string) (string, string, error) {
			res, err := verify.Run(ctx, root, mc.projCfg.Verify, verify.Identity{RunID: mc.rs.run.ID, ProjectID: mc.rs.run.ProjectID})
			if err != nil {
				// A build-system marker without its tool is not a gate error:
				// it is a toolchain the person has not installed, named on the
				// record so the next launch's pre-flight (toolchain.go) can ask.
				var missing *verify.MissingToolchain
				if errors.As(err, &missing) {
					mc.rs.writer.AppendEvent("toolchain_missing", map[string]interface{}{
						"tool": missing.Tool, "marker": missing.Marker,
						"detail": fmt.Sprintf("the tree has %s but %s is not installed — the gate cannot run; install %s to verify this work", missing.Marker, missing.Tool, missing.Tool),
					})
					return "none", "", nil
				}
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		Apply: func(patch string) error {
			return vcs.New(mc.entry.Path).ApplyPatch(patch)
		},
	}

	res, err := strategy.ExecuteTournament(ctx, tp)
	if res != nil {
		mc.rs.wmu.Lock()
		mc.rs.run.Resolution = res.Resolution
		mc.rs.wmu.Unlock()
		mc.rs.writer.AppendEvent("resolution", map[string]interface{}{
			"resolution": res.Resolution,
			"winner":     res.Winner,
			"reason":     res.Reason,
		})
		for _, c := range res.Candidates {
			mc.rs.writer.WriteCandidate(c.Label, c.Diff)
		}
	}
	return err
}

func (s *Service) runSplit(ctx context.Context, mc *modeContext, base strategy.ExecuteParams) error {
	scratch := filepath.Join(mc.entry.Path, ".ducklab", "worktrees")
	var subtaskDucklings []config.DucklingID
	for _, id := range mc.req.Ducklings {
		subtaskDucklings = append(subtaskDucklings, config.DucklingID(id))
	}
	sp := &strategy.SplitParams{
		ExecuteParams: base,
		Ducklings:     subtaskDucklings,
		NewWorkspace:  strategy.NewGitWorkspaceFactory(mc.entry.Path, scratch, mc.rs.run.ID),
		GateIn: func(ctx context.Context, root string) (string, string, error) {
			res, err := verify.Run(ctx, root, mc.projCfg.Verify, verify.Identity{RunID: mc.rs.run.ID, ProjectID: mc.rs.run.ProjectID})
			if err != nil {
				// A build-system marker without its tool is not a gate error:
				// it is a toolchain the person has not installed, named on the
				// record so the next launch's pre-flight (toolchain.go) can ask.
				var missing *verify.MissingToolchain
				if errors.As(err, &missing) {
					mc.rs.writer.AppendEvent("toolchain_missing", map[string]interface{}{
						"tool": missing.Tool, "marker": missing.Marker,
						"detail": fmt.Sprintf("the tree has %s but %s is not installed — the gate cannot run; install %s to verify this work", missing.Marker, missing.Tool, missing.Tool),
					})
					return "none", "", nil
				}
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		CopyFile: copyOwnedFile,
	}

	res, err := strategy.ExecuteSplit(ctx, sp)
	if res != nil && err != nil {
		// A run that stops to ask a person has not failed; it is waiting.
		// Returning the raw error here marked the first real split run FAILED
		// because its architect asked a question.
		err = pendingOrErr(&strategy.ExecuteResult{Outcome: res.Outcome}, err)
	}
	if res != nil {
		mc.rs.writer.AppendEvent("split_result", map[string]interface{}{
			"subtasks": len(res.Subtasks), "integrated": res.Integrated,
			"gate": res.Gate, "retried": res.Retried, "seam_rounds": res.SeamRoundsUsed,
		})
	}
	return err
}

// copyOwnedFile copies one integrated file, reporting whether it existed.
//
// A file a subtask claimed and never created is not an error: deciding a file
// was unnecessary is a legitimate outcome, and removing the target copy would
// discard whatever was there before.
func copyOwnedFile(from, to string) (bool, error) {
	data, err := os.ReadFile(from)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return false, err
	}
	// Preserve the mode of what was written, so an executable stays one.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(from); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(to, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

func distinctDucklings(roster map[config.Role]config.DucklingID, all map[config.DucklingID]config.Duckling, n int) []config.DucklingID {
	seen := map[config.DucklingID]bool{}
	var out []config.DucklingID
	add := func(id config.DucklingID) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	add(roster[config.RoleImplementer])
	add(roster[config.RoleReviewer])
	for id := range all {
		if len(out) >= n {
			break
		}
		add(id)
	}
	// Fewer distinct ducklings than contestants: repeat the first rather than
	// fail. The run is still valid, it just measures self-consistency.
	for len(out) < n && len(out) > 0 {
		out = append(out, out[0])
	}
	return out
}

func gateWord(res *verify.Result) string {
	switch {
	case verify.IsGreen(res):
		return "green"
	case verify.IsRed(res):
		return "red"
	default:
		return "none"
	}
}

func taskPrompt(taskID string) string {
	return fmt.Sprintf("Implement task %s", taskID)
}

func rosterStrings(r map[config.Role]config.DucklingID) map[string]string {
	out := map[string]string{}
	for role, id := range r {
		out[string(role)] = string(id)
	}
	return out
}

func (s *Service) rosterSources(projCfg *config.Project, mode string, chosen []string, seats map[string]string) map[string]string {
	_, sources := s.resolveCanonicalRoster(projCfg, mode)
	out := make(map[string]string, len(sources))
	for role, source := range sources {
		out[string(role)] = source
	}
	// A run pick is the highest-priority, non-persistent override. The request
	// lineup is positional only at the launch boundary; resolution below records
	// its seats before any configured source can be reported.
	for i, role := range []config.Role{config.RoleImplementer, config.RoleReviewer} {
		if i < len(chosen) && chosen[i] != "" {
			out[string(role)] = "request"
		}
	}
	for role, id := range seats {
		if id != "" {
			out[role] = "request"
		}
	}
	return out
}

// Report aggregates this project's runs into the solo-baseline comparison.
func (s *Service) Report(ctx context.Context, projectID string, opts report.Options) (*report.Report, error) {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	// Runs made before spend was recorded have none. Dropping them from the
	// per-duckling table would quietly shrink the history the table exists to
	// summarise, and the information is not lost — it was never rolled up.
	if entry, err := s.registry.Get(projectID); err == nil {
		for _, r := range runs {
			backfillSpend(entry.Path, r)
		}
	}
	return report.Build(runs, opts), nil
}

// pendingOrErr surfaces a human-input pause with the question attached, so the
// caller can checkpoint it rather than treating it as a failure.
func pendingOrErr(res *strategy.ExecuteResult, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tools.ErrHumanNeeded) && res != nil && res.Outcome != nil && res.Outcome.Pending != nil {
		return &pendingErr{q: res.Outcome.Pending, err: err}
	}
	return err
}

// pendingErr carries the question a run stopped on.
type pendingErr struct {
	q   *tools.PendingQuestion
	err error
}

func (e *pendingErr) Error() string { return e.err.Error() }
func (e *pendingErr) Unwrap() error { return e.err }

// recordLimits copies the ceilings a run was given onto its record.
//
// It lived at one of the six places a tracker is created, so five kinds of run —
// stages, review, release, triage, test-first — wrote no limits at all and the
// desktop drew their meters as 0 / 0. Next to the tracker rather than in each
// caller, because that is the one place it cannot be forgotten.
func recordLimits(rs *runState, b *budget.Budget) {
	if rs == nil || b == nil {
		return
	}
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	rs.run.Budget.Limit = runlog.BudgetLimits{
		USD: b.MaxUSD, Tokens: b.MaxTokens, Turns: b.MaxTurns, WallclockS: b.MaxWallclockS,
	}
}

// trackerFromRecord rebuilds a resumed run's tracker from its record: the
// ceilings recorded on it — including any a person lifted while it was paused
// — and a ledger continuing from what it already spent. A tracker reborn at
// zero makes "resume" a way to double every budget and restarts the budget
// clock. It was written out at each resume path; the test-first one never got
// it (B-500).
func trackerFromRecord(run *runlog.Run) (*budget.Budget, *budget.Tracker) {
	b := &budget.Budget{
		MaxUSD: run.Budget.Limit.USD, MaxTokens: run.Budget.Limit.Tokens,
		MaxTurns: run.Budget.Limit.Turns, MaxWallclockS: run.Budget.Limit.WallclockS,
	}
	tracker := budget.NewTracker(b)
	tracker.Spend.AddTokens(run.Budget.Tokens)
	tracker.Spend.AddUSD(run.Budget.USD)
	tracker.Spend.RestoreWallclock(run.Budget.WallclockS)
	for i := 0; i < run.Budget.Turns; i++ {
		tracker.Spend.AddTurn()
	}
	return b, tracker
}

// recordSpend copies the budget tracker's totals onto the run record.
func recordSpend(rs *runState, tracker *budget.Tracker) {
	if rs == nil || rs.run == nil {
		return
	}
	// Settle the active interval whenever execution records progress. A later
	// pause or queue transition therefore cannot leak its wait into history.

	if tracker == nil || tracker.Spend == nil {
		return
	}
	snap := tracker.Spend.Snapshot()
	rs.wmu.Lock()
	defer rs.wmu.Unlock()
	rs.run.Budget.USD = snap.USD
	rs.run.Budget.Tokens = snap.Tokens
	rs.run.Budget.Turns = snap.Turns
	rs.run.Budget.WallclockS = snap.WallclockS

	// WallclockMs remains the legacy elapsed field for reports. Duration
	// escalation instead uses ActiveWallclockMs, which excludes waits.
	if started, err := time.Parse(time.RFC3339, rs.run.StartedAt); err == nil {
		rs.run.WallclockMs = time.Since(started).Milliseconds()
	}
}

// assignChosenDucklings applies the ducklings a person picked for this run to
// the roles the mode will actually use.
//
// Tournament and split read req.Ducklings themselves, because both hand a list
// to a strategy that assigns it positionally. Solo and pair read only the
// roster, so a picker that offered a choice for those modes changed nothing —
// pick pato-sonnet for a solo run and the project roster's implementer ran
// instead, with nothing on screen to say so.
//
// Positional, matching split: first is the implementer, second the reviewer. A
// solo run uses one duckling, so extra picks are the person's business and not
// an error worth refusing a run over.
func assignChosenSeats(roster map[config.Role]config.DucklingID, seats map[string]string) {
	valid := make(map[config.Role]bool)
	for _, role := range config.ValidRoles() {
		valid[role] = true
	}
	for role, id := range seats {
		key := config.Role(role)
		if id != "" && valid[key] {
			roster[key] = config.DucklingID(id)
		}
	}
}

func assignChosenDucklings(roster map[config.Role]config.DucklingID, mode string, chosen []string) {
	if len(chosen) == 0 || roster == nil {
		return
	}
	var roles []config.Role
	switch mode {
	case "", "solo":
		roles = []config.Role{config.RoleImplementer}
	case "pair":
		roles = []config.Role{config.RoleImplementer, config.RoleReviewer}
	default:
		// tournament and split take the list whole; overwriting the roster here
		// would fight the assignment they do themselves.
		return
	}
	for i, role := range roles {
		if i >= len(chosen) || chosen[i] == "" {
			return
		}
		roster[role] = config.DucklingID(chosen[i])
	}
}

// taskDeliverables numbers a task's bullets for the implementer's contract.
// No task, no contract: stage runs and chat carry none.
func (s *Service) taskDeliverables(ctx context.Context, projectID, taskID string) []string {
	if taskID == "" {
		return nil
	}
	task := s.findTask(ctx, projectID, taskID)
	if task == nil {
		return nil
	}
	if acceptance := legacyPromotedAcceptance(task.Body); len(acceptance) > 0 {
		return acceptance
	}
	return strategy.ExtractDeliverables(task.Title, task.Body)
}

var manualVerificationWords = regexp.MustCompile(`(?i)\b(manual(?:ly)?|by (?:the )?person|physical device|live desktop|wayland session|human (?:verification|check|inspection|smoke|confirmation))\b`)

// manualDeliverables identifies acceptance work that only the person or a
// live environment can perform. The wording remains in the accepted task;
// this classification merely keeps the engine from demanding that an
// implementer fabricate evidence it cannot obtain.
func manualDeliverables(items []string) map[int]bool {
	out := map[int]bool{}
	for i, item := range items {
		if manualVerificationWords.MatchString(item) {
			out[i+1] = true
		}
	}
	return out
}

func humanVerificationPayload(items []string) []map[string]interface{} {
	manual := manualDeliverables(items)
	out := make([]map[string]interface{}, 0, len(manual))
	for id, text := range items {
		if manual[id+1] {
			out = append(out, map[string]interface{}{"id": id + 1, "text": text})
		}
	}
	return out
}
