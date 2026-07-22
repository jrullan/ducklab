package strategy

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/source"
)

// Solo is the single-model baseline: one model solves, tests decide, done. It
// exists so every collaborative recipe can be measured against "the model
// alone" inside the same harness, on the same task, with the same artifacts —
// the control arm ducklab's whole thesis is compared to.
type Solo struct{}

func (Solo) Name() string        { return "solo" }
func (Solo) MinContestants() int { return 1 }

func (Solo) Run(env Env) (Outcome, error) {
	if dirty, lines := guardClean(env.Repo); dirty {
		return Outcome{}, fmt.Errorf("repo has uncommitted changes:\n  %s\nCommit/stash first",
			joinMax(lines, 10))
	}
	base := prim.CurrentBranch(env.Repo)
	defer restore(env.Repo, base)

	solver := env.Contestants[0]
	r := env.Run
	_ = r.Set("base_branch", base)
	_ = r.Advance("SOLVE")

	env.stage("SOLVE", solver.Name())
	res, err := solver.Complete(env.Ctx, prim.SolvePrompt(env.Requirement, env.Repo),
		source.Options{Temperature: 0.2, DisableThinking: true, LogPath: r.LogPath()})
	if err != nil {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Message: "solve failed: " + err.Error()}, nil
	}
	_ = r.Write("solution.md", res.Content)

	env.stage("TEST", solver.Name())
	branch := finalBranch(env.TaskID)
	checkoutFresh(env.Repo, branch, base)
	if _, err := prim.ApplyFileBlocks(env.Repo, res.Content); err != nil {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Branch: branch,
			Message: "unparseable solution: " + err.Error()}, nil
	}
	commitAll(env.Repo, "ducklab: "+env.TaskID+" solo")
	ok, out := runTests(env.Repo, env.TestCmd)
	_ = r.Write("test_output.txt", out)
	_ = r.Write("diff_final.patch", snapshotDiff(env.Repo, base))
	_ = r.Set("tests_final", map[string]any{"ok": ok})

	if ok {
		_ = r.Set("resolution", "solo")
		_ = r.Advance("HUMAN_GATE")
		return Outcome{State: "HUMAN_GATE", Resolution: "solo",
			Branch: branch, TestsPass: true,
			Message: solver.Name() + " (single-model baseline) — tests green"}, nil
	}
	_ = r.Advance("ESCALATED")
	return Outcome{State: "ESCALATED", Branch: branch,
		Message: "single-model solution failed tests"}, nil
}

func joinMax(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[:n]
	}
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}
