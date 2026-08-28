package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A release is an inventory of the git range, not an inventory of runs.  In
// particular, ordinary landed commits must remain visible even when no
// Ducklab run exists for them and PR lookup cannot help (this fixture has no
// remote at all).
func TestReleasePlanInventoriesTrailerlessCommitsAlongsideTaskWork(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, root := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Inventory\n\n### T-001 — Tracked work\n\nShip the tracked change.\n",
	})
	git := gitProject(t, root)
	if err := git.Tag("v0.1.0", "previous release"); err != nil {
		t.Fatal(err)
	}

	commit := func(name, subject string, trailer map[string]string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(subject+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := git.AddAll(); err != nil {
			t.Fatal(err)
		}
		var sha string
		var err error
		if trailer == nil {
			sha, err = git.Commit(subject)
		} else {
			sha, err = git.CommitWithTrailer(subject, trailer)
		}
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}

	taskSHA := commit("tracked.txt", "tracked change", map[string]string{"Ducklab-Run": "r-tracked"})
	s.runs["r-tracked"] = &runState{run: &runlog.Run{
		ID: "r-tracked", ProjectID: projectID, TaskID: "T-001", Accepted: true,
		CommitSHA: taskSHA, Verdict: "PASSED", StartedAt: time.Now().UTC().Format(time.RFC3339),
	}}
	olderDirectSHA := commit("direct-one.txt", "operator config fix", nil)
	newerDirectSHA := commit("direct-two.txt", "flaky test repair", nil)
	author := commitAuthor(t, root, newerDirectSHA)

	fake, ok := s.providers["fake"].(*provider.Fake)
	if !ok {
		t.Fatal("test service did not install fake provider")
	}
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "The release prose."},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.ReleasePlan(context.Background(), projectID, ReleaseRequest{Bump: "minor"})
	if err != nil {
		// In particular, lack of gh/network must not make planning fail.
		t.Fatalf("release planning with no PR lookup available: %v", err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done

	body, err := os.ReadFile(release.Path(root, release.Version{Major: 0, Minor: 2, Patch: 0}) + ".proposed")
	if err != nil {
		detail, getErr := s.RunGet(context.Background(), run.ID)
		t.Fatalf("read proposed release: %v (run: %+v, get: %v)", err, detail.Run, getErr)
	}
	notes := string(body)
	for _, want := range []string{
		"tasks: 1\n", "landed_outside: 2\n", "**T-001** Tracked work",
		"## Landed outside the loop\n\n",
		"- flaky test repair (" + newerDirectSHA[:7] + ") — " + author + "\n",
		"- operator config fix (" + olderDirectSHA[:7] + ") — " + author + "\n",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("release inventory missing %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, " [PR #") {
		t.Errorf("a repository without local PR metadata invented PR enrichment:\n%s", notes)
	}
	if strings.Index(notes, newerDirectSHA[:7]) > strings.Index(notes, olderDirectSHA[:7]) {
		t.Errorf("trailerless commits are not in rev-list newest-to-oldest order:\n%s", notes)
	}
}

func commitAuthor(t *testing.T, root, sha string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%an", sha)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read commit author: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// PR lookup is enrichment, not a prerequisite: when the configured gh tool can
// identify a direct commit, its metadata accompanies the durable local facts.
func TestReleasePlanEnrichesTrailerlessCommitWithOptionalPRMetadata(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("fake gh command is POSIX shell script")
	}
	s := serviceWithDucklings(t, "pato-uno")
	projectID, root := projectWithDocs(t, s, nil)
	git := gitProject(t, root)
	if err := git.Tag("v0.1.0", "previous release"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "roster.txt"), []byte("flock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	sha, err := git.Commit("Promote Roster as Flock")
	if err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	gh := "#!/bin/sh\nprintf '[{\"number\":5,\"title\":\"Promote Roster as Flock\"}]'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	fake, ok := s.providers["fake"].(*provider.Fake)
	if !ok {
		t.Fatal("test service did not install fake provider")
	}
	fake.ScriptFunc = func(provider.ChatRequest, int) *provider.ChatResponse {
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message: provider.Message{Role: "assistant", Content: "The release prose."}, FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.ReleasePlan(context.Background(), projectID, ReleaseRequest{Bump: "minor"})
	if err != nil {
		t.Fatalf("release planning with available optional PR lookup: %v", err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done

	body, err := os.ReadFile(release.Path(root, release.Version{Major: 0, Minor: 2, Patch: 0}) + ".proposed")
	if err != nil {
		t.Fatal(err)
	}
	want := "- Promote Roster as Flock (" + sha[:7] + ") — " + commitAuthor(t, root, sha) + " [PR #5: Promote Roster as Flock]\n"
	if !strings.Contains(string(body), want) {
		t.Errorf("release did not enrich the trailerless commit as %q:\n%s", want, body)
	}
}

// A Ducklab-Run trailer is not itself evidence of a task item. If its run
// cannot be resolved, it is neither task-backed nor trailerless, so drafting a
// supposedly complete release must stop and identify every omitted commit.
func TestReleasePlanRefusesUnresolvedTrailerCommits(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, root := projectWithDocs(t, s, nil)
	git := gitProject(t, root)
	if err := git.Tag("v0.1.0", "previous release"); err != nil {
		t.Fatal(err)
	}
	commit := func(name, runID string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(runID+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := git.AddAll(); err != nil {
			t.Fatal(err)
		}
		sha, err := git.CommitWithTrailer("unresolved "+runID, map[string]string{"Ducklab-Run": runID})
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	first := commit("first.txt", "r-missing-one")
	second := commit("second.txt", "r-missing-two")

	run, err := s.ReleasePlan(context.Background(), projectID, ReleaseRequest{Bump: "minor"})
	if err == nil {
		s.runsMu.RLock()
		rs := s.runs[run.ID]
		s.runsMu.RUnlock()
		<-rs.done
		t.Fatal("release planning drafted notes despite commits with unresolved Ducklab-Run trailers")
	}
	for _, sha := range []string{first, second} {
		if !strings.Contains(err.Error(), sha) {
			t.Errorf("incompleteness error does not name %s: %v", sha, err)
		}
	}
}
