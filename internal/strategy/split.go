package strategy

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
)

// OwnershipError is a decomposition the mode refuses to run.
//
// It carries the conflict in words the architect can act on, because the one
// retry it gets is only useful if it is told what was wrong (05 §4.5).
type OwnershipError struct {
	Detail string
}

func (e *OwnershipError) Error() string { return e.Detail }

// ValidateOwnership checks a decomposition's file ownership. Phase 2, and
// deterministic on purpose (05 §4.5).
//
// This is the check the whole mode rests on. Phase 4 integrates by copying each
// subtask's files out of its worktree, which is only safe because no two
// subtasks can have touched the same file. Take this away and integration
// becomes a merge — and a weak model asked to merge whole files destroys
// working code, which is the failure the mode exists to avoid.
//
// It returns the cleaned, repo-relative ownership per subtask, in the order
// given, so callers do not re-derive paths that were already normalised here.
func ValidateOwnership(d *agent.Decomposition) ([][]string, error) {
	if d == nil || len(d.Subtasks) == 0 {
		return nil, &OwnershipError{Detail: "the decomposition has no subtasks"}
	}

	owner := map[string]string{} // clean path -> owning subtask title
	out := make([][]string, len(d.Subtasks))

	for i, st := range d.Subtasks {
		var files []string
		for _, raw := range st.Files {
			clean, err := repoRelative(raw)
			if err != nil {
				return nil, &OwnershipError{Detail: fmt.Sprintf(
					"subtask %q claims %q, which is outside the repository", st.Title, raw)}
			}
			if prev, taken := owner[clean]; taken {
				return nil, &OwnershipError{Detail: fmt.Sprintf(
					"%q is claimed by both %q and %q; each file may have exactly one owner",
					clean, prev, st.Title)}
			}
			owner[clean] = st.Title
			files = append(files, clean)
		}
		sort.Strings(files)
		out[i] = files
	}
	return out, nil
}

// repoRelative normalises a claimed path and rejects anything that leaves the
// repository.
//
// Checked here rather than trusted to the tool path jail: the jail stops a
// contestant from writing outside its worktree at execution time, but phase 4
// copies these paths afterwards, and a copy driven by an unchecked "../" would
// escape long after any tool had a say.
func repoRelative(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute path")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("escapes the repository")
	}
	if clean == "." {
		return "", fmt.Errorf("the repository itself is not a file")
	}
	return filepath.ToSlash(clean), nil
}

// Integrate copies each subtask's owned files out of its workspace into the
// target tree. Phase 4, and deterministic on purpose (05 §4.5).
//
// This is a copy and not a merge, and that is the entire point of the mode. A
// weak model asked to reconcile whole files destroys code that was working;
// disjoint ownership, established in phase 2, turns the reconciliation into a
// file copy that no model takes part in.
//
// A file a subtask claimed but never created is not an error: deciding a file
// was unnecessary is a legitimate outcome. Deleting the target copy in that
// case would be, because it would silently discard whatever was there before.
func Integrate(target string, owned [][]string, roots []string, copyFile FileCopier) ([]string, error) {
	if len(owned) != len(roots) {
		return nil, fmt.Errorf("integrate: %d ownership lists for %d workspaces", len(owned), len(roots))
	}
	var written []string
	for i, files := range owned {
		for _, rel := range files {
			from := filepath.Join(roots[i], filepath.FromSlash(rel))
			to := filepath.Join(target, filepath.FromSlash(rel))
			copied, err := copyFile(from, to)
			if err != nil {
				return written, fmt.Errorf("integrate %s: %w", rel, err)
			}
			if copied {
				written = append(written, rel)
			}
		}
	}
	sort.Strings(written)
	return written, nil
}

// FileCopier copies one file, reporting whether it existed. Injected so the
// integration is testable without a filesystem, and so the real one can be the
// only place that knows about permissions and parent directories.
type FileCopier func(from, to string) (copied bool, err error)

