package stage

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Post-composition repair (B-518).
//
// The post-composition review is the only reader that sees the whole composed
// amendment beside its approved base. When it blocked, the run used to end
// FAILED on the spot — even when the council had approved in round one of
// four (eett: three rounds unspent). A blocked composition is now treated as
// what it is, the closing critique of the round that produced the candidate:
// if the mode's round budget has rounds left, the architect gets a repair
// round with those findings and the loop continues; the composition is
// reviewed again; only a spent budget stops at the gate.
//
// Round accounting. One budget per amendment, the script's MaxRounds (the
// mode default or the run's rounds override). Every conversation is charged
// the rounds it actually used (Script.RoundsUsed) and a repair conversation
// is given only what is left, so an amendment never runs more architect /
// critic rounds than its mode promised, and every repair round ends in a
// fresh composition review. A section-wise update is charged its longest
// section pass: its passes are independent conversations in parallel
// sequence, each with that same per-section budget, and the repair revisits
// only the sections the findings name.

type roundBudget struct {
	max  int
	left int
	// known is false until a conversation reports its rounds. A test double
	// that never runs the scheduler reports none; that is treated as a spent
	// budget, never as permission for an unbounded repair.
	known bool
}

func newRoundBudget(script *strategy.Script) *roundBudget {
	return &roundBudget{max: script.MaxRounds, left: script.MaxRounds}
}

// charge records the rounds one conversation used.
func (b *roundBudget) charge(used int) {
	if used <= 0 {
		b.left = 0
		return
	}
	b.known = true
	b.left -= used
	if b.left < 0 {
		b.left = 0
	}
}

// decide reports whether a blocked composition buys a repair, and with how
// many rounds. It records the decision either way when the budget is known.
func (b *roundBudget) decide(p Params, candidate *artifact.Document, mechanical []string, semantic *agent.Verdict) (int, bool) {
	if !compositionBlocked(mechanical, semantic) {
		return 0, false
	}
	findings := compositionFindingLines(mechanical, semantic)
	if !repairable(candidate, mechanical, semantic) {
		if b.known && p.OnEvent != nil {
			p.OnEvent("composition_repair_skipped", map[string]interface{}{
				"findings": findings,
				"detail":   "only deterministic findings that name no section of the candidate block it (an environment or baseline fact); a model round cannot change them, so the proposal stops at the gate",
			})
		}
		return 0, false
	}
	if b.left <= 0 {
		if b.known && p.OnEvent != nil {
			p.OnEvent("composition_repair_exhausted", map[string]interface{}{
				"max_rounds": b.max, "findings": findings,
				"detail": "the post-composition review blocked and the round budget is spent; the proposal stops at the gate",
			})
		}
		return 0, false
	}
	if p.OnEvent != nil {
		p.OnEvent("composition_repair_started", map[string]interface{}{
			"rounds_left": b.left, "max_rounds": b.max, "findings": findings,
			"detail": "the post-composition review blocked with rounds left; the architect repairs its findings and the composition is reviewed again",
		})
	}
	return b.left, true
}

// spend charges a single conversation and decides in one step.
func (b *roundBudget) spend(p Params, script *strategy.Script, candidate *artifact.Document, mechanical []string, semantic *agent.Verdict) (int, bool) {
	b.charge(script.RoundsUsed)
	return b.decide(p, candidate, mechanical, semantic)
}

func compositionBlocked(mechanical []string, semantic *agent.Verdict) bool {
	return len(mechanical) > 0 || (semantic != nil && semantic.Verdict != "approve")
}

// repairable separates what an architect round can change from what it
// cannot. A semantic block always can. A deterministic finding can when it
// names a section of the candidate (a malformed field, a dangling reference);
// one that names none — "the approved specification could not be loaded" —
// is a fact about the environment, and buying model rounds against it would
// spend the budget to reach the same gate.
func repairable(candidate *artifact.Document, mechanical []string, semantic *agent.Verdict) bool {
	if semantic != nil && semantic.Verdict != "approve" {
		return true
	}
	return len(sectionIDsInFindings(candidate, mechanical)) > 0
}

func compositionFindingLines(mechanical []string, semantic *agent.Verdict) []string {
	var out []string
	for _, m := range mechanical {
		out = append(out, "[mechanical] "+m)
	}
	if semantic != nil && semantic.Verdict != "approve" {
		for _, f := range semantic.Findings {
			line := fmt.Sprintf("[%s] %s: %s", f.Severity, f.File, f.Issue)
			if f.Invariant != "" {
				line += " (invariant: " + f.Invariant + ")"
			}
			if f.Fix != "" {
				line += " Fix: " + f.Fix
			}
			out = append(out, line)
		}
		if len(semantic.Findings) == 0 {
			out = append(out, "[major] the post-composition reviewer requested changes without itemized findings")
		}
	}
	return out
}

// compositionRepairContext is appended to the architect's prompt for a repair
// round: the findings, and the same approved-vs-candidate delta the critics
// read, so the seat repairs against what was there before.
func compositionRepairContext(guard *strategy.AmendmentGuard, candidate string, mechanical []string, semantic *agent.Verdict, scope string) string {
	var b strings.Builder
	b.WriteString("\n\n## Post-composition review — repair round\n\n")
	b.WriteString("An independent reviewer read your fully composed amendment beside the approved document and the request, and blocked it. " +
		"Repair EVERY finding below in the sections it names and change nothing else. " + scope + "\n\n")
	for _, line := range compositionFindingLines(mechanical, semantic) {
		b.WriteString("- " + line + "\n")
	}
	if guard != nil && guard.Delta != nil {
		if delta := strings.TrimSpace(guard.Delta(candidate)); delta != "" {
			b.WriteString("\n" + delta + "\n")
		}
	}
	return b.String()
}
