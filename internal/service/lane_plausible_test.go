package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/store"
	"github.com/jrullan/ducklab/internal/tools"
)

// The TI-36X T-005 question (B-503) asked about "exposed angle/2nd/menu
// indicators". Slash-separated prose must never become a lane offer.
const b503Question = "Should the test assert the exposed angle/2nd/menu indicators, or only the DEG/RAD/GRAD mode?"

func writeLaneFixtures(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlausibleLanePathRefusesProse(t *testing.T) {
	root := t.TempDir()
	writeLaneFixtures(t, root, "src/display/indicators.js", "tests/index.js", "docs/notes.md")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"angle/2nd/menu", false},
		{"and/or", false},
		{"read/write", false},
		{"DEG/RAD/GRAD", false},
		{"1/2", false},
		{"km/h", false},
		{"2026/10/04", false},
		{"v1/2.0", false},
		{"https://example.com/docs/page.html", false},
		{"example.com/docs/page.html", false},
		// A parent that exists is not enough without a file-like name.
		{"src/display/menu", false},
		{"src/display/v2.0", false},
		// A file-like name is not enough without an existing parent.
		{"src/keypad/keys.js", false},
		// The parent must be a directory, not a file.
		{"src/display/indicators.js/extra.js", false},
		{"src/display/indicators.js", true},
		{"src/display", true},
		{"tests/display.test.mjs", true},
		{"README.md", true},
	} {
		if got := plausibleLanePath(tc.path, root); got != tc.want {
			t.Errorf("plausibleLanePath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	// A URL is refused by shape, even when cleaning its "//" would land on a
	// directory that happens to exist.
	if err := os.MkdirAll(filepath.Join(root, "https:", "example.com", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if plausibleLanePath("https://example.com/docs/page.html", root) {
		t.Error("URL accepted as a lane path")
	}
	// The run's worktree is evidence too: a new directory the run created
	// makes a new file under it plausible.
	worktree := t.TempDir()
	writeLaneFixtures(t, worktree, "src/keypad/existing.js")
	if !plausibleLanePath("src/keypad/keys.js", root, worktree) {
		t.Error("new file under a directory present only in the worktree was refused")
	}
}

func TestAdvisorLaneConflictsOffersOnlyRealOrPlausiblePaths(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — Calculator\n\n### T-005 — Indicators\n\n**Owns:** src/display\n",
	})
	writeLaneFixtures(t, dir, "src/display/indicators.js", "src/keys/keypad.js")

	if got := advisorLaneConflicts(dir, "T-005", b503Question); len(got) != 0 {
		t.Fatalf("B-503 question produced a lane offer: %v", got)
	}
	for _, prose := range []string{"and/or", "read/write", "DEG/RAD/GRAD", "1/2", "km/h", "https://example.com/ti/36x/manual.html"} {
		if got := advisorLaneConflicts(dir, "T-005", "Consider "+prose+" here."); len(got) != 0 {
			t.Errorf("prose %q produced a lane offer: %v", prose, got)
		}
	}
	for _, tc := range []struct {
		note string
		want []string
	}{
		{"May I edit src/keys/keypad.js too?", []string{"src/keys/keypad.js"}},
		{"Create src/keys/menu.js for the menu map.", []string{"src/keys/menu.js"}},
		{"Create src/menu/menu.js for the menu map.", nil},
	} {
		got := advisorLaneConflicts(dir, "T-005", tc.note)
		if !slices.Equal(got, tc.want) {
			t.Errorf("advisorLaneConflicts(%q) = %v, want %v", tc.note, got, tc.want)
		}
	}
	worktree := t.TempDir()
	writeLaneFixtures(t, worktree, "src/menu/existing.js")
	if got := advisorLaneConflicts(dir, "T-005", "Create src/menu/menu.js for the menu map.", worktree); !slices.Equal(got, []string{"src/menu/menu.js"}) {
		t.Errorf("new file under a worktree-only directory = %v, want it offered", got)
	}
}

// The question flow itself: pauseForQuestion must not attach the prose offer,
// and must offer a real out-of-lane file under a directory the run created in
// its worktree.
func TestPauseForQuestionOffersOnlyPlausibleLanePaths(t *testing.T) {
	// No ducklings: no advisor consultation races the temp-dir cleanup.
	s := newTestService(t)
	plan := "## M-001 — Calculator\n\n### T-001 — Indicators\n\n**Owns:** src/display\n"
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: plan})
	writeLaneFixtures(t, dir, "src/display/indicators.js")
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-b503-question")
	writeLaneFixtures(t, run.WorktreePath, "src/menu/menu.js")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()

	for _, tc := range []struct {
		question string
		want     []string
	}{
		{b503Question, nil},
		{"May I also edit src/menu/menu.js?", []string{"src/menu/menu.js"}},
	} {
		rs.wmu.Lock()
		rs.run.Status, rs.run.PendingKind, rs.run.PendingData = "running", "", nil
		rs.wmu.Unlock()
		s.pauseForQuestion(rs, &tools.PendingQuestion{ID: tools.QuestionID(tc.question), Question: tc.question})
		rs.wmu.Lock()
		got := stringSliceValue(rs.run.PendingData["lane_widening"])
		rs.wmu.Unlock()
		if !slices.Equal(got, tc.want) {
			t.Errorf("question %q offered %v, want %v", tc.question, got, tc.want)
		}
	}
}

