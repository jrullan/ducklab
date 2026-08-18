package service

import (
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
