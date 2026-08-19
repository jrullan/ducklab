package stage

// The council's final document is the FOLD of the architect's passes, never
// the last reply alone. A spec architect drafted 15 sections in round one;
// its round-two reply carried only the 2 it retouched after critique — and
// taking that reply wholesale proposed a 2-section document with 13 sections
// silently gone, while the transcript below the gate still showed all 15
// (B-089). A later pass replaces the sections it names and appends the ones
// it adds; a section it does not mention survives. Removing a section is a
// person's decision — the same rule the approved-document guard enforces one
// level up.

import (
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
)

// FoldPasses folds the architect's per-round texts into one document and
// names the section ids the final pass omitted but the fold kept.
func FoldPasses(texts []string, kind artifact.Kind) (string, []string) {
	type pass struct {
		doc *artifact.Document
		raw string
	}
	var passes []pass
	for _, t := range texts {
		if strings.TrimSpace(t) == "" {
			continue
		}
		doc, err := artifact.Parse(t, kind)
		if err != nil || len(doc.Sections) == 0 {
			continue
		}
		passes = append(passes, pass{doc: doc, raw: t})
	}
	if len(passes) == 0 {
		if len(texts) == 0 {
			return "", nil
		}
		return texts[len(texts)-1], nil
	}
	if len(passes) == 1 {
		return passes[0].raw, nil
	}

	// New sections all wear the same placeholder id (SPEC-900, T-900 …)
	// until AssignIDs renumbers, so an id-only key made the fold blind: a
	// final pass carrying ONE placeholder section looked like it re-emitted
	// them all, and four verified additions vanished (B-090). A duplicated
	// id folds by id+title instead.
	placeholder := map[string]bool{}
	for _, p := range passes {
		within := map[string]int{}
		for _, sec := range p.doc.Sections {
			within[sec.ID]++
			if within[sec.ID] > 1 {
				placeholder[sec.ID] = true
			}
		}
	}
	key := func(sec artifact.Section) string {
		if placeholder[sec.ID] {
			return sec.ID + "|" + strings.ToLower(strings.TrimSpace(sec.Title))
		}
		return sec.ID
	}

	folded := append([]artifact.Section(nil), passes[0].doc.Sections...)
	index := map[string]int{}
	for i, sec := range folded {
		index[key(sec)] = i
	}
	for _, p := range passes[1:] {
		for _, sec := range p.doc.Sections {
			if i, ok := index[key(sec)]; ok {
				folded[i] = sec
			} else {
				index[key(sec)] = len(folded)
				folded = append(folded, sec)
			}
		}
	}

	final := passes[len(passes)-1].doc
	inFinal := map[string]bool{}
	for _, sec := range final.Sections {
		inFinal[key(sec)] = true
	}
	var kept []string
	for _, sec := range folded {
		if !inFinal[key(sec)] {
			kept = append(kept, sec.ID+" — "+sec.Title)
		}
	}

	var b strings.Builder
	for _, s := range folded {
		fmt.Fprintf(&b, "## %s — %s\n\n", s.ID, s.Title)
		if s.Body != "" {
			b.WriteString(s.Body)
			b.WriteString("\n\n")
		}
		for _, c := range s.Children {
			fmt.Fprintf(&b, "### %s — %s\n\n", c.ID, c.Title)
			if c.Body != "" {
				b.WriteString(c.Body)
				b.WriteString("\n\n")
			}
		}
	}
	return b.String(), kept
}
