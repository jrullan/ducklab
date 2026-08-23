package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func sec(id, title string, implements ...string) artifact.Section {
	return artifact.Section{ID: id, Title: title, Implements: implements}
}

// Models reliably start at 001 whatever they are told. Renumbering an existing
// requirement would leave every SPEC pointing at it aimed somewhere else — the
// spine silently wrong rather than obviously broken.
func TestExistingSectionsKeepTheirIDs(t *testing.T) {
	existing := []artifact.Section{sec("REQ-001", "Login"), sec("REQ-002", "Audit log")}
	produced := []artifact.Section{sec("REQ-001", "Login"), sec("REQ-002", "Audit log")}

	got, remap := AssignIDs(existing, produced, "REQ")
	if got[0].ID != "REQ-001" || got[1].ID != "REQ-002" {
		t.Errorf("ids changed: %v", ids(got))
	}
	if len(remap) != 0 {
		t.Errorf("unnecessary remap: %v", remap)
	}
}

// A model that renumbers an existing item should update it, not duplicate it.
func TestRenumberedExistingItemIsMatchedByTitle(t *testing.T) {
	existing := []artifact.Section{sec("REQ-004", "Audit log")}
	produced := []artifact.Section{sec("REQ-001", "Audit log")}

	got, remap := AssignIDs(existing, produced, "REQ")
	if got[0].ID != "REQ-004" {
		t.Errorf("id = %q, want REQ-004 (matched by title)", got[0].ID)
	}
	if remap["REQ-001"] != "REQ-004" {
		t.Errorf("remap = %v", remap)
	}
}

// An id reused for clearly different content is a collision, not an edit.
func TestCollidingIDForDifferentContentIsRenumbered(t *testing.T) {
	existing := []artifact.Section{sec("REQ-001", "Login")}
	produced := []artifact.Section{sec("REQ-001", "Something else entirely")}

	got, remap := AssignIDs(existing, produced, "REQ")
	if got[0].ID == "REQ-001" {
		t.Error("different content took over an existing id")
	}
	if got[0].ID != "REQ-002" {
		t.Errorf("id = %q, want the next free number", got[0].ID)
	}
	if remap["REQ-001"] != "REQ-002" {
		t.Errorf("remap = %v", remap)
	}
}

func TestNewSectionsGetTheNextFreeNumber(t *testing.T) {
	existing := []artifact.Section{sec("REQ-001", "A"), sec("REQ-007", "B")}
	produced := []artifact.Section{sec("REQ-001", "A"), sec("REQ-002", "Brand new")}

	got, _ := AssignIDs(existing, produced, "REQ")
	if got[1].ID != "REQ-008" {
		t.Errorf("new section got %q, want REQ-008 (after the highest existing)", got[1].ID)
	}
}

// Renumbering must not orphan the things that point at the renumbered id.
func TestReferencesFollowARenumber(t *testing.T) {
	produced := []artifact.Section{
		{ID: "SPEC-001", Title: "Tokens", Implements: []string{"REQ-001"},
			Body: "Implements REQ-001 as described."},
	}
	remap := map[string]string{"REQ-001": "REQ-004"}

	got := RewriteReferences(produced, remap)
	if got[0].Implements[0] != "REQ-004" {
		t.Errorf("edge not rewritten: %v", got[0].Implements)
	}
	if !strings.Contains(got[0].Body, "REQ-004") || strings.Contains(got[0].Body, "REQ-001") {
		t.Errorf("prose still contradicts the edge: %q", got[0].Body)
	}
}

// Rewriting REQ-1 before REQ-10 would corrupt it.
func TestRemapHandlesOverlappingIDs(t *testing.T) {
	produced := []artifact.Section{{ID: "S", Body: "REQ-1 and REQ-10 both matter."}}
	got := RewriteReferences(produced, map[string]string{"REQ-1": "REQ-5", "REQ-10": "REQ-20"})
	if !strings.Contains(got[0].Body, "REQ-5") || !strings.Contains(got[0].Body, "REQ-20") {
		t.Errorf("overlapping ids corrupted: %q", got[0].Body)
	}
	if strings.Contains(got[0].Body, "REQ-50") {
		t.Errorf("REQ-10 was rewritten as a prefix of REQ-1: %q", got[0].Body)
	}
}

func TestNextFreeSkipsGaps(t *testing.T) {
	if got := NextFree([]artifact.Section{sec("REQ-001", "a"), sec("REQ-009", "b")}, "REQ"); got != 10 {
		t.Errorf("next free = %d, want 10", got)
	}
	if got := NextFree(nil, "REQ"); got != 1 {
		t.Errorf("next free on an empty artifact = %d, want 1", got)
	}
}

// Tasks are numbered project-wide, not per milestone: two milestones must not
// each produce a T-001.
func TestPlanTasksAreNumberedAcrossMilestones(t *testing.T) {
	produced := []artifact.Section{
		{ID: "M-01", Title: "Auth", Children: []artifact.Section{
			sec("T-001", "Issue tokens"), sec("T-002", "Expire tokens"),
		}},
		{ID: "M-02", Title: "Reporting", Children: []artifact.Section{
			sec("T-001", "Nightly rollup"),
		}},
	}
	got := PlanTaskIDs(nil, produced)
	all := []string{
		got[0].Children[0].ID, got[0].Children[1].ID, got[1].Children[0].ID,
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("duplicate task id across milestones: %v", all)
		}
		seen[id] = true
	}
	if all[2] != "T-003" {
		t.Errorf("second milestone's task = %q, want T-003", all[2])
	}
}

