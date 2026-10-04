package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/bug"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

const planRel = ".ducklab/docs/plan.md"

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// commitFiles lists the paths one commit changed.
func commitFiles(t *testing.T, dir, sha string) []string {
	t.Helper()
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(gitOut(t, dir, "show", "--name-only", "--format=", sha)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}

func refuseCommits(t *testing.T, dir string) func() {
	t.Helper()
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused by policy >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() { os.Remove(hook) }
}

// gitPromotionProject is the TI-36X shape: a git project with an accepted,
// committed plan, a triaged bug, and the person's own unrelated work pending
// in the checkout — one tracked edit, one staged new file, one untracked file.
func gitPromotionProject(t *testing.T, s *Service) (id, dir string) {
	t.Helper()
	id = projectWithBugs(t, s, BugRequest{Title: "Left label does not update", Body: "1. press 2nd\n2. the label stays"})
	entry, err := s.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	dir = entry.Path
	if err := os.MkdirAll(artifact.DocsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "---\nkind: plan\nversion: 2\napproved_by: human\n---\n\n" + planDoc
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.js"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "fixture: accepted plan")

	if err := os.WriteFile(filepath.Join(dir, "calc.js"), []byte("the person's edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged by the person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	return id, dir
}

func assertPersonsWorkUntouched(t *testing.T, dir string) {
	t.Helper()
	status := gitOut(t, dir, "status", "--porcelain")
	for _, want := range []string{" M calc.js", "A  staged.txt", "?? notes.txt"} {
		if !strings.Contains(status, want) {
			t.Errorf("the person's pending work changed; want %q in:\n%s", want, status)
		}
	}
}

// B-489, the real case: promoting a bug in a checkout with unrelated pending
// work commits the plan it wrote and nothing else, as a new revision.
func TestBugPromotionCommitsOnlyThePlanItWrote(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	out, err := s.BugPromote(context.Background(), id, "B-001", "mcp:codex")
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := out["task"].(string)
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if head == before || out["commit"] != head {
		t.Fatalf("promotion did not commit: head %s (was %s), reported %v", head, before, out["commit"])
	}
	if files := commitFiles(t, dir, head); !slices.Equal(files, []string{planRel}) {
		t.Fatalf("promotion commit carried %v, want only the plan", files)
	}
	message := gitOut(t, dir, "log", "-1", "--format=%B")
	for _, want := range []string{"ducklab: promote B-001 to " + taskID, "Ducklab-Bug: B-001", "Ducklab-Action: bug_promoted"} {
		if !strings.Contains(message, want) {
			t.Errorf("commit message lacks %q:\n%s", want, message)
		}
	}
	if status := gitOut(t, dir, "status", "--porcelain", "--", planRel); status != "" {
		t.Errorf("the plan is still dirty after the promotion commit: %q", status)
	}
	assertPersonsWorkUntouched(t, dir)

	plan, err := artifact.Load(dir, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Section(taskID) == nil {
		t.Fatalf("%s is not in the committed plan", taskID)
	}
	if plan.Front.Version != 3 || plan.Front.ApprovedBy != "mcp:codex" || plan.Front.UpdatedAt == "" {
		t.Errorf("front matter still describes the earlier revision: version=%d approved_by=%q updated_at=%q",
			plan.Front.Version, plan.Front.ApprovedBy, plan.Front.UpdatedAt)
	}
}

// A refused commit leaves no half: the plan is what HEAD knows, the index is
// as the person left it, no task row exists, and the bug is still promotable —
// and promoting it again once the refusal is gone allocates the same id.
func TestBugPromotionRefusedCommitLeavesNothingHalfDone(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	planBefore, err := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	allow := refuseCommits(t, dir)

	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err == nil {
		t.Fatal("a promotion whose commit was refused reported success")
	}
	if head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); head != before {
		t.Fatalf("HEAD moved to %s", head)
	}
	planAfter, err := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	if err != nil {
		t.Fatal(err)
	}
	if string(planAfter) != string(planBefore) {
		t.Fatalf("the plan was not restored:\n%s", planAfter)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only", "--", planRel); staged != "" {
		t.Errorf("the refused plan is still staged: %q", staged)
	}
	assertPersonsWorkUntouched(t, dir)
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTask("T-003"); err == nil {
		t.Error("a task row survived the refused promotion")
	}
	if edges, _ := db.TracesFrom("bug", "B-001"); len(edges) != 0 {
		t.Errorf("trace edges survived the refused promotion: %v", edges)
	}
	rec, err := db.GetBug("B-001")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if rec.Status != string(bug.Triaged) || rec.TaskID != "" {
		t.Fatalf("the bug moved on a refused promotion: status=%s task=%q", rec.Status, rec.TaskID)
	}

	allow()
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatalf("the bug is not promotable after the refusal: %v", err)
	}
	if out["task"] != "T-003" {
		t.Errorf("retry allocated %v, want T-003", out["task"])
	}
}

// A project without git keeps working: the plan gains the task and the result
// says it is uncommitted (commitVisualSetup and commitRunRecord's rule).
func TestBugPromotionWithoutGitStillPromotes(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if vcs.New(dir).HasGit() {
		t.Skip("temp dir is inside a git repository")
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugMove(context.Background(), id, "B-001", "triaged", "human"); err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatalf("promotion failed outside git: %v", err)
	}
	plan, _ := artifact.Load(dir, artifact.KindPlan)
	if plan.Section(out["task"].(string)) == nil {
		t.Fatal("the promoted task is not in the plan")
	}
	if w, _ := out["warning"].(string); !strings.Contains(w, "not a git repository") {
		t.Errorf("warning = %q, want it to say the plan is uncommitted", w)
	}
}

// A repository where git cannot attribute a commit (B-463's fresh machine)
// keeps the promotion rather than refusing it, and says so.
func TestBugPromotionWithoutGitIdentityStillPromotes(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	orig := gitIdentityKnown
	gitIdentityKnown = func(*vcs.Git) bool { return false }
	defer func() { gitIdentityKnown = orig }()
	before := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))

	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatalf("promotion failed without a git identity: %v", err)
	}
	if head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")); head != before {
		t.Fatalf("a commit was made without an identity: %s", head)
	}
	if w, _ := out["warning"].(string); !strings.Contains(w, "identity") {
		t.Errorf("warning = %q, want it to name the missing identity", w)
	}
	plan, _ := artifact.Load(dir, artifact.KindPlan)
	if plan.Section(out["task"].(string)) == nil {
		t.Fatal("the promoted task is not in the plan")
	}
}

