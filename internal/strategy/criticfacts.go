package strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

var criticIDPattern = regexp.MustCompile(`\b(?:REQ|SPEC|M|T)-[0-9]+\b`)

// filterPlanCriticOutcome is the deterministic boundary between an advisory
// semantic judgment and an authoritative repair ledger. H1g showed a focused
// critic inventing M-07, SPEC-009 and REQ-022, then directing the architect to
// implement could/wont scope. The model's raw reply remains in the run record;
// only findings that survive project facts can steer the next draft.
func filterPlanCriticOutcome(params *ExecuteParams, outcome *agent.Outcome, candidate string, round, turn int) {
	if outcome == nil {
		return
	}
	v, ok := outcome.Parsed.(*agent.Verdict)
	if !ok || v == nil || len(v.Findings) == 0 {
		return
	}

	kept := make([]agent.Finding, 0, len(v.Findings))
	rejected := make([]map[string]interface{}, 0)
	sanitized := make([]map[string]interface{}, 0)
	for _, finding := range v.Findings {
		var reasons []string
		finding, reasons = sanitizePlanCriticFinding(params, finding, candidate)
		if len(reasons) > 0 {
			sanitized = append(sanitized, map[string]interface{}{
				"issue": finding.Issue, "reasons": reasons,
			})
		}
		if reason := invalidPlanCriticFinding(params, finding, candidate); reason != "" {
			rejected = append(rejected, map[string]interface{}{
				"issue": finding.Issue, "reason": reason,
			})
			continue
		}
		finding.Fix = safePlanCriticFix(params, finding, candidate)
		kept = append(kept, finding)
	}
	if len(rejected) == 0 && len(sanitized) == 0 {
		return
	}
	v.Findings = kept
	if v.Verdict == "request-changes" && len(kept) == 0 {
		// Facts may prove that every proposed edit is unauthorized, but they do
		// not prove the candidate correct. H1i-1 upgraded an inconclusive review
		// to approval after discarding a real obligation with a bad SPEC label.
		v.Findings = []agent.Finding{{
			Severity:  "major",
			File:      "plan",
			Invariant: "A filtered review cannot establish approval",
			Issue:     "The reviewer requested changes, but every finding conflicted with accepted project facts; the review is inconclusive.",
			Fix:       "Keep the accepted scope and existing topology unchanged, then re-evaluate the candidate against the accepted specification.",
		}}
	}
	emit(params, "critic_findings_filtered", map[string]interface{}{
		"round": round, "turn": turn, "rejected": rejected,
		"rejected_count": len(rejected), "sanitized": sanitized,
		"sanitized_count": len(sanitized), "kept_count": len(v.Findings),
		"effective_verdict": v.Verdict,
		"detail":            "project facts removed findings that cannot authoritatively steer a plan repair",
	})
}

func invalidPlanCriticFinding(params *ExecuteParams, finding agent.Finding, candidate string) string {
	text := finding.Issue + "\n" + finding.Fix
	lower := strings.ToLower(text)
	fixLower := strings.ToLower(finding.Fix)
	for _, id := range uniqueStrings(criticIDPattern.FindAllString(text, -1)) {
		priority := strings.ToLower(params.PriorityByID[id])
		if priority == "wont" && prescribesPositiveWork(fixLower) {
			return fmt.Sprintf("%s is wont; it is a boundary, not positive plan work", id)
		}
		if priority == "could" && !candidateImplements(candidate, id) && prescribesPositiveWork(fixLower) {
			return fmt.Sprintf("%s is could and the candidate does not select it", id)
		}
	}
	for name, priority := range params.PriorityByName {
		if !strings.Contains(lower, name) || !prescribesPositiveWork(fixLower) {
			continue
		}
		if priority == "wont" {
			return fmt.Sprintf("%q is a wont obligation; it is a boundary, not positive plan work", name)
		}
		if priority == "could" && !namedDecisionSelected(candidate, name) {
			return fmt.Sprintf("%q is a could obligation and the candidate does not select it", name)
		}
	}
	return ""
}

