package stage

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

// materializeCandidate freezes the exact document body a final reviewer and
// the proposal gate will share. Previously these operations happened only
// after the council returned, so a valid verdict could describe different
// section identities than the persisted proposal.
func materializeCandidate(current *artifact.Document, texts []string, candidate *agent.Outcome, kind artifact.Kind) (*agent.Outcome, map[string]string, []string, []string, error) {
	if candidate == nil {
		return nil, nil, nil, nil, fmt.Errorf("materialize %s candidate: no architect outcome", kind)
	}
	raw := candidate.Text
	var kept []string
	if len(texts) > 1 {
		raw, kept = FoldPasses(texts, kind)
	}
	produced, err := artifact.Parse(raw, kind)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(produced.Sections) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("materialize %s candidate: no %s sections", kind, kind.Prefix())
	}
	sections, remap := AssignIDs(current.Sections, produced.Sections, kind.Prefix())
	sections = RewriteReferences(sections, remap)
	if kind == artifact.KindPlan {
		sections = PlanTaskIDs(current.Sections, sections)
	}
	produced.Sections = sections
	produced.Preamble = ""
	produced.Front.Kind = kind
	produced.Front.Project = current.Front.Project
	dropped := dedupeSections(produced)
	text := artifact.RenderBody(produced)
	parsed, err := agent.ParseContract("markdown_sections:"+kind.Prefix(), text)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	out := *candidate
	out.Text = text
	out.Parsed = parsed
	return &out, remap, kept, dropped, nil
}

func linkCandidateIntent(projectRoot, runID string, current *artifact.Document, candidate *agent.Outcome) (*agent.Outcome, error) {
	intentID, err := artifact.IntentIDForRun(projectRoot, runID)
	if err != nil || intentID == "" {
		return candidate, err
	}
	doc, err := artifact.Parse(candidate.Text, artifact.KindRequirements)
	if err != nil {
		return nil, err
	}
	artifact.LinkRequirementsDocument(current, doc, intentID)
	text := artifact.RenderBody(doc)
	parsed, err := agent.ParseContract("markdown_sections:REQ", text)
	if err != nil {
		return nil, err
	}
	out := *candidate
	out.Text, out.Parsed = text, parsed
	return &out, nil
}