// The mirror of B-489: removing a task commits the plan without it, only the
// plan, as a new revision.
func TestTaskRemoveCommitsThePlan(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID := out["task"].(string)
	promoted, _ := artifact.Load(dir, artifact.KindPlan)

	removed, err := s.TaskRemove(context.Background(), id, taskID)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	if removed["commit"] != head {
		t.Fatalf("removal did not commit: head %s, reported %v", head, removed["commit"])
	}
	if files := commitFiles(t, dir, head); !slices.Equal(files, []string{planRel}) {
		t.Fatalf("removal commit carried %v, want only the plan", files)
	}
	message := gitOut(t, dir, "log", "-1", "--format=%B")
	for _, want := range []string{"ducklab: remove " + taskID, "Ducklab-Task: " + taskID, "Ducklab-Action: task_removed"} {
		if !strings.Contains(message, want) {
			t.Errorf("commit message lacks %q:\n%s", want, message)
		}
	}
	assertPersonsWorkUntouched(t, dir)
	plan, _ := artifact.Load(dir, artifact.KindPlan)
	if plan.Section(taskID) != nil || plan.Front.Version != promoted.Front.Version+1 {
		t.Errorf("removal revision: task present=%v version=%d (was %d)",
			plan.Section(taskID) != nil, plan.Front.Version, promoted.Front.Version)
	}
}