// SplitParams configures a split run.
type SplitParams struct {
	ExecuteParams
	// NewWorkspace isolates each subtask, exactly as it does a contestant.
	NewWorkspace WorkspaceFactory
	// GateIn runs verification inside a tree.
	GateIn func(ctx context.Context, root string) (gate string, log string, err error)
	// CopyFile moves one owned file from a workspace into the main tree.
	CopyFile FileCopier
	// SeamRounds bounds the pair rounds spent reconciling the integrated tree
	// when the gate is red. Zero means the spec's two (05 §4.5).
	SeamRounds int
}

// SplitResult is the outcome of a split run.
type SplitResult struct {
	Subtasks   []agent.Subtask
	Integrated []string
	Gate       string
	// Retried records that the architect's first decomposition was refused.
	Retried bool
	// SeamRoundsUsed is how many pair rounds the integrated tree needed.
	SeamRoundsUsed int
	// Outcome is the turn that stopped the run, carried so the caller can
	// turn a question into a pause instead of a failure. A run that asks a
	// person something has not failed; it is waiting.
	Outcome *agent.Outcome
}

// DefaultSeamRounds is how many pair rounds a red integration gets (05 §4.5).
const DefaultSeamRounds = 2

// ExecuteSplit decomposes a task, runs the pieces concurrently and integrates
// them deterministically (05 §4.5).
//
// The mode refuses rather than degrading. If the architect cannot produce a
// decomposition whose subtasks own disjoint files — after being told once
// exactly what clashed — the answer is that split is the wrong mode for this
// task, not that the merge should be handed to a model.
func ExecuteSplit(ctx context.Context, p *SplitParams) (*SplitResult, error) {
	if p.NewWorkspace == nil {
		return nil, fmt.Errorf("split: NewWorkspace is required")
	}
	if p.CopyFile == nil {
		return nil, fmt.Errorf("split: CopyFile is required")
	}
	result := &SplitResult{}

	// --- phase 1 & 2: decompose, then validate ownership ---
	decomp, owned, retried, err := decomposeWithRetry(ctx, p, result)
	result.Retried = retried
	if err != nil {
		return result, err
	}
	result.Subtasks = decomp.Subtasks

	// --- phase 3: the subtasks run concurrently, each in its own worktree ---
	roots, cleanup, err := runSubtasks(ctx, p, decomp.Subtasks, result)
	defer cleanup()
	if err != nil {
		return result, err
	}

	// --- phase 4: integrate by copying. No model takes part. ---
	written, err := Integrate(p.ProjectRoot, owned, roots, p.CopyFile)
	result.Integrated = written
	if err != nil {
		return result, err
	}
	emit(&p.ExecuteParams, "integrated", map[string]interface{}{"files": written})

	// --- phase 5: gate the integrated tree, then fix seams if it is red ---
	gate, err := p.gateMain(ctx)
	if err != nil {
		return result, err
	}
	result.Gate = gate

	rounds := p.SeamRounds
	if rounds == 0 {
		rounds = DefaultSeamRounds
	}
	for i := 0; gate == "red" && i < rounds; i++ {
		emit(&p.ExecuteParams, "seam_round", map[string]interface{}{"round": i + 1})
		seam := p.ExecuteParams
		seam.Prompt = seamPrompt(p.Prompt, written)
		seam.Rounds = 1
		if _, err := ExecutePair(ctx, &seam); err != nil {
			return result, err
		}
		result.SeamRoundsUsed = i + 1
		if gate, err = p.gateMain(ctx); err != nil {
			return result, err
		}
		result.Gate = gate
	}
	return result, nil
}

func (p *SplitParams) gateMain(ctx context.Context) (string, error) {
	if p.GateIn == nil {
		return "none", nil
	}
	gate, _, err := p.GateIn(ctx, p.ProjectRoot)
	return gate, err
}

