package strategy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// A document council's last architect turn is the one nobody reviews: after
// the critics speak, the revision goes straight to the gate. The plan that
// reached Neocapture's gate on 2026-08-29 had lost every Implements: line
// its previous draft carried, and a person had to send it back. Structure is
// deterministic; the harness checks it and returns the defects to the
// architect as an error that teaches, before spending a human turn.

// structureFindings lists what a revised draft lost or broke against the
// draft before it and against the contract of its document kind. Empty
// means the draft is structurally sound.
func structureFindings(prev, cur []agent.Section, contract string, known map[string]bool) []string {
	var out []string
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	needsImplements := prefix == "SPEC" || prefix == "M" || prefix == "T"
	implementsLine := regexp.MustCompile(`(?im)^\*\*Implements:\*\*\s*(.+)$`)
	idToken := regexp.MustCompile(`[A-Z]+-\d+`)

	seen := map[string]bool{}
	for _, s := range cur {
		// Every Implements: target must exist in the project's documents;
		// an id that is not there is a dangling reference the spine will
		// report, and a task built against it has no contract.
		if len(known) > 0 {
			for _, m := range implementsLine.FindAllStringSubmatch(s.Body, -1) {
				for _, id := range idToken.FindAllString(m[1], -1) {
					if !known[id] {
						out = append(out, fmt.Sprintf("%s implements %s, which is not a section of any project document — name an id that exists, or drop it", s.ID, id))
					}
				}
			}
		}
		if seen[s.ID] {
			out = append(out, fmt.Sprintf("%s appears twice", s.ID))
		}
		seen[s.ID] = true
		if needsImplements && (strings.HasPrefix(s.ID, "SPEC-") || strings.HasPrefix(s.ID, "T-")) &&
			!strings.Contains(strings.ToLower(s.Body), "**implements:**") {
			out = append(out, fmt.Sprintf("%s has no **Implements:** line", s.ID))
		}
		// One Deliverables heading per TASK. A plan's parsed sections are
		// milestones whose bodies hold their tasks as H3 blocks — counted
		// per milestone, every plan was a false positive and the architect
		// was sent back four times for nothing (benchmark run 2).
		for _, block := range taskBlocks(s.Body) {
			if n := strings.Count(block.body, "**Deliverables:**"); n > 1 {
				out = append(out, fmt.Sprintf("%s has %d **Deliverables:** headings; one per task", block.id, n))
			}
		}
	}
	// Sub-numbered ids are invisible to the spine: a requirements draft with
	// `### REQ-003.1 …` sub-sections sent the spec reviewer on 21 searches for
	// ids that were never sections (Neocapture, 2026-08-30).
	if prefix != "" {
		subID := regexp.MustCompile(`(?m)^#{2,4}\s+` + regexp.QuoteMeta(prefix) + `-\d+[.\-]\d+\b`)
		for _, s := range cur {
			if m := subID.FindString(s.Body); m != "" {
				out = append(out, fmt.Sprintf("%s contains a sub-numbered heading (%q): sub-numbered ids are not sections and their traceability is lost — give the item its own %s-NNN H2 id, or fold it into the section's body as bullets", s.ID, strings.TrimSpace(m), prefix))
			}
		}
	}
	for _, p := range prev {
		if !seen[p.ID] {
			out = append(out, fmt.Sprintf("%s (%s) was in your previous draft and is gone — restore it, or state in it why it no longer applies (Priority: wont)", p.ID, p.Title))
		}
	}
	return out
}

type taskBlock struct {
	id   string
	body string
}

// taskBlocks splits a milestone body into its H3 task blocks; a body with no
// tasks is one block under the section's own id.
func taskBlocks(body string) []taskBlock {
	lines := strings.Split(body, "\n")
	var out []taskBlock
	cur := taskBlock{id: "", body: ""}
	var b strings.Builder
	flush := func() {
		cur.body = b.String()
		out = append(out, cur)
		b.Reset()
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			if cur.id != "" || strings.TrimSpace(b.String()) != "" {
				flush()
			}
			id := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if i := strings.Index(id, " —"); i > 0 {
				id = id[:i]
			}
			cur = taskBlock{id: strings.TrimSpace(id)}
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	for i := range out {
		if out[i].id == "" {
			out[i].id = "(section)"
		}
	}
	return out
}

// structureNote renders findings for the architect's retry prompt.
func structureNote(findings []string) string {
	var b strings.Builder
	b.WriteString("## Structure check — fix these before anything else\n\n")
	b.WriteString("ducklab checked your revision against the document rules and your previous draft. " +
		"It is not yet acceptable. Return the WHOLE document again with every item below repaired, " +
		"changing nothing else:\n\n")
	for _, f := range findings {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// kindOfContract maps a council's document contract to its artifact kind.
// A critic's own contract is "verdict", so the architect turns' contract is
// consulted through the script.
func kindOfContract(contract string, script *Script) string {
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	if prefix == contract && script != nil {
		for _, t := range script.Turns {
			if strings.HasPrefix(t.Contract, "markdown_sections:") {
				prefix = strings.TrimPrefix(t.Contract, "markdown_sections:")
				break
			}
		}
	}
	switch prefix {
	case "REQ":
		return "requirements"
	case "SPEC":
		return "spec"
	case "M", "T":
		return "plan"
	}
	return ""
}

// sectionsOf returns a parsed document's sections, or nil.
func sectionsOf(outcome *agent.Outcome) []agent.Section {
	if outcome == nil {
		return nil
	}
	secs, _ := outcome.Parsed.([]agent.Section)
	return secs
}
