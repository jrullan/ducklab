package stage

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Sectioned updates: the harness as working memory.
//
// A fragment redraft still asks one reply to survey the whole outline and
// emit every change — comfortable for a large seat, a coherence marathon for
// a 32k local model, which is how pato-atom looped. Here the ENGINE keeps
// the working memory: one cheap triage call names the sections the request
// touches, then each section is its own fresh conversation — the request,
// that one section's full text, one answer. Coherence over twenty thousand
// tokens becomes coherence over eight hundred, N independent times, and the
// conversation never grows because there is no conversation to grow.
//
// Triage is solo because it writes no document. Each selected section keeps
// the requested mode: a council means the isolated replacement is reviewed
// before composition, rather than silently downgrading the user's choice.

// sectionedPassCap bounds one update's visits. A triage that names more
// sections than this is redesign wearing an update's clothes.
const sectionedPassCap = 12

var sectionIDRe = regexp.MustCompile(`(?m)^\s*(?:-\s*)?([A-Z]+-\d+)\b`)
var sectionNewRe = regexp.MustCompile(`(?mi)^\s*(?:-\s*)?NEW:\s*(.+)$`)

func runSectioned(ctx context.Context, p Params, base *artifact.Document, ask string) (*Result, error) {
	kind := p.Stage.Kind()
	prefix := kind.Prefix()

	// Pass 0: triage. Outline in, a LIST out — the smallest possible answer.
	triagePrompt, err := buildTriagePassPrompt(p.ProjectRoot, kind, base, ask)
	if err != nil {
		return nil, err
	}
	raw, err := p.Execute(ctx, soloPass(prefix, 0), triagePrompt)
	if err != nil {
		return nil, err
	}
	ids, adds := parseTriagePass(raw, base, prefix)
	if len(ids) == 0 && len(adds) == 0 {
		reason := strings.TrimSpace(raw)
		if len(reason) > 300 {
			reason = reason[:300] + "…"
		}
		return nil, fmt.Errorf("the architect changed no sections: %s", reason)
	}
	if len(ids)+len(adds) > sectionedPassCap {
		return nil, fmt.Errorf("the update names %d sections — more than an update should touch "+
			"(%d). This is a redesign wearing an update's clothes; run it with a larger seat, or "+
			"split the request", len(ids)+len(adds), sectionedPassCap)
	}

	proposed := *base
	proposed.Sections = make([]artifact.Section, len(base.Sections))
	copy(proposed.Sections, base.Sections)
	pass := 1

	// One section, one fresh conversation.
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sec := findSection(&proposed, id)
		if sec == nil {
			continue
		}
		prompt := buildSectionPassPrompt(kind, ask, sec)
		reply, err := p.Execute(ctx, sectionPass(prefix, pass, p.Mode, p.Critics), prompt)
		pass++
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToUpper(reply), "UNCHANGED") && !strings.Contains(reply, "## ") {
			continue
		}
		repl, ok := parseSectionReply(reply, kind)
		if !ok {
			continue // an unusable pass leaves its section untouched — never the document
		}
		repl.ID = sec.ID // the id is not the model's to change
		if kind == artifact.KindPlan {
			repl.Body = stripMilestoneField(repl.Body)
			repl.Children = sec.Children // a milestone edit never claims custody of its tasks
		}
		*sec = repl
	}

	for _, title := range adds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prompt := buildNewSectionPassPrompt(kind, ask, &proposed, title, prefix)
		reply, err := p.Execute(ctx, sectionPass(prefix, pass, p.Mode, p.Critics), prompt)
		pass++
		if err != nil {
			return nil, err
		}
		sec, ok := parseSectionReply(reply, kind)
		if !ok {
			continue
		}
		if kind == artifact.KindPlan {
			// A new plan item is a TASK: the amendment's placement machinery
			// owns milestones, aliases and real ids.
			merged := mergeExtension(&proposed, []artifact.Section{sec})
			proposed = *merged
			continue
		}
		sec.ID = fmt.Sprintf("%s-%03d", prefix, NextFree(proposed.Sections, prefix))
		proposed.Sections = append(proposed.Sections, sec)
	}

	proposed.Front.Kind = kind
	proposed.Front.Project = base.Front.Project
	proposed.Front.Origin = base.Front.Origin
	if err := artifact.WriteProposal(p.ProjectRoot, kind, &proposed, p.RunID, p.Ducklings); err != nil {
		return nil, err
	}
	return &Result{Kind: kind, Proposed: &proposed, Raw: raw}, nil
}

