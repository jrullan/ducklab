package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
)

func landingGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRunGetOffersTrailerLanding(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "landing-offer")
	entry, _ := s.registry.Get(projectID)
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "commit", "--allow-empty", "-m", "initial")
	writeRun(t, entry.Path, projectID, "r-trailer", "done")
	if err := os.WriteFile(filepath.Join(entry.Path, "landed.txt"), []byte("landed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "add", "landed.txt")
	landingGit(t, entry.Path, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "landed work\n\nDucklab-Run: r-trailer")
	sha := landingGit(t, entry.Path, "rev-parse", "HEAD")
	s.RecoverRuns(context.Background())
	detail, err := s.RunGet(context.Background(), "r-trailer")
	if err != nil {
		t.Fatal(err)
	}
	if detail.LandingOffer == nil || detail.LandingOffer.CommitSHA != sha {
		t.Fatalf("landing offer = %+v, want SHA %s", detail.LandingOffer, sha)
	}
	if !strings.Contains(detail.LandingOffer.Evidence, "Ducklab-Run: r-trailer") {
		t.Errorf("evidence = %q, want trailer attribution", detail.LandingOffer.Evidence)
	}
}

func TestRunLandAdvancesBlockedTaskToAccepted(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "landing-task-state")
	entry, _ := s.registry.Get(projectID)
	if err := os.MkdirAll(filepath.Join(entry.Path, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-001 — Work\n\n### T-001 — Landed task\n\nComplete it.\n"
	if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "add", ".ducklab/docs/plan.md")
	landingGit(t, entry.Path, "commit", "-m", "initial")
	writeRun(t, entry.Path, projectID, "r-blocked", "failed")
	writeRun(t, entry.Path, projectID, "r-land", "done")
	s.RecoverRuns(context.Background())

	tasks, err := s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != "blocked" {
		t.Fatalf("before landing tasks = %+v, want one blocked task", tasks)
	}
	if tasks[0].Blocked == "" {
		t.Fatal("blocked task has no reason")
	}
	sha := landingGit(t, entry.Path, "rev-parse", "HEAD")
	if err := s.RunLand(context.Background(), "r-land", sha, "tester", "manual"); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("after landing tasks = %+v, want one task", tasks)
	}
	if tasks[0].Status != "accepted" {
		t.Errorf("after manual landing status = %q, want accepted", tasks[0].Status)
	}
	if tasks[0].Blocked != "" {
		t.Errorf("after manual landing blocked = %q, want empty", tasks[0].Blocked)
	}
}

func TestRunLandRejectsInvalidAndUnreachableSHA(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "landing-guard")
	entry, _ := s.registry.Get(projectID)
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "commit", "--allow-empty", "-m", "initial")
	writeRun(t, entry.Path, projectID, "r-guard", "done")
	s.RecoverRuns(context.Background())

	if err := s.RunLand(context.Background(), "r-guard", "not-a-commit", "tester", "manual"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("nonexistent SHA error = %v, want does not exist", err)
	}

	branch := landingGit(t, entry.Path, "branch", "--show-current")
	landingGit(t, entry.Path, "checkout", "-b", "unreachable-landing")
	if err := os.WriteFile(filepath.Join(entry.Path, "unreachable.txt"), []byte("unreachable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "add", "unreachable.txt")
	unreachable := landingGit(t, entry.Path, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "unreachable")
	unreachable = landingGit(t, entry.Path, "rev-parse", "HEAD")
	landingGit(t, entry.Path, "checkout", branch)
	if err := s.RunLand(context.Background(), "r-guard", unreachable, "tester", "manual"); err == nil || !strings.Contains(err.Error(), "not reachable from the default branch") {
		t.Errorf("unreachable SHA error = %v, want reachability reason", err)
	}
}

func TestTaskLandRecordsExternalCommitAsAcceptedRun(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "task-external-landing")
	entry, _ := s.registry.Get(projectID)
	if err := os.MkdirAll(filepath.Join(entry.Path, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-001 — Work\n\n### T-255 — External work\n\nComplete it.\n"
	if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "add", ".ducklab/docs/plan.md")
	landingGit(t, entry.Path, "commit", "-m", "initial")
	for _, prior := range []*runlog.Run{
		{ID: "r-red-test", ProjectID: projectID, Stage: "test", Mode: "solo", TaskID: "T-255", Status: "done", Accepted: true, StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)},
		{ID: "r-failed-external", ProjectID: projectID, Stage: "build", Mode: "solo", TaskID: "T-255", Status: "failed", StartedAt: time.Now().UTC().Format(time.RFC3339)},
	} {
		w, err := runlog.NewWriter(entry.Path, prior)
		if err != nil {
			t.Fatal(err)
		}
		_ = w.AppendEvent("run_start", map[string]interface{}{"stage": prior.Stage, "task_id": prior.TaskID})
		_ = w.Close()
	}
	s.RecoverRuns(context.Background())
	before, err := s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Status != "blocked" || !containsString(before[0].Next, "land") {
		t.Fatalf("task before external landing = %+v, want blocked with land door", before)
	}

	if err := os.WriteFile(filepath.Join(entry.Path, "landed.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "add", "landed.txt")
	landingGit(t, entry.Path, "commit", "-m", "implement external work\n\nCompletes T-255")
	sha := landingGit(t, entry.Path, "rev-parse", "HEAD")
	run, err := s.TaskLand(context.Background(), projectID, "T-255", TaskLandRequest{
		CommitSHA: sha, Reason: "implemented in a reviewed pull request", Actor: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.Accepted || run.Stage != "build" || run.Mode != "external" || run.CommitSHA != sha {
		t.Fatalf("external run = %+v", run)
	}
	if want := "completed by " + sha + ", declared by codex"; run.Resolution != want {
		t.Errorf("resolution = %q, want %q", run.Resolution, want)
	}
	tasks, err := s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != "accepted" {
		t.Fatalf("tasks after external landing = %+v, want accepted", tasks)
	}
	events, err := runlog.ReadEvents(s.RunDir(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Type == "human" && event.Data["action"] == "task_landed" && event.Data["reason"] == "implemented in a reviewed pull request" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want task_landed provenance", events)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestTaskLandRequiresExactTaskProvenanceOrExplicitConfirmation(t *testing.T) {
	s := newTestService(t)
	projectID := newTestProject(t, s, "task-external-confirm")
	entry, _ := s.registry.Get(projectID)
	if err := os.MkdirAll(filepath.Join(entry.Path, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-001 — Work\n\n### T-001 — External work\n\nComplete it.\n"
	if err := os.WriteFile(filepath.Join(entry.Path, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	landingGit(t, entry.Path, "init")
	landingGit(t, entry.Path, "config", "user.name", "test")
	landingGit(t, entry.Path, "config", "user.email", "test@test")
	landingGit(t, entry.Path, "add", ".ducklab/docs/plan.md")
	landingGit(t, entry.Path, "commit", "-m", "initial")
	landingGit(t, entry.Path, "commit", "--allow-empty", "-m", "Completes T-0010")
	sha := landingGit(t, entry.Path, "rev-parse", "HEAD")

	req := TaskLandRequest{CommitSHA: sha, Reason: "the PR predates the task trailer", Actor: "jose"}
	if _, err := s.TaskLand(context.Background(), projectID, "T-001", req); err == nil || !strings.Contains(err.Error(), "confirm_task=true") {
		t.Fatalf("near-match error = %v, want explicit confirmation instruction", err)
	}
	req.ConfirmTask = true
	if _, err := s.TaskLand(context.Background(), projectID, "T-001", req); err != nil {
		t.Fatalf("confirmed external landing: %v", err)
	}
}
