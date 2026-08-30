package strategy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
)

var ErrStructureFailed = errors.New("document structure did not converge")

// A document council's last architect turn is the one nobody reviews: after
// the critics speak, the revision goes straight to the gate. The plan that
// reached Neocapture's gate on 2026-08-29 had lost every Implements: line
// its previous draft carried, and a person had to send it back. Structure is
// deterministic; the harness checks it and returns the defects to the
// architect as an error that teaches, before spending a human turn.

// structureFindings lists what a revised draft lost or broke against the
// draft before it and against the contract of its document kind. Empty
// means the draft is structurally sound.
func structureFindings(prev, cur []agent.Section, contract string, known map[string]bool, small bool, raw string) []string {
	var out []string
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	needsImplements := prefix == "SPEC" || prefix == "M" || prefix == "T"
	isPlan := prefix == "M" || prefix == "T"

	// Plan rules live at task granularity: every task names what it
	// implements; a small seat's task carries at most three top-level
	// deliverables (the brief asks; the check enforces — T-001 arrived with
	// twelve, benchmark run 4); milestone lanes never overlap.
	if isPlan {
		var blocks []taskBlock
		for _, s := range cur {
			sectionBlocks := taskBlocks(s.Body)
			blocks = append(blocks, sectionBlocks...)
			for _, block := range sectionBlocks {
				if !strings.HasPrefix(block.id, "T-") {
					continue
				}
				if !strings.Contains(strings.ToLower(block.body), "**implements:**") {
					out = append(out, fmt.Sprintf("%s has no **Implements:** line", block.id))
				}
				if small {
					if n := topLevelDeliverables(block.body); n > 3 {
						out = append(out, fmt.Sprintf("%s has %d top-level **Deliverables:** bullets; a small implementer takes at most 3 — split the task", block.id, n))
					}
					if !strings.Contains(strings.ToLower(block.body), "**verification:**") {
						out = append(out, fmt.Sprintf("%s has no **Verification:** line — name the command or deterministic check that exercises this task's changed artifacts; a green project build that ignores them is not verification", block.id))
					} else if taskVerificationCommand(block.body) == "" {
						out = append(out, fmt.Sprintf("%s **Verification:** must put the executable command in backticks; prose is never executed", block.id))
					}
					if !taskHasField(block.body, "Consumes") {
						out = append(out, fmt.Sprintf("%s has no **Consumes:** line — name prerequisite artifacts/capabilities, or write none", block.id))
					}
					produces := taskFieldItems(block.body, "Produces")
					exercises := taskFieldItems(block.body, "Exercises")
					if len(produces) == 0 {
						out = append(out, fmt.Sprintf("%s has no **Produces:** artifacts — name the paths, build targets, or capabilities this task creates", block.id))
					}
					if len(exercises) == 0 {
						out = append(out, fmt.Sprintf("%s has no **Exercises:** artifacts — name which Produced artifacts its Verification actually exercises", block.id))
					} else if len(produces) > 0 && !itemsOverlap(produces, exercises) {
						out = append(out, fmt.Sprintf("%s **Exercises:** none of its **Produces:** artifacts — its verification can be green without checking this task's delta", block.id))
					}
				}
			}
		}
		if small {
			out = append(out, taskGraphFindings(blocks)...)
		}
		if raw != "" {
			if doc, err := artifact.Parse(raw, artifact.KindPlan); err == nil {
				seenPair := map[string]bool{}
				for _, e := range artifact.LaneCollisions(doc) {
					key := e.ID + "|" + e.Detail
					if !seenPair[key] {
						seenPair[key] = true
						out = append(out, fmt.Sprintf("%s: lane collision — %s; make the **Owns:** lanes disjoint or drop them", e.ID, e.Detail))
					}
				}
			}
		}
	}
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

func taskFieldItems(body, name string) []string {
	re := regexp.MustCompile(`(?im)^\*\*` + regexp.QuoteMeta(name) + `:\*\*\s*(.+)$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, item := range strings.Split(m[1], ",") {
		item = strings.TrimSpace(strings.Trim(item, "`"))
		if item != "" && item != "none" && item != "-" {
			out = append(out, item)
		}
	}
	return out
}

func taskHasField(body, name string) bool {
	re := regexp.MustCompile(`(?im)^\*\*` + regexp.QuoteMeta(name) + `:\*\*`)
	return re.MatchString(body)
}

func taskVerificationCommand(body string) string {
	re := regexp.MustCompile("(?im)^\\*\\*Verification:\\*\\*\\s*`([^`]+)`")
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func itemsOverlap(a, b []string) bool {
	seen := map[string]bool{}
	for _, item := range a {
		seen[item] = true
	}
	for _, item := range b {
		if seen[item] {
			return true
		}
	}
	return false
}

func taskGraphFindings(blocks []taskBlock) []string {
	producer := map[string]string{}
	var out []string
	for _, block := range blocks {
		if !strings.HasPrefix(block.id, "T-") {
			continue
		}
		for _, item := range taskFieldItems(block.body, "Produces") {
			if prior := producer[item]; prior != "" && prior != block.id {
				out = append(out, fmt.Sprintf("%s and %s both **Produce:** %s — one artifact needs one owner", prior, block.id, item))
			} else {
				producer[item] = block.id
			}
		}
	}
	for _, block := range blocks {
		if !strings.HasPrefix(block.id, "T-") {
			continue
		}
		deps := map[string]bool{}
		for _, id := range taskFieldItems(block.body, "Depends on") {
			deps[id] = true
		}
		for _, item := range taskFieldItems(block.body, "Consumes") {
			if p := producer[item]; p != "" && p != block.id && !deps[p] {
				out = append(out, fmt.Sprintf("%s consumes %s produced by %s but has no **Depends on:** %s", block.id, item, p, p))
			}
		}
	}
	return out
}

// topLevelDeliverables counts the un-indented bullets under a task's
// **Deliverables:** heading, up to the next field or heading.
func topLevelDeliverables(body string) int {
	n := 0
	in := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(strings.TrimSpace(t), "**Deliverables:**"):
			in = true
			continue
		case in && (strings.HasPrefix(strings.TrimSpace(t), "**") || strings.HasPrefix(t, "#")):
			in = false
		}
		if in && (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) {
			n++
		}
	}
	return n
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

func structureProgressNote(findings, previous []string) string {
	open := map[string]bool{}
	for _, f := range findings {
		open[f] = true
	}
	var resolved []string
	for _, f := range previous {
		if !open[f] {
			resolved = append(resolved, f)
		}
	}
	note := structureNote(findings)
	if len(resolved) == 0 {
		return note
	}
	var b strings.Builder
	b.WriteString(note)
	b.WriteString("\n## Resolved since the previous attempt — do not reintroduce\n\n")
	for _, f := range resolved {
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
