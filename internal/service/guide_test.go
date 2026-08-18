package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

func ids(steps []NextStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID
	}
	return out
}

// The guide walks the document pipeline in the loop's own order, one gate at
// a time — a new user is never shown a step whose prerequisites do not exist.
func TestTheGuideWalksTheDocumentPipeline(t *testing.T) {
	cases := []struct {
		name string
		st   projectSnapshot
		want string
	}{
		{"empty project", projectSnapshot{}, "intake"},
		{"requirements only", projectSnapshot{HasRequirements: true}, "spec"},
		{"spec with open work", projectSnapshot{HasRequirements: true, HasSpec: true, OpenSpecSections: 3}, "plan"},
	}
	for _, c := range cases {
		steps := nextSteps(c.st)
		if len(steps) != 1 || steps[0].ID != c.want {
			t.Errorf("%s: steps = %v, want exactly [%s]", c.name, ids(steps), c.want)
		}
		if steps[0].Reason == "" {
			t.Errorf("%s: a step with no reason is an order, not guidance", c.name)
		}
	}
	// Outcome language leads; the harness term follows. New users do not
	// share the vocabulary yet (intake, spec…), and a guide that speaks only
	// jargon re-creates the mental load it exists to remove.
	first := nextSteps(projectSnapshot{})[0]
	if !strings.Contains(first.Action, "Describe what you want to build") {
		t.Errorf("intake step leads with jargon: %q", first.Action)
	}
}

// Work already paid for outranks everything: a paused run waits on one click,
// and sending a new user off to start new work while a question sits
// unanswered teaches them to ignore the inbox.
func TestPausedRunsOutrankEverything(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks:  []TaskView{{ID: "T-001", Status: "todo"}},
		Paused: []*runlog.Run{{ID: "r-q", Stage: "spec", Status: "paused", PendingKind: "question"}},
	}
	steps := nextSteps(st)
	if steps[0].ID != "answer-run" || steps[0].Ref != "r-q" {
		t.Fatalf("first step = %+v, want the paused run's question", steps[0])
	}
}

// One task, not the backlog: the guide picks the next buildable — test-ready
// first, else the first todo whose dependencies are all accepted. The board
// already lists everything; a guide that does too is a second board.
// Accepted work is not shipped merely because its gate is green. The guide must
// make the release obligation visible and put it ahead of the quiet-project
// doors, so an operator cannot mistake accepted for released.
// A fixed bug still needs a person's verification, and accepted work may be
// deliberately reopened with redo consent. Both doors belong in the same
// guide so clients do not have to know the lifecycle by heart.
func TestTheGuideSurfacesReopenDoors(t *testing.T) {
	steps := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Bugs:  []bug.Bug{{ID: "B-017", Status: bug.Fixed}},
		Tasks: []TaskView{{ID: "T-016", Status: "accepted"}},
	})

	byRef := map[string]NextStep{}
	for _, step := range steps {
		byRef[step.Ref] = step
	}
	bugStep, ok := byRef["B-017"]
	if !ok {
		t.Fatalf("guide = %v, want a verification step for fixed bug B-017", ids(steps))
	}
	if bugStep.ID != "verify-bug" || bugStep.Kind != "bug" {
		t.Errorf("fixed bug step = %+v, want id verify-bug and kind bug", bugStep)
	}
	if !strings.HasPrefix(bugStep.Action, "Verify B-017") || !strings.Contains(strings.ToLower(bugStep.Action), "reopen") || bugStep.Reason == "" {
		t.Errorf("fixed bug step must lead with verification and explain the reopen alternative: %+v", bugStep)
	}

	taskStep, ok := byRef["T-016"]
	if !ok {
		t.Fatalf("guide = %v, want a reopen step for accepted task T-016", ids(steps))
	}
	if taskStep.ID != "reopen-task" || taskStep.Kind != "task" {
		t.Errorf("accepted task step = %+v, want id reopen-task and kind task", taskStep)
	}
	if !strings.Contains(strings.ToLower(taskStep.Action), "reopen") || taskStep.Reason == "" {
		t.Errorf("accepted task step must explain the reopen outcome and reason: %+v", taskStep)
	}
	if !strings.Contains(strings.ToLower(taskStep.Action), "redo") && !strings.Contains(strings.ToLower(taskStep.Reason), "redo") {
		t.Errorf("accepted task reopen must disclose the redo path: %+v", taskStep)
	}

	// Reopen is not a generic action for every bug or task: only fixed bugs
	// and accepted tasks earn it.
	quiet := nextSteps(projectSnapshot{HasRequirements: true, HasSpec: true, HasPlan: true,
		Bugs:  []bug.Bug{{ID: "B-018", Status: bug.Verified}},
		Tasks: []TaskView{{ID: "T-017", Status: "todo"}},
	})
	for _, step := range quiet {
		if step.ID == "reopen-bug" || step.ID == "reopen-task" {
			t.Errorf("ineligible object received reopen step: %+v", step)
		}
	}
}

