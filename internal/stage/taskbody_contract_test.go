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
