package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/release"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

func runlogEventsOf(root, runID string) ([]*runlog.Event, error) {
	return runlog.ReadEvents(filepath.Join(root, ".ducklab", "runs", runID))
}

func releaseVersionForTest(t *testing.T, v string) release.Version {
	t.Helper()
	parsed, ok := release.ParseVersion(v)
	if !ok {
		t.Fatalf("parse version %q", v)
	}
	return parsed
}

func releaseDraftPath(t *testing.T, dir string, v release.Version) string {
	t.Helper()
	draft := release.Path(dir, v) + ".proposed"
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	return draft
}

// acceptIntoRemoteWith is acceptIntoRemote with a hook between project setup
// and the accept, for tests that must shape the project (pr_tool, github repo)
// before publication runs.
func acceptIntoRemoteWith(t *testing.T, s *Service, projectPolicy string, before func(id, root string)) (string, string, string, string) {
	t.Helper()
	id, root := projectWithDocs(t, s, nil)
	setProjectRemote(t, root)
	gitProject(t, root)
	if projectPolicy != "" {
		setProjectAcceptPolicy(t, root, projectPolicy)
	}
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare remote: %v: %s", err, out)
	}
	branch := gitBranch(t, root)
	for _, args := range [][]string{{"remote", "add", "origin", remote}, {"push", "-u", "origin", branch}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if before != nil {
		before(id, root)
	}
	run, _ := pausedWorktreeRun(t, s, id, root, "r-on-accept")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "accepted.txt"), []byte("published\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return id, root, remote, result.CommitSHA
}

// shipHarnessRecords makes a fixture project opt into tracking its receipts
// and bug audit trail, the way ducklab's own .gitignore does (negations after
// the runs rule), and commits that choice so the tree is clean.
func shipHarnessRecords(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".gitignore")
	body, _ := os.ReadFile(path)
	// Git cannot re-include a file under an excluded DIRECTORY, so the runs
	// rule must exclude the directory's contents, exactly as ducklab's own
	// .gitignore does, before the receipt negation can take effect.
	base := strings.ReplaceAll(string(body), ".ducklab/runs/\n", ".ducklab/runs/*\n")
	updated := base + "\n!.ducklab/runs/*/\n.ducklab/runs/*/*\n!.ducklab/runs/*/receipt.json\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", ".gitignore"}, {"commit", "-q", "-m", "ship harness records"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func remoteReceiptsOf(t *testing.T, root, action string) []map[string]interface{} {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".ducklab", "remote-actions.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var r map[string]interface{}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r["action"] == action {
			out = append(out, r)
		}
	}
	return out
}

// fakeGH puts an executable `gh` first in PATH whose `pr list` answers with
// the given open pull requests and whose `pr create` answers with created.
func fakeGH(t *testing.T, listJSON string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1 $2\" in\n  'auth status') exit 0 ;;\n  'pr list') printf '%s' '" + listJSON + "' ;;\n  'pr create') echo https://github.com/example/repo/pull/99 ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// B-289: under on_accept = pr the card promised "pushes a branch, and opens or
// updates a pull request" and the engine returned early. Now the run's branch
// is published at the landed commit, the default branch stays local, and the
// receipt says what happened.
func TestOnAcceptPRPublishesTheRunBranchAndRecordsAPullRequestReceipt(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "pr")
	_, root, remote, sha := acceptIntoRemoteWith(t, s, "", func(id, root string) {
		if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"github.pr_tool": "none", "github.repo": "example/repo"}); err != nil {
			t.Fatal(err)
		}
	})
	detail, err := s.RunGet(context.Background(), "r-on-accept")
	if err != nil {
		t.Fatal(err)
	}
	branch := detail.Run.Branch
	if branch == "" {
		t.Fatal("accepted run records no branch")
	}
	if !remoteHasCommit(t, remote, branch, sha) {
		t.Fatalf("remote %s was not pushed to the landed commit %s", branch, sha)
	}
	if vcs.New(root).BranchContains("origin/"+gitBranch(t, root), sha) {
		t.Fatal("pr policy must not publish the default branch itself")
	}
	receipts := remoteReceiptsOf(t, root, "pr")
	if len(receipts) != 1 || receipts[0]["status"] != "compare_url" || receipts[0]["branch"] != branch {
		t.Fatalf("pr receipts = %#v, want one compare_url receipt for %s", receipts, branch)
	}
	if detail.Run.Warning != "" {
		t.Fatalf("a published pr must not warn: %q", detail.Run.Warning)
	}
	raw, _ := json.Marshal(detail.Run)
	if !strings.Contains(string(raw), `"action":"pr"`) || !strings.Contains(string(raw), `"status":"compare_url"`) {
		t.Fatalf("run does not carry its pr receipt: %s", raw)
	}
}

