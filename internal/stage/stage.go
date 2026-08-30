package stage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
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
	// SmallSeat says the project's implementer is a small seat (a local
	// model): the plan is portioned for it — few criteria per task.
	SmallSeat bool
	// OnEvent, if set, receives the stage's own record events (dedupe).
	OnEvent func(kind string, data map[string]interface{})
	// Ducklings that took part, recorded in the artifact's frontmatter.
	Ducklings []string
	// PriorFragment is this amendment's earlier task fragment. Unlike the
	// approved plan outline, it contains the unapproved tasks being revised.
	PriorFragment string
	// Critics pins each of a council's critique turns to its own duckling, in
	// line-up order. Empty seats the roster's single reviewer.
	Critics []config.DucklingID
	// Adopt turns intake into a survey of the existing tree: requirements for
	// what the code ALREADY satisfies, not an interview about an idea.
	Adopt bool
	// SectionWise routes a fragment update through the sectioned
	// orchestrator: one triage pass, then one fresh conversation per touched
	// section. The caller decides by seat — a small local architect gets the
	// engine as its working memory; a large one takes the whole fragment in
	// one reply, fewer calls.
	SectionWise bool
	// Images are data URLs shown to the architect alongside the amendment
	// text — the screenshot that says what a paragraph cannot. The caller
	// gates them on the architect's vision capability.
	Images []string
	// SplitTask replaces this one approved task with two narrowly-scoped,
	// independently-owned sections through the plan amendment gate.
	SplitTask string
	// Extend is the plan amendment: the architect returns ONLY the new task
	// sections and the engine merges them — a fragment by contract, because
	// re-emitting a hundred-task plan to add two tasks made a cosmetic
	// amendment cost thirty thousand prompt tokens a call and bet the whole
	// document on the model not truncating it.
	Extend string
	// Execute runs the conversation. Injected so the stage logic — prompt
	// assembly, id assignment, the proposal — is testable without a model.
	Execute func(ctx context.Context, script *strategy.Script, prompt string) (string, error)
	// OnInventory records the deterministic adoption inventory produced by the first pass.
	OnInventory func(*agent.Inventory) error
	Inventory   *agent.Inventory
	// Drafts returns the architect's earlier replies from the LAST Execute,
	// newest first, excluding the final one Execute already returned. A
	// council revise that stands pat replies in prose — "verified, no
	// changes" — and the engine keeps the draft it stood on rather than
	// failing the run for work the model refused to re-type. Optional; nil
	// means no fallback.
	Drafts func() []string
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
	if strings.TrimSpace(p.Extend) != "" || strings.TrimSpace(p.SplitTask) != "" {
		return runExtend(ctx, p, current)
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
	// An existing document updates by FRAGMENT: the architect emits only
	// what it adds or changes and the engine merges — never the whole
	// document re-typed through an output cap. First drafts and adoption
	// surveys still write whole documents; they have no unchanged majority
	// to protect. The plan's add-only light path (Extend) branched above;
	// its full updates go by fragment like every other document — a 110-task
	// plan redraft died on a 20k output cap for re-typing what it kept.
	if !p.Adopt && base != nil && len(base.Sections) > 0 {
		ask := strings.TrimSpace(p.Revision)
		if ask == "" {
			ask = strings.TrimSpace(p.Seed)
		}
		if ask == "" {
			if p.Stage == Plan {
				ask = "The specification grew or changed since this plan was written. Bring the " +
					"plan up to date: the engine's computed gap list below is the assignment."
			} else {
				ask = "Review the document against the project as it stands and update only what needs it."
			}
		}
		return runFragment(ctx, p, base, ask)
	}
	if inventoryTurn(p, current) && p.Inventory == nil {
		raw, ierr := p.Execute(ctx, strategy.InventoryScript(), "Survey the project tree and return the inventory JSON. Each item must be {name, kind, evidence-path}; include route, handler, schema, service, client, integration, and config surfaces.")
		if ierr != nil {
			return nil, ierr
		}
		parsed, ierr := agent.ParseContract("json:inventory", raw)
		if ierr != nil {
			return nil, ierr
		}
		p.Inventory = parsed.(*agent.Inventory)
		if len(p.Inventory.Items) > 60 {
			p.Inventory.Items = p.Inventory.Items[:60]
			p.Inventory.Capped = true
		}
		if p.OnInventory != nil {
			if ierr := p.OnInventory(p.Inventory); ierr != nil {
				return nil, ierr
			}
		}
	}
	prompt, err := BuildPrompt(p.ProjectRoot, p.Stage, p.Seed, base, p.Revision, p.Adopt, p.SmallSeat)
	if err != nil {
		return nil, err
	}

	script := strategy.ArtifactScript(kind.Prefix(), p.Mode, p.Critics)
	if inventoryTurn(p, current) && p.Inventory != nil {
		var checklist strings.Builder
		checklist.WriteString("\n## Survey inventory checklist\nEvery inventoried item must be covered by a section or named in the document as deliberately out of scope.\n")
		for _, item := range p.Inventory.Items {
			fmt.Fprintf(&checklist, "- %s (%s) [%s]\n", item.Name, item.Kind, item.EvidencePath)
		}
		prompt += checklist.String()
	}
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

	// A surveyed document says so on its face: these sections were DERIVED
	// from the tree by a model, not decided by a person — the approval gate
	// is the same, but a reader auditing a requirement's origin deserves the
	// distinction.
	if p.Adopt {
		produced.Front.Origin = "adopted"
	}
	// A first spec drafted from adopted requirements is a survey too: it
	// describes the same built system, and its reader deserves the same
	// provenance note the requirements carry.
	if p.Stage == Spec && len(current.Sections) == 0 {
		if reqs, rErr := artifact.Load(p.ProjectRoot, artifact.KindRequirements); rErr == nil && reqs.Front.Origin == "adopted" {
			produced.Front.Origin = "adopted"
		}
	}
	if dropped := dedupeSections(produced); len(dropped) > 0 {
		raw = produced.Raw
		if p.OnEvent != nil {
			p.OnEvent("dedupe", map[string]interface{}{"kind": string(kind), "dropped": dropped})
		}
	}
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
func BuildPrompt(projectRoot string, name Name, seed string, current *artifact.Document, revision string, adopt bool, smallSeat bool) (string, error) {
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
		if seed != "" {
			r.WriteString("## Context from the person\n\n")
			r.WriteString(strings.TrimSpace(seed))
			r.WriteString("\n\n")
		}
		r.WriteString("## The document to revise\n\n")
		r.WriteString(strings.TrimSpace(current.Raw))
		r.WriteString("\n\n")
		// The rest of the original task still applies — the format rules, what
		// the ids mean — but the instruction to write a new document does not.
		return r.String(), nil
	}

	switch name {
	case Intake:
		if adopt {
			b.WriteString("## Your task\n\nSurvey this project's codebase and write the " +
				"requirements it ALREADY satisfies.\n\n" +
				"This product exists. The code in this tree is the source of truth — " +
				"read it before writing: the file layout, the entry points, the tests. " +
				"Then write the requirements as they stand:\n\n" +
				"- One REQ section per capability the code genuinely provides, with " +
				"**Priority:** must.\n" +
				"- Intent you infer but cannot verify in the code carries " +
				"**Assumption:** naming what you inferred it from.\n" +
				"- An exclusion the code clearly commits to — a rejected input, a " +
				"documented \"not supported\" — may carry **Priority:** wont.\n" +
				"- Invent nothing aspirational. A requirement the code does not " +
				"satisfy today does not belong in a survey; new work arrives later " +
				"as briefs.\n\n")
			b.WriteString("Before reading the tree, call skill_list: a project that carries a " +
				"survey guide (module map, where the routes live, which schema is the truth) " +
				"expects you to read it with skill_read and follow it — coverage beats " +
				"wandering.\n\n")
			if seed != "" {
				b.WriteString("## Context from the person\n\n")
				b.WriteString(strings.TrimSpace(seed))
				b.WriteString("\n\n")
			}
			break
		}
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
			if Greenfield(projectRoot) {
				// Said outright, because the harness knows it and the model
				// cannot: a tree with nothing in it looks, to a model with
				// fs_list and artifact_read in hand, like something to survey.
				b.WriteString("## The project is empty\n\n" +
					"There is no code and no document yet — nothing to read, list or search. " +
					"Do not explore the tree or look for existing artifacts; every such call " +
					"returns nothing. Draft the requirements directly from the brief below, " +
					"and reply with the complete document.\n\n")
			}
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
		if Greenfield(projectRoot) {
			b.WriteString(greenfieldDocumentNotice)
		}
		b.WriteString("## Your task\n\nWrite the specification for these requirements. " +
			"Every section must carry an **Implements:** line naming the requirements it covers.\n\n" +
			"A specification says HOW the system delivers its requirements — it is not a " +
			"restatement of them. Name the concrete design: the data model and its key " +
			"entities, the access-control model with its exact scheme, the architecture and " +
			"deployment shape, domain rules with their numbers, workflows with their states. " +
			"A section a reader could have written from the requirement's title alone is not " +
			"yet a specification.\n\n" +
			"Cross-cutting design — architecture, access control, auditing, data model — " +
			"deserves sections of its own; a cross-cutting section carries **Implements:** " +
			"with the several requirements it serves. Do not shape the document as one " +
			"section per requirement.\n\n" +
			"A section that records what will NOT be built must also carry " +
			"**Priority:** wont, so the traceability check knows not to ask for " +
			"a task that implements it.\n\n")
		if reqs.Front.Origin == "adopted" && len(current.Sections) == 0 {
			// The requirements were surveyed from the tree; the first spec is
			// a survey too, and its sections must SAY they describe built
			// behaviour — the marker is what keeps the plan from re-planning
			// the product and the spine from demanding tasks for it.
			b.WriteString("This project was ADOPTED: the requirements describe a codebase " +
				"that already exists, and so does your specification. Ground every " +
				"section in the code — read the tree before writing, and call " +
				"skill_list first: a project that carries a survey guide expects you " +
				"to read it with skill_read and follow it. Mark every " +
				"section that the code already implements with a line:\n\n" +
				"**As-built:** yes\n\n" +
				"Only a section describing a genuine gap — behaviour the requirements " +
				"promise and the code does not deliver — goes without the marker.\n\n")
		}
		// The seed carries what the person attached at launch — context and
		// reference documents. Intake was the only branch that read it: spec
		// refs loaded, logged, landed in the brief, and never reached the
		// architect (B-086). Placed before the requirements so the primary
		// input stays last and most salient.
		if seed != "" {
			b.WriteString("## Context from the person\n\n")
			b.WriteString(strings.TrimSpace(seed))
			b.WriteString("\n\n")
		}
		b.WriteString(requirementInvariantMatrix(reqs))
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
		if Greenfield(projectRoot) {
			b.WriteString(greenfieldDocumentNotice)
		}
		b.WriteString(planInstruction)
		if smallSeat {
			// Portion for the seat that will build it. Tasks of 6-11 criteria
			// went to a local 35B implementer (Neocapture, 2026-08-29); a
			// small seat lands one thing at a time.
			b.WriteString("## Portion the tasks for a small implementer\n\n" +
				"The implementer of this project is a small local model. This rule overrides the " +
				"general '3-8 top-level bullets' below: each task has ONE primary deliverable and " +
				"AT MOST THREE top-level **Deliverables:** bullets, each verifiable by a command or a " +
				"test. Prefer more, smaller tasks with **Depends on:** lines over fewer large ones. A " +
				"task whose deliverables span several files or concerns is two tasks. ducklab checks " +
				"the count and returns a draft that exceeds it.\n\n")
		}
		if hasAsBuilt(spec) {
			b.WriteString("Sections marked **As-built:** yes are already delivered by the " +
				"existing code. Plan NO tasks for them — a task to build what is built " +
				"is invented work. Tasks come only from sections without the marker.\n\n")
		}
		if seed != "" {
			b.WriteString("## Context from the person\n\n")
			b.WriteString(strings.TrimSpace(seed))
			b.WriteString("\n\n")
		}
		if reqs, err := artifact.Load(projectRoot, artifact.KindRequirements); err == nil {
			b.WriteString(requirementInvariantMatrix(reqs))
		}
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

// requirementInvariantMatrix keeps approved obligations and exclusions next
// to each other. A small council alternated between “no configurable shortcut”
// and “desktop integration requires shortcut registration” because those facts
// lived fifty lines apart; a fixed shortcut satisfies both, but only when the
// distinction is visible at the point of design and planning.
func requirementInvariantMatrix(reqs *artifact.Document) string {
	if reqs == nil || len(reqs.Sections) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Approved requirement invariant matrix\n\n")
	b.WriteString("Treat these as simultaneous constraints. `wont` excludes only what its requirement actually names; it does not cancel a related `must`. If two rows appear to conflict, state one explicit policy that satisfies both and use it consistently in every section.\n\n")
	b.WriteString("| ID | Priority | Constraint |\n|---|---|---|\n")
	for _, sec := range approvedSections(reqs) {
		priority := strings.ToLower(strings.TrimSpace(sec.Field("priority")))
		if priority == "" {
			priority = "unspecified"
		}
		constraint := strings.Join(strings.Fields(strings.TrimSpace(sec.Body)), " ")
		constraint = strings.ReplaceAll(constraint, "|", "\\|")
		if len(constraint) > 220 {
			constraint = constraint[:217] + "..."
		}
		fmt.Fprintf(&b, "| %s — %s | %s | %s |\n", sec.ID, strings.ReplaceAll(sec.Title, "|", "\\|"), priority, constraint)
	}
	b.WriteString("\n")
	return b.String()
}

// inventoryTurn limits the extra pass to adoption intake and the first spec
// derived from adopted requirements. Callers may set Adopt for either path;
// the artifact check keeps direct stage users from applying it elsewhere.
func inventoryTurn(p Params, current *artifact.Document) bool {
	if !p.Adopt {
		return false
	}
	if p.Stage == Intake {
		return true
	}
	if p.Stage != Spec || current == nil || len(current.Sections) != 0 {
		return false
	}
	reqs, err := artifact.Load(p.ProjectRoot, artifact.KindRequirements)
	return err == nil && reqs != nil && reqs.Front.Origin == "adopted"
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

// TaskBodyContract is the shape every task body must take, told to every
// architect that writes tasks (plan, extend/amend, gap-fill). The top-level
// bullets under **Deliverables:** become the implementer's numbered work
// contract (strategy/deliverables.go): it reports on each by number, the
// reviewer verifies each against the diff, and an item the implementer
// cannot deliver summons the advisor. A body written as one paragraph gives
// the implementer a single deliverable — the task itself — and loses all of
// that; T-136 was born that way, which is why this is dictated rather than
// hoped for.
const TaskBodyContract = "Write each task body in this shape:\n\n" +
	"<one or two sentences: what the task achieves and why>\n\n" +
	"**Deliverables:**\n" +
	"- <one concrete, verifiable outcome — WHAT is delivered, in the words a reviewer can check against the diff>\n" +
	"  - <indented sub-bullets carry the how: files, conventions, edge cases; they are not deliverables>\n" +
	"- <the next outcome; 3-8 top-level bullets, each independently checkable>\n" +
	"- <tests are a deliverable when the task needs them: name what they must assert>\n\n" +
	"**Produces:** <comma-separated repository paths, build-target:NAME, or capability:NAME this task creates>\n\n" +
	"**Consumes:** <optional comma-separated artifacts from earlier tasks or external capability:NAME values>\n\n" +
	"**Verification:** <the command or deterministic check that exercises THIS task's changed artifacts; a project build that does not consume them is not verification>\n\n" +
	"**Exercises:** <comma-separated Produced artifacts that the verification actually loads, compiles, tests, or validates>\n\n" +
	"**Out of scope:** <what a diligent implementer might reasonably do and must not>\n\n" +
	"**Assumption:** <optional — what you took as given>\n\n" +
	"Top-level bullets are the implementer's numbered contract; it reports on each by number when it " +
	"finishes, and anything it cannot deliver brings it help. Keep each bullet one outcome, not a " +
	"paragraph; put detail in the sub-bullets.\n\n"

// planInstruction is what the architect is told at the plan stage.
//
// It asked for **Implements:** and said nothing about **Depends on:**, so no
// plan ever carried a dependency — while the parser accepted the field,
// TaskNext read it, and the board's Blocked column existed to show it. A whole
// edge of the plan was unreachable because nobody asked for it.
const planInstruction = "## Your task\n\nBreak this specification into milestones and tasks. " +
	"Milestones are H2 (`## M-01 — Title`), tasks are H3 under them (`### T-001 — Title`). " +
	"A milestone may declare its implementation lane with an **Owns:** line listing comma-separated repository paths or directory globs (for example, **Owns:** `internal/service`, `internal/artifact/**`). The lane is inherited by every task under that milestone; do not claim the same path in two live milestones.\n\n" +
	"Every task must carry an **Implements:** line naming the spec section it delivers. " +
	"Name only ids that exist in the specification above; an id that is not there is a broken link, not a placeholder.\n\n" +
	"The stack you chose has a toolchain, and the plan declares it: every milestone that builds or tests carries a " +
	"**Toolchain:** line naming the environment capabilities its tasks need. Use `cmd:NAME` for executables and " +
	"`pkg-config:MODULE>=VERSION` for native development libraries (for example, **Toolchain:** cmd:meson, cmd:ninja, " +
	"pkg-config:gtk4>=4.0, pkg-config:libadwaita-1>=1.0 — or cmd:cargo, or cmd:python3, cmd:pytest). ducklab checks the machine for them " +
	"before the first build of the milestone and asks the person to install what is missing; an undeclared tool is a " +
	"build that fails for a reason nobody named.\n\n" +
	"Lanes are exclusive: two milestones must never list the same path or overlapping directories in their **Owns:** lines, " +
	"and a broad lane (`src/`, `.`) is not a lane. When you cannot name a disjoint path set, write no **Owns:** line at all — " +
	"the absence is honest; an overlap is a collision the person has to untangle.\n\n" +
	"When a task cannot be started until another task is finished — it needs code " +
	"that task writes, not merely code in the same area — add a **Depends on:** line " +
	"naming those task ids. Write it only where it is true: a plan where every task " +
	"depends on the one before it is a plan that can only ever run one task at a " +
	"time, and a task with no real prerequisite should have no line at all. Every **Consumes:** item produced by another task must name that producer in **Depends on:**; ducklab checks this graph.\n\n" +
	TaskBodyContract

// hasAsBuilt reports whether any section carries the as-built marker.
func hasAsBuilt(doc *artifact.Document) bool {
	for _, sec := range doc.Sections {
		v := strings.ToLower(strings.TrimSpace(sec.Field("as-built")))
		if v == "yes" || v == "true" {
			return true
		}
	}
	return false
}
