package stage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/strategy"
)

// The plan amendment: Review's light exit, executed light.
//
// The first implementation ran it as a whole-document revision, which meant a
// cosmetic two-task amendment carried the entire plan in every prompt — 30k
// tokens a call, times a council, times rounds — and required the model to
// re-emit all hundred tasks verbatim, betting the document on it not
// truncating. The architect now returns ONLY the new task sections; the
// engine copies everything else by code, assigns real ids, and places the
// tasks under their milestone. What a model never re-types, a model cannot
// lose.

func runExtend(ctx context.Context, p Params, current *artifact.Document) (*Result, error) {
	kind := p.Stage.Kind()
	if p.Stage != Plan {
		return nil, fmt.Errorf("extend amends the plan; %s grows through a brief", p.Stage)
	}
	if current == nil || len(current.Sections) == 0 {
		return nil, fmt.Errorf("no plan to extend")
	}

	prompt, err := buildExtendPrompt(p.ProjectRoot, current, p.Extend)
	if err != nil {
		return nil, err
	}
	script := strategy.ArtifactScript(kind.Prefix(), p.Mode, p.Critics)
	if p.Rounds > 0 {
		script.MaxRounds = p.Rounds
	}
	// No document contract on an amendment. ArtifactScript demands
	// markdown_sections:M — a full plan's shape — while the amendment prompt
	// demands a T-900 fragment: two contradictory contracts in one turn.
	// Models split between them: one fused its task into an M- heading to
	// satisfy the validator (the phantom-task shape), another obeyed the
	// fragment and was executed by the M contract — "no sections matching M
	// found". The fragment contract in the prompt is the only one that
	// speaks; runExtend's own parse and refusal handling judge the reply.
	for i := range script.Turns {
		script.Turns[i].Contract = ""
	}
	// The evidence rides the architect's own turn, like a bug's screenshots
	// ride the triager's.
	if len(p.Images) > 0 {
		for i := range script.Turns {
			if script.Turns[i].Role == config.RoleArchitect {
				script.Turns[i].Images = p.Images
				break
			}
		}
	}
	raw, err := p.Execute(ctx, script, prompt)
	if err != nil {
		return nil, err
	}

	produced, perr := artifact.Parse(normalizeFragment(raw), kind)
	// Tasks are the parsed sections' CHILDREN: normalization demotes the
	// contract's headings under one synthetic milestone, so refusal prose —
	// which parses as that milestone's body — yields none.
	var tasks []artifact.Section
	real := 0
	if perr == nil {
		for _, sec := range produced.Sections {
			tasks = append(tasks, sec.Children...)
		}
		for _, t := range tasks {
			if !looksLikeMilestoneDecl(t) {
				real++
			}
		}
	}
	if real == 0 {
		// By contract this is the architect judging the change core — or
		// producing nothing usable. Either way the person gets the words.
		reason := strings.TrimSpace(raw)
		if len(reason) > 300 {
			reason = reason[:300] + "…"
		}
		return nil, fmt.Errorf("the architect added no tasks: %s", reason)
	}

	proposed := mergeExtension(current, tasks)
	proposed.Front.Kind = kind
	proposed.Front.Project = current.Front.Project
	if err := artifact.WriteProposal(p.ProjectRoot, kind, proposed, p.RunID, p.Ducklings); err != nil {
		return nil, err
	}
	return &Result{Kind: kind, Proposed: proposed, Raw: raw}, nil
}