// soloPass is one fresh mini-conversation: solo, no document contract, its
// turn coordinates offset so many passes never share a delta lane.
func soloPass(prefix string, pass int) *strategy.Script {
	script := strategy.ArtifactScript(prefix, "solo", nil)
	for i := range script.Turns {
		script.Turns[i].Contract = ""
	}
	script.TurnIndexBase = pass * 10
	return script
}

func sectionPass(prefix string, pass int, mode string, critics []config.DucklingID) *strategy.Script {
	script := strategy.ArtifactScript(prefix, mode, critics)
	for i := range script.Turns {
		if script.Turns[i].Role == config.RoleArchitect {
			script.Turns[i].Contract = ""
		}
	}
	script.TurnIndexBase = pass * 10
	return script
}

func findSection(doc *artifact.Document, id string) *artifact.Section {
	for i := range doc.Sections {
		if strings.EqualFold(doc.Sections[i].ID, id) {
			return &doc.Sections[i]
		}
		for j := range doc.Sections[i].Children {
			if strings.EqualFold(doc.Sections[i].Children[j].ID, id) {
				return &doc.Sections[i].Children[j]
			}
		}
	}
	return nil
}

func buildTriagePassPrompt(projectRoot string, kind artifact.Kind, base *artifact.Document, ask string) (string, error) {
	var b strings.Builder
	if memory, err := artifact.LoadMemory(projectRoot); err == nil {
		if mc := memory.PromptContext(); mc != "" {
			b.WriteString(mc + "\n\n")
		}
	}
	fmt.Fprintf(&b, "## Your task\n\nDecide which sections of this %s the request below touches. "+
		"Do NOT write any content yet — later steps handle one section at a time.\n\n", kind)
	b.WriteString("## The request\n\n" + strings.TrimSpace(ask) + "\n\n")
	b.WriteString("## The document (outline)\n\n")
	for _, sec := range base.Sections {
		fmt.Fprintf(&b, "- %s — %s%s\n", sec.ID, sec.Title, outlineSynopsis(sec.Body))
		for _, c := range sec.Children {
			fmt.Fprintf(&b, "  - %s — %s%s\n", c.ID, c.Title, outlineSynopsis(c.Body))
		}
	}
	b.WriteString(coverageGapsHint(projectRoot, kind, base))
	b.WriteString(planGapsHint(projectRoot, kind))
	b.WriteString("\n## Answer format\n\n" +
		"One line per item, nothing else:\n" +
		"- an existing id to CHANGE, e.g. " + kind.Prefix() + "-012\n" +
		"- NEW: <title> for a section to ADD\n" +
		"- or the single word NONE with one sentence why, if nothing should change.\n")
	return b.String(), nil
}

