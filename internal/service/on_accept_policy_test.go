package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
)

// serviceWithAcceptPolicy loads the policy through the same global TOML path
// an engine uses, rather than assigning an implementation field directly.
func serviceWithAcceptPolicy(t *testing.T, globalPolicy string) *Service {
	t.Helper()
	isolate(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	global := config.DefaultGlobal()
	global.Providers = map[config.ProviderID]config.Provider{
		"fake": {Kind: config.ProviderKindOpenAI, BaseURL: "fake://"},
	}
	global.Ducklings = map[config.DucklingID]config.Duckling{
		"scribe": {Provider: "fake", Model: "test"},
	}
	if err := config.SaveGlobal(path, global); err != nil {
		t.Fatal(err)
	}
	if globalPolicy != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("\n[remote]\non_accept = \"" + globalPolicy + "\"\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := config.LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(loaded, Options{Bus: bus.New(16)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func setProjectRemote(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".ducklab", "project.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const header = "[remote]\n"
	at := strings.Index(string(body), header)
	if at < 0 {
		t.Fatal("project fixture lacks [remote]")
	}
	if strings.Contains(string(body[at+len(header):]), "name = \"origin\"") {
		return
	}
	updated := string(body[:at+len(header)]) + "name = \"origin\"\n" + string(body[at+len(header):])
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setProjectAcceptPolicy(t *testing.T, root, policy string) {
	t.Helper()
	path := filepath.Join(root, ".ducklab", "project.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const header = "[remote]\n"
	at := strings.Index(string(body), header)
	if at < 0 {
		t.Fatal("project fixture lacks [remote]")
	}
	updated := string(body[:at+len(header)]) + "on_accept = \"" + policy + "\"\n" + string(body[at+len(header):])
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func acceptIntoRemote(t *testing.T, s *Service, projectPolicy string) (string, string, string) {
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
	run, _ := pausedWorktreeRun(t, s, id, root, "r-on-accept")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "accepted.txt"), []byte("published\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return id, root, result.CommitSHA
}

func gitBranch(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func remoteHasCommit(t *testing.T, remote, branch, sha string) bool {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", remote, "rev-parse", branch).Output()
	return err == nil && strings.TrimSpace(string(out)) == sha
}

func remotePushReceipts(t *testing.T, root string) []map[string]interface{} {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".ducklab", "remote-actions.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var receipts []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var receipt map[string]interface{}
		if err := json.Unmarshal([]byte(line), &receipt); err != nil {
			t.Fatalf("decode remote receipt %q: %v", line, err)
		}
		if receipt["action"] == "push" {
			receipts = append(receipts, receipt)
		}
	}
	return receipts
}

func TestOnAcceptPushHonorsGlobalDefaultAndProjectOverride(t *testing.T) {
	t.Run("global push applies when project leaves policy unset", func(t *testing.T) {
		s := serviceWithAcceptPolicy(t, "push")
		_, root, sha := acceptIntoRemote(t, s, "")
		remote, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !remoteHasCommit(t, strings.TrimSpace(string(remote)), gitBranch(t, root), sha) {
			t.Fatalf("remote master was not pushed to accepted commit %s", sha)
		}
		receipt, err := os.ReadFile(filepath.Join(root, ".ducklab", "remote-actions.jsonl"))
		if err != nil || !strings.Contains(string(receipt), `"action":"push"`) || !strings.Contains(string(receipt), `"status":"pushed"`) {
			t.Fatalf("push receipt = %q, %v", receipt, err)
		}
		receipts := remotePushReceipts(t, root)
		if len(receipts) != 1 || receipts[0]["branch"] != gitBranch(t, root) || receipts[0]["status"] != "pushed" {
			t.Fatalf("push receipts = %#v, want exactly one successful push of the default branch", receipts)
		}
		detail, err := s.RunGet(context.Background(), "r-on-accept")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(detail.Run)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"action":"push"`) || !strings.Contains(string(raw), `"status":"pushed"`) {
			t.Fatalf("run does not carry its push receipt: %s", raw)
		}
	})

	t.Run("project nothing overrides global push", func(t *testing.T) {
		s := serviceWithAcceptPolicy(t, "push")
		_, root, sha := acceptIntoRemote(t, s, "nothing")
		remote, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
		if err != nil {
			t.Fatal(err)
		}
		if remoteHasCommit(t, strings.TrimSpace(string(remote)), gitBranch(t, root), sha) {
			t.Fatalf("remote master advanced to %s despite project on_accept = nothing", sha)
		}
		if receipts := remotePushReceipts(t, root); len(receipts) != 0 {
			t.Fatalf("project on_accept = nothing recorded push receipts: %#v", receipts)
		}
	})

	t.Run("absent policies default to nothing", func(t *testing.T) {
		s := serviceWithAcceptPolicy(t, "")
		_, root, sha := acceptIntoRemote(t, s, "")
		remote, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
		if err != nil {
			t.Fatal(err)
		}
		if remoteHasCommit(t, strings.TrimSpace(string(remote)), gitBranch(t, root), sha) {
			t.Fatalf("remote master advanced to %s with no on_accept policy", sha)
		}
		if receipts := remotePushReceipts(t, root); len(receipts) != 0 {
			t.Fatalf("absent on_accept policy recorded push receipts: %#v", receipts)
		}
	})
}

func TestOnAcceptPolicyRejectsInvalidValuesAtLoad(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			s := serviceWithAcceptPolicy(t, "")
			_, root := projectWithDocs(t, s, nil)
			if scope == "global" {
				path := filepath.Join(t.TempDir(), "config.toml")
				if err := os.WriteFile(path, []byte("schema = 1\n[remote]\non_accept = \"ship\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				_, err := config.LoadGlobal(path)
				if err == nil || !strings.Contains(err.Error(), "nothing | push | pr") {
					t.Fatalf("invalid global on_accept error = %v", err)
				}
				return
			}
			setProjectAcceptPolicy(t, root, "ship")
			_, err := config.LoadProject(filepath.Join(root, ".ducklab", "project.toml"))
			if err == nil || !strings.Contains(err.Error(), "nothing | push | pr") {
				t.Fatalf("invalid project on_accept error = %v", err)
			}
		})
	}
}

func TestReleaseCutPushesCertifiedTagWhenOnAcceptIsPush(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "push")
	id, root := projectWithDocs(t, s, nil)
	setProjectRemote(t, root)
	gitProject(t, root)
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
	version := "v0.9.1"
	draft := filepath.Join(root, ".ducklab", "docs", "releases", version+".md.proposed")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte("# Release "+version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseCut(context.Background(), id, version); err != nil {
		t.Fatalf("release cut: %v", err)
	}
	if out, err := exec.Command("git", "--git-dir", remote, "rev-parse", "refs/tags/"+version).CombinedOutput(); err != nil {
		t.Fatalf("certified tag %s was not pushed: %v: %s", version, err, out)
	}
}

func TestOnAcceptPushFailureLeavesAcceptedRunWithExactWarning(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "push")
	id, root, sha := acceptIntoRemote(t, s, "")
	// Break the remote only after the initial branch has established it.
	if out, err := exec.Command("git", "-C", root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")).CombinedOutput(); err != nil {
		t.Fatalf("break remote: %v: %s", err, out)
	}

	// A second accepted change attempts publication and must not undo acceptance.
	run, _ := pausedWorktreeRun(t, s, id, root, "r-on-accept-push-failure")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "failed-push.txt"), []byte("still accepted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("push failure must not fail accept: %v", err)
	}
	if result.CommitSHA == sha {
		t.Fatal("second accept did not create a commit")
	}
	wantPrefix := "committed as " + result.CommitSHA + "; push failed: "
	if !strings.HasPrefix(result.Warning, wantPrefix) || len(result.Warning) == len(wantPrefix) {
		t.Fatalf("accept warning = %q, want %q followed by the push failure reason", result.Warning, wantPrefix)
	}
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Run.Accepted || detail.Run.Status != "done" || detail.Run.CommitSHA != result.CommitSHA {
		t.Fatalf("failed publication contaminated acceptance: %+v", detail.Run)
	}
	if detail.Run.Warning != result.Warning {
		t.Fatalf("stored warning = %q, result warning = %q", detail.Run.Warning, result.Warning)
	}
}

// Major 2: baseBranchForPush must resolve the same default branch acceptance
// advances (origin/HEAD, else main/master), never silently substitute an
// unrelated current checkout branch. With a checkout parked on a feature
// branch, publication must still target the default branch.
func TestBaseBranchForPushResolvesDefaultBranchNotCheckout(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "push")
	id, root := projectWithDocs(t, s, nil)
	setProjectRemote(t, root)
	gitProject(t, root)
	p, err := s.remoteProject(id)
	if err != nil {
		t.Fatal(err)
	}
	defaultName := gitBranch(t, root)
	if _, err := exec.Command("git", "-C", root, "checkout", "-b", "ducklab/feature").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	if got := s.baseBranchForPush(p); got != defaultName {
		t.Fatalf("baseBranchForPush = %q, want the default branch %q (not the feature checkout)", got, defaultName)
	}
}

// Major 1: publication lives in acceptRun's common success path, so an accept
// that settles through acceptRun applies the on_accept push policy exactly
// once, with the default branch in the receipt.
func TestInternalAcceptPathPublishesThroughAcceptRun(t *testing.T) {
	s := serviceWithAcceptPolicy(t, "push")
	id, root := projectWithDocs(t, s, nil)
	setProjectRemote(t, root)
	gitProject(t, root)
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
	if _, err := exec.Command("git", "-C", root, "checkout", "-b", "ducklab/feature").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	run, _ := pausedWorktreeRun(t, s, id, root, "r-internal-path")
	if err := os.WriteFile(filepath.Join(run.WorktreePath, "internal.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAccept(context.Background(), run.ID, ""); err != nil {
		t.Fatalf("accept: %v", err)
	}
	receipts := remotePushReceipts(t, root)
	if len(receipts) != 1 {
		t.Fatalf("accept published %d times, want exactly once: %#v", len(receipts), receipts)
	}
	if receipts[0]["status"] != "pushed" {
		t.Fatalf("push status = %v, want pushed", receipts[0]["status"])
	}
	if receipts[0]["branch"] != branch {
		t.Fatalf("pushed branch = %v, want the default branch %q despite the feature checkout", receipts[0]["branch"], branch)
	}
}
