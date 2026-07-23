package strategy

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/source"
)

// Plan is Rubber Duck Mode 4: a peer planning dialogue, then execution and
// verification. Model A drafts a plan and hands it to B conversationally; B
// gives observations — never approve/reject — so A keeps final say. A revises
// (loop) or stands by its plan with reasoning (planning ends). Then A executes
// the ratified plan with SEARCH/REPLACE and the gate verifies it. B's post-hoc
// verification is advisory when a gate ran, and the decisive check when none did.
//
// This is an on-thesis mode: planning-before-execution is one of the ways two
// modest models beat one — the plan is where a decorrelated reviewer catches a
// wrong approach before any code is written.
type Plan struct{}

const planMaxRounds = 3

func (Plan) Name() string        { return "plan" }
func (Plan) MinContestants() int { return 2 }

func (Plan) Run(env Env) (Outcome, error) {
	if dirty, lines := guardClean(env.Repo); dirty {
		return Outcome{}, fmt.Errorf("repo has uncommitted changes:\n  %s\nCommit/stash first",
			joinMax(lines, 10))
	}
	base := prim.CurrentBranch(env.Repo)
	defer restore(env.Repo, base)

	planner := env.Contestants[0]  // Model A
	reviewer := env.Contestants[1] // Model B (the decorrelated peer, not the judge)
	r := env.Run
	_ = r.Set("base_branch", base)
	_ = r.Set("gate", env.Gate.Kind)
	opts := source.Options{Temperature: 0.2, DisableThinking: true, LogPath: r.LogPath(), OnDone: env.OnCall, OnRetry: env.OnRetry}

	// ── planning dialogue ──────────────────────────────────────────
	_ = r.Advance("HANDOFF")
	lastPlan, lastReview, planRes := "", "", ""
	for round := 1; round <= planMaxRounds; round++ {
		env.stage(fmt.Sprintf("HANDOFF r%d/%d", round, planMaxRounds), planner.Name())
		ho, err := planner.Complete(env.Ctx,
			prim.PlanHandoffPrompt(env.Requirement, env.Repo, lastReview, round, planMaxRounds), opts)
		if err != nil {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Message: "planner failed: " + err.Error()}, nil
		}
		_ = r.Write(fmt.Sprintf("handoff_%d.md", round), ho.Content)

		if round > 1 && prim.IsPlanRejection(ho.Content) {
			planRes = fmt.Sprintf("stood_by_plan_r%d", round) // A keeps the prior plan
			break
		}
		lastPlan = prim.ExtractPlan(ho.Content)
		if round == planMaxRounds {
			planRes = fmt.Sprintf("max_rounds_r%d", round)
			break
		}

		env.stage(fmt.Sprintf("REVIEW r%d/%d", round, planMaxRounds), reviewer.Name())
		rv, err := reviewer.Complete(env.Ctx,
			prim.PlanReviewPrompt(env.Requirement, ho.Content, env.Repo), opts)
		if err != nil {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Message: "reviewer failed: " + err.Error()}, nil
		}
		_ = r.Write(fmt.Sprintf("review_%d.md", round), rv.Content)
		lastReview = rv.Content
	}
	if strings.TrimSpace(lastPlan) == "" {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Message: "no executable plan was produced"}, nil
	}
	_ = r.Write("final_plan.md", lastPlan)
	_ = r.Set("plan_resolution", planRes)

	// ── execute ────────────────────────────────────────────────────
	_ = r.Advance("EXECUTE")
	env.stage("EXECUTE", planner.Name())
	ex, err := planner.Complete(env.Ctx,
		prim.PlanExecutePrompt(env.Requirement, lastPlan, env.Repo), opts)
	if err != nil {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Message: "execution failed: " + err.Error()}, nil
	}
	_ = r.Write("execution.md", ex.Content)

	branch := finalBranch(env.TaskID)
	checkoutFresh(env.Repo, branch, base)
	// Apply the whole updated file(s); fails only when the reply has no usable
	// === FILE: === blocks (or content that would corrupt a file).
	if _, err := prim.ApplyFileBlocks(env.Repo, ex.Content); err != nil {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Branch: branch,
			Message: "execution produced no usable file blocks: " + err.Error()}, nil
	}
	commitAll(env.Repo, "ducklab: "+env.TaskID+" execute plan")
	diff := snapshotDiff(env.Repo, base)
	_ = r.Write("diff_final.patch", diff)

	// ── verify ─────────────────────────────────────────────────────
	_ = r.Advance("VERIFY")
	env.stage("VERIFY", "")
	ran, ok, out := runGate(env.Repo, env.Gate)
	_ = r.Write("test_output_final.txt", out)

	// B checks the execution against the plan (advisory when a gate ran).
	env.stage("CHECK", reviewer.Name())
	gateInfo := out
	if !ran {
		gateInfo = "(no automated gate — judge against the plan and the requirement)"
	}
	vr, verr := reviewer.Complete(env.Ctx,
		prim.PlanVerifyPrompt(env.Requirement, lastPlan, env.Repo, diff, gateInfo), opts)
	if verr == nil {
		_ = r.Write("execution_review.md", vr.Content)
	}
	approved := verr == nil && strings.Contains(strings.ToUpper(vr.Content), "APPROVED") && len(vr.Content) > 50

	if ran {
		// Tests are ground truth: green passes regardless of B's advisory note.
		_ = r.Set("tests_final", map[string]any{"ok": ok})
		if ok {
			_ = r.Set("resolution", "plan_executed")
			_ = r.Advance("HUMAN_GATE")
			return Outcome{State: "HUMAN_GATE", Resolution: "plan_executed", Branch: branch,
				TestsPass: true, Message: fmt.Sprintf("%s planned & executed, %s verified — %s green",
					planner.Name(), reviewer.Name(), env.Gate.Kind)}, nil
		}
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Branch: branch,
			Message: "execution failed " + env.Gate.Kind}, nil
	}

	// No gate: B's verification is the decisive check.
	_ = r.Set("tests_final", map[string]any{"verified": false})
	if approved {
		_ = r.Set("resolution", "plan_executed")
		_ = r.Advance("UNVERIFIED")
		return Outcome{State: "UNVERIFIED", Resolution: "plan_executed", Branch: branch,
			Message: fmt.Sprintf("%s executed the plan, %s approved (no automated gate — review the diff)",
				planner.Name(), reviewer.Name())}, nil
	}
	_ = r.Advance("ESCALATED")
	return Outcome{State: "ESCALATED", Branch: branch,
		Message: reviewer.Name() + " did not approve the execution (no automated gate)"}, nil
}