func parseTriagePass(raw string, base *artifact.Document, prefix string) (ids []string, adds []string) {
	seen := map[string]bool{}
	for _, m := range sectionIDRe.FindAllStringSubmatch(raw, -1) {
		id := strings.ToUpper(m[1])
		// The plan is two-level: its milestones wear the document prefix and
		// its tasks wear T- — both are editable units.
		ok := strings.HasPrefix(id, prefix+"-") || (prefix == "M" && strings.HasPrefix(id, "T-"))
		if !ok || seen[id] || findSection(base, id) == nil {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, m := range sectionNewRe.FindAllStringSubmatch(raw, -1) {
		title := strings.TrimSpace(m[1])
		if title != "" {
			adds = append(adds, title)
		}
	}
	return ids, adds
}

func buildSectionPassPrompt(kind artifact.Kind, ask string, sec *artifact.Section) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Your task\n\nUpdate ONE section of a %s for the request below. "+
		"Everything else is handled elsewhere — this section is your whole world.\n\n", kind)
	b.WriteString("## The request\n\n" + strings.TrimSpace(ask) + "\n\n")
	fmt.Fprintf(&b, "## The section today\n\n## %s — %s\n\n%s\n\n", sec.ID, sec.Title, sec.Body)
	b.WriteString("## Scope rule\n\nThe request may contain several independent changes assigned to other " +
		"section passes. Apply ONLY the clauses whose subject belongs to this section's existing " +
		"title and behavior. Do not copy, summarize, or mention clauses about another capability. " +
		"If no clause belongs to this section, answer UNCHANGED.\n\n")
	if kind == artifact.KindRequirements {
		b.WriteString("## Requirements invariants\n\n" +
			"- Make the smallest semantic delta and preserve every unrelated constraint already in this section.\n" +
			"- Keep exactly one `**Priority:**` marker, whose value is exactly `must`, `should`, `could`, or `wont`.\n" +
			"- `shall`, `must`, and `required` are mandatory and require `Priority: must`; optional/`may` behavior is not mandatory.\n" +
			"- Metadata belongs to this section: never put a clause about another subject into `Assumption` or another field.\n" +
			"- Do not add a storage destination, integration, or behavior the request did not name.\n\n")
	}
	fmt.Fprintf(&b, "## Answer format\n\nEither the full replacement section — heading and body, "+
		"under the SAME id:\n\n## %s — <title>\n<body>\n\nOr, if the request does not change this "+
		"section, reply with exactly the single word UNCHANGED.\n", sec.ID)
	return b.String()
}

func outlineSynopsis(body string) string {
	text := strings.Join(strings.Fields(body), " ")
	if text == "" {
		return ""
	}
	// Requirements often keep a short out-of-scope list in one section. A
	// 220-character title synopsis cut off the fifth bullet — file saving in
	// Neocapture corrida 44 — and made triage recreate the excluded capability
	// instead of transforming it. Five hundred remains compact while retaining
	// the behavior-bearing part of ordinary sections.
	const limit = 500
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	return " :: " + text
}

func buildNewSectionPassPrompt(kind artifact.Kind, ask string, doc *artifact.Document, title, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Your task\n\nWrite ONE new section for this %s: \"%s\". "+
		"Everything else is handled elsewhere.\n\n", kind, title)
	b.WriteString("## The request\n\n" + strings.TrimSpace(ask) + "\n\n")
	b.WriteString("## Scope rule\n\nThe request may contain several independent changes assigned to other " +
		"section passes. Apply ONLY the clause whose subject is named by this new section title. " +
		"Do not copy, summarize, assume, or mention clauses about another capability.\n\n")
	if kind == artifact.KindRequirements {
		b.WriteString("## Requirements invariants\n\n" +
			"- State the requested behavior explicitly; merely deleting or referring to an opposite decision is not implementation.\n" +
			"- Include exactly one `**Priority:**` marker, whose value is exactly `must`, `should`, `could`, or `wont`.\n" +
			"- `shall`, `must`, and `required` are mandatory and require `Priority: must`.\n" +
			"- Do not add a storage destination, integration, input constraint, or platform behavior the assigned clause did not name.\n\n")
	}
	b.WriteString("## The document (outline, for fit)\n\n")
	for _, sec := range doc.Sections {
		fmt.Fprintf(&b, "- %s — %s\n", sec.ID, sec.Title)
	}
	if kind == artifact.KindPlan {
		fmt.Fprintf(&b, "\n## Answer format\n\nThe one new task, heading and body, with its "+
			"milestone named:\n\n## T-900 — %s\n**Milestone:** <an existing M-id from the "+
			"outline>\n<body>\n", title)
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Answer format\n\nThe one new section, heading and body:\n\n"+
		"## %s — %s\n<body>\n", fragmentPlaceholder(prefix), title)
	return b.String()
}

// parseSectionReply reads ONE section out of a pass reply, whatever level
// the model emitted it at. The plan's two-level parser needs normalization
// (a bare ## T-012 is not a valid plan top level); flat documents parse
// directly.
func parseSectionReply(reply string, kind artifact.Kind) (artifact.Section, bool) {
	if kind == artifact.KindPlan {
		items, _ := parsePlanItems(reply)
		for _, it := range items {
			return it, true
		}
		return artifact.Section{}, false
	}
	got, perr := artifact.Parse(reply, kind)
	if perr != nil || len(got.Sections) == 0 {
		return artifact.Section{}, false
	}
	return got.Sections[0], true
}
