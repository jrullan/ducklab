package service

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	mu          sync.Mutex
	loops       map[config.DucklingID]*agent.Loop
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
		// A contestant works in its own worktree. Leaving the project root
		// here is what let every tournament contestant edit the shared tree.
		if tc.Root != "" {
			turnCtx.ProjectRoot = tc.Root
		}
		return agent.RunTurn(ctx, loop, &agent.Turn{
			Role: t.Role, Duckling: d, Prompt: prompt, Toolbelt: belt,
			Contract: t.Contract, MaxTurns: t.MaxTurns, Anonymize: t.Anonymize,
			Round: tc.Round, Index: tc.Index,
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
		// The request, then the configured default for this mode, then the
		// script's own count. The counts lived only in the scripts, so changing
		// how many times a reviewer got to push back meant editing Go.
		Rounds: s.roundsFor(mc.rs.run.Mode, mc.req.Rounds),
		Runner: s.runnerFor(mc.cache, mc.roster, mc.ectx),
		Roster: mc.roster,
		// So tournament and split, which build their own turns, honour the same
		// per-role caps as every other mode.
		TurnCaps: s.roleTurnCaps(),
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
		res, err := strategy.ExecuteScript(ctx, s.applyRoleTurns(strategy.SoloScript()), &base)
		return pendingOrErr(res, err)

	case "pair":
		res, err := strategy.ExecuteScript(ctx, s.applyRoleTurns(strategy.PairScript()), &base)
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
			res, err := verify.Run(root, mc.projCfg.Verify)
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
