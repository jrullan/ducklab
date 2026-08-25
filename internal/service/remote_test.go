package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
				t.Fatalf("cherry-pick landed %s, want original %s", landed, sha)
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