// "Opens or updates": a pull request already open for the branch is updated
// by the push, not shadowed by a failed second create.
func TestOnAcceptPRUpdatesAnOpenPullRequestOrOpensOne(t *testing.T) {
	for _, tc := range []struct{ name, list, want string }{
		{"updates the open one", `[{"url":"https://github.com/example/repo/pull/7"}]`, "updated"},
		{"opens one when none is open", `[]`, "created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeGH(t, tc.list)
			s := serviceWithAcceptPolicy(t, "pr")
			_, root, _, _ := acceptIntoRemoteWith(t, s, "", func(id, root string) {
				if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"github.repo": "example/repo"}); err != nil {
					t.Fatal(err)
				}
			})
			receipts := remoteReceiptsOf(t, root, "pr")
			if len(receipts) != 1 || receipts[0]["status"] != tc.want || receipts[0]["pr_url"] == "" {
				t.Fatalf("pr receipts = %#v, want one %s receipt with a pr_url", receipts, tc.want)
			}
		})
	}
}

func TestOnAcceptPRFailureLeavesAcceptedRunWithExactWarning(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "pr")
	id, root, _, sha := acceptIntoRemoteWith(t, s, "", func(id, root string) {
		if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{"github.pr_tool": "none", "github.repo": "example/repo"}); err != nil {
			t.Fatal(err)
		}
	})
	if out, err := exec.Command("git", "-C", root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")).CombinedOutput(); err != nil {
		t.Fatalf("break remote: %v: %s", err, out)
	}
	run, _ := pausedWorktreeRun(t, s, id, root, "r-on-accept-pr-failure")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "failed-pr.txt"), []byte("still accepted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("pr failure must not fail accept: %v", err)
	}
	if result.CommitSHA == sha {
		t.Fatal("second accept did not create a commit")
	}
	wantPrefix := "committed as " + result.CommitSHA + "; pr failed: "
	if !strings.HasPrefix(result.Warning, wantPrefix) || len(result.Warning) == len(wantPrefix) {
		t.Fatalf("accept warning = %q, want %q followed by the reason", result.Warning, wantPrefix)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Run.Accepted || detail.Run.Status != "done" || detail.Run.CommitSHA != result.CommitSHA {
		t.Fatalf("failed publication contaminated acceptance: %+v", detail.Run)
	}
}

