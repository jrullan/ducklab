package stage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/strategy"
)

var artifactIDRe = regexp.MustCompile(`\b(?:INT|REQ|SPEC)-\d+\b`)

// reviewComposition gives every amendment route the same last look at the
// exact artifact it is about to persist. Local council approval proves that
// each generated part is plausible; this pass catches meaning lost or
// reintroduced only after the engine folds those parts together.
func reviewComposition(ctx context.Context, p Params, kind artifact.Kind, ask string, base, proposed *artifact.Document) ([]string, *agent.Verdict, error) {
	mechanical := []string{}
	if kind == artifact.KindPlan {
		mechanical = planCompositionFindings(p.ProjectRoot, base, proposed)
	}
	contractFindings := referenceContractFindings(kind, proposed, p.ReferenceContracts)
	mechanical = append(mechanical, contractFindings...)
	baseBody := artifact.RenderBody(base)
	delta := strings.TrimSpace(ask)
	candidateBody := artifact.RenderBody(proposed)
	digests := map[string]interface{}{
		"base_digest":      fullContentHash(baseBody),
		"delta_digest":     fullContentHash(delta),
		"candidate_digest": fullContentHash(candidateBody),
	}
	if p.OnEvent != nil {
		p.OnEvent("composition_mechanical_check", map[string]interface{}{
			"findings": mechanical, "contract_findings": contractFindings, "count": len(mechanical),
		})
	}
	// A machine contract has already proved the candidate invalid. Spending a
	// model turn cannot reverse that fact and would blur mechanical evidence
	// into a semantic opinion.
	if len(contractFindings) > 0 {
		return mechanical, nil, nil
	}
	if p.OnEvent != nil {
		started := copyEventFields(digests)
		started["category"] = "semantic"
		started["detail"] = "one bounded reviewer is judging the fully composed amendment"
		p.OnEvent("composition_review_started", started)
	}

	prompt := buildArtifactCompositionReviewPrompt(p.ProjectRoot, kind, delta, base, proposed)
	raw, err := p.Execute(ctx, strategy.CompositionReviewScript(), prompt)
	if err != nil {
		return mechanical, nil, err
	}
	parsed, err := agent.ParseContract("verdict", raw)
	if err != nil {
		return mechanical, nil, fmt.Errorf("composition reviewer: %w", err)
	}
	semantic := parsed.(*agent.Verdict)
	if p.OnEvent != nil {
		completed := copyEventFields(digests)
		completed["category"] = "semantic"
		completed["verdict"] = semantic.Verdict
		completed["findings"] = semantic.Findings
		completed["count"] = len(semantic.Findings)
		p.OnEvent("composition_review_completed", completed)
	}
	return mechanical, semantic, nil
}