// A refused removal commit restores the plan before the database is touched,
// so the task, its row and its bug all stay as they were.
func TestTaskRemoveRefusedCommitChangesNothing(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := gitPromotionProject(t, s)
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskID := out["task"].(string)
	planBefore, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	refuseCommits(t, dir)

	if _, err := s.TaskRemove(context.Background(), id, taskID); err == nil {
		t.Fatal("a removal whose commit was refused reported success")
	}
	planAfter, _ := os.ReadFile(artifact.Path(dir, artifact.KindPlan))
	if string(planAfter) != string(planBefore) {
		t.Fatal("the plan was not restored after the refused removal")
	}
	db, err := s.openProjectDB(id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.GetTask(taskID); err != nil {
		t.Errorf("the task row was deleted by a refused removal: %v", err)
	}
	if rec, _ := db.GetBug("B-001"); rec.TaskID != taskID || rec.Status != string(bug.InProgress) {
		t.Errorf("the bug moved on a refused removal: %+v", rec)
	}
}

// specAcceptanceProject has an accepted plan whose T-110 a pending spec
// proposal covers, so accepting the spec also rewrites the plan.
func specAcceptanceProject(t *testing.T, s *Service, runID string) (id, dir string, git *vcs.Git) {
	t.Helper()
	id, dir = projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — Core\n\n### T-110 — Weight indicator\n\nFix the sign.\n",
		artifact.KindSpec: "## SPEC-001 — Snapshot\n\nShows weight.\n",
	})
	git = gitProject(t, dir)
	if err := artifact.WriteProposal(dir, artifact.KindSpec, &artifact.Document{Sections: []artifact.Section{
		{ID: "SPEC-001", Title: "Snapshot", Body: "Shows weight."},
		{ID: "SPEC-009", Title: "Weight indicator format", Body: "**As-built:** yes\n**Covers:** T-110\n\nSigned one-decimal pounds."},
	}}, runID, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("the person's edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id, dir, git
}

func registerPausedStageRun(t *testing.T, s *Service, id, dir, runID, stage string) {
	t.Helper()
	run := &runlog.Run{
		ID: runID, ProjectID: id, Stage: stage, Status: "paused", PendingKind: "gate", Verdict: "PASSED",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The desktop's Accept (ArtifactPromote) commits the accepted spec and the plan
// wiring it caused, under the producing run's trailer, and nothing the person
// left pending.
func TestArtifactPromoteCommitsTheAcceptedDocuments(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir, git := specAcceptanceProject(t, s, "r-spec")
	registerPausedStageRun(t, s, id, dir, "r-spec", "spec")

	out, err := s.ArtifactPromote(context.Background(), id, "spec", "human")
	if err != nil {
		t.Fatal(err)
	}
	head := mustHead(t, git)
	if out["commit"] != head {
		t.Fatalf("acceptance did not commit: head %s, reported %v", head, out["commit"])
	}
	files := commitFiles(t, dir, head)
	if !slices.Equal(files, []string{planRel, ".ducklab/docs/spec.md"}) {
		t.Fatalf("acceptance commit carried %v, want the spec and the plan it wired", files)
	}
	message := gitOut(t, dir, "log", "-1", "--format=%B")
	for _, want := range []string{"ducklab: accept spec", "Ducklab-Action: artifact_promoted", "Ducklab-Artifact: spec", "Ducklab-Run: r-spec"} {
		if !strings.Contains(message, want) {
			t.Errorf("commit message lacks %q:\n%s", want, message)
		}
	}
	if status := gitOut(t, dir, "status", "--porcelain", "--", "index.html"); !strings.HasPrefix(status, " M") {
		t.Errorf("the person's pending edit was swept: %q", status)
	}
}

// A proposal whose run cannot resolve as an accepted document run (unknown to
// the engine, or rejected) gets no Ducklab-Run trailer: the release inventory
// refuses a trailer it cannot resolve (B-351).
func TestArtifactPromoteOmitsAnUnresolvableRunTrailer(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir, git := specAcceptanceProject(t, s, "r-gone")

	if _, err := s.ArtifactPromote(context.Background(), id, "spec", "human"); err != nil {
		t.Fatal(err)
	}
	message := gitOut(t, dir, "log", "-1", "--format=%B", mustHead(t, git))
	if strings.Contains(message, "Ducklab-Run") || !strings.Contains(message, "Ducklab-Action: artifact_promoted") {
		t.Fatalf("commit message = %q, want the action trailer and no run trailer", message)
	}
}

// A document the acceptance did not change stays out of its commit, even when
// it is one it could have written: the person's pending plan edit is theirs.
func TestArtifactPromoteLeavesAnUntouchedPlanToThePerson(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — Core\n\n### T-110 — Weight indicator\n\nFix the sign.\n",
		artifact.KindSpec: "## SPEC-001 — Snapshot\n\nShows weight.\n",
	})
	git := gitProject(t, dir)
	if err := artifact.WriteProposal(dir, artifact.KindSpec, &artifact.Document{Sections: []artifact.Section{
		{ID: "SPEC-001", Title: "Snapshot", Body: "Shows weight, signed."},
	}}, "", nil); err != nil {
		t.Fatal(err)
	}
	planPath := artifact.Path(dir, artifact.KindPlan)
	raw, _ := os.ReadFile(planPath)
	if err := os.WriteFile(planPath, append(raw, []byte("\n<!-- the person's note -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ArtifactPromote(context.Background(), id, "spec", "human"); err != nil {
		t.Fatal(err)
	}
	if files := commitFiles(t, dir, mustHead(t, git)); !slices.Equal(files, []string{".ducklab/docs/spec.md"}) {
		t.Fatalf("acceptance commit carried %v, want only the spec", files)
	}
	if status := gitOut(t, dir, "status", "--porcelain", "--", planRel); !strings.HasPrefix(status, " M") {
		t.Errorf("the person's plan edit did not stay pending: %q", status)
	}
}

// A refused acceptance commit restores every document, the proposal included,
// so the run is still at its gate with the same decision in front of it.
func TestArtifactPromoteRefusedCommitKeepsTheGate(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir, git := specAcceptanceProject(t, s, "r-spec")
	registerPausedStageRun(t, s, id, dir, "r-spec", "spec")
	before := mustHead(t, git)
	read := func(kind artifact.Kind, proposed bool) string {
		path := artifact.Path(dir, kind)
		if proposed {
			path = artifact.ProposedPath(dir, kind)
		}
		data, _ := os.ReadFile(path)
		return string(data)
	}
	spec, proposal, plan := read(artifact.KindSpec, false), read(artifact.KindSpec, true), read(artifact.KindPlan, false)
	refuseCommits(t, dir)

	if _, err := s.ArtifactPromote(context.Background(), id, "spec", "human"); err == nil {
		t.Fatal("an acceptance whose commit was refused reported success")
	}
	if mustHead(t, git) != before {
		t.Fatal("HEAD moved")
	}
	if read(artifact.KindSpec, false) != spec || read(artifact.KindSpec, true) != proposal || read(artifact.KindPlan, false) != plan {
		t.Fatal("the documents were not restored after the refused acceptance")
	}
	s.runsMu.RLock()
	run := s.runs["r-spec"].snapshotRun()
	s.runsMu.RUnlock()
	if run.Status != "paused" || run.Accepted {
		t.Fatalf("the run left its gate on a refused acceptance: %s accepted=%v", run.Status, run.Accepted)
	}
}

// Outside git the acceptance still lands, and says it is uncommitted.
func TestArtifactPromoteWithoutGitStillAccepts(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindSpec: "## SPEC-001 — Old\n"})
	if vcs.New(dir).HasGit() {
		t.Skip("temp dir is inside a git repository")
	}
	artifact.WriteProposal(dir, artifact.KindSpec,
		&artifact.Document{Sections: []artifact.Section{{ID: "SPEC-001", Title: "New"}}}, "", nil)
	out, err := s.ArtifactPromote(context.Background(), id, "spec", "human")
	if err != nil {
		t.Fatalf("acceptance failed outside git: %v", err)
	}
	if w, _ := out["warning"].(string); !strings.Contains(w, "not a git repository") {
		t.Errorf("warning = %q", w)
	}
	if doc, _ := artifact.Load(dir, artifact.KindSpec); doc.Section("SPEC-001") == nil || doc.Section("SPEC-001").Title != "New" {
		t.Error("the proposal was not accepted")
	}
}

// RunAccept on a spec run lands the plan wiring in the run's own commit; it
// used to commit the spec and leave the wired plan in the working tree.
func TestSpecRunAcceptLandsThePlanWiringInItsCommit(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir, git := specAcceptanceProject(t, s, "r-spec")
	registerPausedStageRun(t, s, id, dir, "r-spec", "spec")

	result, err := s.RunAccept(context.Background(), "r-spec", "")
	if err != nil {
		t.Fatal(err)
	}
	files := commitFiles(t, dir, result.CommitSHA)
	if !slices.Contains(files, planRel) || !slices.Contains(files, ".ducklab/docs/spec.md") {
		t.Fatalf("the spec run's commit carried %v, want the spec and the wired plan", files)
	}
	if strings.Count(gitOut(t, dir, "log", "--format=%H", "-n", "5"), "\n") != 2 {
		t.Errorf("RunAccept committed the documents twice:\n%s", gitOut(t, dir, "log", "--oneline", "-n", "5"))
	}
	// The run's own commit, not ArtifactPromote's: RunAccept would otherwise
	// find its documents already committed and take the "nothing to commit"
	// shortcut, landing under a message the run did not write.
	message, _ := git.CommitMessage(result.CommitSHA)
	if !strings.Contains(message, "Ducklab-Run: r-spec") || strings.Contains(message, "Ducklab-Action: artifact_promoted") {
		t.Errorf("the documents did not land in the run's own commit:\n%s", message)
	}
	if status := gitOut(t, dir, "status", "--porcelain", "--", planRel); status != "" {
		t.Errorf("the wired plan is still dirty: %q", status)
	}
}

// Review of #153: any refusal before the commit puts every document back.
// Accepting a requirements proposal links it to its intent first; a stale
// proposal was then refused with those links already written into it.
func TestArtifactPromoteRefusedMidwayRestoresTheDocuments(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Login\n\n**Priority:** must\n",
	})
	if _, err := artifact.AppendIntent(dir, "r-intake", time.Now().UTC().Format(time.RFC3339), "Let people log out."); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteProposal(dir, artifact.KindRequirements, &artifact.Document{Sections: []artifact.Section{
		{ID: "REQ-001", Title: "Login", Body: "**Priority:** must"},
		{ID: "REQ-002", Title: "Logout", Body: "**Priority:** must"},
	}}, "r-intake", nil); err != nil {
		t.Fatal(err)
	}
	// The approved document moves while the proposal waits: promotion is
	// refused as stale, after the linking step has run.
	reqPath := artifact.Path(dir, artifact.KindRequirements)
	if err := os.WriteFile(reqPath, []byte("## REQ-001 — Login\n\n**Priority:** should\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := acceptedDocPaths(dir, artifact.KindRequirements)
	before := map[string]string{}
	for _, p := range paths {
		data, _ := os.ReadFile(p)
		before[p] = string(data)
	}

	_, err := s.ArtifactPromote(context.Background(), id, "requirements", "human")
	if !errors.Is(err, artifact.ErrProposalStale) {
		t.Fatalf("err = %v, want ErrProposalStale", err)
	}
	for _, p := range paths {
		data, _ := os.ReadFile(p)
		if string(data) != before[p] {
			t.Errorf("%s changed on a refused acceptance:\n%s", filepath.Base(p), data)
		}
	}
}
