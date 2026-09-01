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

// Fragment redrafts: the amendment's lesson, generalized.
//
// Every update to an existing document used to feed the model the WHOLE
// document with orders to return the whole document changed — and one night
// killed three spec runs on three different walls (a turn cap, a stream
// timeout, an output cap) that were all the same wall: re-emitting twenty
// thousand tokens to change two hundred. The architect now returns ONLY the
// sections it adds or changes; the engine merges them by code. What a model
// never re-types, a model cannot lose — and no cap is ever spent on the
// unchanged.

// fragmentPlaceholder is the literal id the contract asks for on NEW
// sections; real ids are assigned by the engine at merge.
func fragmentPlaceholder(prefix string) string { return prefix + "-900" }

func runFragment(ctx context.Context, p Params, base *artifact.Document, ask string) (*Result, error) {
	if p.SectionWise {
		return runSectioned(ctx, p, base, ask)
	}
	kind := p.Stage.Kind()
	prefix := kind.Prefix()
	// Tombstones are input instructions and disappear from the materialized
	// candidate. Keep their effect for the lifetime of the amendment so a
	// later round cannot re-merge the approved base and resurrect a section.
	deletedSections := map[string]bool{}

	prompt, err := buildFragmentPrompt(p.ProjectRoot, kind, base, ask)
	if err != nil {
		return nil, err
	}
	script := artifactUpdateScript(prefix, p.Mode, p.Critics)
	script.FragmentPrefix = prefix
	script.MaterializeCandidate = func(_ []string, candidate *agent.Outcome) (*agent.Outcome, error) {
		if candidate == nil {
			return nil, fmt.Errorf("materialize %s fragment: no architect outcome", kind)
		}
		var proposed *artifact.Document
		if kind == artifact.KindPlan {
			items, real := parsePlanItems(candidate.Text)
			if real == 0 {
				return nil, fmt.Errorf("materialize plan fragment: no task or milestone sections")
			}
			proposed = mergePlanFragment(base, items)
		} else {
			produced, err := artifact.Parse(candidate.Text, kind)
			if err != nil || len(produced.Sections) == 0 {
				return nil, fmt.Errorf("materialize %s fragment: no sections", kind)
			}
			rememberFragmentDeletes(produced.Sections, deletedSections)
			proposed = mergeFragment(base, produced.Sections, prefix)
			applyFragmentDeletes(proposed, deletedSections)
		}
		if p.Stage == Intake && !p.Adopt {
			intentID, err := artifact.IntentIDForRun(p.ProjectRoot, p.RunID)
			if err != nil {
				return nil, err
			}
			artifact.LinkRequirementsDocument(base, proposed, intentID)
		}
		out := *candidate
		out.Text = artifact.RenderBody(proposed)
		out.Parsed = nil // fragment architect turns intentionally use freeform
		return &out, nil
	}
	if p.Rounds > 0 {
		script.MaxRounds = p.Rounds
	}
	// The architect's document contract demands a full document's shape; the
	// fragment contract in the prompt is the only law for AUTHOR turns. A
	// reviewer verdict remains a contract: an approve rendered as JSON but
	// parsed as freeform bought a needless second council round in Neocapture.
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect && strings.HasPrefix(script.Turns[i].Contract, "markdown_sections:") {
			script.Turns[i].Contract = ""
		}
	}
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

	var proposed *artifact.Document
	if kind == artifact.KindPlan {
		items, real := parsePlanItems(raw)
		if real == 0 {
			// The revise stood pat: fall back through the architect's own
			// earlier drafts before declaring nothing happened. The draft
			// the critique verified IS the proposal.
			for _, draft := range drafts(p) {
				if items2, real2 := parsePlanItems(draft); real2 > 0 {
					items, real = items2, real2
					break
				}
			}
		}
		if real == 0 {
			return nil, fmt.Errorf("the architect changed no sections: %s", clip(raw))
		}
		proposed = mergePlanFragment(base, items)
	} else {
		produced, perr := artifact.Parse(raw, kind)
		if perr != nil || len(produced.Sections) == 0 {
			for _, draft := range drafts(p) {
				if d2, e2 := artifact.Parse(draft, kind); e2 == nil && len(d2.Sections) > 0 {
					produced, perr = d2, nil
					break
				}
			}
		}
		if perr != nil || len(produced.Sections) == 0 {
			return nil, fmt.Errorf("the architect changed no sections: %s", clip(raw))
		}
		rememberFragmentDeletes(produced.Sections, deletedSections)
		proposed = mergeFragment(base, produced.Sections, prefix)
		applyFragmentDeletes(proposed, deletedSections)
	}
	proposed.Front.Kind = kind
	proposed.Front.Project = base.Front.Project
	// A surveyed origin survives an update: the document still describes a
	// built system.
	proposed.Front.Origin = base.Front.Origin
	if err := artifact.WriteProposal(p.ProjectRoot, kind, proposed, p.RunID, p.Ducklings); err != nil {
		return nil, err
	}
	return &Result{Kind: kind, Proposed: proposed, Raw: raw}, nil
}

