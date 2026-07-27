package stage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Name identifies a lifecycle stage.
type Name string

const (
	Intake Name = "intake"
	Spec   Name = "spec"
	Plan   Name = "plan"
)

// Valid reports whether a string names a stage this package runs.
func Valid(s string) bool {
	switch Name(s) {
	case Intake, Spec, Plan:
		return true
	}
	return false
}

// Kind is the artifact a stage produces.
func (n Name) Kind() artifact.Kind {
	switch n {
	case Intake:
		return artifact.KindRequirements
	case Spec:
		return artifact.KindSpec
	case Plan:
		return artifact.KindPlan
	}
	return ""
}

// Params configure one stage execution.
type Params struct {
	ProjectRoot string
	Stage       Name
	RunID       string
	// Seed is an existing document to work from (`--from brief.txt`) instead
	// of interviewing the human.
	Seed string
	// Ducklings that took part, recorded in the artifact's frontmatter.
	Ducklings []string
	// Execute runs the conversation. Injected so the stage logic — prompt
	// assembly, id assignment, the proposal — is testable without a model.
	Execute func(ctx context.Context, script *strategy.Script, prompt string) (string, error)
}

// Result is what a stage produced.
type Result struct {
	Kind artifact.Kind
	// Proposed is the document written to <kind>.md.proposed.
	Proposed *artifact.Document
	// Remapped records ids the stage had to reassign, so the caller can say so.
	Remapped map[string]string
	// Raw is the model's text, kept for the transcript.
	Raw string
}

// Run executes a stage and writes a proposal.
//
// It never writes the artifact itself: a stage produces a candidate and the
// human decides (05 §1.1). Nothing here asks a model whether the result is
// good — that is the gate's job, and for artifact stages the gate is the
// person plus the deterministic trace check.
func Run(ctx context.Context, p Params) (*Result, error) {
	if !Valid(string(p.Stage)) {
		return nil, fmt.Errorf("unknown stage %q", p.Stage)
	}
	if p.Execute == nil {
		return nil, fmt.Errorf("stage %s: no executor", p.Stage)
	}
	kind := p.Stage.Kind()

	current, err := artifact.Load(p.ProjectRoot, kind)
	if err != nil {
		return nil, err
	}
	prompt, err := BuildPrompt(p.ProjectRoot, p.Stage, p.Seed, current)
	if err != nil {
		return nil, err
	}

	script := strategy.CouncilScript(kind.Prefix())
	raw, err := p.Execute(ctx, script, prompt)
	if err != nil {
		return nil, err
	}

	produced, err := artifact.Parse(raw, kind)
	if err != nil {
		return nil, err
	}
	if len(produced.Sections) == 0 {
		// The contract should have caught this, but a stage that silently
		// wrote an empty artifact would erase the previous one on accept.
		return nil, fmt.Errorf("stage %s produced no %s sections", p.Stage, kind.Prefix())
	}

	sections, remap := AssignIDs(current.Sections, produced.Sections, kind.Prefix())
	sections = RewriteReferences(sections, remap)
	if p.Stage == Plan {
		sections = PlanTaskIDs(current.Sections, sections)
	}
	produced.Sections = sections
	produced.Front.Kind = kind
	produced.Front.Project = current.Front.Project

	if err := artifact.WriteProposal(p.ProjectRoot, kind, produced, p.RunID, p.Ducklings); err != nil {
		return nil, err
	}
	return &Result{Kind: kind, Proposed: produced, Remapped: remap, Raw: raw}, nil
}

// BuildPrompt assembles what the architect is asked (04 §1.2).
//
// Each stage sees only what it needs: intake sees the brief, spec sees approved
// requirements, plan sees the spec. Feeding a stage the whole cycle would bury
// the thing it is meant to work on.
func BuildPrompt(projectRoot string, name Name, seed string, current *artifact.Document) (string, error) {
	var b strings.Builder

	memory, err := artifact.LoadMemory(projectRoot)
	if err != nil {
		return "", err
	}
	if mc := memory.PromptContext(); mc != "" {
		b.WriteString(mc)
		b.WriteString("\n\n")
	}

	switch name {
	case Intake:
		b.WriteString("## Your task\n\nWrite the project's requirements.\n\n")
		if seed != "" {
			b.WriteString("## The brief you were given\n\n")
			b.WriteString(strings.TrimSpace(seed))
			b.WriteString("\n\n")
		} else {
			b.WriteString("No brief was provided. Ask the human what they are building " +
				"before drafting, at most five questions, one message at a time.\n\n")
		}

	case Spec:
		reqs, err := artifact.Load(projectRoot, artifact.KindRequirements)
		if err != nil {
			return "", err
		}
		approved := approvedSections(reqs)
		if len(approved) == 0 {
			return "", fmt.Errorf("spec needs requirements: run `ducklab intake` first")
		}
		b.WriteString("## Your task\n\nWrite the specification for these requirements. " +
			"Every section must carry an **Implements:** line naming the requirements it covers.\n\n")
		b.WriteString("## Requirements\n\n")
		for _, r := range approved {
			fmt.Fprintf(&b, "### %s — %s\n%s\n\n", r.ID, r.Title, strings.TrimSpace(r.Body))
		}

	case Plan:
		spec, err := artifact.Load(projectRoot, artifact.KindSpec)
		if err != nil {
			return "", err
		}
		if len(spec.Sections) == 0 {
			return "", fmt.Errorf("plan needs a spec: run `ducklab spec` first")
		}
		b.WriteString("## Your task\n\nBreak this specification into milestones and tasks. " +
			"Milestones are H2 (`## M-01 — Title`), tasks are H3 under them (`### T-001 — Title`). " +
			"Every task must carry an **Implements:** line naming the spec section it delivers.\n\n")
		b.WriteString("## Specification\n\n")
		for _, s := range spec.Sections {
			fmt.Fprintf(&b, "### %s — %s\n%s\n\n", s.ID, s.Title, strings.TrimSpace(s.Body))
		}
	}

	// Tell the architect which ids exist and where new ones start, so it does
	// not have to guess and the orchestrator does not have to renumber.
	kind := name.Kind()
	if len(current.Sections) > 0 {
		b.WriteString("## Sections that already exist\n\n")
		for _, s := range current.Sections {
			fmt.Fprintf(&b, "- %s — %s\n", s.ID, s.Title)
		}
		b.WriteString("\nKeep these ids for these items. ")
	}
	fmt.Fprintf(&b, "Allocate new ids from %s-%03d upward.\n",
		kind.Prefix(), NextFree(current.Sections, kind.Prefix()))

	return b.String(), nil
}

// approvedSections returns requirements that are not dropped.
//
// Status is advisory here: a requirement with no status is treated as live,
// because refusing to spec an unstatused requirement would make the common
// case — a fresh intake — fail.
func approvedSections(doc *artifact.Document) []artifact.Section {
	var out []artifact.Section
	for _, s := range doc.Sections {
		if strings.EqualFold(s.Field("status"), "dropped") {
			continue
		}
		out = append(out, s)
	}
	return out
}
