package strategy

import (
	"context"
	"fmt"
	"sync"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
)

// Resolution records how a tournament ended. Reported per mode so the value of
// the judge can be measured rather than assumed.
const (
	ResolutionShortCircuit = "short_circuit" // exactly one green; applied verbatim
	ResolutionJudgePick    = "judge_pick"    // several green; judge chose among them
	ResolutionJudgePickRed = "judge_pick_red"
	ResolutionNoWinner     = "no_winner"
)

// Workspace is an isolated copy of the repo where one contestant works.
type Workspace interface {
	Root() string
	// Patch returns the contestant's work as a unified diff against the base.
	Patch() (string, error)
	Close() error
}

// WorkspaceFactory creates an isolated workspace for a contestant.
type WorkspaceFactory func(ctx context.Context, label string) (Workspace, error)

// TournamentParams configures a tournament run.
type TournamentParams struct {
	ExecuteParams
	// Contestants is how many implementers compete. Default 2.
	Contestants int
	// Ducklings assigns a duckling per contestant, positionally.
	Ducklings []config.DucklingID
	// NewWorkspace isolates each contestant.
	NewWorkspace WorkspaceFactory
	// GateIn runs verification inside a workspace.
	GateIn func(ctx context.Context, root string) (gate string, log string, err error)
	// Apply writes the winning patch to the main tree, verbatim.
	Apply func(patch string) error
}

// TournamentResult is the outcome of a tournament.
type TournamentResult struct {
	Resolution string
	Winner     string // candidate label, or ""
	Reason     string
	Candidates []conv.Candidate
	Rounds     int
	Err        error
}

// ExecuteTournament runs N implementers independently and arbitrates (05 §4.3).
//
// The hard rule (I8): a candidate whose gate is green is applied VERBATIM. The
// judge evaluates; it never rewrites. On modest models free regeneration
// corrupts working code, and that is the single most expensive lesson baked
// into this design.
func ExecuteTournament(ctx context.Context, p *TournamentParams) (*TournamentResult, error) {
	n := p.Contestants
	if n <= 0 {
		n = 2
	}
	if p.NewWorkspace == nil {
		return nil, fmt.Errorf("tournament: NewWorkspace is required")
	}
	if p.Apply == nil {
		return nil, fmt.Errorf("tournament: Apply is required")
	}

	result := &TournamentResult{Rounds: 1}

	// --- phase 1: contestants run concurrently, each blind to the others ---
	type entry struct {
		cand conv.Candidate
		err  error
	}
	entries := make([]entry, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entries[i] = runContestant(ctx, p, i)
		}(i)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return result, err
	}

	var cands []conv.Candidate
	var firstErr error
	for _, e := range entries {
		if e.err != nil {
			// One contestant failing is not the run failing: the others may
			// still produce a green candidate.
			if firstErr == nil {
				firstErr = e.err
			}
			continue
		}
		cands = append(cands, e.cand)
	}
	if len(cands) == 0 {
		result.Resolution = ResolutionNoWinner
		if firstErr != nil {
			result.Err = firstErr
			return result, firstErr
		}
		return result, nil
	}

	// --- phase 2: label anonymously, by content ---
	cands = conv.AnonymiseCandidates(cands)
	result.Candidates = cands
	green := conv.GreenCandidates(cands)

	// --- phase 3: resolve ---
	switch {
	case len(green) == 1:
		// Short circuit. No judge call at all: there is nothing to decide, and
		// asking would only create an opportunity to pick wrong.
		result.Resolution = ResolutionShortCircuit
		result.Winner = green[0].Label
		result.Reason = "only candidate whose verification passed"
		if err := p.Apply(green[0].Diff); err != nil {
			result.Err = err
			return result, err
		}
		return result, nil

	case len(green) > 1:
		choice, err := judge(ctx, p, green)
		if err != nil {
			result.Err = err
			return result, err
		}
		result.Reason = choice.Reason
		win := conv.FindCandidate(green, choice.Choice)
		if win == nil {
			// The judge named something that is not on the table. Falling back
			// to a green candidate keeps the run honest: a green solution
			// exists and must not be discarded over a malformed choice.
			result.Resolution = ResolutionJudgePick
			result.Winner = green[0].Label
			result.Reason = "judge named an unavailable candidate; applied the first green one"
			if err := p.Apply(green[0].Diff); err != nil {
				result.Err = err
				return result, err
			}
			return result, nil
		}
		result.Resolution = ResolutionJudgePick
		result.Winner = win.Label
		if err := p.Apply(win.Diff); err != nil {
			result.Err = err
			return result, err
		}
		return result, nil

	default:
		// Nothing is green. The judge may still pick the closest attempt to
		// build on, or refuse.
		choice, err := judge(ctx, p, cands)
		if err != nil {
			result.Err = err
			return result, err
		}
		result.Reason = choice.Reason
		if !choice.Chosen() {
			result.Resolution = ResolutionNoWinner
			return result, nil
		}
		win := conv.FindCandidate(cands, choice.Choice)
		if win == nil {
			result.Resolution = ResolutionNoWinner
			result.Reason = "judge named an unavailable candidate"
			return result, nil
		}
		result.Resolution = ResolutionJudgePickRed
		result.Winner = win.Label
		if err := p.Apply(win.Diff); err != nil {
			result.Err = err
			return result, err
		}
		return result, nil
	}
}

