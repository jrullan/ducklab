package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// Proposal suffix. A stage writes here first and the artifact is only replaced
// when a human accepts (05 §1.1): a model must never overwrite an approved
// document just by producing text.
const proposedSuffix = ".proposed"

// DocsDir is where artifacts live.
func DocsDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".ducklab", "docs")
}

// Path is an artifact's committed path.
func Path(projectRoot string, kind Kind) string {
	return filepath.Join(DocsDir(projectRoot), kind.Filename())
}

// ProposedPath is where a stage writes before the human gate.
func ProposedPath(projectRoot string, kind Kind) string {
	return Path(projectRoot, kind) + proposedSuffix
}

// Load reads the committed artifact. A missing artifact is an empty document,
// not an error: every project starts without one.
func Load(projectRoot string, kind Kind) (*Document, error) {
	data, err := os.ReadFile(Path(projectRoot, kind))
	if err != nil {
		if os.IsNotExist(err) {
			return &Document{Front: Frontmatter{Kind: kind}}, nil
		}
		return nil, err
	}
	return Parse(string(data), kind)
}

// LoadProposed reads a pending proposal, or nil if there is none.
func LoadProposed(projectRoot string, kind Kind) (*Document, error) {
	data, err := os.ReadFile(ProposedPath(projectRoot, kind))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return Parse(string(data), kind)
}

// WriteProposal stores a stage's output next to the artifact without replacing
// it. The human sees a diff and decides.
func WriteProposal(projectRoot string, kind Kind, doc *Document, runID string, ducklings []string) error {
	current, err := Load(projectRoot, kind)
	if err != nil {
		return err
	}
	doc.Front.Kind = kind
	doc.Front.Version = current.Front.Version + 1
	doc.Front.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	doc.Front.RunID = runID
	doc.Front.Ducklings = ducklings
	// A proposal is never pre-approved. Approval is the human's act.
	doc.Front.ApprovedBy = ""

	if err := os.MkdirAll(DocsDir(projectRoot), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(ProposedPath(projectRoot, kind), []byte(Render(doc)), 0o644)
}

// Promote replaces the artifact with its proposal and records who approved it.
func Promote(projectRoot string, kind Kind, approvedBy string) (*Document, error) {
	doc, err := LoadProposed(projectRoot, kind)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("no proposal pending for %s", kind)
	}
	if approvedBy == "" {
		approvedBy = "human"
	}
	doc.Front.ApprovedBy = approvedBy
	doc.Front.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := xplat.AtomicWrite(Path(projectRoot, kind), []byte(Render(doc)), 0o644); err != nil {
		return nil, err
	}
	// The proposal is consumed, not kept: leaving it would make the next
	// `status` report a pending decision that has already been made.
	_ = os.Remove(ProposedPath(projectRoot, kind))
	return doc, nil
}

// DiscardProposal drops a rejected proposal.
//
// The file is KEPT by default (05 §1.1 step 8: "on reject, the proposal is
// kept"), so a rejected draft is not lost work. This exists for the explicit
// discard.
func DiscardProposal(projectRoot string, kind Kind) error {
	err := os.Remove(ProposedPath(projectRoot, kind))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Diff renders a line diff between the committed artifact and its proposal, so
// a human can see exactly what a stage wants to change.
func Diff(projectRoot string, kind Kind) (string, error) {
	current, err := Load(projectRoot, kind)
	if err != nil {
		return "", err
	}
	proposed, err := LoadProposed(projectRoot, kind)
	if err != nil {
		return "", err
	}
	if proposed == nil {
		return "", nil
	}
	return LineDiff(current.Raw, proposed.Raw), nil
}

// LineDiff is a minimal unified-style diff.
//
// Deliberately simple: this is read by a person deciding whether to accept a
// document, not applied by a machine, so a longest-common-subsequence
// implementation would add risk for no gain.
func LineDiff(before, after string) string {
	a := strings.Split(strings.TrimRight(before, "\n"), "\n")
	b := strings.Split(strings.TrimRight(after, "\n"), "\n")

	// Common prefix and suffix, then everything between is the change.
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}

	var out strings.Builder
	if start == endA && start == endB {
		return ""
	}
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", start+1, endA-start, start+1, endB-start)
	for _, line := range a[start:endA] {
		fmt.Fprintf(&out, "-%s\n", line)
	}
	for _, line := range b[start:endB] {
		fmt.Fprintf(&out, "+%s\n", line)
	}
	return out.String()
}
