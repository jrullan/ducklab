package service

import (
	"os"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/release"
)

// The scribe got forty-five titles and a four-call budget, and did the
// diligent thing: artifact_read on every task, one per call, until the
// budget ran out with no notes written. The prompt now carries each task's
// own summary and says so; the model has nothing to go and fetch.
func TestScribePromptCarriesEachTasksSummary(t *testing.T) {
	v, _ := release.ParseVersion("v0.6.0")
	notes := release.Notes{Version: v, Since: "v0.5.0", Milestones: []release.Milestone{{ID: "M-1", Items: []release.Item{
		{TaskID: "T-040", Title: "Roster board", Summary: "A board where seats are assigned per mode by dragging from the flock."},
		{TaskID: "T-041", Title: "No summary"},
	}}}}
	p := scribePrompt(notes)
	if !strings.Contains(p, "- T-040: Roster board — A board where seats are assigned per mode by dragging from the flock.") {
		t.Errorf("summary missing from the prompt:\n%s", p)
	}
	if !strings.Contains(p, "- T-041: No summary\n") {
		t.Errorf("an item without a summary must still be listed:\n%s", p)
	}
	if !strings.Contains(p, "Do not read the tasks one by one") {
		t.Errorf("the prompt does not tell the scribe it already has what it needs")
	}
}

func TestFirstParagraphIsTheOpeningNotTheDeliverables(t *testing.T) {
	body := "# T-040 — Roster board\n\nA board where seats are assigned per mode by dragging from the flock. Global and project scope.\n\nDeliverables:\n- one\n- two\n"
	if got := firstParagraph(body, 280); got != "A board where seats are assigned per mode by dragging from the flock. Global and project scope." {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("word ", 100)
	if got := firstParagraph(long, 50); len(got) > 60 || !strings.HasSuffix(got, "…") {
		t.Errorf("not trimmed: %q", got)
	}
	if firstParagraph("", 10) != "" {
		t.Error("empty body must give an empty summary")
	}
}

// The release cycle had no "almost": the person could accept the scribe's
// draft or abort it. Revision is the same door every document stage has —
// the note and the draft it is about reach the scribe, the rewrite targets
// the DRAFT's version whatever bump made it, and the superseded run's gate
// does not linger.
func TestReleaseReviseCarriesTheNoteAndTheDraft(t *testing.T) {
	addendum := revisionAddendum("Say what the roster is FOR in the first line.", "---\nkind: release\n---\n\n# v0.7.0\n\nold prose")
	if !strings.Contains(addendum, "## Revision requested") ||
		!strings.Contains(addendum, "Say what the roster is FOR") ||
		!strings.Contains(addendum, "old prose") ||
		!strings.Contains(addendum, "Keep what the note does not touch") {
		t.Errorf("addendum:\n%s", addendum)
	}
	if revisionAddendum("", "draft") != "" {
		t.Error("no note must mean no addendum")
	}
}

func TestNewestProposedFindsTheDraftBeingRevised(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(release.Dir(dir), 0o755)
	os.WriteFile(release.Dir(dir)+"/v0.6.0.md", []byte("cut"), 0o644)
	os.WriteFile(release.Dir(dir)+"/v0.6.1.md.proposed", []byte("draft"), 0o644)
	os.WriteFile(release.Dir(dir)+"/v0.7.0.md.proposed", []byte("draft"), 0o644)
	v, ok := newestProposed(dir)
	if !ok || v.String() != "v0.7.0" {
		t.Fatalf("newestProposed = %v %v, want v0.7.0", v, ok)
	}
	if _, ok := newestProposed(t.TempDir()); ok {
		t.Error("an empty dir invented a draft")
	}
}
