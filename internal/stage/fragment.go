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

	produced, perr := artifact.Parse(raw, kind)
	if perr != nil || len(produced.Sections) == 0 {
		reason := strings.TrimSpace(raw)
		if len(reason) > 300 {
			reason = reason[:300] + "…"
		}
		return nil, fmt.Errorf("the architect changed no sections: %s", reason)
	}

	proposed := mergeFragment(base, produced.Sections, prefix)
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
