package service

import (
	"fmt"
	"sort"
	"strings"
)

// Scope references under the caps (Codex on #174). A scope reference is the
// person's explicit target; a cap that silently drops or cuts it recreates
// B-520 — the architect is told to build toward a reference it never got.
// So scope files load first, and whatever still does not arrive whole is
// said: in the prompt (no scope content is claimed that is not there), on
// the run's warning, and as a warning event.

// scopeFirst orders scope files ahead of background ones, each group keeping
// its sorted order. A run without scope references keeps its order exactly.
func scopeFirst(files []string, roleOf func(string) string) []string {
	out := append([]string(nil), files...)
	sort.SliceStable(out, func(i, j int) bool {
		return roleOf(out[i]) == RefRoleScope && roleOf(out[j]) != RefRoleScope
	})
	return out
}

// refScopeShortfall is one scope reference the stage did not receive whole.
type refScopeShortfall struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// scopeShortfall names the scope files that were dropped, cut or unreadable.
func scopeShortfall(files []string, roleOf func(string) string, loaded []refFile) []refScopeShortfall {
	byPath := make(map[string]refFile, len(loaded))
	for _, f := range loaded {
		byPath[f.Path] = f
	}
	var out []refScopeShortfall
	for _, f := range files {
		if roleOf(f) != RefRoleScope {
			continue
		}
		rf, ok := byPath[f]
		switch {
		case !ok:
			out = append(out, refScopeShortfall{f, "dropped: the reference caps (max_files / total_chars) were reached"})
		case rf.Unreadable != "":
			out = append(out, refScopeShortfall{f, "unreadable: the model saw only a notice"})
		case rf.Truncated:
			out = append(out, refScopeShortfall{f, "truncated at the reference budget"})
		}
	}
	return out
}

// renderScopeShortfallNote tells the stage which scope references it did not
// get whole, so a missing tail is never read as the reference omitting it.
func renderScopeShortfallNote(short []refScopeShortfall) string {
	if len(short) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n### Scope references not delivered in full\n\n" +
		"The person marked these as SCOPE, but they did not reach you whole. Do not treat what " +
		"is missing as the reference leaving that behavior out; say in your reply which scope " +
		"reference was incomplete.\n\n")
	for _, s := range short {
		fmt.Fprintf(&b, "- %s — %s\n", s.Path, s.Reason)
	}
	return b.String()
}

// warnScopeShortfall puts the shortfall on the run's warning and its events,
// where the person sees it before trusting the draft.
func warnScopeShortfall(rs *runState, short []refScopeShortfall) {
	if len(short) == 0 {
		return
	}
	parts := make([]string, 0, len(short))
	for _, s := range short {
		parts = append(parts, s.Path+" ("+s.Reason+")")
	}
	detail := fmt.Sprintf("%d scope reference(s) did not reach the stage in full — raise the reference caps or narrow the references: %s",
		len(short), strings.Join(parts, " | "))
	rs.wmu.Lock()
	if rs.run.Warning == "" {
		rs.run.Warning = detail
	} else {
		rs.run.Warning += " · " + detail
	}
	rs.wmu.Unlock()
	rs.writer.WriteState()
	rs.writer.AppendEvent("warning", map[string]interface{}{
		"detail": detail, "kind": "scope_reference_incomplete", "references": short,
	})
}
