package artifact

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func emptyProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(DocsDir(root), 0o755)
	return root
}

func doc(sections ...Section) *Document {
	return &Document{Sections: sections}
}

// AC-38: a stage writes .proposed first and never touches the artifact.
func TestProposalDoesNotTouchTheArtifact(t *testing.T) {
	root := emptyProject(t)
	os.WriteFile(Path(root, KindSpec), []byte("## SPEC-001 — Original\n\nkeep me\n"), 0o644)

	err := WriteProposal(root, KindSpec, doc(Section{ID: "SPEC-001", Title: "Replaced"}), "r-1", []string{"pato"})
	if err != nil {
		t.Fatal(err)
	}

	current, _ := Load(root, KindSpec)
	if !strings.Contains(current.Raw, "keep me") {
		t.Error("the committed artifact was modified by a proposal")
	}
	proposed, _ := LoadProposed(root, KindSpec)
	if proposed == nil || proposed.Sections[0].Title != "Replaced" {
		t.Fatal("the proposal was not written")
	}
	// A proposal is never pre-approved: approval is the human's act.
	if proposed.Front.Approved() {
		t.Error("a proposal arrived pre-approved")
	}
	if proposed.Front.RunID != "r-1" {
		t.Errorf("run id not recorded: %q", proposed.Front.RunID)
	}
}

func TestPromoteReplacesAndRecordsApproval(t *testing.T) {
	root := emptyProject(t)
	os.WriteFile(Path(root, KindSpec), []byte("## SPEC-001 — Original\n"), 0o644)
	WriteProposal(root, KindSpec, doc(Section{ID: "SPEC-001", Title: "Approved version"}), "r-1", nil)

	got, err := Promote(root, KindSpec, "human")
	if err != nil {
		t.Fatal(err)
	}
	if got.Front.ApprovedBy != "human" {
		t.Errorf("approved_by = %q", got.Front.ApprovedBy)
	}

	current, _ := Load(root, KindSpec)
	if current.Sections[0].Title != "Approved version" {
		t.Error("promote did not replace the artifact")
	}
	// A consumed proposal must not linger, or status keeps reporting a
	// decision that has already been made.
	if _, err := os.Stat(ProposedPath(root, KindSpec)); !os.IsNotExist(err) {
		t.Error("the proposal survived promotion")
	}
}

// AC-38: rejecting leaves the artifact untouched and the proposal on disk.
func TestRejectingKeepsTheProposalOnDisk(t *testing.T) {
	root := emptyProject(t)
	os.WriteFile(Path(root, KindSpec), []byte("## SPEC-001 — Original\n\nkeep me\n"), 0o644)
	WriteProposal(root, KindSpec, doc(Section{ID: "SPEC-001", Title: "Rejected draft"}), "r-1", nil)

	// Rejecting is simply not promoting.
	current, _ := Load(root, KindSpec)
	if !strings.Contains(current.Raw, "keep me") {
		t.Error("artifact changed without promotion")
	}
	if _, err := os.Stat(ProposedPath(root, KindSpec)); err != nil {
		t.Error("the rejected draft was discarded; that is lost work")
	}
}

func TestPromoteWithNoProposalIsAnError(t *testing.T) {
	if _, err := Promote(emptyProject(t), KindSpec, "human"); err == nil {
		t.Error("promoted with nothing pending")
	}
}

func TestVersionIncrementsPerProposal(t *testing.T) {
	root := emptyProject(t)
	for want := 1; want <= 3; want++ {
		WriteProposal(root, KindSpec, doc(Section{ID: "SPEC-001", Title: "v"}), "r", nil)
		p, _ := LoadProposed(root, KindSpec)
		if p.Front.Version != want {
			t.Errorf("version = %d, want %d", p.Front.Version, want)
		}
		Promote(root, KindSpec, "human")
	}
}

func TestLoadMissingArtifactIsEmptyNotAnError(t *testing.T) {
	d, err := Load(t.TempDir(), KindRequirements)
	if err != nil {
		t.Fatalf("a project without requirements errored: %v", err)
	}
	if len(d.Sections) != 0 || d.Front.Kind != KindRequirements {
		t.Errorf("doc = %+v", d.Front)
	}
}

