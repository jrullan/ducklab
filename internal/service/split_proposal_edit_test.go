package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
)

// B-284: the split proposal was triager-only. The bug PUT ignored the field,
// ApplyTriage was the only writer and it touches open bugs only, so a triaged
// bug could never gain, correct or lose a proposal — deciding was reduced to
// promote-all-or-one. The doctrine says triage recommends and the person
// decides; deciding includes writing the portions.
func TestAPersonCanAuthorEditAndDiscardASplitProposal(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "saving a profile loses its avatar and leaves the cache stale",
	}); err != nil {
		t.Fatal(err)
	}

	authored := []bug.Portion{
		{Title: "Persist the avatar on profile save", Acceptance: []string{"a saved avatar is present after reload"}, Owns: []string{"internal/profile/persist.go"}},
		{Title: "Refresh the avatar cache after profile save", Acceptance: []string{"the cache serves the newly saved avatar"}, Owns: []string{"internal/profile/cache.go"}},
	}
	got, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &authored})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposal) != 2 || got.Proposal[1].Owns[0] != "internal/profile/cache.go" {
		t.Fatalf("authored proposal did not take: %+v", got.Proposal)
	}
	// Only the words were sent; the report itself is untouched.
	if got.Title != "saving a profile loses its avatar and leaves the cache stale" || string(got.Status) != "open" {
		t.Fatalf("a proposal edit changed the report: %+v", got)
	}

	// An edit that says nothing about the proposal leaves it alone.
	got, err = s.BugEdit(context.Background(), id, "B-001", BugRequest{Severity: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposal) != 2 {
		t.Fatalf("a severity edit touched the proposal: %+v", got.Proposal)
	}

	// Correcting one portion replaces the split as a whole: what is stored is
	// what the form showed.
	edited := []bug.Portion{{Title: "Persist and cache the avatar", Acceptance: []string{"a saved avatar is present after reload", "the cache serves it"}, Owns: []string{"internal/profile/persist.go", "internal/profile/cache.go"}}}
	got, err = s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &edited})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposal) != 1 || got.Proposal[0].Title != "Persist and cache the avatar" {
		t.Fatalf("edited proposal did not replace the stored one: %+v", got.Proposal)
	}
	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs[0].Proposal) != 1 {
		t.Fatalf("the list does not show the stored proposal: %+v", bugs[0].Proposal)
	}

	// An empty list is the person discarding the split: promote makes one task.
	none := []bug.Portion{}
	got, err = s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &none})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposal) != 0 {
		t.Fatalf("discarded proposal still shown: %+v", got.Proposal)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("promotion after discarding the split made %d tasks, want one", len(tasks)-2)
	}
}

// Promote must not care who wrote the split. A triager stores one, the person
// corrects a portion the triager got wrong, and the plan carries the corrected
// lanes — same milestone, same owns, same acceptance, same bug-ladder rules as
// the triager-stored path proven in promote_carries_test.go.
func TestAPersonEditedSplitPromotesLikeATriagerStoredOne(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "saving a profile loses its avatar and leaves the cache stale",
		Body:  "Both persistence and the cache update fail on save.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "the report spans persistence and caching",
		"proposal": []interface{}{
			map[string]interface{}{"title": "Persist the avatar on profile save", "acceptance": []interface{}{"a saved avatar is present after reload"}, "owns": []interface{}{"internal/profile/persist.go"}},
			map[string]interface{}{"title": "Refresh the avatar cache", "acceptance": []interface{}{"the cache serves the newly saved avatar"}, "owns": []interface{}{"internal/profile/persist.go"}},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(bugs[0].Status) != "triaged" || len(bugs[0].Proposal) != 2 {
		t.Fatalf("triage did not store the split on a triaged bug: %s %+v", bugs[0].Status, bugs[0].Proposal)
	}

	// The triager put both portions on the same file. The person fixes the
	// second lane and sharpens its title — on the triaged bug, the state the
	// triage sweep never revisits.
	corrected := append([]bug.Portion(nil), bugs[0].Proposal...)
	corrected[1].Title = "Refresh the avatar cache after profile save"
	corrected[1].Owns = []string{"internal/profile/cache.go"}
	if _, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &corrected}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.TaskList(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Fatalf("promotion created %d tasks, want one per portion", len(tasks)-2)
	}
	plan, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ owns, acceptance string }{
		"Persist the avatar on profile save":          {"internal/profile/persist.go", "a saved avatar is present after reload"},
		"Refresh the avatar cache after profile save": {"internal/profile/cache.go", "the cache serves the newly saved avatar"},
	}
	var portionIDs []string
	for _, task := range tasks {
		portion, ok := want[task.Title]
		if !ok {
			continue
		}
		portionIDs = append(portionIDs, task.ID)
		section := plan.Section(task.ID)
		if section == nil || len(section.Owns) != 1 || section.Owns[0] != portion.owns {
			t.Errorf("%q owns %v, want the corrected lane %q", task.Title, section, portion.owns)
		}
		if !strings.Contains(task.Body, portion.acceptance) {
			t.Errorf("%q lost its acceptance criterion:\n%s", task.Title, task.Body)
		}
	}
	if len(portionIDs) != 2 {
		t.Fatalf("promoted portions = %v, want the corrected titles %v", portionIDs, want)
	}
	for _, task := range tasks {
		if task.Title == "Refresh the avatar cache" {
			t.Fatalf("the triager's uncorrected title was promoted: %+v", task)
		}
	}
	if fixed, err := s.BugFixedByTask(context.Background(), id, portionIDs[0]); err != nil || fixed != "" {
		t.Fatalf("one accepted portion fixed %q, %v; the other is still open", fixed, err)
	}
	if fixed, err := s.BugFixedByTask(context.Background(), id, portionIDs[1]); err != nil || fixed != "B-001" {
		t.Fatalf("all accepted portions fixed %q, %v; want B-001", fixed, err)
	}
}

