// Package review renders the review stage's record: what a reviewer said
// about one task's diff (05 §1).
//
// A review is a record, not a proposal. The artifact stages write a `.proposed`
// file because they want to change the project's own description of itself and
// a person must agree first. A review changes nothing — it reports a reading —
// so it is written where it belongs and the human gate is about what to do
// next, not about whether the reading may be filed.
package review

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
)

// Dir is where reviews live, relative to the project root (02 §2).
func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, ".ducklab", "docs", "reviews")
}

// Path is the review document for one task.
func Path(projectRoot, taskID string) string {
	return filepath.Join(Dir(projectRoot), taskID+".md")
}

// Record is everything a rendered review needs.
type Record struct {
	TaskID    string
	Title     string
	Verdict   *agent.Verdict
	Ducklings []string
	RunID     string
	CommitSHA string
	// Mode is solo or council: which shape of review produced this.
	Mode string
	// At is the review time. Injected rather than read from the clock so the
	// rendering is a pure function and its test does not depend on today.
	At time.Time
}

// Render writes the review as markdown.
//
// Findings are ordered by severity and then by file, so the reader meets the
// critical ones first regardless of the order a model happened to emit them.
// The order a model chose is not information.
func Render(r Record) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("kind: review\n")
	fmt.Fprintf(&b, "task: %s\n", r.TaskID)
	fmt.Fprintf(&b, "verdict: %s\n", verdictWord(r.Verdict))
	if r.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", r.RunID)
	}
	if r.CommitSHA != "" {
		fmt.Fprintf(&b, "commit: %s\n", r.CommitSHA)
	}
	if r.Mode != "" {
		fmt.Fprintf(&b, "mode: %s\n", r.Mode)
	}
	if len(r.Ducklings) > 0 {
		fmt.Fprintf(&b, "ducklings: [%s]\n", strings.Join(r.Ducklings, ", "))
	}
	fmt.Fprintf(&b, "reviewed_at: %s\n", r.At.UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")

	title := r.Title
	if title == "" {
		title = r.TaskID
	}
	fmt.Fprintf(&b, "# Review — %s\n\n", title)

	findings := sortedFindings(r.Verdict)
	if len(findings) == 0 {
		if verdictWord(r.Verdict) == "request-changes" {
			// Blocking with nothing to fix is a reviewer failing its job, and
			// the record should not make it look like a considered rejection.
			b.WriteString("Changes were requested with no findings given.\n")
		} else {
			b.WriteString("No findings.\n")
		}
		return b.String()
	}

	for _, f := range findings {
		fmt.Fprintf(&b, "## %s — %s\n\n", strings.ToUpper(f.Severity), f.Issue)
		if f.File != "" {
			where := f.File
			if f.Line > 0 {
				where = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Fprintf(&b, "**Where:** `%s`\n\n", where)
		}
		if strings.TrimSpace(f.Fix) != "" {
			fmt.Fprintf(&b, "**Suggested fix:** %s\n\n", strings.TrimSpace(f.Fix))
		}
	}
	return b.String()
}

func verdictWord(v *agent.Verdict) string {
	if v == nil || v.Verdict == "" {
		return "unreviewed"
	}
	return v.Verdict
}

var severityRank = map[string]int{"critical": 0, "major": 1, "minor": 2}

func sortedFindings(v *agent.Verdict) []agent.Finding {
	if v == nil {
		return nil
	}
	out := make([]agent.Finding, len(v.Findings))
	copy(out, v.Findings)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Severity), rank(out[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return out[i].File < out[j].File
	})
	return out
}

func rank(sev string) int {
	if r, ok := severityRank[strings.ToLower(sev)]; ok {
		return r
	}
	// An unknown severity sorts last rather than first: inventing urgency for
	// a word we do not recognise would push real critical findings down.
	return len(severityRank)
}