func TestDiffShowsWhatWouldChange(t *testing.T) {
	root := emptyProject(t)
	os.WriteFile(Path(root, KindSpec), []byte("line one\nline two\nline three\n"), 0o644)
	os.WriteFile(ProposedPath(root, KindSpec), []byte("line one\nline CHANGED\nline three\n"), 0o644)

	d, err := Diff(root, KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "-line two") || !strings.Contains(d, "+line CHANGED") {
		t.Errorf("diff does not show the change:\n%s", d)
	}
	if strings.Contains(d, "line one") {
		t.Errorf("diff includes unchanged context it should have trimmed:\n%s", d)
	}
}

func TestDiffWithNoProposalIsEmpty(t *testing.T) {
	d, err := Diff(emptyProject(t), KindSpec)
	if err != nil || d != "" {
		t.Errorf("diff = %q, err = %v", d, err)
	}
}

func TestIdenticalContentDiffsToNothing(t *testing.T) {
	if got := LineDiff("a\nb\n", "a\nb\n"); got != "" {
		t.Errorf("identical content produced a diff: %q", got)
	}
}

// A proposal is a frozen photograph, and promotion writes it over the approved
// document WHOLESALE. Two bug-promoted tasks were added 52 minutes after a
// plan proposal was accepted; with the order reversed they would simply have
// vanished, unannounced. Promotion now refuses when the document it would
// overwrite is not the one the proposal was drafted against.
func TestPromoteRefusesAProposalDraftedAgainstAnOlderDocument(t *testing.T) {
	root := t.TempDir()
	approved := &Document{
		Front:    Frontmatter{Kind: KindPlan, ApprovedBy: "human"},
		Sections: []Section{{ID: "M-001", Title: "First", Children: []Section{{ID: "T-001", Title: "Work"}}}},
	}
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root, KindPlan), []byte(Render(approved)), 0o644); err != nil {
		t.Fatal(err)
	}

	draft := &Document{Sections: []Section{{ID: "M-001", Title: "First", Children: []Section{
		{ID: "T-001", Title: "Work"}, {ID: "T-002", Title: "More"},
	}}}}
	if err := WriteProposal(root, KindPlan, draft, "r-1", nil); err != nil {
		t.Fatal(err)
	}

	// The approved document moves while the proposal waits: a bug promotion
	// appends T-048.
	moved, _ := Load(root, KindPlan)
	moved.Sections[0].Children = append(moved.Sections[0].Children, Section{ID: "T-048", Title: "Promoted"})
	if err := os.WriteFile(Path(root, KindPlan), []byte(Render(moved)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Promote(root, KindPlan, "human")
	if err == nil {
		t.Fatal("a stale proposal was promoted over interleaved edits")
	}
	if !errors.Is(err, ErrProposalStale) {
		t.Errorf("not the typed refusal: %v", err)
	}
	// It names what would be erased, so the person can decide with eyes open.
	if !strings.Contains(err.Error(), "T-048") {
		t.Errorf("the refusal does not name the section at stake: %v", err)
	}
	// The document and the proposal are both left standing.
	if after, _ := Load(root, KindPlan); !sectionIDs(after)["T-048"] {
		t.Error("the refusal still overwrote the document")
	}
	if p, _ := LoadProposed(root, KindPlan); p == nil {
		t.Error("the refusal consumed the proposal")
	}
}

// An untouched base promotes exactly as before.
func TestPromoteAcceptsWhenTheBaseIsUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	draft := &Document{Sections: []Section{{ID: "REQ-001", Title: "A"}}}
	if err := WriteProposal(root, KindRequirements, draft, "r-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(root, KindRequirements, "human"); err != nil {
		t.Fatalf("an honest promotion was refused: %v", err)
	}
}

// Proposals written before based_on existed carry no stamp and still promote:
// a guard that bricked every pending decision on upgrade would be worse than
// the race it closes.
func TestPromoteAcceptsAnOldProposalWithoutAStamp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	old := &Document{Front: Frontmatter{Kind: KindRequirements, Version: 2},
		Sections: []Section{{ID: "REQ-001", Title: "A"}}}
	if err := os.WriteFile(ProposedPath(root, KindRequirements), []byte(Render(old)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Promote(root, KindRequirements, "human"); err != nil {
		t.Fatalf("an unstamped proposal was refused: %v", err)
	}
}
