package strategy

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/source"
)

// Tournament is Rubber Duck Mode 2: two contestants solve the same task
// independently; an independent judge evaluates both. Resolution follows a
// hard-won hierarchy (the legacy pilot proved free synthesis corrupts on weak
// models): a declared, green winner is applied verbatim (short-circuit); a
// winner carrying a blocking finding is overridden to a clean green rival;
// otherwise synthesis is attempted, falling back to any green contestant before
// escalating. The judge's value is evaluation, not regeneration.
type Tournament struct{}

func (Tournament) Name() string        { return "tournament" }
func (Tournament) MinContestants() int { return 2 }

func (Tournament) Run(env Env) (Outcome, error) {
	if dirty, lines := guardClean(env.Repo); dirty {
		return Outcome{}, fmt.Errorf("repo has uncommitted changes:\n  %s\nCommit/stash first",
			joinMax(lines, 10))
	}
	base := prim.CurrentBranch(env.Repo)
	defer restore(env.Repo, base)

	a, b := env.Contestants[0], env.Contestants[1]
	judge := env.Judge
	r := env.Run
	_ = r.Set("base_branch", base)
	opts := source.Options{Temperature: 0.2, DisableThinking: true, LogPath: r.LogPath()}

	// --- SOLVE + TEST each (a red contestant does not abort the tournament) ---
	type cand struct {
		src   source.Client
		green bool
		note  string
	}
	cands := []cand{{src: a}, {src: b}}
	for i := range cands {
		src := cands[i].src
		env.stage("SOLVE", src.Name())
		res, err := src.Complete(env.Ctx, prim.SolvePrompt(env.Requirement, env.Repo), opts)
		content := ""
		if err == nil {
			content = res.Content
		}
		if err != nil || !strings.Contains(content, "=== FILE:") {
			cands[i].note = "solve failed"
			if err != nil {
				cands[i].note = "solve failed: " + err.Error()
			}
			_ = r.Write("solution_"+src.Name()+".md", content)
			_ = r.Set("tests_"+src.Name(), map[string]any{"ok": false, "note": cands[i].note})
			continue
		}
		_ = r.Write("solution_"+src.Name()+".md", content)

		env.stage("TEST", src.Name())
		branch := "ducklab/" + env.TaskID + "/" + src.Name()
		checkoutFresh(env.Repo, branch, base)
		if _, err := prim.ApplyFileBlocks(env.Repo, content); err != nil {
			cands[i].note = "unparseable: " + err.Error()
			_ = r.Set("tests_"+src.Name(), map[string]any{"ok": false, "note": cands[i].note})
			continue
		}
		commitAll(env.Repo, fmt.Sprintf("ducklab: %s solution (%s)", env.TaskID, src.Name()))
		ok, out := runTests(env.Repo, env.TestCmd)
		_ = r.Write("test_output_"+src.Name()+".txt", out)
		_ = r.Write("diff_"+src.Name()+".patch", snapshotDiff(env.Repo, base))
		cands[i].green = ok
		_ = r.Set("tests_"+src.Name(), map[string]any{"ok": ok})
	}

	// --- JUDGE ---
	env.stage("JUDGE", judge.Name())
	diffA, _ := r.Read("diff_" + a.Name() + ".patch")
	diffB, _ := r.Read("diff_" + b.Name() + ".patch")
	verdicts := fmt.Sprintf("A(%s)=%v B(%s)=%v", a.Name(), cands[0].green, b.Name(), cands[1].green)
	jr, err := judge.Complete(env.Ctx, prim.JudgePrompt(env.Requirement, diffA, diffB, verdicts), opts)
	if err != nil {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Message: "judge failed: " + err.Error()}, nil
	}
	_ = r.Write("judge.md", jr.Content)

	// --- RESOLVE ---
	env.stage("RESOLVE", "")
	report := prim.ParseJudge(jr.Content)
	byLetter := map[string]source.Client{"A": a, "B": b}
	greenOf := map[string]bool{"A": cands[0].green, "B": cands[1].green}
	decision := report.Decision
	_ = r.Set("decision", decision)

	// NONE -> escalate immediately, no synthesis.
	if decision == "NONE" {
		_ = r.Advance("ESCALATED")
		return Outcome{State: "ESCALATED", Resolution: "judge_none",
			Message: "judge declared no acceptable solution"}, nil
	}

	// Declared winner with a blocking finding -> override to a clean green rival.
	if (decision == "A" || decision == "B") && report.Blocking[decision] {
		other := "A"
		if decision == "A" {
			other = "B"
		}
		if !report.Blocking[other] && greenOf[other] {
			_ = r.Set("overridden", decision)
			decision = other
			_ = r.Set("decision", decision)
		}
	}

	branch := finalBranch(env.TaskID)
	applyStored := func(letter, resolution string) (bool, string) {
		src := byLetter[letter]
		checkoutFresh(env.Repo, branch, base)
		sol, _ := r.Read("solution_" + src.Name() + ".md")
		if _, err := prim.ApplyFileBlocks(env.Repo, sol); err != nil {
			return false, ""
		}
		commitAll(env.Repo, fmt.Sprintf("ducklab: %s final (%s)", env.TaskID, resolution))
		ok, out := runTests(env.Repo, env.TestCmd)
		_ = r.Write("test_output_final.txt", out)
		_ = r.Write("diff_final.patch", snapshotDiff(env.Repo, base))
		_ = r.Set("tests_final", map[string]any{"ok": ok})
		if ok {
			_ = r.Set("winner", src.Name())
			_ = r.Set("resolution", resolution)
		}
		return ok, src.Name()
	}

	// Short-circuit: declared winner that is green -> apply verbatim.
	if (decision == "A" || decision == "B") && greenOf[decision] {
		ok, name := applyStored(decision, "short_circuit")
		if ok {
			_ = r.Advance("HUMAN_GATE")
			return Outcome{State: "HUMAN_GATE", Resolution: "short_circuit", Winner: name,
				Branch: branch, TestsPass: true,
				Message: "declared winner green — applied without regeneration"}, nil
		}
		// fall through to synthesis if it somehow does not reproduce
	}

	// HYBRID or non-green winner -> synthesis.
	env.stage("SYNTHESIZE", judge.Name())
	syn, err := judge.Complete(env.Ctx,
		prim.SynthesizePrompt(env.Requirement, jr.Content, env.Repo), opts)
	if err == nil {
		_ = r.Write("final_solution.md", syn.Content)
		checkoutFresh(env.Repo, branch, base)
		if _, perr := prim.ApplyFileBlocks(env.Repo, syn.Content); perr == nil {
			commitAll(env.Repo, "ducklab: "+env.TaskID+" final (synthesis)")
			ok, out := runTests(env.Repo, env.TestCmd)
			_ = r.Write("test_output_final.txt", out)
			_ = r.Write("diff_final.patch", snapshotDiff(env.Repo, base))
			_ = r.Set("tests_final", map[string]any{"ok": ok})
			if ok {
				_ = r.Set("resolution", "synthesis")
				_ = r.Advance("HUMAN_GATE")
				return Outcome{State: "HUMAN_GATE", Resolution: "synthesis", Branch: branch,
					TestsPass: true, Message: "judge synthesis green"}, nil
			}
		}
	}

	// Fallback: any green contestant before escalating.
	for letter, g := range greenOf {
		if g {
			ok, name := applyStored(letter, "fallback")
			if ok {
				_ = r.Advance("HUMAN_GATE")
				return Outcome{State: "HUMAN_GATE", Resolution: "fallback", Winner: name,
					Branch: branch, TestsPass: true,
					Message: "synthesis failed; fell back to green contestant " + name}, nil
			}
		}
	}

	_ = r.Advance("ESCALATED")
	return Outcome{State: "ESCALATED", Branch: branch,
		Message: "synthesis failed and no contestant is green"}, nil
}
