package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func project(t *testing.T, files map[Kind]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	for kind, body := range files {
		if err := os.WriteFile(Path(root, kind), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const linkedReqs = "## REQ-001 — Login\n\n**Priority:** must\n"
const linkedSpec = "## SPEC-001 — Session tokens\n\n**Implements:** REQ-001\n"
const linkedPlan = "## M-01 — Auth\n\n### T-001 — Issue tokens\n\n**Implements:** SPEC-001\n"

func TestFullyLinkedSpineHasNoErrors(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs, KindSpec: linkedSpec, KindPlan: linkedPlan,
	})
	spine, err := LoadSpine(root)
	if err != nil {
		t.Fatal(err)
	}
	if errs := spine.Check(); len(errs) != 0 {
		t.Errorf("a fully linked cycle reported %v", errs)
	}
}

// AC-40: a `must` requirement with no spec section is named specifically.
func TestOrphanRequirementIsNamed(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs + "\n## REQ-002 — Audit log\n\n**Priority:** must\n",
		KindSpec:         linkedSpec, KindPlan: linkedPlan,
	})
	spine, _ := LoadSpine(root)
	errs := spine.Check()

	var found *TraceError
	for i := range errs {
		if errs[i].Kind == OrphanRequirement && errs[i].ID == "REQ-002" {
			found = &errs[i]
		}
	}
	if found == nil {
		t.Fatalf("orphan REQ-002 not reported: %v", errs)
	}
	if !strings.Contains(found.String(), "REQ-002") {
		t.Errorf("error does not name the id: %s", found)
	}
}

// A `could` with no spec is a decision, not a defect. Flagging it would train
// people to ignore the check.
func TestLowPriorityRequirementIsNotAnOrphan(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs + "\n## REQ-003 — Dark mode\n\n**Priority:** could\n",
		KindSpec:         linkedSpec, KindPlan: linkedPlan,
	})
	spine, _ := LoadSpine(root)
	for _, e := range spine.Check() {
		if e.ID == "REQ-003" {
			t.Errorf("a 'could' requirement was reported: %v", e)
		}
	}
}

func TestSpecWithNoRequirementIsReported(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs,
		KindSpec:         linkedSpec + "\n## SPEC-002 — Rate limiting\n\nNo implements line.\n",
		KindPlan:         linkedPlan,
	})
	spine, _ := LoadSpine(root)
	if !hasError(spine.Check(), UnimplementedSpec, "SPEC-002") {
		t.Errorf("a spec section with no requirement was not reported: %v", spine.Check())
	}
}

func TestTaskWithNoSpecIsReported(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs, KindSpec: linkedSpec,
		KindPlan: linkedPlan + "\n### T-002 — Mystery work\n\nNo implements.\n",
	})
	spine, _ := LoadSpine(root)
	if !hasError(spine.Check(), UnjustifiedTask, "T-002") {
		t.Errorf("an unjustified task was not reported: %v", spine.Check())
	}
}

// A reference to an id that does not exist is worse than a missing one: it
// looks linked.
func TestDanglingReferenceIsReported(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs,
		KindSpec:         "## SPEC-001 — X\n\n**Implements:** REQ-999\n",
		KindPlan:         linkedPlan,
	})
	spine, _ := LoadSpine(root)
	errs := spine.Check()
	if !hasError(errs, DanglingReference, "SPEC-001") {
		t.Fatalf("dangling reference not reported: %v", errs)
	}
	for _, e := range errs {
		if e.Kind == DanglingReference && e.Missing != "REQ-999" {
			t.Errorf("error does not name what is missing: %+v", e)
		}
	}
}

func TestSpecWithNoTaskIsReported(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs,
		KindSpec:         linkedSpec + "\n## SPEC-002 — Refresh tokens\n\n**Implements:** REQ-001\n",
		KindPlan:         linkedPlan,
	})
	spine, _ := LoadSpine(root)
	if !hasError(spine.Check(), UnimplementedSpec, "SPEC-002") {
		t.Errorf("a spec section with no task was not reported: %v", spine.Check())
	}
}

// An empty project is not broken, it is empty.
func TestEmptyProjectHasNoErrors(t *testing.T) {
	spine, err := LoadSpine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if errs := spine.Check(); len(errs) != 0 {
		t.Errorf("an empty project reported %v", errs)
	}
}

func TestWalkFollowsTheSpineBothWays(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: linkedReqs, KindSpec: linkedSpec, KindPlan: linkedPlan,
	})
	spine, _ := LoadSpine(root)

	req, err := spine.Walk("REQ-001")
	if err != nil {
		t.Fatal(err)
	}
	if req.Kind != "requirement" || len(req.Down) != 1 || req.Down[0] != "SPEC-001" {
		t.Errorf("requirement node = %+v", req)
	}

	sp, _ := spine.Walk("SPEC-001")
	if len(sp.Up) != 1 || sp.Up[0] != "REQ-001" {
		t.Errorf("spec up = %v", sp.Up)
	}
	if len(sp.Down) != 1 || sp.Down[0] != "T-001" {
		t.Errorf("spec down = %v", sp.Down)
	}

	task, _ := spine.Walk("T-001")
	if task.Kind != "task" || len(task.Up) != 1 {
		t.Errorf("task node = %+v", task)
	}
}

func TestWalkUnknownIDIsAnError(t *testing.T) {
	spine, _ := LoadSpine(t.TempDir())
	if _, err := spine.Walk("REQ-404"); err == nil {
		t.Error("walked an id that does not exist")
	}
}

// Errors must be ordered, or a check that reports the same breaks in a
// different order every run looks like it found different breaks.
func TestCheckIsDeterministic(t *testing.T) {
	root := project(t, map[Kind]string{
		KindRequirements: "## REQ-003 — C\n\n**Priority:** must\n## REQ-001 — A\n\n**Priority:** must\n## REQ-002 — B\n\n**Priority:** must\n",
		KindSpec:         "", KindPlan: "",
	})
	spine, _ := LoadSpine(root)
	first := spine.Check()
	for i := 0; i < 10; i++ {
		again := spine.Check()
		for j := range first {
			if again[j] != first[j] {
				t.Fatal("Check is not deterministic")
			}
		}
	}
	if len(first) != 3 || first[0].ID != "REQ-001" {
		t.Errorf("errors not sorted by id: %v", first)
	}
}

func hasError(errs []TraceError, kind TraceErrorKind, id string) bool {
	for _, e := range errs {
		if e.Kind == kind && e.ID == id {
			return true
		}
	}
	return false
}

var _ = filepath.Join