// Ordinary fixed bugs await the human-only I2 judgement: verification is the
// headline, while reopening is only the alternative if the fix missed the report.
func TestTheGuidePrioritizesVerificationForFixedBugs(t *testing.T) {
	steps := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Bugs: []bug.Bug{
			{ID: "B-004", Status: bug.Fixed},
			{ID: "B-007", Status: bug.Fixed},
			{ID: "B-018", Status: bug.Fixed},
		},
	})

	for _, id := range []string{"B-004", "B-007", "B-018"} {
		var found *NextStep
		for i := range steps {
			if steps[i].Ref == id {
				found = &steps[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("guide = %v, want a step for fixed bug %s", ids(steps), id)
		}
		if found.ID != "verify-bug" || found.Kind != "bug" {
			t.Errorf("fixed bug %s step = %+v, want verify-bug bug step", id, *found)
		}
		if !strings.HasPrefix(found.Action, "Verify "+id+" — confirm the fix answers the report") {
			t.Errorf("fixed bug %s action = %q, want Verify as the headline", id, found.Action)
		}
		if !strings.Contains(strings.ToLower(found.Action), "reopen") {
			t.Errorf("fixed bug %s action = %q, want reopen as the decision alternative", id, found.Action)
		}
	}
}

func TestTheGuideSurfacesAcceptedUnreleasedWork(t *testing.T) {
	steps := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{
			{ID: "T-001", Status: "accepted", Branch: "ducklab/T-001"},
		},
		UnreleasedBranches: 1,
	})
	if len(steps) == 0 || steps[0].ID != "release" {
		t.Fatalf("guide = %v, want release first for accepted-but-unreleased work", ids(steps))
	}
	if !strings.Contains(steps[0].Action, "1") || !strings.Contains(steps[0].Reason, "accepted") {
		t.Errorf("release step does not surface the accepted count: %+v", steps[0])
	}
}

