package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
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
	l.CapLift = c.capLift
	l.OnCapNear = c.onCapNear
	l.OnCall = c.onCall
	c.loops[id] = l
	return l, nil
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

	// Declared capabilities win over probing: probing costs a request, and on
	// a local endpoint the declaration is usually more accurate anyway.
	caps := &duckling.Capabilities{NativeTools: false, ContextTokens: 32768}
	if cfg, ok := s.cfg.Ducklings[id]; ok && cfg.Caps.NativeTools != nil {
		caps.NativeTools = *cfg.Caps.NativeTools
		if cfg.Caps.ContextTokens != nil {
			caps.ContextTokens = *cfg.Caps.ContextTokens
		}
	} else if probed, err := s.ducklings.Probe(ctx, id); err == nil {
		caps = probed
	}
	if cfg, ok := s.cfg.Ducklings[id]; ok && cfg.Caps.Vision != nil {
		caps.Vision = *cfg.Caps.Vision
	}

	loop := &agent.Loop{
		Provider: p,
		Duckling: &agent.DucklingConfig{
			ID: id, Provider: d.Provider, Model: d.Model,
			Params: d.Params, Caps: duckling.ProviderCaps(caps), Cost: d.Cost,
		},
		Registry:       tools.NewRegistry(),
		Budget:         tracker,
		MaxTurns:       s.cfg.Defaults.AgentMaxTurns,
		RepairAttempts: s.cfg.Defaults.RepairAttempts,
		RunWriter:      writer,
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
	for id := range s.cfg.Ducklings {
		available[id] = true
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
		availableIDs := make([]config.DucklingID, 0, len(available))
		for id := range available {
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
		return agent.RunTurn(ctx, loop, &agent.Turn{
			Role: t.Role, Duckling: d, Prompt: prompt, Toolbelt: belt,
			Contract: t.Contract, MaxTurns: t.MaxTurns, Anonymize: t.Anonymize,
			Persona: t.Persona,
			// The screenshots. Every field above was forwarded and this one
			// was not, so the engine WARNED "shown to the triager" while the
			// wire carried text alone — the model then truthfully reported
			// the screenshot absent, twice, to a person who had attached it.
			Images: t.Images,
			Round:  tc.Round, Index: tc.Index,
		}, &turnCtx)
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

// capOverride resolves a run's AgentTurns override: negative means no cap.
func capOverride(override int) int {
	if override < 0 {
		return uncappedTurns
	}
	return override
}

// roleTurnCapsFor is the configured caps, unless the run asked for its own:
// a per-run override applies to every role, because the person raising it is
// unblocking THIS work, not retuning the fleet. Negative lifts the cap.
func (s *Service) roleTurnCapsFor(override int) map[config.Role]int {
	caps := s.roleTurnCaps()
	if override == 0 {
		return caps
	}
	for _, role := range config.ValidRoles() {
		if role != config.RoleHuman {
			caps[role] = capOverride(override)
		}
	}
	return caps
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
	defer s.runsMu.RUnlock()
	var turns []float64
	for id, rs := range s.runs {
		if id == exclude || rs == nil || rs.run == nil || rs.run.Mode != mode || rs.run.Budget.Turns <= 0 {
			continue
		}
		turns = append(turns, float64(rs.run.Budget.Turns))
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
	return &strategy.ResumeTurn{Round: t.Round, Index: t.Index, Role: config.Role(t.Role), Notes: t.Notes}
}

func stringValueAny(v interface{}) string {
	s, _ := v.(string)
	return s
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
	base := strategy.ExecuteParams{
		LiveToolEvents:       true,
		EscalationCandidates: escalationCandidates,
		CurrentLowerBound:    currentFloor,
		ModeMedian:           s.modeTurnMedian(mc.rs.run.Mode, mc.rs.run.ID),
		ResumeFrom:           resumeTurn(mc.rs.run),
		ProjectRoot:          root,
		TaskID:               mc.req.TaskID,
		// Answers the person already gave ride ON the prompt: a resumed run
		// replays from scratch, and a model that cannot see the decisions
		// re-asks them in new words forever.
		Prompt: s.buildTaskPrompt(ctx, mc.rs.run.ProjectID, mc.entry.Path, mc.req.TaskID) +
			humanNote(mc.req.Note) + mc.rs.answeredDecisions(),
		// The task's bullets, numbered: the implementer's work contract
		// (strategy/deliverables.go). The plan's words, never the model's.
		Deliverables: s.taskDeliverables(ctx, mc.rs.run.ProjectID, mc.req.TaskID),
		ExecContext:  mc.ectx,
		// The request, then the configured default for this mode, then the
		// script's own count. The counts lived only in the scripts, so changing
		// how many times a reviewer got to push back meant editing Go.
		Rounds: s.roundsFor(mc.rs.run.Mode, mc.req.Rounds),
		Runner: s.runnerFor(mc.cache, mc.roster, mc.ectx),
		Roster: mc.roster,
		// So tournament and split, which build their own turns, honour the same
		// per-role caps as every other mode.
		TurnCaps: s.roleTurnCapsFor(mc.req.AgentTurns),
		Gate: func(ctx context.Context) (string, string, error) {
			mc.rs.gateRoot = root
			mc.rs.run.GateRoot = root
			mc.rs.writer.WriteState()
			res, err := verify.Run(ctx, root, mc.projCfg.Verify, verify.Identity{RunID: mc.rs.run.ID, ProjectID: mc.rs.run.ProjectID})
			if err != nil {
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		Diff: func() (string, error) {
			return vcs.New(root).DiffExcluding(mc.rs.run.LinkedDeps...)
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			mc.rs.writer.AppendEvent(kind, data)
			if kind == "turn_interrupted" {
				mc.rs.run.InterruptedTurn = &runlog.InterruptedTurn{Round: intValue(data["round"]), Index: intValue(data["turn"]), Role: stringValueAny(data["role"]), Notes: stringValueAny(data["notes"])}
				mc.rs.writer.WriteState()
			} else if kind == "turn_end" && data["incomplete"] != true {
				mc.rs.run.InterruptedTurn = nil
				mc.rs.writer.WriteState()
			}
		},
	}

	switch mc.rs.run.Mode {
	case "", "solo":
		res, err := strategy.ExecuteScript(ctx, s.applyRoleTurns(strategy.SoloScript(), mc.req.AgentTurns), &base)
		return pendingOrErr(res, err)

	case "pair":
		res, err := strategy.ExecuteScript(ctx, s.applyRoleTurns(strategy.PairScript(), mc.req.AgentTurns), &base)
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
		mc.rs.run.Resolution = res.Resolution
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
	rs.run.Budget.Limit = runlog.BudgetLimits{
		USD: b.MaxUSD, Tokens: b.MaxTokens, Turns: b.MaxTurns, WallclockS: b.MaxWallclockS,
	}
}

// recordSpend copies the budget tracker's totals onto the run record.
func recordSpend(rs *runState, tracker *budget.Tracker) {
	if tracker == nil || tracker.Spend == nil {
		return
	}
	snap := tracker.Spend.Snapshot()
	rs.run.Budget.USD = snap.USD
	rs.run.Budget.Tokens = snap.Tokens
	rs.run.Budget.Turns = snap.Turns
	rs.run.Budget.WallclockS = snap.WallclockS

	// WallclockMs is what reports average over; deriving it here means a run
	// that ends any way at all still records its duration.
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
	return strategy.ExtractDeliverables(task.Title, task.Body)
}
