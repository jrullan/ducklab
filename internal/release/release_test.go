package release

import (
	"strings"
	"testing"
)

func TestBumpFollowsSemver(t *testing.T) {
	v := Version{1, 4, 2}
	for _, tc := range []struct {
		kind string
		want string
	}{
		{"major", "v2.0.0"},
		{"minor", "v1.5.0"},
		{"patch", "v1.4.3"},
		{"", "v1.5.0"}, // minor is the default
	} {
		got, err := Bump(v, tc.kind)
		if err != nil {
			t.Fatalf("Bump(%q): %v", tc.kind, err)
		}
		if got.String() != tc.want {
			t.Errorf("Bump(%q) = %s, want %s", tc.kind, got, tc.want)
		}
	}
	if _, err := Bump(v, "sideways"); err == nil {
		t.Error("an unknown bump was accepted")
	}
}

// Ordered by version, not by tag date. A patch cut after a minor is still
// older, and reading history by when someone happened to tag would put it last.
func TestLatestOrdersByVersionNotByOrderSeen(t *testing.T) {
	got, ok := Latest([]string{"v1.0.0", "v1.10.0", "v1.9.0", "v1.10.1"})
	if !ok || got.String() != "v1.10.1" {
		t.Errorf("latest = %s (%v), want v1.10.1", got, ok)
	}
	// v1.9.0 must not beat v1.10.0 on a string comparison.
	if got, _ := Latest([]string{"v1.10.0", "v1.9.0"}); got.String() != "v1.10.0" {
		t.Errorf("latest = %s; versions were compared as strings", got)
	}
}

// A tag that is not a release is not our business.
func TestLatestIgnoresTagsThatAreNotReleases(t *testing.T) {
	if _, ok := Latest([]string{"nightly", "release-candidate", "v1"}); ok {
		t.Error("a non-release tag was read as a version")
	}
	got, ok := Latest([]string{"nightly", "v0.2.0"})
	if !ok || got.String() != "v0.2.0" {
		t.Errorf("latest = %s (%v)", got, ok)
	}
}

// A task built from a hand-written spec is still something that shipped.
func TestGroupKeepsWorkWithNoMilestone(t *testing.T) {
	ms := Group([]Item{
		{TaskID: "T-002", Milestone: "M-01"},
		{TaskID: "T-009"},
		{TaskID: "T-001", Milestone: "M-01"},
	})
	if len(ms) != 2 {
		t.Fatalf("milestones = %d, want 2 (M-01 and unassigned)", len(ms))
	}
	if ms[0].ID != "M-01" || ms[1].ID != "unassigned" {
		t.Errorf("groups = %s, %s", ms[0].ID, ms[1].ID)
	}
	// Within a milestone, by task id: a release read twice must read the same.
	if ms[0].Items[0].TaskID != "T-001" {
		t.Errorf("items are not ordered: %v", ms[0].Items)
	}
}

func TestRenderRecordsWhatShipped(t *testing.T) {
	got := Render(Notes{
		Version: Version{0, 3, 0}, Since: "v0.2.0",
		Milestones: Group([]Item{
			{TaskID: "T-001", Title: "Add login", Milestone: "M-01", CommitSHA: "abc1234def"},
		}),
	}, "You can now log in.")

	for _, want := range []string{
		"version: v0.3.0", "since: v0.2.0", "tasks: 1",
		"# v0.3.0", "You can now log in.",
		"## What shipped", "### M-01", "**T-001** Add login", "`abc1234`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the release does not record %q:\n%s", want, got)
		}
	}
}

// A release note that reads the same whether or not anything was tested is a
// release note that cannot be trusted (P3).
func TestRenderSaysWhenNothingWasVerified(t *testing.T) {
	got := Render(Notes{
		Version:    Version{0, 1, 0},
		Unverified: 2,
		Milestones: Group([]Item{{TaskID: "T-001"}, {TaskID: "T-002"}}),
	}, "Some changes.")
	if !strings.Contains(got, "unverified: 2") {
		t.Error("the frontmatter hides that nothing was verified")
	}
	if !strings.Contains(got, "nothing was verified about them") {
		t.Errorf("the body does not warn the reader:\n%s", got)
	}
}

// The inventory is written here and never by a model: a release whose contents
// were paraphrased is a release nobody can audit.
func TestRenderIsHonestAboutAnEmptyRelease(t *testing.T) {
	got := Render(Notes{Version: Version{0, 1, 0}}, "Lots of exciting improvements!")
	if strings.Contains(got, "exciting") {
		t.Error("prose was rendered for a release with nothing in it")
	}
	if !strings.Contains(got, "No user-visible changes.") {
		t.Errorf("an empty release does not say so:\n%s", got)
	}
}

// "1 of these changes were accepted with no gate" named nothing and sent the
// reader hunting through the inventory. The sentence names the tasks and
// agrees in number; the frontmatter carries the ids so the list can too.
func TestUnverifiedSentenceNamesTheTasks(t *testing.T) {
	if got := unverifiedSentence(1, []string{"T-049"}); got != "1 of these changes (T-049) was accepted with no gate that could run, so nothing was verified about it beyond a person reading the diff." {
		t.Errorf("one: %q", got)
	}
	if got := unverifiedSentence(2, []string{"T-001", "T-002"}); !strings.HasPrefix(got, "2 of these changes (T-001, T-002) were accepted") {
		t.Errorf("two: %q", got)
	}
	v, _ := ParseVersion("v0.6.0")
	md := Render(Notes{Version: v, Unverified: 1, UnverifiedTasks: []string{"T-049"}, Milestones: []Milestone{{ID: "M-1", Items: []Item{{TaskID: "T-049", Title: "Recover"}}}}}, "notes")
	if !strings.Contains(md, "unverified_tasks: T-049\n") || !strings.Contains(md, "> 1 of these changes (T-049) was accepted") {
		t.Errorf("rendered:\n%s", md)
	}
}