// normalizeFragment makes the architect's fragment parseable as a plan: the
// contract's `## TASK — title` headings become H3 tasks under one synthetic
// milestone, which is what the plan parser reads — models also emit H3 or a
// dash id ("TASK-1") and both survive. The synthetic milestone never reaches
// the plan; mergeExtension flattens it away.
func normalizeFragment(raw string) string {
	var b strings.Builder
	b.WriteString("## M-000 — amendment fragment\n")
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") && !strings.HasPrefix(t, "### ") {
			line = "#" + t // demote H2 headings to H3
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// buildExtendPrompt is compact by design: the plan as an OUTLINE (ids and
// titles — placement and duplicate-checking need no bodies), the spec as a
// wiring list, the change, and the fragment contract.
func buildExtendPrompt(projectRoot string, plan *artifact.Document, change string) (string, error) {
	var b strings.Builder

	memory, err := artifact.LoadMemory(projectRoot)
	if err == nil {
		if mc := memory.PromptContext(); mc != "" {
			b.WriteString(mc)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Your task\n\nExtend this plan for the change below, WITHOUT a redesign. " +
		"Return ONLY the new task section(s) — never the rest of the plan; the engine merges " +
		"your fragment into the document itself.\n\n")
	b.WriteString("## The change\n\n" + strings.TrimSpace(change) + "\n\n")

	b.WriteString("## The plan today (outline)\n\n")
	for _, m := range plan.Sections {
		fmt.Fprintf(&b, "## %s — %s\n", m.ID, m.Title)
		for _, t := range m.Children {
			fmt.Fprintf(&b, "- %s — %s\n", t.ID, t.Title)
		}
	}
	b.WriteString("\n")

	if spec, sErr := artifact.Load(projectRoot, artifact.KindSpec); sErr == nil && spec != nil && len(spec.Sections) > 0 {
		b.WriteString("## Spec sections you may wire to\n\n")
		for _, sp := range spec.Sections {
			fmt.Fprintf(&b, "- %s — %s\n", sp.ID, sp.Title)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n\n" +
		"- One to three tasks, the fewest that deliver the change, each formatted exactly:\n\n" +
		"## T-900 — <imperative title>\n" +
		"**Milestone:** <an existing M-id from the outline; omit to use the last>\n" +
		"**Implements:** <existing SPEC ids that genuinely cover this, comma-separated; omit " +
		"if none — the task will wear spec-debt until the spec catches up>\n" +
		"<what to do, and how \"done\" is verified>\n\n" +
		"- Use the literal id T-900 for EVERY task (repeat it) — real ids are assigned by the engine.\n" +
		"- Never invent SPEC ids; wire only to the list above.\n" +
		"- If the change alters what the product IS — its requirements — return NO sections: " +
		"one sentence saying why, and the person will write a feature brief instead.\n")
	return b.String(), nil
}

// mergeExtension appends the produced tasks to a copy of the current plan:
// fresh sequential ids, placed under the named milestone or the last one.
// The untouched hundred tasks are copied by code, which cannot truncate.
func mergeExtension(current *artifact.Document, tasks []artifact.Section) *artifact.Document {
	out := *current
	out.Sections = make([]artifact.Section, len(current.Sections))
	copy(out.Sections, current.Sections)

	var existing []artifact.Section
	for _, m := range out.Sections {
		existing = append(existing, m.Children...)
	}

	// An architect extending the plan may declare a NEW milestone: a heading
	// like "## M-015 — Dashboard UI" above its tasks. Flattened, that heading
	// arrived in the task list and became a phantom task — title, no body —
	// which the person launched first, and a test-writer handed an empty
	// brief invented one. A fragment section wearing the milestone prefix is
	// a placement declaration: find it by id or title, create it with a real
	// id when it is genuinely new, and alias the fragment's id so the tasks'
	// own Milestone: fields resolve to the milestone that actually exists.
	alias := map[string]string{}
	lastDeclared := ""
	resolveMilestone := func(decl artifact.Section) string {
		for i := range out.Sections {
			if strings.EqualFold(out.Sections[i].ID, decl.ID) ||
				strings.EqualFold(strings.TrimSpace(out.Sections[i].Title), strings.TrimSpace(decl.Title)) {
				return out.Sections[i].ID
			}
		}
		id := fmt.Sprintf("M-%03d", NextFree(out.Sections, "M"))
		out.Sections = append(out.Sections, artifact.Section{ID: id, Title: decl.Title})
		return id
	}

	for _, t := range tasks {
		if looksLikeMilestoneDecl(t) {
			real := resolveMilestone(t)
			alias[strings.ToUpper(t.ID)] = real
			lastDeclared = real
			continue
		}
		id := fmt.Sprintf("T-%03d", NextFree(existing, "T"))
		milestone := strings.TrimSpace(t.Field("milestone"))
		if real, ok := alias[strings.ToUpper(milestone)]; ok {
			milestone = real
		}
		if milestone == "" {
			milestone = lastDeclared
		}
		// The placement field did its job; the task body should not carry it.
		var kept []string
		for _, line := range strings.Split(t.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "**Milestone:**") {
				continue
			}
			kept = append(kept, line)
		}
		task := artifact.Section{
			ID:         id,
			Title:      t.Title,
			Body:       strings.TrimSpace(strings.Join(kept, "\n")),
			Implements: t.Implements,
		}
		existing = append(existing, task)

		placed := false
		for i := range out.Sections {
			if strings.EqualFold(out.Sections[i].ID, milestone) {
				out.Sections[i].Children = append(out.Sections[i].Children, task)
				placed = true
				break
			}
		}
		if !placed && len(out.Sections) > 0 {
			last := len(out.Sections) - 1
			out.Sections[last].Children = append(out.Sections[last].Children, task)
		}
	}
	return &out
}

// looksLikeMilestoneDecl separates placement from work by the evidence, not
// the id alone. The contract asks for T-900 headings, but an architect fused
// its milestone and its task into one M- heading — carrying a full brief,
// Implements, out-of-scope notes — and the id-only rule filed real work as a
// declaration and refused the run. A declaration is a bare heading: empty
// body, or field lines only. Prose is a brief, and a brief is a task,
// whatever id the model dressed it in.
func looksLikeMilestoneDecl(s artifact.Section) bool {
	if !strings.HasPrefix(strings.ToUpper(s.ID), "M-") {
		return false
	}
	for _, line := range strings.Split(s.Body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "**") {
			continue
		}
		return false // prose: this is work wearing the wrong id
	}
	return true
}
