package strategy

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/source"
)

// Driver is Rubber Duck Mode 1: one model drives with surgical fenced
// search/replace edits, a second observes — runs tests, reads the diff, and
// either approves or returns specific corrections. A net loss of unrelated code
// is a blocking finding for the observer. Up to maxRounds of drive→observe.
type Driver struct{}

const driverMaxRounds = 3

func (Driver) Name() string        { return "driver" }
func (Driver) MinContestants() int { return 2 }

func (Driver) Run(env Env) (Outcome, error) {
	if dirty, lines := guardClean(env.Repo); dirty {
		return Outcome{}, fmt.Errorf("repo has uncommitted changes:\n  %s\nCommit/stash first",
			joinMax(lines, 10))
	}
	base := prim.CurrentBranch(env.Repo)
	defer restore(env.Repo, base)

	driver := env.Contestants[0]
	observer := env.Contestants[1] // Model B (the decorrelated peer, not the judge)
	r := env.Run
	branch := finalBranch(env.TaskID)
	_ = r.Set("base_branch", base)
	opts := source.Options{Temperature: 0.2, DisableThinking: true, LogPath: r.LogPath(), OnDone: env.OnCall, OnRetry: env.OnRetry}

	for round := 1; round <= driverMaxRounds; round++ {
		// --- DRIVE ---
		env.stage(fmt.Sprintf("DRIVE r%d/%d", round, driverMaxRounds), driver.Name())
		feedback := ""
		if round > 1 {
			feedback, _ = r.Read(fmt.Sprintf("feedback_%d.md", round-1))
		}
		res, err := driver.Complete(env.Ctx,
			prim.DriverPrompt(env.Requirement, env.Repo, feedback, round, driverMaxRounds), opts)
		if err != nil {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Message: "driver failed: " + err.Error()}, nil
		}
		_ = r.Write(fmt.Sprintf("changes_%d.md", round), res.Content)

		if round == 1 {
			checkoutFresh(env.Repo, branch, base)
		} else {
			prim.Git("checkout -q "+branch, env.Repo)
			prim.Git("reset -q HEAD", env.Repo) // unstage; keep committed tree
		}
		// Apply the fenced search/replace edits (or whole-file blocks). Nothing
		// applied means the reply's edits didn't parse/match — re-ask.
		if applied := prim.ApplyEdits(env.Repo, res.Content); applied.Applied == 0 {
			fb := "No edits could be applied (" + strings.Join(applied.Rejected, "; ") +
				"). Use the exact === FILE: === + ```search / ```replace format; copy search text " +
				"verbatim from the file shown."
			_ = r.Write(fmt.Sprintf("feedback_%d.md", round), fb)
			if round >= driverMaxRounds {
				_ = r.Advance("ESCALATED")
				return Outcome{State: "ESCALATED", Branch: branch,
					Message: "no edits could be applied after max rounds"}, nil
			}
			continue
		}
		commitAll(env.Repo, fmt.Sprintf("ducklab: %s driver round %d", env.TaskID, round))

		// --- VERIFY (skipped when unverified — the reviewer is then the gate) ---
		env.stage(fmt.Sprintf("VERIFY r%d/%d", round, driverMaxRounds), "")
		ran, ok, out := runGate(env.Repo, env.Gate)
		_ = r.Write(fmt.Sprintf("test_output_%d.txt", round), out)
		_ = r.Write(fmt.Sprintf("diff_%d.patch", round), snapshotDiff(env.Repo, base))
		_ = r.Set(fmt.Sprintf("tests_%d", round), map[string]any{"ran": ran, "ok": ok})

		if ran && !ok {
			// Gate red: hand the failure straight back to the driver.
			_ = r.Write(fmt.Sprintf("feedback_%d.md", round), cap2000(out))
			if round >= driverMaxRounds {
				_ = r.Advance("ESCALATED")
				return Outcome{State: "ESCALATED", Branch: branch,
					Message: env.Gate.Kind + " never went green"}, nil
			}
			continue
		}

		// --- OBSERVE ---
		obsInput := out
		if !ran {
			obsInput = "(no automated gate — judge the change against the task and the code)"
		}
		env.stage(fmt.Sprintf("OBSERVE r%d/%d", round, driverMaxRounds), observer.Name())
		review, err := observer.Complete(env.Ctx,
			prim.ObserverPrompt(env.Requirement, env.Repo, base, obsInput, round, driverMaxRounds), opts)
		if err != nil {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Branch: branch,
				Message: "observer failed: " + err.Error()}, nil
		}
		_ = r.Write(fmt.Sprintf("review_%d.md", round), review.Content)

		if strings.Contains(strings.ToUpper(review.Content), "APPROVED") && len(review.Content) > 50 {
			_ = r.Write("test_output_final.txt", out)
			_ = r.Write("diff_final.patch", snapshotDiff(env.Repo, base))
			_ = r.Set("gate", env.Gate.Kind)
			_ = r.Set("resolution", fmt.Sprintf("approved_round_%d", round))
			if !ran {
				_ = r.Set("tests_final", map[string]any{"verified": false})
				_ = r.Advance("UNVERIFIED")
				return Outcome{State: "UNVERIFIED", Resolution: fmt.Sprintf("approved_round_%d", round),
					Branch: branch,
					Message: fmt.Sprintf("%s drove, %s approved (no automated gate — review the diff)",
						driver.Name(), observer.Name())}, nil
			}
			_ = r.Set("tests_final", map[string]any{"ok": true})
			_ = r.Advance("HUMAN_GATE")
			return Outcome{State: "HUMAN_GATE", Resolution: fmt.Sprintf("approved_round_%d", round),
				Branch: branch, TestsPass: true,
				Message: fmt.Sprintf("%s drove, %s approved in round %d",
					driver.Name(), observer.Name(), round)}, nil
		}

		// Observer wants corrections.
		_ = r.Write(fmt.Sprintf("feedback_%d.md", round), review.Content)
		if round >= driverMaxRounds {
			_ = r.Advance("ESCALATED")
			return Outcome{State: "ESCALATED", Branch: branch,
				Message: "no APPROVED within max rounds"}, nil
		}
	}
	_ = r.Advance("ESCALATED")
	return Outcome{State: "ESCALATED", Branch: branch, Message: "max rounds exhausted"}, nil
}

func cap2000(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}
