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

func TestLaneCollisionAcrossMilestones(t *testing.T) {
	spine := planWith("**Owns:** internal/service/**\n\n### T-001 — First\n\n**Implements:** SPEC-001\n")
	spine.Plan.Sections = append(spine.Plan.Sections, Section{ID: "M-02", Owns: []string{"internal/service"}, Children: []Section{{ID: "T-002"}}})
	if !hasError(spine.Check(), LaneCollision, "M-01") {
		t.Fatal("overlapping lanes were not reported")
	}
}

func TestInheritedLaneIsNotSelfCollision(t *testing.T) {
	spine := planWith("**Owns:** internal/service\n\n### T-001 — First\n\n**Implements:** SPEC-001\n\n### T-002 — Second\n\n**Implements:** SPEC-001\n")
	if hasError(spine.Check(), LaneCollision, "M-01") {
		t.Fatal("milestone lane collided with inherited sibling tasks")
	}
}

var _ = filepath.Join

// A spec section that records what will NOT be built has nothing for a task to
// implement. Demanding one turns the check into noise, exactly as a `wont`
// requirement with no spec section would.
func TestNonNormativeSpecSectionNeedsNoTask(t *testing.T) {
	spine := &Spine{
		Requirements: &Document{Sections: []Section{{ID: "REQ-001", Title: "Out of scope"}}},
		Spec: &Document{Sections: []Section{
			{ID: "SPEC-001", Title: "Login", Implements: []string{"REQ-001"}},
			{ID: "SPEC-009", Title: "Out of Scope", Implements: []string{"REQ-001"},
				Fields: map[string]string{"priority": "wont"}},
		}},
		Plan: &Document{Sections: []Section{{ID: "M-01", Children: []Section{
			{ID: "T-001", Title: "Login", Implements: []string{"SPEC-001"}},
		}}}},
	}
	for _, e := range spine.Check() {
		if e.ID == "SPEC-009" {
			t.Errorf("SPEC-009 is marked wont and was still flagged: %s", e.Detail)
		}
	}
}

// The exemption keys on the marker, never on the title. A spine that inferred
// "non-normative" from prose a model happened to write would silently drop
// real gaps whenever a section was named unluckily.
func TestOutOfScopeTitleAloneDoesNotExempt(t *testing.T) {
	spine := &Spine{
		Requirements: &Document{Sections: []Section{{ID: "REQ-001", Title: "Scope"}}},
		Spec: &Document{Sections: []Section{
			{ID: "SPEC-009", Title: "Out of Scope", Implements: []string{"REQ-001"}},
		}},
		Plan: &Document{},
	}
	found := false
	for _, e := range spine.Check() {
		if e.ID == "SPEC-009" && e.Kind == UnimplementedSpec {
			found = true
		}
	}
	if !found {
		t.Error("an unmarked section was exempted on its title alone")
	}
}

func planWith(tasks string) *Spine {
	plan, _ := Parse("## M-01 — Work\n\n"+tasks, KindPlan)
	reqs, _ := Parse("## REQ-001 — A\n\n**Priority:** must\n", KindRequirements)
	spec, _ := Parse("## SPEC-001 — A\n\n**Implements:** REQ-001\n", KindSpec)
	return &Spine{Requirements: reqs, Spec: spec, Plan: plan}
}

func kinds(errs []TraceError) []TraceErrorKind {
	var out []TraceErrorKind
	for _, e := range errs {
		out = append(out, e.Kind)
	}
	return out
}

func hasKind(errs []TraceError, k TraceErrorKind) *TraceError {
	for i := range errs {
		if errs[i].Kind == k {
			return &errs[i]
		}
	}
	return nil
}

// Check walked the vertical spine only. The **Depends on:** field was parsed
// and the board reads it to decide what is blocked, but nothing ever looked at
// the graph it forms — so a plan could ship a wait that never ends.
func TestADependencyOnANonexistentTaskIsReported(t *testing.T) {
	errs := planWith(
		"### T-001 — First\n\n**Implements:** SPEC-001\n\n" +
			"### T-002 — Second\n\n**Implements:** SPEC-001\n**Depends on:** T-404\n",
	).Check()

	e := hasKind(errs, DanglingReference)
	if e == nil {
		t.Fatalf("a dependency on a task that does not exist was not reported: %v", kinds(errs))
	}
	if e.ID != "T-002" || e.Missing != "T-404" {
		t.Errorf("error = %+v", *e)
	}
	// The board would show T-002 waiting on T-404 forever, so the reason must
	// say the wait is permanent, not merely unmet.
	if !strings.Contains(e.Detail, "never start") {
		t.Errorf("the detail does not say the wait never ends: %q", e.Detail)
	}
}

func TestATaskDependingOnItselfIsReported(t *testing.T) {
	errs := planWith(
		"### T-001 — Only\n\n**Implements:** SPEC-001\n**Depends on:** T-001\n",
	).Check()
	if e := hasKind(errs, DependencyCycle); e == nil {
		t.Fatalf("a self-dependency was not reported: %v", kinds(errs))
	}
}