// artifactUpdateScript removes plan's topology-manifest turn from updates.
// The manifest constrains a first plan before Markdown exists. A fragment or
// section-wise revision already has a checkpointed topology; seating that
// persona again gives one turn two incompatible jobs (JSON manifest and
// Markdown fragment) and fails before the actual edit can run.
func artifactUpdateScript(prefix, mode string, critics []config.DucklingID) *strategy.Script {
	script := strategy.ArtifactScript(prefix, mode, critics)
	if prefix != artifact.KindPlan.Prefix() {
		return script
	}
	turns := script.Turns[:0]
	for _, turn := range script.Turns {
		if turn.Persona != strategy.PersonaPlanManifest {
			turns = append(turns, turn)
		}
	}
	script.Turns = turns
	return script
}

// buildFragmentPrompt is compact by design: the document as an OUTLINE plus
// the request. The architect's toolbelt carries artifact_read — it reads the
// full text of whatever it decides to touch, instead of every section riding
// every prompt.
func buildFragmentPrompt(projectRoot string, kind artifact.Kind, base *artifact.Document, ask string) (string, error) {
	prefix := kind.Prefix()
	var b strings.Builder

	if memory, err := artifact.LoadMemory(projectRoot); err == nil {
		if mc := memory.PromptContext(); mc != "" {
			b.WriteString(mc)
			b.WriteString("\n\n")
		}
	}

	fmt.Fprintf(&b, "## Your task\n\nUpdate this %s for the request below WITHOUT rewriting it. "+
		"Return ONLY the sections you add or change — the engine merges your fragment into the "+
		"document, and every section you do not emit survives exactly as it is.\n\n", kind)
	b.WriteString("## The request\n\n" + strings.TrimSpace(ask) + "\n\n")

	fmt.Fprintf(&b, "## The document today (outline)\n\n")
	for _, sec := range base.Sections {
		fmt.Fprintf(&b, "- %s — %s\n", sec.ID, sec.Title)
		for _, c := range sec.Children {
			fmt.Fprintf(&b, "  - %s — %s\n", c.ID, c.Title)
		}
	}
	b.WriteString("\n")

	b.WriteString(coverageGapsHint(projectRoot, kind, base))
	b.WriteString(planGapsHint(projectRoot, kind))
	if kind == artifact.KindPlan {
		b.WriteString("## Rules\n\n" +
			"- Read before you write: use artifact_read to see the full text of anything you " +
			"consider changing — the outline above carries titles only.\n" +
			"- To CHANGE a task, emit it in full under its EXISTING id: ## T-012 — Title. Its " +
			"place in the plan is preserved.\n" +
			"- To CHANGE a milestone's title or description, emit ## M-002 — Title with the new " +
			"prose; its tasks are untouched.\n" +
			"- To ADD a task, use the literal id T-900 with a **Milestone:** field naming where " +
			"it belongs — real ids are assigned by the engine.\n" +
			"- Emit nothing else: no unchanged tasks, no prose between sections. What you leave " +
			"out is untouched by construction.\n" +
			"- If nothing should change, return NO sections: one sentence saying why.\n")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "## Rules\n\n"+
		"- Read before you write: use artifact_read to see the full text of any section you "+
		"consider changing — the outline above carries titles only.\n"+
		"- Make the smallest semantic delta that satisfies the request. In particular, "+
		"'not required' removes a mandatory constraint; it does NOT delete or prohibit the "+
		"capability. Preserve it as optional and state the alternative path unless the human "+
		"explicitly asks to remove/forbid it.\n"+
		"- Preserve the force of the human's decision: 'shall', 'must', and 'required' are "+
		"mandatory and use **Priority:** must; never weaken them to should/could. Optional or "+
		"'may' behavior is not mandatory.\n"+
		"- Before ADDING, check the outline and read any section that already names the same "+
		"capability, including an out-of-scope or opposite decision. Transform that EXISTING "+
		"section instead. Never both change an existing section and add another section for "+
		"the same requested behavior; add only a distinct, independently testable behavior "+
		"that no existing section represents.\n"+
		"- A changed section keeps only fields that belong to that section. Never copy an "+
		"Assumption or other field from a different section; add a new field only when the "+
		"human request or this section's behavior requires it.\n"+
		"- To CHANGE a section, emit it in full under its EXISTING id: ## %s-012 — Title.\n"+
		"- To DELETE an existing section, emit only its existing H2 heading followed by "+
		"**Delete:** yes. Omitting it does NOT delete it, and prose such as REMOVED has no effect.\n"+
		"- To ADD a section, use the literal id %s (repeat it for each new one) — real ids "+
		"are assigned by the engine.\n"+
		"- Emit nothing else: no unchanged sections, no prose between sections. A section "+
		"you leave out is untouched by construction.\n"+
		"- If nothing should change, return NO sections: one sentence saying why.\n",
		prefix, fragmentPlaceholder(prefix))
	return b.String(), nil
}