// seamPrompt asks for the integration to be reconciled, not rewritten.
//
// The subtasks each passed on their own; what fails now is where they meet.
// Naming the integrated files keeps the pair on the seam instead of
// reimplementing work that already verified.
func seamPrompt(task string, files []string) string {
	var b strings.Builder
	b.WriteString(task)
	b.WriteString("\n\n## Integration\n\nThese files were written independently and then combined:\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("\nEach piece verified on its own, so the failure is where they meet. " +
		"Fix the seam. Do not reimplement work that already passes.\n")
	return b.String()
}

// decomposeWithRetry runs phase 1 and phase 2, giving the architect exactly one
// second attempt when its ownership clashes (05 §4.5).
//
// One retry, not a loop: an architect that cannot separate the files twice is
// telling you the task does not decompose, and asking a third time is how a
// mode degrades into pretending it can.
func decomposeWithRetry(ctx context.Context, p *SplitParams, result *SplitResult) (*agent.Decomposition, [][]string, bool, error) {
	prompt := decomposePrompt(p.Prompt)
	for attempt := 0; attempt < 2; attempt++ {
		turn := &Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "full",
			Contract: "json:decomposition",
			MaxTurns: 8,
		}
		belt, err := turn.ResolveToolbelt(registryFrom(&p.ExecuteParams))
		if err != nil {
			return nil, nil, attempt > 0, err
		}
		runner := p.Runner
		if runner == nil {
			runner = defaultRunner(&p.ExecuteParams)
		}
		duckling := config.DucklingID("")
		if p.Roster != nil {
			duckling = p.Roster[config.RoleArchitect]
		}

		emit(&p.ExecuteParams, "turn_start", map[string]interface{}{
			"round": attempt + 1, "turn": 0, "role": string(config.RoleArchitect),
			"duckling": string(duckling),
		})
		out, err := runner(ctx, turn, duckling, prompt, belt, TurnContext{Round: attempt + 1, Index: 0})
		if out != nil {
			result.Outcome = out
		}
		if err != nil {
			return nil, nil, attempt > 0, err
		}
		emitMessage(&p.ExecuteParams, attempt+1, 0, config.RoleArchitect, duckling, out)
		emit(&p.ExecuteParams, "turn_end", map[string]interface{}{
			"round": attempt + 1, "turn": 0, "role": string(config.RoleArchitect),
		})

		decomp, ok := out.Parsed.(*agent.Decomposition)
		if !ok || decomp == nil {
			return nil, nil, attempt > 0, fmt.Errorf("split: the architect produced no decomposition")
		}

		owned, verr := ValidateOwnership(decomp)
		if verr == nil {
			emit(&p.ExecuteParams, "decomposition", map[string]interface{}{
				"subtasks": len(decomp.Subtasks), "attempt": attempt + 1,
			})
			return decomp, owned, attempt > 0, nil
		}
		if attempt == 1 {
			// Refused, not degraded: split is the wrong mode for this task and
			// saying so beats handing the merge to a model (05 §4.5).
			return nil, nil, true, fmt.Errorf("split: %w", verr)
		}
		emit(&p.ExecuteParams, "decomposition_rejected", map[string]interface{}{
			"detail": verr.Error(),
		})
		prompt = decomposePrompt(p.Prompt) + "\n\n## Your previous decomposition was rejected\n\n" +
			verr.Error() + "\n\nEvery file must have exactly one owner. Try again.\n"
	}
	return nil, nil, true, fmt.Errorf("split: no valid decomposition")
}

// decomposePrompt states the job.
//
// The first real split run failed because this did not exist: the architect
// was handed the task prompt, a read-only toolbelt and a json:decomposition
// contract it had never been told about. It explored, tried to write the files
// itself with a tool it did not have, and ran out of turns having produced
// nothing. A contract name is not an instruction — the model never sees it.
func decomposePrompt(task string) string {
	return task + `

## Your task

Do NOT implement anything. Break this work into ` + fmt.Sprint(agent.MinSubtasks) + ` to ` +
		fmt.Sprint(agent.MaxSubtasks) + ` subtasks that can be built independently and at the
same time, then stop.

Every subtask must list the files it alone will create or edit. Two subtasks
may not name the same file: the pieces are combined afterwards by copying each
one's files, with no model involved, and that is only safe when every file has
exactly one owner. If the work cannot be divided that way, say so rather than
inventing a division.

Answer with JSON and nothing else:

{"subtasks":[{"title":"...","files":["path/one.go"],"body":"what to build"}]}
`
}