// sanitizePlanCriticFinding separates a semantic observation from the edit a
// fallible critic proposed for it. H1i proved that a bad SPEC label or a new
// task number can coexist with the exact issue a human later rejects. Facts
// remove those unsafe coordinates; they do not erase the observation.
func sanitizePlanCriticFinding(params *ExecuteParams, finding agent.Finding, candidate string) (agent.Finding, []string) {
	var reasons []string
	for _, id := range uniqueStrings(criticIDPattern.FindAllString(finding.Issue, -1)) {
		prefix := strings.SplitN(id, "-", 2)[0]
		if (prefix == "REQ" || prefix == "SPEC") && len(params.KnownIDs) > 0 && !params.KnownIDs[id] {
			finding.Issue = strings.ReplaceAll(finding.Issue, id, "the accepted specification")
			reasons = append(reasons, id+" is not an accepted project id; the issue was retained without that coordinate")
		}
	}
	unsafeFix := false
	for _, id := range uniqueStrings(criticIDPattern.FindAllString(finding.Fix, -1)) {
		prefix := strings.SplitN(id, "-", 2)[0]
		switch prefix {
		case "REQ", "SPEC":
			if len(params.KnownIDs) > 0 && !params.KnownIDs[id] {
				unsafeFix = true
				reasons = append(reasons, id+" is not an accepted project id; the fix was neutralized")
			}
		case "M", "T":
			if !strings.Contains(candidate, id) && prescribesNamedTopology(finding.Fix, id) {
				unsafeFix = true
				reasons = append(reasons, id+" is not in the candidate; the fix cannot allocate topology ids")
			}
		}
	}
	if unsafeFix {
		finding.Fix = "Address the stated issue using accepted specification IDs and the existing candidate topology; do not allocate a named task or milestone in this review fix."
	}
	return finding, reasons
}

func namedDecisionSelected(candidate, name string) bool {
	// A deferred decision is selected by naming its behavior in a Work unit or
	// Acceptance slice, not merely by citing a mixed parent SPEC id.
	lower := strings.ToLower(candidate)
	for _, field := range []string{"**work unit:**", "**acceptance slices:**"} {
		at := 0
		for {
			i := strings.Index(lower[at:], field)
			if i < 0 {
				break
			}
			i += at + len(field)
			end := strings.Index(lower[i:], "\n\n**")
			if end < 0 {
				end = len(lower) - i
			}
			if strings.Contains(lower[i:i+end], name) {
				return true
			}
			at = i + end
		}
	}
	return false
}

func prescribesNamedTopology(text, id string) bool {
	lower := strings.ToLower(text)
	id = strings.ToLower(id)
	return strings.Contains(lower, "missing "+id) || strings.Contains(lower, "add "+id) ||
		strings.Contains(lower, "create "+id) || strings.Contains(lower, "introduce "+id)
}

func prescribesPositiveWork(text string) bool {
	for _, phrase := range []string{
		"add an acceptance", "add acceptance", "add a task", "dedicated task",
		"add a milestone", "implement", "exercise", "work unit", "must cover",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func candidateImplements(candidate, id string) bool {
	pattern := regexp.MustCompile(`(?im)^\*\*Implements:\*\*[^\n]*\b` + regexp.QuoteMeta(id) + `\b`)
	return pattern.MatchString(candidate)
}

// A semantic finding may be valid while its prescribed edit is structurally
// impossible for a small seat. Keep the issue, but remove the authority to
// append a fourth slice; the architect must choose a bounded correction.
func safePlanCriticFix(params *ExecuteParams, finding agent.Finding, candidate string) string {
	if !params.SmallSeat || !strings.Contains(strings.ToLower(finding.Fix), "add") ||
		!strings.Contains(strings.ToLower(finding.Fix), "acceptance slice") {
		return finding.Fix
	}
	doc, err := artifact.Parse(candidate, artifact.KindPlan)
	if err != nil {
		return finding.Fix
	}
	for _, id := range criticIDPattern.FindAllString(finding.Issue+"\n"+finding.Fix, -1) {
		if !strings.HasPrefix(id, "T-") {
			continue
		}
		if task := doc.Section(id); task != nil && topLevelChecklistItems(task.Body, "Acceptance slices") >= 3 {
			return "Address the stated behavior without adding a fourth Acceptance slice: refine an existing slice, or split a genuinely distinct work unit into a new task while preserving existing topology and IDs."
		}
	}
	return finding.Fix
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
