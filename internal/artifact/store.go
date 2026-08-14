package artifact

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// The photograph is stamped with what it is a photograph OF, so promotion
	// can tell whether the approved document moved while the proposal waited.
	doc.Front.BasedOn = ContentHash(current.Raw)
	// A proposal is never pre-approved. Approval is the human's act.
	doc.Front.ApprovedBy = ""

	if err := os.MkdirAll(DocsDir(projectRoot), 0o755); err != nil {
		return err
	}
	return xplat.AtomicWrite(ProposedPath(projectRoot, kind), []byte(Render(doc)), 0o644)
}

// ErrProposalStale marks a promotion refused because the approved document
// changed after the proposal was drafted. Callers that treat other promotion
// errors as warnings must NOT downgrade this one: accepting through it would
// silently erase the interleaved edits.
var ErrProposalStale = errors.New("the approved document changed after this proposal was drafted")

// Promote replaces the artifact with its proposal and records who approved it.
//
// Refused when the approved document is not the one the proposal was drafted
// against. A proposal is a frozen photograph, and promotion writes it over the
// document WHOLESALE — so a task removed, or a bug promotion appending one,
// while the proposal sat at the gate would be erased without anyone being
// told. The two bug-promoted tasks of one real morning were added 52 minutes
// after a plan proposal was accepted; with the order reversed they would
// simply have vanished.
func Promote(projectRoot string, kind Kind, approvedBy string) (*Document, error) {
	doc, err := LoadProposed(projectRoot, kind)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("no proposal pending for %s", kind)
	}
	if doc.Front.BasedOn != "" {
		current, cErr := Load(projectRoot, kind)
		if cErr != nil {
			return nil, cErr
		}
		if got := ContentHash(current.Raw); got != doc.Front.BasedOn {
			detail := "its edits would be overwritten"
			if lost := sectionIDs(current).minus(sectionIDs(doc)); len(lost) > 0 {
				detail = fmt.Sprintf("accepting would erase %s", strings.Join(lost, ", "))
			}
			return nil, fmt.Errorf("%w: %s. Reject this proposal and redraft, so the new draft starts from today's document", ErrProposalStale, detail)
		}
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
	// Section-aware when both sides have structure: a fragment update
	// replaces mid-document and appends at the end, and the prefix/suffix
	// LineDiff — fooled first by the frontmatter, which ALWAYS differs —
	// showed a whole-document replacement over four added sections. The
	// person deciding reads sections; the diff speaks in them.
	if len(current.Sections) > 0 && len(proposed.Sections) > 0 {
		if d := SectionDiff(current, proposed); d != "" || current.Raw == proposed.Raw {
			return d, nil
		}
	}
	return LineDiff(current.Raw, proposed.Raw), nil
}

type diffUnit struct {
	ID    string
	Title string
	Text  string
}

func flattenUnits(doc *Document) []diffUnit {
	var out []diffUnit
	for _, sec := range doc.Sections {
		out = append(out, diffUnit{ID: sec.ID, Title: sec.Title, Text: sec.Body})
		for _, c := range sec.Children {
			out = append(out, diffUnit{ID: c.ID, Title: c.Title, Text: c.Body})
		}
	}
	return out
}

// SectionDiff renders one hunk per added, changed or removed section — the
// units a person deciding a document actually reads. Frontmatter is ignored:
// version and timestamps ALWAYS differ and say nothing about the content.
func SectionDiff(current, proposed *Document) string {
	cur := flattenUnits(current)
	prop := flattenUnits(proposed)
	curMap := map[string]diffUnit{}
	for _, u := range cur {
		curMap[u.ID] = u
	}
	propSeen := map[string]bool{}
	var out strings.Builder

	for _, u := range prop {
		propSeen[u.ID] = true
		before, existed := curMap[u.ID]
		switch {
		case !existed:
			fmt.Fprintf(&out, "@@ %s — %s (new) @@\n", u.ID, u.Title)
			for _, line := range strings.Split(strings.TrimRight(u.Text, "\n"), "\n") {
				fmt.Fprintf(&out, "+%s\n", line)
			}
		case before.Text != u.Text || before.Title != u.Title:
			fmt.Fprintf(&out, "@@ %s — %s @@\n", u.ID, u.Title)
			out.WriteString(lineDiffBody(before.Text, u.Text))
		}
	}
	for _, u := range cur {
		if !propSeen[u.ID] {
			fmt.Fprintf(&out, "@@ %s — %s (removed) @@\n", u.ID, u.Title)
			for _, line := range strings.Split(strings.TrimRight(u.Text, "\n"), "\n") {
				fmt.Fprintf(&out, "-%s\n", line)
			}
		}
	}
	return out.String()
}

// lineDiffBody is LineDiff's core without the hunk header: prefix and suffix
// trimmed, the middle as -/+ lines.
func lineDiffBody(before, after string) string {
	a := strings.Split(strings.TrimRight(before, "\n"), "\n")
	b := strings.Split(strings.TrimRight(after, "\n"), "\n")
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
	for _, line := range a[start:endA] {
		fmt.Fprintf(&out, "-%s\n", line)
	}
	for _, line := range b[start:endB] {
		fmt.Fprintf(&out, "+%s\n", line)
	}
	return out.String()
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

// ContentHash fingerprints a document's raw content, for based_on stamps.
func ContentHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:8])
}

type idSet map[string]bool

func sectionIDs(doc *Document) idSet {
	ids := idSet{}
	if doc == nil {
		return ids
	}
	for _, sec := range doc.Sections {
		ids[sec.ID] = true
		for _, c := range sec.Children {
			ids[c.ID] = true
		}
	}
	return ids
}

// minus returns the ids present here and absent there, sorted — the sections
// a stale promotion would erase.
func (a idSet) minus(b idSet) []string {
	var out []string
	for id := range a {
		if id != "" && !b[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
