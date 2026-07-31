package stage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
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
	// Rounds overrides the script's limit. A round is the whole turn sequence
	// — draft, critique, revise — not one turn, and the loop stops early when
	// the reviewer approves, so raising this costs nothing on a draft that
	// converges.
	Rounds int
	// Mode picks the script. Empty means council, which is the spec's answer
	// for an artifact stage (05 §4.4).
	Mode string
	// Revision is what a person asked to be changed about the draft they were
	// shown. Set only when revising, and it changes the job entirely: the
	// architect edits an existing document rather than writing a new one.
	Revision string
	// Ducklings that took part, recorded in the artifact's frontmatter.
	Ducklings []string
	// Critics pins each of a council's critique turns to its own duckling, in
	// line-up order. Empty seats the roster's single reviewer.
	Critics []config.DucklingID
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
	// A revision works from the draft that was actually shown, which is the
	// proposal when one is pending. Revising the accepted version instead
	// would silently discard everything the last run produced.
	base := current
	if p.Revision != "" {
		if proposed, _ := artifact.LoadProposed(p.ProjectRoot, kind); proposed != nil {
			base = proposed
		}
	}
	prompt, err := BuildPrompt(p.ProjectRoot, p.Stage, p.Seed, base, p.Revision)
	if err != nil {
		return nil, err
	}

	script := strategy.ArtifactScript(kind.Prefix(), p.Mode, p.Critics)
	if p.Rounds > 0 {
		script.MaxRounds = p.Rounds
	}
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
	// Anything before the first section is the model narrating its own
	// process ("Let me check the requirements I wrote…"), not the artifact.
	// The architect's contract is sections; a preamble a human wrote in an
	// existing document is preserved by Parse, but a stage's proposal starts
	// at its first section.
	produced.Preamble = ""
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
func BuildPrompt(projectRoot string, name Name, seed string, current *artifact.Document, revision string) (string, error) {
	var b strings.Builder

	memory, err := artifact.LoadMemory(projectRoot)
	if err != nil {
		return "", err
	}
	if mc := memory.PromptContext(); mc != "" {
		b.WriteString(mc)
		b.WriteString("\n\n")
	}

	// A revision replaces the task, rather than adding to it.
	//
	// Accept and reject are a verdict on a document that is usually almost
	// right, and "almost" has no button. This is the third answer: keep it,
	// change this. The draft goes back with the note attached, so the
	// architect edits rather than starts again — everything not mentioned is
	// meant to survive, and the diff shown at the gate is how a person checks
	// that it did.
	if revision != "" {
		var r strings.Builder
		r.WriteString("## Your task\n\nRevise the document below. It was reviewed by the person " +
			"who asked for it, and they want one thing changed.\n\n")
		r.WriteString("## What they asked for\n\n")
		r.WriteString(strings.TrimSpace(revision))
		r.WriteString("\n\n## Rules for this revision\n\n")
		r.WriteString("- Change what the note asks for, and nothing else. Every other section " +
			"must come back **exactly as it is**, same id, same wording.\n")
		r.WriteString("- Keep every id. A section that keeps its id and changes its text is a " +
			"revision; a renumbered one breaks every reference to it.\n")
		r.WriteString("- If the note asks for something that cannot be done, say so in the " +
			"section rather than silently doing something else.\n")
		r.WriteString("- Return the whole document, not a fragment.\n\n")
		r.WriteString("## The document to revise\n\n")
		r.WriteString(strings.TrimSpace(current.Raw))
		r.WriteString("\n\n")
		// The rest of the original task still applies — the format rules, what
		// the ids mean — but the instruction to write a new document does not.
		return r.String(), nil
	}

	switch name {
	case Intake:
		if len(current.Sections) > 0 {
			// The brief here is an ADDITION — a feature, a change — not a
			// restatement of the product. Said explicitly, because "write the
			// requirements" plus a brief describing one feature reads as
			// "the requirements are this one feature".
			b.WriteString("## Your task\n\nExtend the project's requirements. " +
				"The brief below describes NEW work for an existing product; the " +
				"approved requirements follow further down and must survive.\n\n")
		} else {
			b.WriteString("## Your task\n\nWrite the project's requirements.\n\n")
		}
		if seed != "" {
			b.WriteString("A requirement describing what is explicitly out of scope must " +
				"carry **Priority:** wont.\n\n")
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
			"Every section must carry an **Implements:** line naming the requirements it covers.\n\n" +
			"A section that records what will NOT be built must also carry " +
			"**Priority:** wont, so the traceability check knows not to ask for " +
			"a task that implements it.\n\n")
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
		b.WriteString(planInstruction)
		b.WriteString("## Specification\n\n")
		for _, s := range spec.Sections {
			fmt.Fprintf(&b, "### %s — %s\n%s\n\n", s.ID, s.Title, strings.TrimSpace(s.Body))
		}
	}

	// Tell the architect which ids exist and where new ones start, so it does
	// not have to guess and the orchestrator does not have to renumber.
	kind := name.Kind()
	if len(current.Sections) > 0 {
		// The WHOLE document, not an id list. This used to show only ids and
		// titles with "keep these ids for these items" — and a model cannot
		// return unchanged what it was never given. Since the proposal replaces
		// the document wholesale on accept, a re-run to add a feature would
		// propose a document missing every body it had not seen: growing a
		// project — the normal case after the first week — silently fought the
		// person every time.
		b.WriteString("## The document as it stands, already approved\n\n")
		b.WriteString(strings.TrimSpace(current.Raw))
		b.WriteString("\n\n## Rules for extending an approved document\n\n")
		b.WriteString("- Your draft REPLACES this document wholesale, so return the WHOLE " +
			"document: every section above comes back **exactly as it is** — same id, " +
			"same wording — unless what you were asked for genuinely changes it.\n")
		b.WriteString("- Add new sections for what is new. Never renumber an existing one: " +
			"a kept id with changed text is an edit, a changed id breaks every " +
			"reference to it.\n")
		b.WriteString("- Removing a section is a decision a person makes, not you. If " +
			"something seems obsolete, keep it and say so in its body.\n\n")
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

// planInstruction is what the architect is told at the plan stage.
//
// It asked for **Implements:** and said nothing about **Depends on:**, so no
// plan ever carried a dependency — while the parser accepted the field,
// TaskNext read it, and the board's Blocked column existed to show it. A whole
// edge of the plan was unreachable because nobody asked for it.
const planInstruction = "## Your task\n\nBreak this specification into milestones and tasks. " +
	"Milestones are H2 (`## M-01 — Title`), tasks are H3 under them (`### T-001 — Title`). " +
	"Every task must carry an **Implements:** line naming the spec section it delivers.\n\n" +
	"When a task cannot be started until another task is finished — it needs code " +
	"that task writes, not merely code in the same area — add a **Depends on:** line " +
	"naming those task ids. Write it only where it is true: a plan where every task " +
	"depends on the one before it is a plan that can only ever run one task at a " +
	"time, and a task with no real prerequisite should have no line at all.\n\n"
