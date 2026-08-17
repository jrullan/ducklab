package strategy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// What a reviewer may remember between rounds.
//
// The reviewer must not read the implementer's reasoning (I7) — that is what
// makes a second model worth having — but nothing forbids it remembering its
// OWN work. Each turn is a fresh conversation, so round 2's reviewer re-read
// every file round 1's reviewer had read: 20 calls and 800k tokens of
// research repeated to check whether three findings were addressed. Two facts
// the harness already holds cut that: which files' hunks changed since the
// last review, and which files the reviewer already looked at.

// reviewMemory is carried from one round's reviewer turn to the next.
type reviewMemory struct {
	// diff is the change the reviewer judged last round.
	diff string
	// looked is what the reviewer read or searched last round: tool + target.
	looked []string
}

// rememberReview records a finished reviewer turn.
func rememberReview(diff string, outcome *agent.Outcome) *reviewMemory {
	m := &reviewMemory{diff: diff}
	if outcome == nil {
		return m
	}
	seen := map[string]bool{}
	for _, c := range outcome.ToolCalls {
		if c.Result == nil || c.Result.IsError {
			continue
		}
		switch c.Name {
		case "fs_read", "fs_search", "fs_list", "artifact_read", "task_read", "git_diff", "git_log":
		default:
			continue
		}
		line := c.Name
		if d := argDigest(c.Args); d != "" {
			line += " " + d
		}
		if !seen[line] {
			seen[line] = true
			m.looked = append(m.looked, line)
		}
	}
	return m
}

// diffByFile splits a unified diff into per-file bodies keyed by path.
func diffByFile(diff string) map[string]string {
	out := map[string]string{}
	var cur string
	var b strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = ""
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				cur = strings.TrimPrefix(parts[3], "b/")
			}
			continue
		}
		if cur != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()
	return out
}

// sinceLastReview renders, for the reviewer, what moved between the diff it
// judged and the diff in front of it now, plus what it already looked at.
// Empty when there is no previous review.
func sinceLastReview(prev *reviewMemory, diff string) string {
	if prev == nil {
		return ""
	}
	before, after := diffByFile(prev.diff), diffByFile(diff)
	var changed, unchanged, gone []string
	for path, body := range after {
		if old, ok := before[path]; !ok || old != body {
			changed = append(changed, path)
		} else {
			unchanged = append(unchanged, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			gone = append(gone, path)
		}
	}
	sort.Strings(changed)
	sort.Strings(unchanged)
	sort.Strings(gone)

	var b strings.Builder
	b.WriteString("## Since your last review\n\n")
	b.WriteString("You reviewed an earlier version of this change; your findings are in the conversation below. " +
		"Verify that they were addressed and that what moved is sound. Files whose hunks are byte-identical to what you already judged need no second reading.\n\n")
	if len(changed) > 0 {
		b.WriteString("Hunks changed since your review: " + strings.Join(changed, ", ") + "\n")
	} else {
		b.WriteString("Hunks changed since your review: none — the diff is identical to what you judged.\n")
	}
	if len(unchanged) > 0 {
		b.WriteString("Unchanged since your review: " + strings.Join(unchanged, ", ") + "\n")
	}
	if len(gone) > 0 {
		b.WriteString("No longer in the diff: " + strings.Join(gone, ", ") + "\n")
	}
	if len(prev.looked) > 0 {
		b.WriteString("\nWhat you already read last round (no need to read it again unless it changed):\n")
		max := 40
		for i, l := range prev.looked {
			if i == max {
				fmt.Fprintf(&b, "- … and %d more\n", len(prev.looked)-max)
				break
			}
			b.WriteString("- " + l + "\n")
		}
	}
	return b.String()
}
