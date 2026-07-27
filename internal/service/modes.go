package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/report"
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
	svc     *Service
	tracker *budget.Tracker
	writer  agent.RunLogWriter
	onDelta func(config.Role, config.DucklingID, string)
	mu      sync.Mutex
	loops   map[config.DucklingID]*agent.Loop
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
func (s *Service) resolveRoster(projCfg *config.Project) (map[config.Role]config.DucklingID, string) {
	available := make([]config.DucklingID, 0, len(s.cfg.Ducklings))
	for id := range s.cfg.Ducklings {
		available = append(available, id)
	}
	sort.Slice(available, func(i, j int) bool { return available[i] < available[j] })

	// Roles that must be decorrelated from the implementer get the next
	// distinct duckling when one exists.
	fallbackFor := func(role config.Role) config.DucklingID {
		if len(available) == 0 {
			return ""
		}
		switch role {
		case config.RoleReviewer, config.RoleJudge:
			if len(available) > 1 {
				return available[1]
			}
		}
		return available[0]
	}

	out := map[config.Role]config.DucklingID{}
	for _, role := range config.ValidRoles() {
		if role == config.RoleHuman {
			continue
		}
		if id, ok := projCfg.Roster[role]; ok && id != "" {
			out[role] = id
			continue
		}
		out[role] = fallbackFor(role)
	}

	warning := ""
	if out[config.RoleImplementer] != "" && out[config.RoleImplementer] == out[config.RoleReviewer] {
		warning = fmt.Sprintf(
			"%s is on both sides of the pair: this measures self-consistency, not review. "+
				"Configure a second duckling, or set [roster] reviewer in project.toml.",
			out[config.RoleImplementer])
	}
	return out, warning
}

// runnerFor returns a TurnRunner that picks the loop matching each turn's role.
func (s *Service) runnerFor(cache *loopCache, roster map[config.Role]config.DucklingID, ectx *tools.ExecContext) strategy.TurnRunner {
	return func(ctx context.Context, t *strategy.Turn, d config.DucklingID, prompt string, belt []string) (*agent.Outcome, error) {
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
		return agent.RunTurn(ctx, loop, &agent.Turn{
			Role: t.Role, Duckling: d, Prompt: prompt, Toolbelt: belt,
			Contract: t.Contract, MaxTurns: t.MaxTurns, Anonymize: t.Anonymize,
		}, &turnCtx)
	}
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
func (s *Service) dispatchMode(ctx context.Context, mc *modeContext) error {
	base := strategy.ExecuteParams{
		ProjectRoot: mc.entry.Path,
		TaskID:      mc.req.TaskID,
		Prompt:      s.buildTaskPrompt(ctx, mc.rs.run.ProjectID, mc.entry.Path, mc.req.TaskID),
		ExecContext: mc.ectx,
		Rounds:      mc.req.Rounds,
		Runner:      s.runnerFor(mc.cache, mc.roster, mc.ectx),
		Roster:      mc.roster,
		Gate: func(ctx context.Context) (string, string, error) {
			res, err := verify.Run(mc.entry.Path, mc.projCfg.Verify)
			if err != nil {
				return "none", "", err
			}
			return gateWord(res), res.Output, nil
		},
		Diff: func() (string, error) {
			return vcs.New(mc.entry.Path).Diff()
		},
		OnEvent: func(kind string, data map[string]interface{}) {
			mc.rs.writer.AppendEvent(kind, data)
		},
	}

	switch mc.rs.run.Mode {
	case "", "solo":
		res, err := strategy.ExecuteSolo(ctx, &base)
		return pendingOrErr(res, err)

	case "pair":
		res, err := strategy.ExecutePair(ctx, &base)
		return pendingOrErr(res, err)

	case "tournament":
		return s.runTournament(ctx, mc, base)

	default:
		return fmt.Errorf("unknown mode %q (available: solo, pair, tournament)", mc.rs.run.Mode)
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
			res, err := verify.Run(root, mc.projCfg.Verify)
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

// Report aggregates this project's runs into the solo-baseline comparison.
func (s *Service) Report(ctx context.Context, projectID string, opts report.Options) (*report.Report, error) {
	runs, err := s.RunList(ctx, RunFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
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