// runSubtasks runs phase 3: every subtask concurrently, each in its own tree.
//
// The returned cleanup releases the workspaces, and must run even when a
// subtask fails — a worktree left registered makes the next run's `worktree
// add` fail on a path git still believes in.
func runSubtasks(ctx context.Context, p *SplitParams, subtasks []agent.Subtask, result *SplitResult) ([]string, func(), error) {
	roots := make([]string, len(subtasks))
	spaces := make([]Workspace, len(subtasks))
	errs := make([]error, len(subtasks))
	var mu sync.Mutex

	cleanup := func() {
		for _, ws := range spaces {
			if ws != nil {
				ws.Close()
			}
		}
	}

	var wg sync.WaitGroup
	for i := range subtasks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, err := p.NewWorkspace(ctx, fmt.Sprintf("s%d", i))
			if err != nil {
				errs[i] = fmt.Errorf("subtask %d: workspace: %w", i, err)
				return
			}
			spaces[i] = ws
			roots[i] = ws.Root()

			turn := &Turn{
				Role:     config.RoleImplementer,
				Toolbelt: "full",
				Contract: "edits",
				MaxTurns: 24,
			}
			belt, err := turn.ResolveToolbelt(registryFrom(&p.ExecuteParams))
			if err != nil {
				errs[i] = err
				return
			}
			runner := p.Runner
			if runner == nil {
				runner = defaultRunner(&p.ExecuteParams)
			}
			duckling := config.DucklingID("")
			if p.Roster != nil {
				duckling = p.Roster[config.RoleImplementer]
			}

			emit(&p.ExecuteParams, "turn_start", map[string]interface{}{
				"round": 1, "turn": i, "role": string(config.RoleImplementer),
				"duckling": string(duckling), "subtask": subtasks[i].Title,
			})
			out, err := runner(ctx, turn, duckling, subtaskPrompt(subtasks[i]), belt,
				TurnContext{Round: 1, Index: i, Root: ws.Root()})
			if out != nil && out.Pending != nil {
				// One subtask stopping to ask stops the run: the pieces are
				// parts, not alternatives, so there is nothing to integrate
				// until the question is answered.
				mu.Lock()
				result.Outcome = out
				mu.Unlock()
			}
			if err != nil {
				errs[i] = fmt.Errorf("subtask %q: %w", subtasks[i].Title, err)
				return
			}
			emitMessage(&p.ExecuteParams, 1, i, config.RoleImplementer, duckling, out)
			emit(&p.ExecuteParams, "turn_end", map[string]interface{}{
				"round": 1, "turn": i, "role": string(config.RoleImplementer),
			})
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			// Unlike a tournament, one failed piece is the whole run failing:
			// the pieces are not alternatives, they are parts.
			return roots, cleanup, err
		}
	}
	return roots, cleanup, nil
}

// subtaskPrompt states the piece and the files it owns.
//
// The ownership list is repeated to the implementer because it is a boundary,
// not a hint: a file written outside it is not integrated, so the work would
// be silently lost at phase 4.
func subtaskPrompt(st agent.Subtask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Your subtask\n\n%s\n\n", st.Title)
	if strings.TrimSpace(st.Body) != "" {
		b.WriteString(strings.TrimSpace(st.Body))
		b.WriteString("\n\n")
	}
	b.WriteString("## Files you own\n\nYou may create or edit only these:\n\n")
	for _, f := range st.Files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("\nAnything you write outside this list is discarded when the pieces are " +
		"combined, so work that belongs elsewhere is work thrown away.\n")
	return b.String()
}