func TestASplitProposalIsHeldToTheTriageContract(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "two things broke"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		portions []bug.Portion
		want     string
	}{
		{"no owns", []bug.Portion{{Title: "a", Acceptance: []string{"x"}}}, "need title, 1-2 acceptance criteria, and owns"},
		{"no title", []bug.Portion{{Title: "  ", Acceptance: []string{"x"}, Owns: []string{"a.go"}}}, "need title"},
		{"three criteria", []bug.Portion{{Title: "a", Acceptance: []string{"x", "y", "z"}, Owns: []string{"a.go"}}}, "1-2 acceptance"},
		{"shared lane", []bug.Portion{
			{Title: "a", Acceptance: []string{"x"}, Owns: []string{"a.go"}},
			{Title: "b", Acceptance: []string{"y"}, Owns: []string{"b.go", "a.go"}},
		}, "lanes must be disjoint"},
	}
	for _, tc := range cases {
		_, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &tc.portions})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	bugs, err := s.BugList(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs[0].Proposal) != 0 {
		t.Fatalf("a refused proposal was stored: %+v", bugs[0].Proposal)
	}
}

func TestASplitProposalCannotBeEditedOncePromoted(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "two things broke"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err != nil {
		t.Fatal(err)
	}
	late := []bug.Portion{{Title: "a", Acceptance: []string{"x"}, Owns: []string{"a.go"}}}
	_, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Proposal: &late})
	if err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("err = %v, want the promoted refusal", err)
	}
	// The words are still the person's to correct.
	if _, err := s.BugEdit(context.Background(), id, "B-001", BugRequest{Title: "two things broke on save"}); err != nil {
		t.Fatal(err)
	}
}

// Three requests, three meanings: absent leaves the split alone, [] discards
// it, a list replaces it. The wire shape has to keep them apart.
func TestBugRequestKeepsAbsentAndEmptyProposalsApart(t *testing.T) {
	var absent, empty, some BugRequest
	if err := json.Unmarshal([]byte(`{"title":"x"}`), &absent); err != nil || absent.Proposal != nil {
		t.Fatalf("absent: %+v %v", absent.Proposal, err)
	}
	if err := json.Unmarshal([]byte(`{"proposal":[]}`), &empty); err != nil || empty.Proposal == nil || len(*empty.Proposal) != 0 {
		t.Fatalf("empty: %+v %v", empty.Proposal, err)
	}
	if err := json.Unmarshal([]byte(`{"proposal":[{"title":"a","acceptance":["x"],"owns":["a.go"]}]}`), &some); err != nil || some.Proposal == nil || len(*some.Proposal) != 1 {
		t.Fatalf("some: %+v %v", some.Proposal, err)
	}
}
