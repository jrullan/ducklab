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

func acceptedOrphan(t *testing.T, s *Service) (string, string, string) {
	t.Helper()
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	base, err := git.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recovered.txt"), []byte("orphaned work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.Add("recovered.txt"); err != nil {
		t.Fatal(err)
	}
	sha, err := git.Commit("orphaned accept")
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{ID: "r-orphan", ProjectID: id, TaskID: "T-140", Stage: "build", Status: "done", Accepted: true, CommitSHA: sha, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	cmd := exec.Command("git", "reset", "--hard", base)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v: %s", err, out)
	}
	return id, dir, sha
}

func TestRemoteAuditFlagsOrphanAndLocalOnly(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _, sha := acceptedOrphan(t, s)
	sub, cancel := s.bus.Subscribe("remote-audit", nil)
	defer cancel()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := s.RunList(context.Background(), RunFilter{ProjectID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || !runs[0].LocalOnly {
		t.Fatalf("accepted orphan badge = %+v, want local_only", runs)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-sub.Ch:
			if event.Type == "remote_warning" {
				if event.Data["warning"] != "ORPHANED ACCEPTED COMMITS: "+sha {
					t.Fatalf("warning = %+v", event)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing orphan warning")
		}
	}
}

func TestProjectRecoveryDoors(t *testing.T) {
	for _, action := range []string{"cherry-pick-chain", "restore-as-fresh-commit"} {
		t.Run(action, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir, sha := acceptedOrphan(t, s)
			// Git commit timestamps have second resolution. Crossing that boundary
			// makes an unpinned cherry-pick produce a different object ID.
			time.Sleep(1100 * time.Millisecond)
			if err := s.RecoverRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			landed, err := s.ProjectRecover(context.Background(), id, action, sha, "operator")
			if err != nil {
				t.Fatal(err)
			}
			if landed == "" {
				t.Fatal("recovery did not report landed commit")
			}
			got, err := os.ReadFile(filepath.Join(dir, "recovered.txt"))
			if err != nil || string(got) != "orphaned work\n" {
				t.Fatalf("recovered file = %q, %v", got, err)
			}
			if action == "cherry-pick-chain" && landed != sha {
				t.Fatalf("cherry-pick recovery SHA = %s, want original orphan SHA %s", landed, sha)
			}
			if action == "restore-as-fresh-commit" && landed == sha {
				t.Fatal("fresh recovery reused original commit")
			}
			runs, err := s.RunList(context.Background(), RunFilter{ProjectID: id})
			if err != nil || len(runs) != 1 || runs[0].LocalOnly {
				t.Fatalf("recovery did not clear local_only badge: %+v, %v", runs, err)
			}
		})
	}
}

func TestRemoteAuditFailureWarnsWithoutBlockingRecovery(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	run := &runlog.Run{ID: "r-bad-audit", ProjectID: id, Status: "done", Accepted: true, CommitSHA: "not-a-sha", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	sub, cancel := s.bus.Subscribe("remote-failure", nil)
	defer cancel()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatalf("audit blocked recovery: %v", err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-sub.Ch:
			if event.Type == "remote_warning" {
				return
			}
		case <-deadline:
			t.Fatal("missing audit failure warning")
		}
	}
}

func remoteReadyProject(t *testing.T) (*Service, string, string) {
	t.Helper()
	s := serviceWithDucklings(t, "scribe")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	opened, err := s.ProjectOpen(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	id = opened.ID
	remote := filepath.Join(t.TempDir(), "remote.git")
	cmd := exec.Command("git", "init", "--bare", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init remote: %v: %s", err, out)
	}
	for _, args := range [][]string{{"remote", "add", "origin", remote}, {"push", "-u", "origin", "master"}} {
		cmd = exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"remote.name": "origin"}); err != nil {
		t.Fatal(err)
	}
	return s, id, dir
}

func TestPullDivergenceRequestsPersonDecisionAndRecordsActor(t *testing.T) {
	s, id, dir := remoteReadyProject(t)
	// Commit locally, then advance the shared branch from an independent clone.
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "local.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	cmd = exec.Command("git", "commit", "-m", "local")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	clone := filepath.Join(t.TempDir(), "clone")
	// Use a worktree clone of the configured bare remote instead.
	remote, _ := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if out, err := exec.Command("git", "clone", strings.TrimSpace(string(remote)), clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	for _, args := range [][]string{{"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "shared"}, {"push"}} {
		cmd = exec.Command("git", args...)
		cmd.Dir = clone
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shared git %v: %s", args, out)
		}
	}
	out, err := s.Pull(context.Background(), id, RemoteRequest{Actor: "person"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "decision_required" || !strings.Contains(out.Prompt, "Nothing was merged") {
		t.Fatalf("pull = %+v", out)
	}
	receipt, err := os.ReadFile(filepath.Join(dir, ".ducklab", "remote-actions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(receipt), `"actor":"person"`) {
		t.Fatalf("receipt = %s", receipt)
	}
}

func TestPushRequiresRemoteAndRefusesAutopilot(t *testing.T) {
	s := serviceWithDucklings(t, "scribe")
	id, dir := projectWithDocs(t, s, nil)
	gitProject(t, dir)
	opened, err := s.ProjectOpen(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	id = opened.ID
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"remote.name": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Push(context.Background(), id, RemoteRequest{Actor: "person"}); err == nil || !strings.Contains(err.Error(), "no [remote]") {
		t.Fatalf("missing remote error = %v", err)
	}
	s, id, _ = remoteReadyProject(t)
	if _, err := s.Push(context.Background(), id, RemoteRequest{Actor: "person", Origin: "autopilot"}); err == nil || !strings.Contains(err.Error(), "explicit person") {
		t.Fatalf("autopilot error = %v", err)
	}
}

func TestPRFallsBackToCompareURL(t *testing.T) {
	s, id, _ := remoteReadyProject(t)
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"github.pr_tool": "none", "github.repo": "example/repo"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.PR(context.Background(), id, RemoteRequest{Actor: "person", Title: "Ready"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "compare_url" || !strings.Contains(out.CompareURL, "/compare/") || out.Actor != "person" {
		t.Fatalf("pr = %+v", out)
	}
}