func TestPlanTasksKeepExistingIDsByTitle(t *testing.T) {
	existing := []artifact.Section{
		{ID: "M-01", Children: []artifact.Section{sec("T-007", "Issue tokens")}},
	}
	produced := []artifact.Section{
		{ID: "M-01", Children: []artifact.Section{sec("T-001", "Issue tokens"), sec("T-002", "New work")}},
	}
	got := PlanTaskIDs(existing, produced)
	if got[0].Children[0].ID != "T-007" {
		t.Errorf("existing task renumbered: %q", got[0].Children[0].ID)
	}
	if got[0].Children[1].ID != "T-008" {
		t.Errorf("new task = %q, want T-008", got[0].Children[1].ID)
	}
}

func ids(sections []artifact.Section) []string {
	var out []string
	for _, s := range sections {
		out = append(out, s.ID)
	}
	return out
}

// The architect numbers its own tasks and PlanTaskIDs renumbers them. A task it
// wrote as T-002 can land as T-004, and a "Depends on: T-002" written next to
// it would then name a different task — a silently wrong edge rather than an
// obviously broken one. The board reads that edge to decide what is blocked.
func TestRenumberingCarriesDependenciesWithIt(t *testing.T) {
	existing := []artifact.Section{{
		ID: "M-01", Title: "Groundwork",
		Children: []artifact.Section{
			{ID: "T-001", Title: "Scaffold"},
			{ID: "T-002", Title: "Config loader"},
		},
	}}
	// A second plan run: the model restarts its numbering at 001, and only
	// "Scaffold" matches something that already exists.
	produced := []artifact.Section{{
		ID: "M-01", Title: "Groundwork",
		Children: []artifact.Section{
			{ID: "T-001", Title: "Scaffold"},
			{
				ID: "T-002", Title: "State model",
				Body:   "**Depends on:** T-001\n",
				Fields: map[string]string{"depends on": "T-001"},
			},
			{
				ID: "T-003", Title: "Geometry",
				Body:   "**Depends on:** T-002\n",
				Fields: map[string]string{"depends on": "T-002"},
			},
		},
	}}

	out := PlanTaskIDs(existing, produced)
	kids := out[0].Children
	if kids[1].ID != "T-003" || kids[2].ID != "T-004" {
		t.Fatalf("ids = %s, %s, %s", kids[0].ID, kids[1].ID, kids[2].ID)
	}
	// "State model" depended on Scaffold, which kept T-001.
	if got := kids[1].Field("depends on"); got != "T-001" {
		t.Errorf("State model depends on %q, want T-001", got)
	}
	// "Geometry" depended on "State model", which moved from T-002 to T-003.
	if got := kids[2].Field("depends on"); got != "T-003" {
		t.Errorf("Geometry depends on %q — it still names the id the model chose, "+
			"which is now a different task", got)
	}
	// The body is what gets written to plan.md; a field that disagrees with it
	// produces a document that contradicts itself on the next read.
	if !strings.Contains(kids[2].Body, "**Depends on:** T-003") {
		t.Errorf("the body was not rewritten: %q", kids[2].Body)
	}
}

// The architect is asked for dependencies, and told not to invent them.
// Task ids in narrative prose must follow the same renumbering as task ids and
// dependency fields. References embedded within a longer id are not references
// to the task and must remain intact.
func TestPlanTaskIDsRenumbersTaskReferencesInBodyProse(t *testing.T) {
	existing := []artifact.Section{{
		ID: "M-01",
		Children: []artifact.Section{{ID: "T-095", Title: "Existing work"}},
	}}
	produced := []artifact.Section{{
		ID: "M-02",
		Children: []artifact.Section{
			{
				ID:    "T-900",
				Title: "First new task",
				Body:  "Coordinate with T-901. Keep T-9000 as an external fixture.",
			},
			{
				ID:    "T-901",
				Title: "Second new task",
				Body:  "Build on T-900 before release.",
			},
		},
	}}

	out := PlanTaskIDs(existing, produced)
	first, second := out[0].Children[0], out[0].Children[1]
	if first.ID != "T-096" || second.ID != "T-097" {
		t.Fatalf("ids = %q, %q; want T-096, T-097", first.ID, second.ID)
	}
	if want := "Coordinate with T-097. Keep T-9000 as an external fixture."; first.Body != want {
		t.Errorf("first task body = %q, want %q", first.Body, want)
	}
	if want := "Build on T-096 before release."; second.Body != want {
		t.Errorf("second task body = %q, want %q", second.Body, want)
	}
}

func TestThePlanPromptAsksForDependencies(t *testing.T) {
	for _, want := range []string{"**Depends on:**", "only where it is true"} {
		if !strings.Contains(planInstruction, want) {
			t.Errorf("the plan instruction never says %q", want)
		}
	}
}
