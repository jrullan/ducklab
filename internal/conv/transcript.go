package conv

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
)

// Entry is one recorded turn.
type Entry struct {
	Round    int
	Index    int
	Role     config.Role
	Duckling config.DucklingID
	Text     string
	// Label is the anonymous name ("A", "B", …) used when this entry is shown
	// to a role that must not know who wrote it (I7).
	Label string
}

// Transcript accumulates the turns of one conversation.
type Transcript struct {
	Entries []Entry
	labels  map[config.DucklingID]string
}

// Add records a turn.
func (t *Transcript) Add(e Entry) {
	t.Entries = append(t.Entries, e)
}

// Label returns the stable anonymous label for a duckling within this
// conversation, assigning the next free letter on first sight.
func (t *Transcript) Label(id config.DucklingID) string {
	if t.labels == nil {
		t.labels = map[config.DucklingID]string{}
	}
	if l, ok := t.labels[id]; ok {
		return l
	}
	l := string(rune('A' + len(t.labels)))
	t.labels[id] = l
	return l
}

// Render produces the "Conversation so far" block injected into a turn's
// prompt (04 §1.3).
//
// When anonymise is true this enforces I7: duckling ids are replaced by
// letters, and entries written by authorRole are omitted entirely — a reviewer
// that reads the author's rationalisation adopts it, which is exactly what
// makes a second model worth having.
func (t *Transcript) Render(anonymise bool, omitRole config.Role) string {
	var b strings.Builder
	b.WriteString("## Conversation so far\n")
	n := 0
	for _, e := range t.Entries {
		if anonymise && omitRole != "" && e.Role == omitRole {
			continue
		}
		n++
		who := string(e.Duckling)
		if anonymise {
			who = t.Label(e.Duckling)
		}
		fmt.Fprintf(&b, "\n### Turn %d — %s (%s)\n%s\n", n, e.Role, who, strings.TrimSpace(e.Text))
	}
	if n == 0 {
		return ""
	}
	return b.String()
}

// Candidate is one anonymised solution put in front of a judge.
type Candidate struct {
	Label    string
	Diff     string
	Gate     string // green | red | none
	GateLog  string
	Duckling config.DucklingID // never rendered; kept only for the run record
}

// AnonymiseCandidates assigns labels A, B, … to candidates.
//
// Labels are ordered by a hash of the diff, NOT by duckling order or
// completion time. If A were always the first-listed duckling, a judge would
// learn a positional convention across runs and the anonymity would be
// cosmetic (05 §4.3).
func AnonymiseCandidates(in []Candidate) []Candidate {
	out := make([]Candidate, len(in))
	copy(out, in)

	sort.SliceStable(out, func(i, j int) bool {
		return diffOrder(out[i].Diff) < diffOrder(out[j].Diff)
	})
	for i := range out {
		out[i].Label = string(rune('A' + i))
	}
	return out
}

func diffOrder(diff string) uint64 {
	sum := sha256.Sum256([]byte(diff))
	return binary.BigEndian.Uint64(sum[:8])
}

// RenderCandidates formats candidates for a judge turn. The duckling that
// wrote each one is never included.
func RenderCandidates(cands []Candidate) string {
	var b strings.Builder
	for _, c := range cands {
		fmt.Fprintf(&b, "## Candidate %s\n\nVerification: %s\n\n", c.Label, gateWord(c.Gate))
		if strings.TrimSpace(c.GateLog) != "" {
			fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(c.GateLog))
		}
		// Compacted like the reviewer's diff: a judge comparing candidates
		// drowns just as fast in a generated file's churn.
		fmt.Fprintf(&b, "```diff\n%s\n```\n\n", strings.TrimSpace(CompactDiff(c.Diff)))
	}
	return b.String()
}

func gateWord(gate string) string {
	switch gate {
	case "green":
		return "PASSED — the project's verification command exited 0"
	case "red":
		return "FAILED — the project's verification command exited non-zero"
	default:
		return "NOT RUN — there was nothing executable to verify with"
	}
}

// GreenCandidates returns only the candidates whose gate is green.
func GreenCandidates(cands []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cands {
		if c.Gate == "green" {
			out = append(out, c)
		}
	}
	return out
}

// FindCandidate returns the candidate with a label, or nil.
func FindCandidate(cands []Candidate, label string) *Candidate {
	for i := range cands {
		if cands[i].Label == label {
			return &cands[i]
		}
	}
	return nil
}

// RenderFindings formats a reviewer's findings for the next implementer turn
// (05 §4.2).
func RenderFindings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Review of your previous attempt\n")
	for _, f := range findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		if f.File == "*" || (loc == "" && strings.TrimSpace(f.Invariant) != "") {
			// A class-level finding: the rule is the location.
			loc = "everywhere the invariant applies"
		}
		fmt.Fprintf(&b, "- [%s] %s — %s", f.Severity, loc, f.Issue)
		if strings.TrimSpace(f.Fix) != "" {
			fmt.Fprintf(&b, " → %s", f.Fix)
		}
		if strings.TrimSpace(f.Invariant) != "" {
			fmt.Fprintf(&b, " (invariant: %s)", strings.TrimSpace(f.Invariant))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Finding mirrors agent.Finding. conv must not import agent (agent is the
// caller), so the shape is repeated here and converted at the boundary.
type Finding struct {
	Severity  string
	File      string
	Line      int
	Issue     string
	Fix       string
	Invariant string
}