func referenceContractFindings(kind artifact.Kind, proposed *artifact.Document, contracts []capability.ReferenceContract) []string {
	if kind != artifact.KindSpec || proposed == nil || len(contracts) == 0 {
		return nil
	}
	body := artifact.RenderBody(proposed)
	seen := map[string]capability.ReferenceContract{}
	for _, contract := range contracts {
		if _, exists := seen[contract.Operation]; !exists {
			seen[contract.Operation] = contract
		}
	}
	operations := make([]string, 0, len(seen))
	for operation := range seen {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	var findings []string
	for _, operation := range operations {
		contract := seen[operation]
		fields, declarations := declaredOperationOutputFields(body, operation)
		identity := fmt.Sprintf("%s (%s, %s)", operation, contract.Source, contract.Digest)
		if declarations == 0 {
			findings = append(findings, fmt.Sprintf("reference contract %s has no mechanically readable output declaration in the specification", identity))
			continue
		}
		if declarations > 1 {
			findings = append(findings, fmt.Sprintf("reference contract %s has %d output declarations; exactly one is required", identity, declarations))
			continue
		}
		for _, violation := range contract.ValidateOutputFields(fields) {
			findings = append(findings, fmt.Sprintf("reference contract %s: %s", identity, violation))
		}
	}
	return findings
}

func declaredOperationOutputFields(body, operation string) ([]string, int) {
	lineRE := regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?` + "`" + regexp.QuoteMeta(operation) + "`" + `\s*(?:output\s*)?:\s*([^\n]+)$`)
	matches := lineRE.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return nil, len(matches)
	}
	fieldRE := regexp.MustCompile("`([^`]+)`")
	var fields []string
	for _, match := range fieldRE.FindAllStringSubmatch(matches[0][1], -1) {
		fields = append(fields, match[1])
	}
	return fields, 1
}

func copyEventFields(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in)+3)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildArtifactCompositionReviewPrompt(projectRoot string, kind artifact.Kind, ask string, base, proposed *artifact.Document) string {
	var b strings.Builder
	b.WriteString("## Assignment\n\nReview the exact final candidate below as one semantic object after its parts were composed. ")
	b.WriteString("Deterministic syntax, IDs, traceability, coverage, and dependency checks are reported separately. ")
	b.WriteString("Do not repeat formatting or mechanical findings, inspect the repository, repair the candidate, or invent implementation details.\n\n")
	b.WriteString(compositionInvariants(kind))
	b.WriteString("\nReturn `approve` only when the candidate preserves the requested change without contradiction, omission, duplication, or excluded scope. ")
	b.WriteString("Every finding must name the affected section ids and the violated semantic invariant. This is one read-only review; no repair follows automatically.\n\n")
	b.WriteString("## Base artifact — exact\n\n")
	b.WriteString(artifact.RenderBody(base))
	b.WriteString("\n## Requested delta — exact\n\n")
	b.WriteString(ask)
	b.WriteString("\n\n## Final candidate — exact\n\n")
	b.WriteString(artifact.RenderBody(proposed))
	b.WriteString("\n## Referenced normative sections — bounded\n\n")
	b.WriteString(referencedNormativeSections(projectRoot, kind, ask, proposed))
	return b.String()
}

func compositionInvariants(kind artifact.Kind) string {
	switch kind {
	case artifact.KindRequirements:
		return "Judge only whether the requirements preserve the user's amended intent, retain explicit exclusions, and avoid introducing solution design that the intent did not authorize.\n"
	case artifact.KindSpec:
		return "Judge only whether the specification implements the referenced requirements, preserves explicit exclusions, and assigns each behavioral contract exactly once without conflicting contracts.\n"
	case artifact.KindPlan:
		return "Judge only these cross-task invariants:\n\n" +
			"- Every distinct concern requested by the amendment is owned by exactly one Work unit.\n" +
			"- No two tasks claim the same behavior or artifact responsibility under different wording.\n" +
			"- A deliverable and its explanation or verification are not mistaken for independent concerns.\n" +
			"- A requested split neither retains the split-away concern in its original task nor loses it from the composed plan.\n" +
			"- Unchanged tasks are context; do not object to unrelated historical wording or scope.\n"
	default:
		return "Judge only whether the amendment preserves the request and does not contradict the approved base.\n"
	}
}

// referencedNormativeSections includes only upstream sections explicitly
// linked by the candidate or named by the amendment. Whole upstream documents
// made the bounded review another context-heavy council and encouraged it to
// reopen unrelated, already-approved decisions.
func referencedNormativeSections(projectRoot string, kind artifact.Kind, ask string, proposed *artifact.Document) string {
	upstream, ok := upstreamKind(kind)
	if !ok {
		return "(none)\n"
	}
	doc, err := artifact.Load(projectRoot, upstream)
	if err != nil || doc == nil || len(doc.Sections) == 0 {
		return "(none available)\n"
	}
	wanted := referencedIDs(kind, ask, proposed)
	selected := &artifact.Document{}
	for _, section := range doc.Sections {
		if wanted[strings.ToUpper(section.ID)] {
			selected.Sections = append(selected.Sections, section)
		}
	}
	if len(selected.Sections) == 0 {
		return "(none explicitly referenced)\n"
	}
	return artifact.RenderBody(selected)
}

func upstreamKind(kind artifact.Kind) (artifact.Kind, bool) {
	switch kind {
	case artifact.KindRequirements:
		return artifact.KindIntent, true
	case artifact.KindSpec:
		return artifact.KindRequirements, true
	case artifact.KindPlan:
		return artifact.KindSpec, true
	default:
		return "", false
	}
}

func referencedIDs(kind artifact.Kind, ask string, proposed *artifact.Document) map[string]bool {
	wanted := map[string]bool{}
	for _, id := range artifactIDRe.FindAllString(strings.ToUpper(ask), -1) {
		wanted[id] = true
	}
	var visit func(artifact.Section)
	visit = func(section artifact.Section) {
		for _, id := range section.Implements {
			wanted[strings.ToUpper(id)] = true
		}
		if kind == artifact.KindRequirements {
			for _, id := range artifactIDRe.FindAllString(strings.ToUpper(section.Field("originates from")), -1) {
				wanted[id] = true
			}
		}
		for _, child := range section.Children {
			visit(child)
		}
	}
	for _, section := range proposed.Sections {
		visit(section)
	}
	return wanted
}

func fullContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}

// buildCompositionReviewPrompt remains the focused plan helper exercised by
// older unit tests. Production supplies projectRoot to add bounded references.
func buildCompositionReviewPrompt(ask string, base, proposed *artifact.Document) string {
	return buildArtifactCompositionReviewPrompt("", artifact.KindPlan, ask, base, proposed)
}
