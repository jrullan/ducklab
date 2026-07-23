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
// the ratified plan with fenced search/replace edits and the gate verifies it.
// B's post-hoc verification is advisory when a gate ran, decisive when none did.
//
// This is an on-thesis mode: planning-before-execution is one of the ways two
// modest models beat one — the plan is where a decorrelated reviewer catches a
// wrong approach before any code is written.
type Plan struct{}

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
	maxRounds := env.rounds()

	// ── planning dialogue ──────────────────────────────────────────
	_ = r.Advance("HANDOFF")
	lastPlan, lastReview, planRes := "", "", ""
	for round := 1; round <= maxRounds; round++ {
		env.stage(fmt.Sprintf("HANDOFF r%d/%d", round, maxRounds), planner.Name())
		ho, err := planner.Complete(env.Ctx,
			prim.PlanHandoffPrompt(env.Requirement, env.Repo, lastReview, round, maxRounds), opts)
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
		if round == maxRounds {
			planRes = fmt.Sprintf("max_rounds_r%d", round)
			break
		}

		env.stage(fmt.Sprintf("REVIEW r%d/%d", round, maxRounds), reviewer.Name())
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

	// ── execute ↔ verify (retry until the reviewer is satisfied) ────
	// A one-shot execution let A ignore the plan and escalate with no recourse.
	// Now B's "you missed steps 2–5" feeds back into another execution round.
	_ = r.Advance("EXECUTE")
	branch := finalBranch(env.TaskID)
	feedback := ""
	for exRound := 1; exRound <= maxRounds; exRound++ {
		env.stage(fmt.Sprintf("EXECUTE r%d/%d", exRound, maxRounds), planner.Name())
		ex, err := planner.Complete(env.Ctx,
			prim.PlanExecutePrompt(env.Requirement, lastPlan, env.Repo, feedback, exRound, maxRounds), opts)
		if err != nil {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Message: "execution failed: " + err.Error()}, nil
		}
		_ = r.Write("execution.md", ex.Content)

		if exRound == 1 {
			checkoutFresh(env.Repo, branch, base)
		} else {
			prim.Git("checkout -q "+branch, env.Repo)
			prim.Git("reset -q HEAD", env.Repo) // keep prior rounds' committed edits
		}
		if applied := prim.ApplyEdits(env.Repo, ex.Content); applied.Applied == 0 {
			feedback = "None of your edits applied (" + strings.Join(applied.Rejected, "; ") +
				"). Re-read the current file and produce valid edits for every plan step."
			if exRound >= maxRounds {
				_ = r.Write("execution_rejected.md", strings.Join(applied.Rejected, "\n"))
				_ = r.Advance("ESCALATED")
				return Outcome{State: "ESCALATED", Branch: branch,
					Message: fmt.Sprintf("no applicable edits after %d rounds", maxRounds)}, nil
			}
			continue
		}
		commitAll(env.Repo, fmt.Sprintf("ducklab: %s execute plan (round %d)", env.TaskID, exRound))
		diff := snapshotDiff(env.Repo, base)
		_ = r.Write("diff_final.patch", diff)

		env.stage(fmt.Sprintf("VERIFY r%d/%d", exRound, maxRounds), "")
		ran, ok, out := runGate(env.Repo, env.Gate)
		_ = r.Write("test_output_final.txt", out)
		if ran && ok {
			_ = r.Set("tests_final", map[string]any{"ok": true})
			_ = r.Set("resolution", "plan_executed")
			_ = r.Advance("HUMAN_GATE")
			return Outcome{State: "HUMAN_GATE", Resolution: "plan_executed", Branch: branch, TestsPass: true,
				Message: fmt.Sprintf("%s executed the plan — %s green", planner.Name(), env.Gate.Kind)}, nil
		}

		// B checks the execution against the plan (the gate for unverified runs).
		env.stage(fmt.Sprintf("CHECK r%d/%d", exRound, maxRounds), reviewer.Name())
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

		if !ran && approved {
			_ = r.Set("tests_final", map[string]any{"verified": false})
			_ = r.Set("resolution", "plan_executed")
			_ = r.Advance("UNVERIFIED")
			return Outcome{State: "UNVERIFIED", Resolution: "plan_executed", Branch: branch,
				Message: fmt.Sprintf("%s executed the plan, %s approved (no automated gate — review the diff)",
					planner.Name(), reviewer.Name())}, nil
		}

		// Not done — hand the specific gaps back to A for another round.
		feedback = vr.Content
		if ran && !ok {
			feedback = "The automated check FAILED:\n" + cap2000(out) + "\n\nReviewer notes:\n" + vr.Content
		}
		if exRound >= maxRounds {
			_ = r.Set("tests_final", map[string]any{"ok": ok, "verified": ran})
			_ = r.Advance("ESCALATED")
			msg := fmt.Sprintf("%s still found the execution incomplete after %d rounds", reviewer.Name(), maxRounds)
			if ran && !ok {
				msg = fmt.Sprintf("execution failed %s after %d rounds", env.Gate.Kind, maxRounds)
			}
			return Outcome{State: "ESCALATED", Branch: branch, Message: msg}, nil
		}
	}
	_ = r.Advance("ESCALATED")
	return Outcome{State: "ESCALATED", Branch: branch, Message: "plan execution did not converge"}, nil
}