// Cutting a release marks precisely the accepted commits reachable from its tag
// as shipped. Persisted branch names are provenance, not release state: they
// survive merging and deleting a worktree, so they cannot keep a release door
// open. A later accepted commit remains actionable.
func TestReleaseGuidanceCountsOnlyWorkAfterTheLatestRelease(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Ship it\n",
		artifact.KindSpec:         "## SPEC-001 — Work\n\n**Implements:** REQ-001\n",
		artifact.KindPlan: `## M-001 — Release

### T-001 — Shipped work

**Implements:** SPEC-001

### T-002 — New work

**Implements:** SPEC-001
`,
	})
	git := gitProject(t, dir)

	commit := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := git.Add(path); err != nil {
			t.Fatal(err)
		}
		sha, err := git.Commit("build " + name)
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	writeAccepted := func(id, task, sha string) {
		t.Helper()
		run := &runlog.Run{ID: id, ProjectID: projectID, TaskID: task, Stage: "build",
			Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha,
			// Deliberately retain a branch that no longer exists. Its name must not
			// decide whether this accepted commit shipped.
			Branch: "ducklab/" + task,
		}
		w, err := runlog.NewWriter(dir, run)
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
	}

	shipped := commit("shipped.txt")
	writeAccepted("r-shipped", "T-001", shipped)
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	v := release.Version{Major: 0, Minor: 1, Patch: 0}
	draft := release.Path(dir, v) + ".proposed"
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	// This is the inventory the release actually computed and presented for
	// approval. The subsequent tag includes the accepted T-001 commit.
	if err := os.WriteFile(draft, []byte(release.Render(release.Notes{Version: v,
		Milestones: release.Group([]release.Item{{TaskID: "T-001", CommitSHA: shipped}}),
	}, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseCut(context.Background(), projectID, v.String()); err != nil {
		t.Fatal(err)
	}

	newCommit := commit("new.txt")
	writeAccepted("r-new", "T-002", newCommit)
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	status, err := s.ProjectStatus(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if status.AcceptedUnreleased != 1 || status.UnreleasedBranches != 1 {
		t.Errorf("status counts = accepted %d / branches %d, want only T-002 after v0.1.0", status.AcceptedUnreleased, status.UnreleasedBranches)
	}

	steps, err := s.ProjectNext(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	var releaseStep *NextStep
	for i := range steps {
		if steps[i].ID == "release" {
			releaseStep = &steps[i]
			break
		}
	}
	if releaseStep == nil {
		t.Fatalf("guide = %v, want a release step for T-002", ids(steps))
	}
	if !strings.Contains(releaseStep.Action, "1 accepted task(s)") || strings.Contains(releaseStep.Action, "2 accepted task(s)") {
		t.Errorf("release step = %+v, want it to count only the post-tag task", *releaseStep)
	}

	// The tag is the release boundary, not branch deletion. Prove the fixture
	// really retained the stale branch names while the release exists.
	if vcs.New(dir).HasTag(v.String()) == false {
		t.Fatal("setup did not create the release tag")
	}
}

func TestTheGuideSuggestsOneBuildableTask(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{
			{ID: "T-001", Status: "accepted"},
			{ID: "T-002", Status: "todo", DependsOn: []string{"T-009"}}, // dep not accepted
			{ID: "T-003", Status: "todo", DependsOn: []string{"T-001"}},
			{ID: "T-004", Status: "todo"},
		},
	}
	steps := nextSteps(st)
	if len(steps) != 1 || steps[0].ID != "test-first" || steps[0].Ref != "T-003" {
		t.Errorf("steps = %v (ref %s), want one test-first step for T-003", ids(steps), steps[0].Ref)
	}

	// A committed failing test is the strongest pull: done is already defined.
	st.Tasks = append(st.Tasks, TaskView{ID: "T-005", Status: "todo", TestReady: true})
	steps = nextSteps(st)
	if steps[0].ID != "build" || steps[0].Ref != "T-005" {
		t.Errorf("with a test ready, first = %+v, want build T-005", steps[0])
	}
}

// Bugs are classified before new work starts, and a quiet project is told so
// instead of shown an empty list.
func TestBugsThenQuiet(t *testing.T) {
	st := projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Bugs: []bug.Bug{
			{ID: "B-001", Status: bug.Open},
			{ID: "B-002", Status: bug.Triaged},
		},
		Tasks: []TaskView{{ID: "T-001", Status: "todo"}},
	}
	got := ids(nextSteps(st))
	want := []string{"triage", "promote", "test-first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}

	quiet := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{{ID: "T-001", Status: "accepted"}},
	})
	// Three doors, brief first — the autopilot reads steps[0] to know the
	// project is done, and each door carries its own destination.
	if strings.Join(ids(quiet), ",") != "brief,amend,release" {
		t.Errorf("a finished project's guide = %v, want [brief amend release]", ids(quiet))
	}
	if quiet[1].Ref != "plan" || quiet[1].Kind != "stage" {
		t.Errorf("the amendment must land on the plan view: %+v", quiet[1])
	}
}

// The real gather: a freshly initialized project asks for its requirements.
func TestProjectNextOnAFreshProject(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	steps, err := s.ProjectNext(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 || steps[0].ID != "intake" {
		t.Errorf("fresh project guide = %v, want intake first", ids(steps))
	}
}

// The engine-side dissent check: the desktop's reviewerDissent only protects
// the person watching, and under auto nobody is. The LAST verdict decides —
// an early request-changes answered by a later approve is agreement — and a
// green gate with standing dissent must reach a human, not acceptRun.
func TestFinalDissentReadsTheLastVerdict(t *testing.T) {
	write := func(t *testing.T, events []string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"),
			[]byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	// Dissent standing: request_changes is the final word.
	dir := write(t, []string{
		`{"type":"message","data":{"role":"implementer","content":"done"}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"approve","findings":[]}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"request_changes","findings":[{"issue":"a"},{"issue":"b"}]}}`,
	})
	v, n, dissent := finalDissent(dir)
	if !dissent || v != "request_changes" || n != 2 {
		t.Errorf("= %q %d %v, want standing request_changes with 2 findings", v, n, dissent)
	}

	// Dissent answered: a later approve settles it.
	dir = write(t, []string{
		`{"type":"message","data":{"role":"reviewer","verdict":"request_changes","findings":[{"issue":"a"}]}}`,
		`{"type":"message","data":{"role":"reviewer","verdict":"approve","findings":[]}}`,
	})
	if _, _, dissent := finalDissent(dir); dissent {
		t.Error("an answered objection still reads as dissent")
	}

	// No verdicts at all (solo run): nothing to dissent.
	dir = write(t, []string{`{"type":"message","data":{"role":"implementer","content":"done"}}`})
	if _, _, dissent := finalDissent(dir); dissent {
		t.Error("a run with no reviewer invented a dissent")
	}
}

// A project.toml that does not parse fails EVERY run at load — a duplicated
// key did exactly that, and each relaunch died with the same excavated
// one-liner. Broken config outranks every other suggestion: nothing else the
// guide would say is trustworthy while it stands.
func TestBrokenConfigOutranksEverything(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "proj")
	entry, _ := s.registry.Get(projectID)
	tomlPath := filepath.Join(entry.Path, ".ducklab", "project.toml")
	if err := os.MkdirAll(filepath.Dir(tomlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// The exact real-world break: one key defined twice.
	broken := "[verify]\n  timeout_s = 120\n  timeout_s = 900\n"
	if err := os.WriteFile(tomlPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	steps, err := s.ProjectNext(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].ID != "config" {
		t.Fatalf("guide = %v, want exactly the config tripwire", ids(steps))
	}
	if !strings.Contains(steps[0].Reason, "already been defined") {
		t.Errorf("the reason does not carry the parse error: %q", steps[0].Reason)
	}
}

// The amendment's toll is the guide's business: the person should learn the
// spec fell behind from the rail, not from counting markers on the board —
// and clicking through lands where settling is one button.
func TestTheGuideSurfacesSpecDebt(t *testing.T) {
	steps := nextSteps(projectSnapshot{
		HasRequirements: true, HasSpec: true, HasPlan: true,
		Tasks: []TaskView{
			{ID: "T-110", Status: "accepted", SpecDebt: true},
			{ID: "T-111", Status: "accepted"},
		},
	})
	found := false
	for _, st := range steps {
		if st.ID == "spec-debt" {
			found = true
			if st.Ref != "spec" || st.Kind != "stage" {
				t.Errorf("the debt step must land on the spec stage: %+v", st)
			}
			if !strings.Contains(st.Action, "1 task(s)") {
				t.Errorf("the step does not count the debt: %q", st.Action)
			}
		}
	}
	if !found {
		t.Fatalf("no spec-debt step in %v", ids(steps))
	}
}