// A stale or hand-crafted offer must not reach the plan: approval re-applies
// the same test the offer uses, on the question path and at Accept.
func TestLaneWideningApprovalRefusesProsePaths(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	plan := "## M-001 — Calculator\n\n### T-001 — Indicators\n\n**Owns:** src/display\n"
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: plan})
	writeLaneFixtures(t, dir, "src/display/indicators.js")
	git := gitProject(t, dir)
	base := mustHead(t, git)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-b503-approve")
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()

	planBefore, err := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	assertPlanUnchanged := func(step string) {
		t.Helper()
		after, err := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(planBefore) {
			t.Fatalf("%s wrote the prose lane into the plan:\n%s", step, after)
		}
		if got := mustHead(t, git); got != base {
			t.Fatalf("%s committed a lane amendment: head %s, want %s", step, got, base)
		}
	}

	rs.wmu.Lock()
	rs.run.PendingKind = "question"
	rs.run.PendingData = map[string]interface{}{
		"question_id": "q-b503", "question": b503Question,
		"lane_widening": []string{"angle/2nd/menu"},
	}
	rs.wmu.Unlock()
	err = s.RunAnswerWithLane(context.Background(), run.ID, "q-b503", "only DEG/RAD/GRAD", []string{"angle/2nd/menu"})
	if err == nil || !strings.Contains(err.Error(), "names nothing in the project") {
		t.Fatalf("question approval error = %v, want the prose path refused", err)
	}
	assertPlanUnchanged("question approval")

	rs.wmu.Lock()
	rs.run.PendingKind = "gate"
	rs.run.PendingData = map[string]interface{}{"lane_widening": []string{"angle/2nd/menu"}}
	rs.wmu.Unlock()
	_, err = s.RunAcceptAsWithOptions(context.Background(), run.ID, "", "human", AcceptOptions{LaneWidening: []string{"angle/2nd/menu"}})
	if err == nil || !strings.Contains(err.Error(), "names nothing in the project") {
		t.Fatalf("accept approval error = %v, want the prose path refused", err)
	}
	assertPlanUnchanged("accept approval")
}

// The Accept-gate offer comes from the real diff. A run that creates a file in
// a directory that exists only in its worktree must still be approvable: the
// approval check consults the worktree, not only the main checkout.
func TestAcceptGateWidensANewFileInADirectoryTheRunCreated(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	plan := "## M-001 — Calculator\n\n### T-001 — Indicators\n\n**Owns:** src/display\n"
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: plan})
	writeLaneFixtures(t, dir, "src/display/indicators.js")
	gitProject(t, dir)
	run, _ := pausedWorktreeRun(t, s, id, dir, "r-b503-new-dir")
	writeLaneFixtures(t, run.WorktreePath, "src/menu/menu.js")

	if _, err := s.RunAccept(context.Background(), run.ID, ""); err == nil || !strings.Contains(err.Error(), "outside T-001") {
		t.Fatalf("accept error = %v, want lane refusal", err)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	offered := stringSliceValue(detail.Run.PendingData["lane_widening"])
	if !slices.Equal(offered, []string{"src/menu/menu.js"}) {
		t.Fatalf("offer = %v, want the new file", offered)
	}
	if _, err := s.RunAcceptAsWithOptions(context.Background(), run.ID, "", "human", AcceptOptions{LaneWidening: offered}); err != nil {
		t.Fatalf("approve the real new file: %v", err)
	}
	accepted, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if task := accepted.Section("T-001"); task == nil || !slices.Contains(task.Owns, "src/menu/menu.js") {
		t.Fatalf("approved lane was not recorded: %+v", task)
	}
}

// Bug promotion writes Owns from triage text as well; the same rule applies.
func TestPromotionNamedTestPathsRefusesProse(t *testing.T) {
	root := t.TempDir()
	writeLaneFixtures(t, root, "tests/index.js")
	rec := &store.Bug{
		TestStrategy: "test-first",
		TestReason:   "Cover tests/and/or and add tests/indicators.test.ts; see test/missing/menu_test.go.",
	}
	got := promotionNamedTestPaths(root, rec, config.DefaultProject("ti36x", "TI-36X").Verify.TestGlobs)
	if !slices.Equal(got, []string{"tests/indicators.test.ts"}) {
		t.Fatalf("named test paths = %v, want only the plausible new test file", got)
	}
}