// runContestant executes one implementer in its own workspace and captures the
// result of verifying inside it.
func runContestant(ctx context.Context, p *TournamentParams, i int) (e struct {
	cand conv.Candidate
	err  error
}) {
	label := fmt.Sprintf("c%d", i)
	ws, err := p.NewWorkspace(ctx, label)
	if err != nil {
		e.err = fmt.Errorf("contestant %d: workspace: %w", i, err)
		return e
	}
	// Always released, including on panic and on abort, or the next run's
	// `worktree add` fails on a path that is still registered.
	defer ws.Close()

	duckling := config.DucklingID("")
	if i < len(p.Ducklings) {
		duckling = p.Ducklings[i]
	}

	turn := &Turn{
		Role:     config.RoleImplementer,
		Toolbelt: "full",
		Contract: "edits",
		MaxTurns: 24,
	}
	belt, err := turn.ResolveToolbelt(registryFrom(&p.ExecuteParams))
	if err != nil {
		e.err = err
		return e
	}

	runner := p.Runner
	if runner == nil {
		runner = defaultRunner(&p.ExecuteParams)
	}
	if _, err := runner(ctx, turn, duckling, p.Prompt, belt); err != nil {
		e.err = fmt.Errorf("contestant %d: %w", i, err)
		return e
	}

	patch, err := ws.Patch()
	if err != nil {
		e.err = fmt.Errorf("contestant %d: patch: %w", i, err)
		return e
	}

	gate, log := "none", ""
	if p.GateIn != nil {
		gate, log, err = p.GateIn(ctx, ws.Root())
		if err != nil {
			e.err = fmt.Errorf("contestant %d: gate: %w", i, err)
			return e
		}
	}

	e.cand = conv.Candidate{Diff: patch, Gate: gate, GateLog: log, Duckling: duckling}
	return e
}

// judge asks the judge role to choose among anonymised candidates.
func judge(ctx context.Context, p *TournamentParams, cands []conv.Candidate) (*agent.Choice, error) {
	turn := &Turn{
		Role:      config.RoleJudge,
		Toolbelt:  "full",
		Contract:  "choice",
		MaxTurns:  6,
		Anonymize: true,
	}
	belt, err := turn.ResolveToolbelt(registryFrom(&p.ExecuteParams))
	if err != nil {
		return nil, err
	}

	prompt := p.Prompt + "\n\n" + conv.RenderCandidates(cands)

	runner := p.Runner
	if runner == nil {
		runner = defaultRunner(&p.ExecuteParams)
	}
	duckling := config.DucklingID("")
	if p.Roster != nil {
		duckling = p.Roster[config.RoleJudge]
	}

	out, err := runner(ctx, turn, duckling, prompt, belt)
	if err != nil {
		return nil, err
	}
	choice, ok := out.Parsed.(*agent.Choice)
	if !ok || choice == nil {
		return nil, fmt.Errorf("judge did not produce a choice")
	}
	return choice, nil
}
