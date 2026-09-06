package strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

var criticIDPattern = regexp.MustCompile(`\b(?:REQ|SPEC|M|T)-[0-9]+\b`)
var criticArtifactPattern = regexp.MustCompile(`\b(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.*/-]+\b`)

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
	selectedCouldID := false
	for _, id := range uniqueStrings(criticIDPattern.FindAllString(text, -1)) {
		priority := strings.ToLower(params.PriorityByID[id])
		if priority == "wont" && prescribesPositiveWork(fixLower) && !removesCandidateMapping(finding.Fix, candidate, id) {
			return fmt.Sprintf("%s is wont; it is a boundary, not positive plan work", id)
		}
		if priority == "could" {
			if candidateImplements(candidate, id) {
				selectedCouldID = true
			} else if prescribesPositiveWork(fixLower) {
				return fmt.Sprintf("%s is could and the candidate does not select it", id)
			}
		}
	}
	for name, priority := range params.PriorityByName {
		if !mentionsDecisionName(lower, name) || !prescribesPositiveWork(fixLower) {
			continue
		}
		if priority == "wont" {
			return fmt.Sprintf("%q is a wont obligation; it is a boundary, not positive plan work", name)
		}
		// A reviewer may be asking to remove an accidental Implements mapping
		// to a could section. H1j discarded exactly that valid correction after
		// seeing the section's name in the issue. An explicit selected could ID
		// beats the weaker name heuristic.
		if priority == "could" && !selectedCouldID && !namedDecisionSelected(candidate, name) {
			return fmt.Sprintf("%q is a could obligation and the candidate does not select it", name)
		}
	}
	for _, path := range uniqueStrings(criticArtifactPattern.FindAllString(finding.Issue, -1)) {
		if !strings.Contains(candidate, path) && claimsCandidateArtifact(finding.Issue, path) {
			return fmt.Sprintf("%s is not declared by the candidate", path)
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
			// An unknown ID copied from the candidate is the evidence for a
			// broken-link finding. Removing it here turns a precise defect into
			// "the accepted specification" and may then trigger an unrelated
			// could/wont name filter. Only sanitize coordinates the critic
			// invented itself.
			if strings.Contains(candidate, id) {
				continue
			}
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
				if !removesCandidateMapping(finding.Fix, candidate, id) {
					unsafeFix = true
					reasons = append(reasons, id+" is not an accepted project id; the fix was neutralized")
				}
			}
		case "M", "T":
			if !strings.Contains(candidate, id) && (prescribesNamedTopology(finding.Fix, id) || prescribesPositiveWork(strings.ToLower(finding.Fix))) {
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

func removesCandidateMapping(fix, candidate, id string) bool {
	if !candidateImplements(candidate, id) {
		return false
	}
	lower := strings.ToLower(fix)
	for _, verb := range []string{"remove", "drop", "delete", "omit", "replace"} {
		if strings.Contains(lower, verb) && strings.Contains(lower, strings.ToLower(id)) {
			return true
		}
	}
	return false
}

func claimsCandidateArtifact(issue, path string) bool {
	lower := strings.ToLower(issue)
	pathAt := strings.Index(lower, strings.ToLower(path))
	if pathAt < 0 {
		return false
	}
	start := pathAt - 80
	if start < 0 {
		start = 0
	}
	end := pathAt + len(path) + 80
	if end > len(lower) {
		end = len(lower)
	}
	window := lower[start:end]
	for _, claim := range []string{" owns", " produces", " consumes", "declares", "declared", "lists", "listed", "claims"} {
		if strings.Contains(window, claim) {
			return true
		}
	}
	return false
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
			if mentionsDecisionName(lower[i:i+end], name) {
				return true
			}
			at = i + end
		}
	}
	return false
}

func mentionsDecisionName(text, name string) bool {
	text, name = strings.ToLower(text), strings.ToLower(name)
	if strings.Contains(text, name) {
		return true
	}
	words := regexp.MustCompile(`[a-z0-9]+`).FindAllString(name, -1)
	var meaningful []string
	stop := map[string]bool{"a": true, "an": true, "the": true, "of": true, "and": true, "or": true, "out": true, "scope": true}
	for _, word := range words {
		if !stop[word] {
			meaningful = append(meaningful, word)
		}
	}
	if len(meaningful) < 2 {
		return false
	}
	at := 0
	for _, word := range meaningful {
		i := strings.Index(text[at:], word)
		if i < 0 {
			return false
		}
		at += i + len(word)
	}
	return true
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
	if pattern.MatchString(candidate) {
		return true
	}
	// Manifest critics review JSON before Markdown exists. Treat a declared
	// Implements entry there as selection too, so removal of an accidental
	// optional mapping cannot be filtered as a demand for new optional work.
	jsonPattern := regexp.MustCompile(`(?i)"implements"\s*:\s*\[[^\]]*"` + regexp.QuoteMeta(id) + `"`)
	return jsonPattern.MatchString(candidate)
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
			return "Address the stated behavior without adding a fourth Acceptance slice: refine or replace the task's existing slices while preserving manifest topology and IDs. If that is impossible, report that the accepted manifest is semantically invalid instead of allocating a new task."
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
