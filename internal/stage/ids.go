// Package stage runs the lifecycle stages: intake, spec and plan.
//
// A stage is a conversation that produces an artifact proposal plus a human
// gate (05 §1.1). Steps after the conversation — parsing, id assignment,
// writing the proposal, the gate — are the orchestrator's, never a model's.
package stage

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
)

// AssignIDs reconciles the ids a model produced with the ids already in use.
//
// Models reliably start at 001 whatever they are told. Left alone, a second
// intake run would renumber REQ-004 as REQ-001 and every SPEC pointing at it
// would now point somewhere else — the traceability spine would be silently
// wrong rather than obviously broken.
//
// So: a section whose id already exists keeps it and updates in place; a
// section with a colliding-but-new title, or an id outside the existing set,
// is renumbered from the next free number. Existing ids are never reused for
// different content.
func AssignIDs(existing, produced []artifact.Section, prefix string) ([]artifact.Section, map[string]string) {
	byID := map[string]artifact.Section{}
	byTitle := map[string]string{} // normalised title -> existing id
	for _, s := range existing {
		byID[s.ID] = s
		byTitle[normaliseTitle(s.Title)] = s.ID
	}

	next := nextFree(existing, prefix)
	remap := map[string]string{}
	used := map[string]bool{}

	out := make([]artifact.Section, 0, len(produced))
	for _, s := range produced {
		final := ""

		switch {
		case byID[s.ID].ID != "" && !used[s.ID] && sameThing(byID[s.ID], s):
			// The model reused an id for what is recognisably the same item.
			final = s.ID
		default:
			// Prefer matching by title: a model that renumbered an existing
			// requirement should update it, not duplicate it.
			if id, ok := byTitle[normaliseTitle(s.Title)]; ok && !used[id] {
				final = id
			}
		}

		if final == "" {
			final = fmt.Sprintf("%s-%03d", prefix, next)
			next++
		}
		if final != s.ID {
			remap[s.ID] = final
		}
		used[final] = true
		s.ID = final
		out = append(out, s)
	}
	return out, remap
}

// sameThing reports whether a produced section is plausibly an update of an
// existing one rather than a different item wearing its id.
func sameThing(existing, produced artifact.Section) bool {
	if normaliseTitle(existing.Title) == normaliseTitle(produced.Title) {
		return true
	}
	// An id reused for a clearly different title is a collision, not an edit.
	return false
}

func normaliseTitle(t string) string {
	return strings.ToLower(strings.Join(strings.Fields(t), " "))
}

// NextFree returns the next unused number for a prefix.
func NextFree(sections []artifact.Section, prefix string) int {
	return nextFree(sections, prefix)
}

func nextFree(sections []artifact.Section, prefix string) int {
	max := 0
	for _, s := range sections {
		if n, ok := idNumber(s.ID, prefix); ok && n > max {
			max = n
		}
		for _, c := range s.Children {
			if n, ok := idNumber(c.ID, prefix); ok && n > max {
				max = n
			}
		}
	}
	return max + 1
}

func idNumber(id, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(id, prefix+"-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// RewriteReferences applies a remap to every Implements edge, so renumbering a
// section does not orphan the things that point at it.
func RewriteReferences(sections []artifact.Section, remap map[string]string) []artifact.Section {
	if len(remap) == 0 {
		return sections
	}
	out := make([]artifact.Section, len(sections))
	copy(out, sections)
	for i := range out {
		out[i].Implements = remapIDs(out[i].Implements, remap)
		out[i].Body = remapBody(out[i].Body, remap)
		for j := range out[i].Children {
			out[i].Children[j].Implements = remapIDs(out[i].Children[j].Implements, remap)
			out[i].Children[j].Body = remapBody(out[i].Children[j].Body, remap)
		}
	}
	return out
}

func remapIDs(ids []string, remap map[string]string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if to, ok := remap[id]; ok {
			out[i] = to
		} else {
			out[i] = id
		}
	}
	return out
}

// remapBody rewrites ids in the prose too, so the rendered document does not
// contradict its own edges.
func remapBody(body string, remap map[string]string) string {
	if body == "" || len(remap) == 0 {
		return body
	}
	// Match all ids in one pass so a replacement cannot be rewritten again.
	// Longest ids first also keeps overlapping alternatives unambiguous.
	froms := make([]string, 0, len(remap))
	for from := range remap {
		froms = append(froms, from)
	}
	sort.Slice(froms, func(i, j int) bool { return len(froms[i]) > len(froms[j]) })
	patterns := make([]string, len(froms))
	for i, from := range froms {
		patterns[i] = regexp.QuoteMeta(from)
	}
	re := regexp.MustCompile(`\b(?:` + strings.Join(patterns, "|") + `)\b`)
	return re.ReplaceAllStringFunc(body, func(id string) string {
		if to, ok := remap[id]; ok {
			return to
		}
		return id
	})
}

// PlanTaskIDs assigns task ids across a plan's milestones, since tasks are
// numbered project-wide rather than per milestone.
func PlanTaskIDs(existing, produced []artifact.Section) []artifact.Section {
	var existingTasks []artifact.Section
	for _, m := range existing {
		existingTasks = append(existingTasks, m.Children...)
	}
	next := nextFree(existingTasks, "T")

	byTitle := map[string]string{}
	for _, t := range existingTasks {
		byTitle[normaliseTitle(t.Title)] = t.ID
	}
	used := map[string]bool{}

	out := make([]artifact.Section, len(produced))
	copy(out, produced)
	// The architect numbers its own tasks and we renumber them, so a task it
	// wrote as T-005 may land as T-007. Any "Depends on: T-005" it also wrote
	// would then name a different task, or none. Remember what moved.
	renamed := map[string]string{}
	for i := range out {
		children := make([]artifact.Section, len(out[i].Children))
		copy(children, out[i].Children)
		for j := range children {
			id := ""
			if existingID, ok := byTitle[normaliseTitle(children[j].Title)]; ok && !used[existingID] {
				id = existingID
			} else {
				id = fmt.Sprintf("T-%03d", next)
				next++
			}
			used[id] = true
			if old := children[j].ID; old != "" && old != id {
				renamed[old] = id
			}
			children[j].ID = id
		}
		out[i].Children = children
	}
	if len(renamed) > 0 {
		for i := range out {
			for j := range out[i].Children {
				out[i].Children[j].Body = remapBody(out[i].Children[j].Body, renamed)
				remapDeps(&out[i].Children[j], renamed)
			}
		}
	}
	return out
}

// remapDeps rewrites a task's "Depends on" edge after renumbering, in both the
// parsed field and the body text the field was read from — the body is what
// gets written back to plan.md, so changing only one of them would produce a
// document that disagrees with itself.
func remapDeps(t *artifact.Section, renamed map[string]string) {
	dep := t.Field("depends on")
	if dep == "" {
		return
	}
	updated := taskIDRe.ReplaceAllStringFunc(dep, func(id string) string {
		if to, ok := renamed[id]; ok {
			return to
		}
		return id
	})
	if updated == dep {
		return
	}
	t.Fields["depends on"] = updated
	t.Body = strings.ReplaceAll(t.Body, "**Depends on:** "+dep, "**Depends on:** "+updated)
}

var taskIDRe = regexp.MustCompile(`T-\d+`)