// B-291 (scope A): the harness's own record of an acceptance has a commit
// owner. Right after the landing, the run's receipt lands in a worded commit
// of its own, marked Ducklab-Record, and the release inventory never counts
// it as delivered work. Churn this run did not make stays for the sweep.
func TestAcceptRecordsItsReceiptInAFollowingCommit(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "")
	id, root, _, sha := acceptIntoRemoteWith(t, s, "", func(id, root string) {
		shipHarnessRecords(t, root)
		// Unrelated bug-ladder churn the engine dirtied before this accept.
		audit := filepath.Join(root, ".ducklab", "bugs", "audit.jsonl")
		if err := os.MkdirAll(filepath.Dir(audit), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(audit, []byte(`{"bug":"B-9","from":"open","to":"triaged","actor":"human","ts":"2026-09-09T00:00:00Z","via":"move"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	git := vcs.New(root)
	head, err := git.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if head == sha {
		t.Fatal("no record commit followed the landing commit")
	}
	parent, err := git.ParentSHA(head)
	if err != nil || parent != sha {
		t.Fatalf("record commit parent = %s (%v), want the landing commit %s", parent, err, sha)
	}
	out, err := exec.Command("git", "-C", root, "show", "--format=%s%n%b", "--name-only", head).Output()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.HasPrefix(text, "ducklab: record r-on-accept (") {
		t.Fatalf("record commit subject = %q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "Ducklab-Record: r-on-accept") {
		t.Fatalf("record commit lacks its trailer:\n%s", text)
	}
	if !strings.Contains(text, ".ducklab/runs/r-on-accept/receipt.json") {
		t.Fatalf("record commit does not carry the receipt:\n%s", text)
	}
	if strings.Contains(text, "audit.jsonl") {
		t.Fatalf("record commit swept churn this run did not make:\n%s", text)
	}
	items, landed, _, _, err := s.releaseInventory(context.Background(), id, root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range landed {
		if l.SHA == head {
			t.Fatalf("release inventory counts the record commit as landed work: %+v", l)
		}
	}
	found := false
	for _, it := range items {
		if it.CommitSHA == sha {
			found = true
		}
	}
	if !found {
		t.Fatalf("release inventory lost the task's own commit %s: %+v", sha, items)
	}
}

// Under push the record rides the same push: the remote default branch ends
// at the record commit, which descends from the landing commit.
func TestOnAcceptPushPublishesTheRecordCommitToo(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "push")
	_, root, remote, sha := acceptIntoRemoteWith(t, s, "", func(id, root string) { shipHarnessRecords(t, root) })
	head, _ := vcs.New(root).HeadSHA()
	if head == sha || !remoteHasCommit(t, remote, gitBranch(t, root), head) {
		t.Fatalf("remote default branch is not at the record commit %s (landing %s)", head, sha)
	}
}

// The release cut is the sweep's owner: harness state the engine dirtied
// since the last release rides the release commit and the message says so.
func TestReleaseCutSweepsHarnessStateAndNamesIt(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	shipHarnessRecords(t, dir)
	audit := filepath.Join(dir, ".ducklab", "bugs", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(audit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audit, []byte(`{"bug":"B-1","from":"open","to":"triaged","actor":"human","ts":"2026-09-09T00:00:00Z","via":"move"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	version := releaseVersionForTest(t, "v0.9.1")
	draft := releaseDraftPath(t, dir, version)
	if err := os.WriteFile(draft, []byte("# Release v0.9.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cut, err := s.ReleaseCut(context.Background(), projectID, version.String())
	if err != nil {
		t.Fatalf("release cut: %v", err)
	}
	commit := cut["commit"].(string)
	out, err := exec.Command("git", "-C", dir, "show", "--format=%s%n%b", "--name-only", commit).Output()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, ".ducklab/bugs/audit.jsonl") {
		t.Fatalf("release commit did not sweep the audit trail:\n%s", text)
	}
	if !strings.Contains(text, "Sweeps 1 harness state file(s) under .ducklab") {
		t.Fatalf("release commit message does not name the sweep:\n%s", text)
	}
	if head, _ := git.HeadSHA(); head != commit {
		t.Fatalf("HEAD %s != release commit %s", head, commit)
	}
}

// A project whose .gitignore keeps run receipts out of the tree has nothing to
// record: the accept says so on its stream and never warns.
func TestAcceptSkipsTheRecordCommitWhenReceiptsAreIgnored(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "")
	_, root, _, sha := acceptIntoRemoteWith(t, s, "", nil)
	head, _ := vcs.New(root).HeadSHA()
	if head != sha {
		t.Fatalf("a record commit %s landed although receipts are ignored", head)
	}
	events, err := runlogEventsOf(root, "r-on-accept")
	if err != nil {
		t.Fatal(err)
	}
	skipped, warned := false, false
	for _, e := range events {
		if e.Type == "record_skipped" {
			skipped = true
		}
		if e.Type == "warning" && strings.Contains(fmt.Sprint(e.Data["detail"]), "record") {
			warned = true
		}
	}
	if !skipped || warned {
		t.Fatalf("skipped=%v warned=%v, want a declared skip and no warning", skipped, warned)
	}
}
