package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Every door an architect writes tasks through dictates the body shape whose
// top-level bullets become the implementer's numbered contract. T-136 was
// born as one paragraph from plan-amend — one deliverable, itself.
func TestEveryTaskWritingPromptDictatesTheBodyContract(t *testing.T) {
	if !strings.Contains(planInstruction, "**Deliverables:**") {
		t.Error("the plan stage instruction does not dictate Deliverables")
	}
	plan := &artifact.Document{Sections: []artifact.Section{{ID: "M-001", Title: "Core"}}}
	extend, err := buildExtendPrompt(t.TempDir(), plan, "add camera capture")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extend, "**Deliverables:**") {
		t.Errorf("plan-extend prompt does not dictate Deliverables:\n%s", extend)
	}
}

// The contract's own example must parse to the numbered list it promises —
// otherwise the model is taught a shape the harness cannot read.
func TestTheContractExampleParsesAsDeliverables(t *testing.T) {
	body := "Achieves X because Y.\n\n" +
		"**Deliverables:**\n" +
		"- Add the file input to MeasurementConfigModal\n" +
		"  - accept=\"image/*\", capture for mobile camera\n" +
		"- Post the file as multipart/form-data to POST /api/user-exercises/:id/image\n" +
		"- Tests asserting the upload path and the failure path\n\n" +
		"**Out of scope:** cropping, progress indicators.\n\n" +
		"**Assumption:** T-135 returns the resolved image url.\n"
	got := strategy.ExtractDeliverables("Add controls", body)
	if len(got) != 3 || !strings.HasPrefix(got[0], "Add the file input") || !strings.HasPrefix(got[2], "Tests asserting") {
		t.Errorf("contract example extracts to %q", got)
	}
}

// An amendment's own dependencies survive the merge: placeholder ids in
// **Depends on:** become the real ids the tasks were given. The first
// amendment under the new contract said "Depends on: T-900" and would have
// shipped two tasks blocked on a task that never existed.
func TestMergeRewritesPlaceholderDependencies(t *testing.T) {
	current := &artifact.Document{Sections: []artifact.Section{
		{ID: "M-001", Title: "Core", Children: []artifact.Section{{ID: "T-001", Title: "Old"}}},
	}}
	frag := []artifact.Section{
		{ID: "T-900", Title: "Model", Body: "Do the model."},
		{ID: "T-901", Title: "Board", Body: "**Depends on:** T-900\n\nDo the board.", Fields: map[string]string{"depends on": "T-900"}},
		{ID: "T-902", Title: "MCP", Body: "**Depends on:** T-900, T-901\n\nDo the tool.", Fields: map[string]string{"depends on": "T-900, T-901"}},
	}
	out := mergeExtension(current, frag)
	kids := out.Sections[0].Children
	if len(kids) != 4 {
		t.Fatalf("got %d tasks, want 4", len(kids))
	}
	if kids[1].ID != "T-002" || kids[2].ID != "T-003" || kids[3].ID != "T-004" {
		t.Fatalf("ids: %s %s %s", kids[1].ID, kids[2].ID, kids[3].ID)
	}
	if !strings.Contains(kids[2].Body, "**Depends on:** T-002") || strings.Contains(kids[2].Body, "T-900") {
		t.Errorf("board's dependency not rewritten:\n%s", kids[2].Body)
	}
	if !strings.Contains(kids[3].Body, "**Depends on:** T-002, T-003") {
		t.Errorf("mcp's dependencies not rewritten:\n%s", kids[3].Body)
	}
	// Fields are re-derived when the proposal is re-parsed from markdown; the
	// body line is the truth the parser reads.
}

// The old contract repeated T-900 for every task; a later task saying
// "Depends on: T-900" means the first one, and never itself.
func TestMergeRepeatedPlaceholderMeansTheFirstTask(t *testing.T) {
	current := &artifact.Document{Sections: []artifact.Section{{ID: "M-001", Title: "Core"}}}
	frag := []artifact.Section{
		{ID: "T-900", Title: "A", Body: "a"},
		{ID: "T-900", Title: "B", Body: "**Depends on:** T-900\n\nb"},
	}
	out := mergeExtension(current, frag)
	kids := out.Sections[0].Children
	if !strings.Contains(kids[1].Body, "**Depends on:** T-001") {
		t.Errorf("repeated placeholder did not resolve to the first task:\n%s", kids[1].Body)
	}
	if strings.Contains(kids[0].Body, "Depends on") {
		t.Errorf("first task grew a dependency: %s", kids[0].Body)
	}
}