// A cycle is fatal: every task in it sits in Blocked forever, waiting on
// another that is waiting on it. With twenty tasks you cannot see it by eye.
func TestACycleIsReportedOnceNamingEveryTaskInIt(t *testing.T) {
	errs := planWith(
		"### T-001 — First\n\n**Implements:** SPEC-001\n**Depends on:** T-003\n\n" +
			"### T-002 — Second\n\n**Implements:** SPEC-001\n**Depends on:** T-001\n\n" +
			"### T-003 — Third\n\n**Implements:** SPEC-001\n**Depends on:** T-002\n",
	).Check()

	var cycles []TraceError
	for _, e := range errs {
		if e.Kind == DependencyCycle {
			cycles = append(cycles, e)
		}
	}
	// A cycle is reachable from every task leading into it, so a plain walk
	// finds it repeatedly. Three reports of one cycle read as three problems.
	if len(cycles) != 1 {
		t.Fatalf("got %d cycle reports, want 1: %+v", len(cycles), cycles)
	}
	for _, id := range []string{"T-001", "T-002", "T-003"} {
		if !strings.Contains(cycles[0].Missing, id) {
			t.Errorf("the cycle does not name %s: %q", id, cycles[0].Missing)
		}
	}
}

// Not a break: the plan is runnable, but its order lies. Working top to bottom
// reaches the task before the thing it needs, and a model handed a task whose
// prerequisite is missing writes the prerequisite too — which is how one task
// eats another.
func TestADependencyOnALaterTaskIsReported(t *testing.T) {
	errs := planWith(
		"### T-001 — Drag\n\n**Implements:** SPEC-001\n**Depends on:** T-002\n\n" +
			"### T-002 — Solver\n\n**Implements:** SPEC-001\n",
	).Check()

	e := hasKind(errs, ForwardDependency)
	if e == nil {
		t.Fatalf("a dependency on a later task was not reported: %v", kinds(errs))
	}
	if e.ID != "T-001" || e.Missing != "T-002" {
		t.Errorf("error = %+v", *e)
	}
}

// The common case must stay silent, or the check becomes noise the reader
// learns to skip.
func TestABackwardDependencyIsNotAFinding(t *testing.T) {
	errs := planWith(
		"### T-001 — Solver\n\n**Implements:** SPEC-001\n\n" +
			"### T-002 — Drag\n\n**Implements:** SPEC-001\n**Depends on:** T-001\n",
	).Check()
	if len(errs) != 0 {
		t.Errorf("a well-ordered plan reported %+v", errs)
	}
}

// A promoted bug task is justified by the report it fixes, not by a spec
// section. Flagging every "Fixes B-007" as unjustified taught people to
// ignore the spine — the one thing a check must never teach.
func TestABugFixTaskIsNotUnjustified(t *testing.T) {
	spine := &Spine{
		Requirements: &Document{Sections: []Section{{ID: "REQ-001", Fields: map[string]string{"priority": "must"}}}},
		Spec: &Document{Sections: []Section{{ID: "SPEC-001", Implements: []string{"REQ-001"},
			Fields: map[string]string{}}}},
		Plan: &Document{Sections: []Section{{ID: "M-001", Children: []Section{
			{ID: "T-001", Implements: []string{"SPEC-001"}},
			{ID: "T-048", Body: "Fixes B-007.\n\n## Reported\n\nIt broke."},
			{ID: "T-099", Body: "No justification at all."},
		}}}},
	}
	errs := spine.Check()
	for _, e := range errs {
		if e.ID == "T-048" && e.Kind == UnjustifiedTask {
			t.Error("a bug-fix task was flagged as unjustified")
		}
	}
	found := false
	for _, e := range errs {
		if e.ID == "T-099" && e.Kind == UnjustifiedTask {
			found = true
		}
	}
	if !found {
		t.Error("a genuinely unjustified task escaped the check")
	}
}

// An adopted spec's sections are delivered by the tree, not by tasks.
func TestAnAsBuiltSectionNeedsNoTask(t *testing.T) {
	spine := &Spine{
		Requirements: &Document{Sections: []Section{{ID: "REQ-001", Fields: map[string]string{"priority": "must"}}}},
		Spec: &Document{Sections: []Section{
			{ID: "SPEC-001", Implements: []string{"REQ-001"}, Fields: map[string]string{"as-built": "yes"}},
			{ID: "SPEC-002", Implements: []string{"REQ-001"}, Fields: map[string]string{}},
		}},
		Plan: &Document{},
	}
	errs := spine.Check()
	for _, e := range errs {
		if e.ID == "SPEC-001" && e.Kind == UnimplementedSpec {
			t.Error("an as-built section was asked for a task")
		}
	}
	found := false
	for _, e := range errs {
		if e.ID == "SPEC-002" && e.Kind == UnimplementedSpec {
			found = true
		}
	}
	if !found {
		t.Error("an unmarked section without a task escaped the check")
	}
}