// mergeFragment applies the architect's sections to a copy of the base:
// an existing id replaces that section in place; the placeholder (or any
// unknown id) appends with the next free id. The unchanged majority is
// copied by code, which cannot truncate.
func mergeFragment(base *artifact.Document, produced []artifact.Section, prefix string) *artifact.Document {
	out := *base
	out.Sections = make([]artifact.Section, len(base.Sections))
	copy(out.Sections, base.Sections)

	for _, sec := range produced {
		replaced := false
		for i := range out.Sections {
			if strings.EqualFold(out.Sections[i].ID, sec.ID) {
				if deleteFragmentSection(sec) {
					out.Sections = append(out.Sections[:i], out.Sections[i+1:]...)
					replaced = true
					break
				}
				// In place, id preserved: references to it stay true.
				sec.ID = out.Sections[i].ID
				out.Sections[i] = sec
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		// A tombstone for an id not present in the approved base is already
		// satisfied. It must never become a new section.
		if deleteFragmentSection(sec) {
			continue
		}
		sec.ID = fmt.Sprintf("%s-%03d", prefix, NextFree(out.Sections, prefix))
		out.Sections = append(out.Sections, sec)
	}
	return &out
}

func deleteFragmentSection(sec artifact.Section) bool {
	v := strings.ToLower(strings.TrimSpace(sec.Field("delete")))
	if v == "yes" || v == "true" {
		return true
	}
	// Small seats occasionally keep the instruction on the H2 line despite
	// the example putting it below. The parser then treats it as title text;
	// accept that unambiguous spelling instead of persisting a mutilated H2.
	title := strings.ToLower(sec.Title)
	return strings.Contains(title, "**delete:** yes") || strings.Contains(title, "**delete:** true")
}

func rememberFragmentDeletes(sections []artifact.Section, deleted map[string]bool) {
	for _, sec := range sections {
		id := strings.ToUpper(strings.TrimSpace(sec.ID))
		if id == "" {
			continue
		}
		if deleteFragmentSection(sec) {
			deleted[id] = true
			continue
		}
		// An explicit later section with the same id restores it. A normal
		// post-merge candidate cannot accidentally clear the tombstone because
		// the deleted id is absent from that candidate.
		delete(deleted, id)
	}
}

func applyFragmentDeletes(doc *artifact.Document, deleted map[string]bool) {
	if doc == nil || len(deleted) == 0 {
		return
	}
	kept := doc.Sections[:0]
	for _, sec := range doc.Sections {
		if !deleted[strings.ToUpper(sec.ID)] {
			kept = append(kept, sec)
		}
	}
	doc.Sections = kept
}

// mergePlanFragment applies a plan fragment to a copy of the base. Task
// edits replace in place — id and milestone position preserved, so nothing
// re-orders under the reader. A milestone edit with prose updates its title
// and body and KEEPS its children: a heading is not custody of the tasks
// beneath it. Everything new — bare milestone declarations and unknown
// tasks — rides the amendment's own placement machinery.
func mergePlanFragment(base *artifact.Document, items []artifact.Section) *artifact.Document {
	out := *base
	out.Sections = make([]artifact.Section, len(base.Sections))
	copy(out.Sections, base.Sections)

	var appendix []artifact.Section
	for _, it := range items {
		up := strings.ToUpper(it.ID)
		if strings.HasPrefix(up, "M-") {
			if looksLikeMilestoneDecl(it) {
				appendix = append(appendix, it) // placement for what follows
				continue
			}
			edited := false
			for i := range out.Sections {
				if strings.EqualFold(out.Sections[i].ID, it.ID) {
					kept := out.Sections[i].Children
					id := out.Sections[i].ID
					out.Sections[i] = it
					out.Sections[i].ID = id
					out.Sections[i].Children = kept
					edited = true
					break
				}
			}
			if !edited {
				appendix = append(appendix, it)
			}
			continue
		}
		// A task: replace in place when it exists, else it is new work.
		replaced := false
		for mi := range out.Sections {
			for ti := range out.Sections[mi].Children {
				if strings.EqualFold(out.Sections[mi].Children[ti].ID, it.ID) {
					it.ID = out.Sections[mi].Children[ti].ID
					it.Body = stripMilestoneField(it.Body)
					out.Sections[mi].Children[ti] = it
					replaced = true
					break
				}
			}
			if replaced {
				break
			}
		}
		if !replaced {
			appendix = append(appendix, it)
		}
	}
	if len(appendix) > 0 {
		return mergeExtension(&out, appendix)
	}
	return &out
}

func stripMilestoneField(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "**Milestone:**") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// coverageGapsHint is the spine speaking into a spec update: the engine
// KNOWS which requirements no spec section implements, deterministically —
// and a small architect's triage under-selects without it. A spec produced
// by a sectioned update simply skipped two new requirements; nobody noticed
// until the person read both documents side by side. The engine reads them
// on every update instead.
func coverageGapsHint(projectRoot string, kind artifact.Kind, base *artifact.Document) string {
	if kind != artifact.KindSpec {
		return ""
	}
	covered := map[string]bool{}
	if base != nil {
		for _, sec := range base.Sections {
			for _, id := range sec.Implements {
				covered[strings.ToUpper(strings.TrimSpace(id))] = true
			}
		}
	}
	titles := map[string]string{}
	reqs, err := artifact.Load(projectRoot, artifact.KindRequirements)
	if err != nil || reqs == nil {
		return ""
	}
	for _, r := range reqs.Sections {
		titles[r.ID] = r.Title
	}
	var gaps []string
	for _, req := range reqs.Sections {
		if !covered[strings.ToUpper(req.ID)] {
			gaps = append(gaps, fmt.Sprintf("- %s — %s", req.ID, titles[req.ID]))
		}
	}
	if len(gaps) == 0 {
		return ""
	}
	return "## Coverage gaps (computed by the engine)\n\nThese requirements have NO spec " +
		"section implementing them. Cover every one this request touches — add sections for " +
		"them, with **Implements:** naming the requirement:\n" + strings.Join(gaps, "\n") + "\n\n" +
		"The gap list above is your WHOLE assignment — do not audit the rest of the spec " +
		"against the code; unlisted sections are covered and not yours to revisit. Work in " +
		"this order:\n" +
		"1. Read ONE existing spec section (artifact_read) only to learn the format.\n" +
		"2. Take the gaps one at a time: search the code for THAT behaviour " +
		"(fs_search/fs_read), then emit its section before moving to the next.\n" +
		"3. The tense comes from the code: behaviour that already exists gets " +
		"**As-built:** yes and describes reality; behaviour not built yet gets no as-built " +
		"marker and describes the contract to build — the plan generates tasks ONLY from " +
		"sections without the marker, so a wrong tense creates tasks for finished work or " +
		"omits the ones that are owed.\n\n"
}

// planGapsHint is the spine speaking into a plan update: the engine KNOWS
// which open spec sections — not as-built, not excluded — no task delivers.
// Without it a plan update opened on the generic "review the project" and a
// small architect enumerated its own priorities with the real assignment
// (four freshly accepted spec sections) nowhere in them.
func planGapsHint(projectRoot string, kind artifact.Kind) string {
	if kind != artifact.KindPlan {
		return ""
	}
	spec, err := artifact.Load(projectRoot, artifact.KindSpec)
	if err != nil || spec == nil || len(spec.Sections) == 0 {
		return ""
	}
	plan, err := artifact.Load(projectRoot, artifact.KindPlan)
	if err != nil || plan == nil {
		return ""
	}
	covered := map[string]bool{}
	for _, m := range plan.Sections {
		for _, t := range m.Children {
			for _, im := range t.Implements {
				covered[strings.ToUpper(im)] = true
			}
		}
	}
	var gaps []string
	for _, sp := range spec.Sections {
		if strings.EqualFold(strings.TrimSpace(sp.Field("priority")), "wont") {
			continue
		}
		if v := strings.ToLower(strings.TrimSpace(sp.Field("as-built"))); v == "yes" || v == "true" {
			continue
		}
		if covered[strings.ToUpper(sp.ID)] {
			continue
		}
		gaps = append(gaps, fmt.Sprintf("- %s — %s", sp.ID, sp.Title))
	}
	if len(gaps) == 0 {
		return ""
	}
	return "## Plan gaps (computed by the engine)\n\nThese spec sections are NOT as-built and " +
		"NO task delivers them. This list is your WHOLE assignment — do not audit the rest of " +
		"the plan against the code; unlisted work is covered or already built. Take them one " +
		"at a time: read THAT spec section (artifact_read), then emit the task(s) delivering " +
		"it — **Implements:** naming the section, **Milestone:** naming where it belongs (or " +
		"declare a new milestone heading above its tasks):\n" + strings.Join(gaps, "\n") + "\n\n" +
		TaskBodyContract
}

// drafts unwraps p.Drafts for the stand-pat fallback: nil-safe, empty when
// the executor keeps no history.
func drafts(p Params) []string {
	if p.Drafts == nil {
		return nil
	}
	return p.Drafts()
}

// clip bounds a model reply quoted inside an error message.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
