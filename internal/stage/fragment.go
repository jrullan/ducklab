package stage

import (
	"context"
	"fmt"
	"strings"

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

	prompt, err := buildFragmentPrompt(p.ProjectRoot, kind, base, ask)
	if err != nil {
		return nil, err
	}
	script := strategy.ArtifactScript(prefix, p.Mode, p.Critics)
	if p.Rounds > 0 {
		script.MaxRounds = p.Rounds
	}
	// The document contract demands a full document's shape; the fragment
	// contract in the prompt is the only law (the amendment learned this
	// the hard way: two contradictory contracts split the models between
	// them).
	for i := range script.Turns {
		script.Turns[i].Contract = ""
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
			reason := strings.TrimSpace(raw)
			if len(reason) > 300 {
				reason = reason[:300] + "…"
			}
			return nil, fmt.Errorf("the architect changed no sections: %s", reason)
		}
		proposed = mergePlanFragment(base, items)
	} else {
		produced, perr := artifact.Parse(raw, kind)
		if perr != nil || len(produced.Sections) == 0 {
			reason := strings.TrimSpace(raw)
			if len(reason) > 300 {
				reason = reason[:300] + "…"
			}
			return nil, fmt.Errorf("the architect changed no sections: %s", reason)
		}
		proposed = mergeFragment(base, produced.Sections, prefix)
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

	b.WriteString(coverageGapsHint(projectRoot, kind))
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
		"- To CHANGE a section, emit it in full under its EXISTING id: ## %s-012 — Title.\n"+
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
		sec.ID = fmt.Sprintf("%s-%03d", prefix, NextFree(out.Sections, prefix))
		out.Sections = append(out.Sections, sec)
	}
	return &out
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
func coverageGapsHint(projectRoot string, kind artifact.Kind) string {
	if kind != artifact.KindSpec {
		return ""
	}
	spine, err := artifact.LoadSpine(projectRoot)
	if err != nil {
		return ""
	}
	titles := map[string]string{}
	if reqs, rerr := artifact.Load(projectRoot, artifact.KindRequirements); rerr == nil && reqs != nil {
		for _, r := range reqs.Sections {
			titles[r.ID] = r.Title
		}
	}
	var gaps []string
	for _, te := range spine.Check() {
		if te.Kind == artifact.OrphanRequirement {
			gaps = append(gaps, fmt.Sprintf("- %s — %s", te.ID, titles[te.ID]))
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
