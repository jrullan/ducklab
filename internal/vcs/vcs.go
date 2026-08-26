// Package vcs handles all git operations. Models never call these directly;
// only the orchestrator does.
package vcs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jrullan/ducklab/internal/xplat"
)

// Git wraps git operations for a repository.
type Git struct {
	Root string
}

// New creates a new Git for the given root.
func New(root string) *Git {
	return &Git{Root: root}
}

func (g *Git) run(args ...string) (string, error) {
	return g.runEnv(nil, args...)
}

func (g *Git) runEnv(env map[string]string, args ...string) (string, error) {
	cmdLine := "git " + strings.Join(args, " ")
	var commandEnv []string
	if env != nil {
		commandEnv = os.Environ()
		for i, entry := range commandEnv {
			key, _, ok := strings.Cut(entry, "=")
			if value, replace := env[key]; ok && replace {
				commandEnv[i] = key + "=" + value
				delete(env, key)
			}
		}
		for key, value := range env {
			commandEnv = append(commandEnv, key+"="+value)
		}
	}
	cmd := xplat.Shell(g.Root, commandEnv, cmdLine)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// HasGit returns whether the root is inside a git repository.
func (g *Git) HasGit() bool {
	_, err := g.run("rev-parse", "--git-dir")
	return err == nil
}

// Init creates the repository and gives it a first commit.
//
// A repo with no commits has no HEAD, and every run that asks for a diff dies
// on "fatal: ambiguous argument 'HEAD'" after the ducklings have already done
// the work. `project init --git-init` used to leave exactly that: a project
// that looked ready and could not complete a single run.
func (g *Git) Init() error {
	if _, err := g.run("init"); err != nil {
		return err
	}
	if sha, err := g.HeadSHA(); err == nil && strings.TrimSpace(sha) != "" {
		return nil // an existing history; leave it alone
	}
	if _, err := g.run("add", "-A"); err != nil {
		return err
	}
	// --allow-empty so a brand-new directory still gets a root commit; without
	// it an empty repo stays HEAD-less and the problem survives.
	//
	// shellEscape because run joins its arguments into one command line: an
	// unquoted message loses everything after the first space.
	if _, err := g.run("commit", "--allow-empty", "-m",
		shellEscape("Initial commit (created by ducklab project init)")); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}
	return nil
}

// CurrentBranch returns the current branch name.
func (g *Git) CurrentBranch() (string, error) {
	out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// ParentSHA returns the first parent of a commit.
func (g *Git) ParentSHA(sha string) (string, error) {
	out, err := g.run("rev-parse", sha+"^1")
	return strings.TrimSpace(out), err
}

// HeadSHA returns the HEAD commit SHA.
func (g *Git) HeadSHA() (string, error) {
	out, err := g.run("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// HeadHasTrailer reports whether HEAD carries the exact trailer written for a
// run acceptance commit. It distinguishes a retry's existing commit from an
// otherwise clean worktree.
func (g *Git) HeadHasTrailer(key, value string) (bool, error) {
	out, err := g.run("log", "-1", "--format="+shellEscape("%B"))
	if err != nil {
		return false, err
	}
	needle := key + ": " + value
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == needle {
			return true, nil
		}
	}
	return false, nil
}

// GitPath resolves a git-internal path for this worktree. Linked worktrees use
// a .git file, so callers must not assume .git is a directory.
func (g *Git) GitPath(name string) (string, error) {
	out, err := g.run("rev-parse", "--git-path", name)
	return strings.TrimSpace(out), err
}

// UncommitOwn undoes a commit this process made a moment ago, keeping its
// changes in the working tree and index. It refuses unless sha is HEAD: the
// only commit that is safe to take back is the one nobody could have built
// on yet. History moves back one step; the diff stays where the person and
// the next accept can see it.
func (g *Git) UncommitOwn(sha string) error {
	head, err := g.HeadSHA()
	if err != nil {
		return fmt.Errorf("read HEAD before uncommit: %w", err)
	}
	if head != strings.TrimSpace(sha) {
		return fmt.Errorf("HEAD is %s, not %s; another commit landed on top", head[:min(8, len(head))], sha[:min(8, len(sha))])
	}
	_, err = g.run("reset", "--soft", "HEAD~1")
	return err
}

// IsClean returns whether the working tree is clean.
func (g *Git) IsClean() (bool, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// DirtyPaths lists what `git status --porcelain` reports, one path per
// entry. For telling a person WHICH files block a clean-tree guard — "the
// tree has uncommitted changes" sent them to a terminal to find out what.
func (g *Git) DirtyPaths() []string {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if len(l) > 3 {
			paths = append(paths, strings.TrimSpace(l[3:]))
		}
	}
	return paths
}

// PathIsCommitted reports whether a path matches what is committed.
//
// False for anything git does not know about, and for anything changed since
// the last commit. Used to tell an accepted skill from one this run just
// wrote (05 §7.1).
//
// A project with no git has nothing to accept against, so everything there
// counts as committed: refusing every skill in a repo someone is trying out
// would break the feature to enforce a gate that does not exist.
func (g *Git) PathIsCommitted(path string) bool {
	if !g.HasGit() {
		return true
	}
	out, err := g.run("status", "--porcelain", "--", path)
	if err != nil {
		return true
	}
	return strings.TrimSpace(out) == ""
}

// CreateBranch creates and checks out a new branch from the current HEAD.
func (g *Git) CreateBranch(name string) error {
	_, err := g.run("checkout", "-b", name)
	return err
}

// Checkout checks out a branch.
func (g *Git) Checkout(name string) error {
	_, err := g.run("checkout", name)
	return err
}

// Add stages files.
func (g *Git) Add(paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, err := g.run(args...)
	return err
}

// AddAll stages all changes.
func (g *Git) AddAll() error {
	return g.AddAllExcluding()
}

// AddAllExcluding stages all changes except runtime-only paths. Callers use it
// for linked dependency trees that are available in an isolated checkout but
// must never become repository content.
//
// Do not put exclusions in the add pathspec. A linked dependency may be an
// ignored symlink, and git then errors before the exclusion can take effect.
// Stage the tree without pathspecs (ignored files are still ignored by git),
// then explicitly remove the runtime paths from the index.
func (g *Git) AddAllExcluding(excluded ...string) error {
	if _, err := g.run("add", "-A"); err != nil {
		return err
	}
	// Render output is evidence, never candidate work, even when a caller did
	// not provide the run's dependency exclusions.
	paths := uniquePaths(append(excluded, ".ducklab-render-captures"))
	if len(paths) == 0 {
		return nil
	}
	_, err := g.run(append([]string{"rm", "-r", "--cached", "--ignore-unmatch", "--"}, paths...)...)
	return err
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimSuffix(path, "/")
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// Clean discards tracked and untracked working-tree changes.
func (g *Git) Clean() error {
	if _, err := g.run("reset", "--hard", "HEAD"); err != nil {
		return err
	}
	_, err := g.run("clean", "-fd")
	return err
}

// Commit creates a commit.
//
// The message is escaped because run joins its arguments into one shell
// command line: an unquoted "ducklab: release v0.1.0" reached git as four
// pathspecs and the commit failed. CommitWithTrailer had always escaped;
// this one had not, and the tests that used it discarded the error.
func (g *Git) Commit(message string) (string, error) {
	_, err := g.run("commit", "-m", shellEscape(message))
	if err != nil {
		return "", err
	}
	return g.HeadSHA()
}

// CommitWithTrailer creates a commit with trailers.
func (g *Git) CommitWithTrailer(message string, trailers map[string]string) (string, error) {
	args := []string{"commit", "-m", shellEscape(message)}
	for k, v := range trailers {
		args = append(args, "-m", shellEscape(fmt.Sprintf("%s: %s", k, v)))
	}
	_, err := g.run(args...)
	if err != nil {
		return "", err
	}
	return g.HeadSHA()
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// DiffBetween returns the canonical binary diff between two commits.
func (g *Git) DiffBetween(base, head string) ([]byte, error) {
	cmd := exec.Command("git", "-c", "color.ui=false", "-c", "diff.external=", "diff", "--no-ext-diff", "--binary", base, head)
	cmd.Dir = g.Root
	return cmd.Output()
}

// DiffSHA256 returns the SHA-256 of a canonical binary commit diff.
func (g *Git) DiffSHA256(base, head string) (string, error) {
	data, err := g.DiffBetween(base, head)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ChangedPaths lists paths changed between two revisions.
func (g *Git) ChangedPaths(base, head string) ([]string, error) {
	out, err := g.run("diff", "--name-only", base, head)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// Diff returns the working tree diff, including files git has never seen.
//
// `git diff HEAD` alone shows nothing for a file that was created rather than
// edited, so a run that adds files recorded an empty diff: a split integrated
// two new files, passed its gate, and left diff.patch at zero bytes while the
// desktop said "No changes yet." on work that had changed everything.
//
// The intent-to-add is what makes them visible. DiffAgainst already did this
// and said why; this one did not.
func (g *Git) Diff() (string, error) {
	return g.DiffExcluding()
}

// DiffExcluding returns the working-tree diff while excluding generated paths.
// Dependency links in an isolated checkout are runtime inputs, not candidate
// changes, even when a project does not ignore their directory.
func (g *Git) DiffExcluding(excluded ...string) (string, error) {
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	// The harness's own state never rides a task's diff. project.toml is a
	// tracked file, and flipping the project's autonomy in settings dirtied
	// it — T-097's reviewer then flagged, CRITICAL, that the diff "loosens
	// harness safety constraints without task justification". It was right
	// about everything except whose change it was.
	paths := []string{".", ":^.ducklab", ":^.ducklab-render-captures"}
	for _, path := range uniquePaths(excluded) {
		paths = append(paths, ":^"+path)
	}
	return g.run(append([]string{"diff", "HEAD", "--"}, paths...)...)
}

// DiffStat returns the diff stat, including untracked files.
func (g *Git) DiffStat() (string, error) {
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	return g.run("diff", "--stat", "HEAD", "--", ".", ":^.ducklab")
}

// worktreeLocks protects git's shared .git/worktrees metadata. Git does not
// serialize concurrent worktree add/remove/prune commands itself.
var worktreeLocks sync.Map // map[string]*sync.Mutex, keyed by canonical repository path

func worktreeLock(root string) *sync.Mutex {
	key, err := filepath.Abs(root)
	if err != nil {
		key = filepath.Clean(root)
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	lock, _ := worktreeLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// WorktreeAdd creates a worktree.
func (g *Git) WorktreeAdd(path, branch string) error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "add", path, "-b", branch)
	return err
}

// WorktreeAddForce reattaches a branch whose registered worktree directory
// disappeared. --force is deliberately limited to this recovery path.
func (g *Git) WorktreeAddForce(path, branch string) error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "add", "--force", path, branch)
	return err
}

// WorktreeAddAt creates a branch worktree at rev. The caller supplies the
// exact base rather than inheriting the current checkout's potentially dirty HEAD.
func (g *Git) WorktreeAddAt(path, branch, rev string) error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "add", "-b", branch, path, rev)
	return err
}

// defaultBranchRef returns the local ref that acceptance may advance. Remote
// origin/HEAD identifies the default by name, but is not itself mergeable.
func (g *Git) defaultBranchRef() (string, error) {
	out, err := g.run("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil && strings.TrimSpace(out) != "" {
		name := strings.TrimPrefix(strings.TrimSpace(out), "origin/")
		ref := "refs/heads/" + name
		if _, err := g.revParse(ref); err == nil {
			return ref, nil
		}
	}
	// Local repositories commonly have no origin. Prefer the conventional
	// default names rather than accidentally accepting into a feature checkout.
	for _, ref := range []string{"refs/heads/main", "refs/heads/master"} {
		if _, err := g.revParse(ref); err == nil {
			return ref, nil
		}
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return "", err
	}
	return "refs/heads/" + branch, nil
}

// TrailerCommitOnDefault finds the newest commit reachable from the default
// branch whose commit message contains the exact run trailer.
func (g *Git) TrailerCommitOnDefault(key, value string) (string, error) {
	ref, err := g.defaultBranchRef()
	if err != nil {
		return "", err
	}
	out, err := g.run("log", ref, "--format=%H%x1f%B%x1e")
	if err != nil {
		return "", err
	}
	needle := key + ": " + value
	for _, record := range strings.Split(out, "\x1e") {
		parts := strings.SplitN(record, "\x1f", 2)
		if len(parts) < 2 {
			continue
		}
		for _, line := range strings.Split(parts[1], "\n") {
			if strings.TrimSpace(line) == needle {
				return strings.TrimSpace(parts[0]), nil
			}
		}
	}
	return "", nil
}

// IsReachableFromDefault verifies that a commit exists and is an ancestor of
// the configured default branch. It deliberately reports a useful distinction
// to callers rather than allowing arbitrary object ids to become evidence.
func (g *Git) IsReachableFromDefault(sha string) error {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return fmt.Errorf("landing commit sha is required")
	}
	ref, err := g.defaultBranchRef()
	if err != nil {
		return err
	}
	if _, err := g.revParse(sha + "^{commit}"); err != nil {
		return fmt.Errorf("landing commit %q does not exist", sha)
	}
	if _, err := g.run("merge-base", "--is-ancestor", sha, ref); err != nil {
		return fmt.Errorf("landing commit %q is not reachable from the default branch", sha)
	}
	return nil
}

// DefaultBranchHead returns the configured default branch's current commit.
func (g *Git) DefaultBranchHead() (string, error) {
	ref, err := g.defaultBranchRef()
	if err != nil {
		return "", err
	}
	return g.revParse(ref)
}

// OnDefaultBranch reports whether this checkout is currently on the branch
// that acceptance will advance. It must be checked before that ref moves.
func (g *Git) OnDefaultBranch() (bool, error) {
	ref, err := g.defaultBranchRef()
	if err != nil {
		return false, err
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return false, err
	}
	return ref == "refs/heads/"+branch, nil
}

func (g *Git) revParse(rev string) (string, error) {
	out, err := g.run("rev-parse", rev)
	return strings.TrimSpace(out), err
}

// RebaseOnto rebases the current worktree branch onto rev. ConflictedPaths
// is non-empty only when git left the rebase stopped on conflicts.
func (g *Git) RebaseOnto(rev string) ([]string, error) {
	if _, err := g.run("rebase", rev); err != nil {
		out, _ := g.run("diff", "--name-only", "--diff-filter=U")
		var paths []string
		for _, path := range strings.Split(strings.TrimSpace(out), "\n") {
			if path = strings.TrimSpace(path); path != "" {
				paths = append(paths, path)
			}
		}
		return paths, err
	}
	return nil, nil
}

// AbortIntegration clears a rebase or merge left by an interrupted operation.
func (g *Git) AbortIntegration() {
	_, _ = g.run("rebase", "--abort")
	_, _ = g.run("merge", "--abort")
}

// PathsAreClean reports whether none of paths has staged, unstaged, or
// untracked changes in this checkout.
func (g *Git) PathsAreClean(paths []string) (bool, error) {
	if len(paths) == 0 {
		return true, nil
	}
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err := g.run(args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// SyncPathsToRevision updates paths from rev without disturbing local changes
// outside them. It is used after update-ref has advanced a branch but left this
// checkout's index and working files at the old commit.
func (g *Git) SyncPathsToRevision(rev string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"checkout", rev, "--"}, paths...)
	_, err := g.run(args...)
	return err
}

// FastForwardDefault advances the default branch only when branch descends
// from the observed default commit. update-ref's old value makes a concurrent
// advance fail rather than forcing over it, and leaves the person's checkout
// (which may be on another branch) untouched.
func (g *Git) FastForwardDefault(branch, expectedDefault string) error {
	ref, err := g.defaultBranchRef()
	if err != nil {
		return err
	}
	if _, err := g.run("merge-base", "--is-ancestor", expectedDefault, branch); err != nil {
		return fmt.Errorf("refuse non-fast-forward merge: %w", err)
	}
	_, err = g.run("update-ref", ref, branch, expectedDefault)
	return err
}

// WorktreeAddDetached checks out a commit without changing a branch. It is
// used when a caller must verify precisely what a commit contains, rather than
// the potentially dirtier working tree that produced it.
func (g *Git) WorktreeAddDetached(path, rev string) error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "add", "--detach", path, rev)
	return err
}

// WorktreeRemove removes a worktree.
func (g *Git) WorktreeRemove(path string) error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "remove", "--force", path)
	return err
}

// WorktreeList lists worktrees.
func (g *Git) WorktreeList() ([]string, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			result = append(result, strings.TrimPrefix(line, "worktree "))
		}
	}
	return result, nil
}

// FileTree returns a depth-limited tree of tracked files.
func (g *Git) FileTree(maxEntries int) (string, error) {
	out, err := g.run("ls-files")
	if err != nil {
		return "", err
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	if len(files) > maxEntries {
		return fmt.Sprintf("%d files (truncated)", len(files)), nil
	}
	return strings.Join(files, "\n"), nil
}

// EnsureGitignore ensures .gitignore contains the given entries.
func EnsureGitignore(root string, entries []string) error {
	path := filepath.Join(root, ".gitignore")
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	var toAdd []string
	for _, e := range entries {
		if !strings.Contains(existing, e) {
			toAdd = append(toAdd, e)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	content := existing
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// EnsureGitattributes ensures .gitattributes contains the given entries.
func EnsureGitattributes(root string, entries []string) error {
	path := filepath.Join(root, ".gitattributes")
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	var toAdd []string
	for _, e := range entries {
		if !strings.Contains(existing, e) {
			toAdd = append(toAdd, e)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	content := existing
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// ApplyPatch applies a unified diff to the working tree.
//
// This is how a tournament winner is applied: the candidate's patch is written
// to the main tree byte-for-byte. No model is involved, and nothing is
// regenerated — on modest models, free regeneration corrupts working code
// (I8).
func (g *Git) ApplyPatch(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return fmt.Errorf("empty patch")
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = g.Root
	cmd.Stdin = strings.NewReader(ensureTrailingNewline(patch))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Tags lists the repository's tags.
func (g *Git) Tags() ([]string, error) {
	out, err := g.run("tag", "--list")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

// RevListAfter lists the commits reachable from HEAD but not from a ref.
func (g *Git) RevListAfter(ref string) ([]string, error) {
	out, err := g.run("rev-list", ref+"..HEAD")
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			shas = append(shas, sha)
		}
	}
	return shas, nil
}

// Fetch refreshes one configured remote. Callers must make this an explicit or opted-in action.
func (g *Git) Fetch(remote string) error {
	_, err := g.run("fetch", shellEscape(remote))
	return err
}

// Push sends branch to a configured remote. Authentication is deliberately
// delegated to git's credential helper; no credential enters Ducklab.
func (g *Git) Push(remote, branch string) error {
	_, err := g.run("push", shellEscape(remote), shellEscape(branch))
	return err
}

// FastForwardOnly advances the current branch only when no merge commit or
// conflict resolution is required.
func (g *Git) FastForwardOnly(ref string) error {
	_, err := g.run("merge", "--ff-only", shellEscape(ref))
	return err
}

// RemoteURL returns the configured URL without exposing any credential.
func (g *Git) RemoteURL(remote string) (string, error) {
	out, err := g.run("remote", "get-url", shellEscape(remote))
	return strings.TrimSpace(out), err
}

var gitObjectSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

func safeGitObjectSHA(sha string) (string, error) {
	if !gitObjectSHA.MatchString(sha) {
		return "", fmt.Errorf("invalid git object sha %q", sha)
	}
	return shellEscape(sha), nil
}

// RemoteContains reports whether sha is reachable from a ref under remote.
func (g *Git) RemoteContains(remote, sha string) (bool, error) {
	sha, err := safeGitObjectSHA(sha)
	if err != nil {
		return false, err
	}
	out, err := g.run("branch", "-r", "--contains", sha)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), remote+"/") {
			return true, nil
		}
	}
	return false, nil
}

// AnyBranchContains reports whether sha remains reachable from any local or remote branch.
func (g *Git) AnyBranchContains(sha string) (bool, error) {
	sha, err := safeGitObjectSHA(sha)
	if err != nil {
		return false, err
	}
	out, err := g.run("branch", "-a", "--contains", sha)
	return strings.TrimSpace(out) != "", err
}

// AheadBehind reports HEAD's divergence from the remote tracking ref for branch.
func (g *Git) AheadBehind(remote, branch string) (ahead, behind int, err error) {
	ref := shellEscape(remote + "/" + branch + "...HEAD")
	out, err := g.run("rev-list", "--left-right", "--count", ref)
	if err != nil {
		return 0, 0, err
	}
	if _, err = fmt.Sscanf(strings.TrimSpace(out), "%d %d", &behind, &ahead); err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// CherryPick applies a recorded commit as a new commit on the current branch.
func (g *Git) CherryPick(sha string) (string, error) {
	sha, err := safeGitObjectSHA(sha)
	if err != nil {
		return "", err
	}
	// A cherry-pick normally uses the current wall-clock time for the
	// committer date. Pin both identities and the author date from the orphan
	// so an otherwise identical recovery recreates its original object ID.
	metadata, err := g.run("show", "-s", "--format=%aI%x00%an%x00%ae%x00%cI%x00%cn%x00%ce", sha)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimSuffix(metadata, "\n"), "\x00")
	if len(parts) != 6 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("read author metadata for %s: malformed git output", sha)
	}
	if _, err := g.runEnv(map[string]string{
		"GIT_AUTHOR_DATE":     parts[0],
		"GIT_COMMITTER_DATE":  parts[3],
		"GIT_AUTHOR_NAME":     parts[1],
		"GIT_AUTHOR_EMAIL":    parts[2],
		"GIT_COMMITTER_NAME":  parts[4],
		"GIT_COMMITTER_EMAIL": parts[5],
	}, "cherry-pick", sha); err != nil {
		_, _ = g.run("cherry-pick", "--abort")
		return "", err
	}
	return g.HeadSHA()
}

// RestoreAsFreshCommit applies sha's patch but deliberately records a new commit.
func (g *Git) RestoreAsFreshCommit(sha string) (string, error) {
	sha, err := safeGitObjectSHA(sha)
	if err != nil {
		return "", err
	}
	if _, err := g.run("cherry-pick", "--no-commit", sha); err != nil {
		_, _ = g.run("cherry-pick", "--abort")
		return "", err
	}
	out, err := g.run("show", "-s", "--format=%s", sha)
	if err != nil {
		_, _ = g.run("cherry-pick", "--abort")
		return "", err
	}
	return g.Commit("ducklab: restore " + strings.TrimSpace(out))
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (g *Git) IsAncestor(ancestor, descendant string) (bool, error) {
	_, err := g.run("merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	// git merge-base uses exit status 1 for the ordinary "not an ancestor"
	// result; preserve real invocation errors for callers.
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

// Tag creates an annotated tag on HEAD.
//
// Annotated rather than lightweight: a release is a claim about a moment, and
// an annotated tag records who made it and when. A lightweight tag is a
// pointer with no story.
func (g *Git) Tag(name, message string) error {
	_, err := g.run("tag", "-a", name, "-m", shellEscape(message))
	return err
}

// HasTag reports whether a tag already exists.
func (g *Git) HasTag(name string) bool {
	out, err := g.run("tag", "--list", name)
	return err == nil && strings.TrimSpace(out) != ""
}

// Revert creates a commit that undoes another. --no-edit keeps it
// deterministic — no model, no editor, git's own inverse patch. On conflict
// the revert is aborted so the tree is left exactly as found, and the error
// carries git's account of which files could not be unwound.
func (g *Git) Revert(sha string) (string, error) {
	if _, err := g.run("revert", "--no-edit", sha); err != nil {
		_, _ = g.run("revert", "--abort")
		return "", err
	}
	return g.HeadSHA()
}

// ShowCommit returns the diff a commit introduced.
//
// This is what the review stage reads: a review is of the work that was
// accepted, not of whatever happens to be uncommitted now.
func (g *Git) ShowCommit(sha string) (string, error) {
	return g.run("show", "--format=", "--patch", sha)
}

// DiffAgainst returns the diff of the working tree against a commit, including
// files git does not yet track. A contestant that creates a new file must have
// it captured, or its patch silently loses the work.
func (g *Git) DiffAgainst(ref string) (string, error) {
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	return g.run("diff", ref)
}

// PruneWorktrees removes worktree records whose directories are gone.
// Called at engine start: a killed engine leaves stale entries that make a
// later `worktree add` on the same path fail.
func (g *Git) PruneWorktrees() error {
	lock := worktreeLock(g.Root)
	lock.Lock()
	defer lock.Unlock()
	_, err := g.run("worktree", "prune")
	return err
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// BranchExists reports whether a local branch still exists.
func (g *Git) BranchExists(name string) bool {
	_, err := g.run("show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// DeleteBranch removes a branch, ignoring the case where it is already gone.
func (g *Git) DeleteBranch(name string) error {
	_, err := g.run("branch", "-D", name)
	return err
}

// runWithIndex runs git against a temporary index file, so reading or building
// a tree never disturbs the real one a person may be mid-staging in.
func (g *Git) runWithIndex(indexFile string, args ...string) (string, error) {
	cmd := xplat.Shell(g.Root, nil, "git "+strings.Join(args, " "))
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// harnessExclude keeps ducklab's own record out of snapshots. The run log is
// APPENDED TO while the run executes; a restore that rewound it would destroy
// the evidence of the very failure being cleaned up after.
const harnessExclude = `":^.ducklab"`

// SnapshotTree captures the working tree — tracked and untracked, .gitignore
// respected — as a tree object, without touching the real index or the tree.
//
// Taken at run start so that a run which ends without acceptance can be undone.
// Runs edit the shared working tree live (that is what the gate verifies), and
// commits happen only on accept — so a failed or rejected run used to leave its
// half-made edits sitting in the tree. The next attempt of the same task found
// them, concluded somebody had already fixed it, and both the run and every
// report measuring it were working on a lie.
func (g *Git) SnapshotTree() (string, error) {
	// The name only: git refuses a zero-byte index file, so the path must be
	// free for it to create.
	idx, err := os.CreateTemp("", "ducklab-snap-index-*")
	if err != nil {
		return "", err
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())

	if _, err := g.runWithIndex(idx.Name(), "add", "-A", "--", ".", harnessExclude); err != nil {
		return "", err
	}
	out, err := g.runWithIndex(idx.Name(), "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RestoreTree puts the working tree back to a snapshot. New callers should
// prefer RestoreTreeAtHead so cleanup cannot overwrite commits made after the
// snapshot.
func (g *Git) RestoreTree(snapshot string) error {
	return g.restoreTree(snapshot)
}

// RestoreTreeAtHead restores a snapshot only if HEAD remains the commit that
// was current when it was captured. This prevents cleanup from making the
// working tree older than history that landed while a run was paused.
func (g *Git) RestoreTreeAtHead(snapshot, expectedHead string) error {
	return g.RestoreTreeAtHeadScoped(snapshot, expectedHead, nil)
}

// RestoreTreeAtHeadScoped is RestoreTreeAtHead limited to the paths the run
// is KNOWN to have written (from its own tool calls), when the caller knows
// them.
//
// The snapshot boundary alone cannot tell the run's edits from a person's
// concurrent ones: a reject restored the whole tree to run-start and erased
// engine work a person had written — uncommitted — while the run was going
// (B-077). The run's write set can: whatever the run did not write is not
// the run's to undo. wrote nil means unknown, which keeps the whole-tree
// behaviour for callers without a record.
func (g *Git) RestoreTreeAtHeadScoped(snapshot, expectedHead string, wrote []string) error {
	if strings.TrimSpace(expectedHead) == "" {
		// Runs recorded before TreeSnapshotHead existed cannot distinguish their
		// own changes from later history. Preserve their established cleanup
		// behavior; every newly captured snapshot records expectedHead above.
		return g.restoreTree(snapshot)
	}
	head, err := g.HeadSHA()
	if err != nil {
		return fmt.Errorf("read HEAD before restore: %w", err)
	}
	if head != expectedHead {
		commits, err := g.RevListAfter(expectedHead)
		if err != nil {
			return fmt.Errorf("cannot safely restore: HEAD changed since the run began: %w", err)
		}
		return g.restoreAroundCommits(snapshot, expectedHead, len(commits), wrote)
	}
	if wrote != nil {
		return g.restorePaths(snapshot, wrote)
	}
	return g.restoreTree(snapshot)
}

// restoreAroundCommits undoes a run's own edits while commits that landed
// after its snapshot stand untouched.
//
// The full restore writes EVERY snapshot file over the worktree, so it must
// refuse whenever HEAD has advanced — which made a run at its gate
// reject-undecidable the moment anyone else committed (B-074: three
// unrelated commits landed while T-071 waited, and Retry-with-note died on
// the guard). Surgical instead: the paths that differ between the snapshot
// and the tree now, minus the paths the landed commits touched. A landed
// path whose worktree matches HEAD holds no run residue and is skipped; one
// that differs from HEAD was edited by both, and picking a side silently
// would destroy somebody's work — refused, naming the paths.
func (g *Git) restoreAroundCommits(snapshot, expectedHead string, commits int, wrote []string) error {
	landedOut, err := g.run("diff", "--name-only", expectedHead, "HEAD")
	if err != nil {
		return fmt.Errorf("cannot safely restore: %w", err)
	}
	landed := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(landedOut), "\n") {
		if name != "" {
			landed[name] = true
		}
	}
	now, err := g.SnapshotTree()
	if err != nil {
		return fmt.Errorf("cannot safely restore: %w", err)
	}
	dirtyOut, err := g.run("diff-tree", "-r", "--name-only", snapshot, now)
	if err != nil {
		return fmt.Errorf("cannot safely restore: %w", err)
	}
	unstagedOut, _ := g.run("diff", "--name-only", "HEAD")
	unstaged := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(unstagedOut), "\n") {
		if name != "" {
			unstaged[name] = true
		}
	}
	inWrote := map[string]bool{}
	for _, name := range wrote {
		inWrote[name] = true
	}
	var restore, overlap []string
	for _, name := range strings.Split(strings.TrimSpace(dirtyOut), "\n") {
		if name == "" {
			continue
		}
		if wrote != nil && !inWrote[name] {
			continue // not the run's to undo
		}
		if !landed[name] {
			restore = append(restore, name)
			continue
		}
		if unstaged[name] {
			overlap = append(overlap, name)
		}
	}
	if len(overlap) > 0 {
		return fmt.Errorf("%d commits landed since this run began and both the run and those commits touched %s; resolve by hand",
			commits, strings.Join(overlap, ", "))
	}
	if len(restore) == 0 {
		return nil // the run left nothing of its own in the tree
	}
	return g.restorePaths(snapshot, restore)
}

// restorePaths is restoreTree limited to the named paths: snapshot content
// written back where the file existed, the file removed where it did not.
func (g *Git) restorePaths(snapshot string, paths []string) error {
	idx, err := os.CreateTemp("", "ducklab-restore-index-*")
	if err != nil {
		return err
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())
	if _, err := g.runWithIndex(idx.Name(), "read-tree", snapshot); err != nil {
		return err
	}
	inSnapOut, err := g.run("ls-tree", "-r", "--name-only", snapshot)
	if err != nil {
		return err
	}
	inSnapshot := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(inSnapOut), "\n") {
		if name != "" {
			inSnapshot[name] = true
		}
	}
	var removed []string
	for _, name := range paths {
		if inSnapshot[name] {
			if _, err := g.runWithIndex(idx.Name(), "checkout-index", "-f", "--", shellEscape(name)); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(filepath.Join(g.Root, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	if len(removed) > 0 {
		args := []string{"reset", "-q", "--"}
		for _, name := range removed {
			args = append(args, shellEscape(name))
		}
		if _, err := g.run(args...); err != nil {
			return fmt.Errorf("clear index entries for removed files: %w", err)
		}
	}
	return nil
}

// restoreTree performs the destructive restore after its caller has established
// that doing so cannot overwrite newer history.
func (g *Git) restoreTree(snapshot string) error {
	if strings.TrimSpace(snapshot) == "" {
		return fmt.Errorf("no snapshot to restore")
	}
	idx, err := os.CreateTemp("", "ducklab-restore-index-*")
	if err != nil {
		return err
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())

	// What exists now, measured the same way the snapshot was, so the two
	// lists can be compared name for name.
	if _, err := g.runWithIndex(idx.Name(), "add", "-A", "--", ".", harnessExclude); err != nil {
		return err
	}
	nowOut, err := g.runWithIndex(idx.Name(), "ls-files")
	if err != nil {
		return err
	}

	// Write every snapshot file over the tree.
	if _, err := g.runWithIndex(idx.Name(), "read-tree", snapshot); err != nil {
		return err
	}
	if _, err := g.runWithIndex(idx.Name(), "checkout-index", "-a", "-f"); err != nil {
		return err
	}

	// And delete what the run created: present now, absent from the snapshot.
	thenOut, err := g.run("ls-tree", "-r", "--name-only", snapshot)
	if err != nil {
		return err
	}
	inSnapshot := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSpace(thenOut), "\n") {
		if name != "" {
			inSnapshot[name] = true
		}
	}
	var removed []string
	for _, name := range strings.Split(strings.TrimSpace(nowOut), "\n") {
		if name == "" || inSnapshot[name] {
			continue
		}
		if err := os.Remove(filepath.Join(g.Root, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	// Clear the REAL index's record of what was just deleted. Diff() marks
	// run-created files intent-to-add (add -AN) in the real index, so deleting
	// the file leaves a ghost entry — `git status` showed " D" forever on a
	// file that was never committed and no longer exists, and every clean-tree
	// guard (test retire among them) refused on a tree that was actually clean.
	if len(removed) > 0 {
		args := []string{"reset", "-q", "--"}
		for _, name := range removed {
			args = append(args, shellEscape(name))
		}
		if _, err := g.run(args...); err != nil {
			return fmt.Errorf("clear index entries for removed files: %w", err)
		}
	}
	return nil
}

// LsFiles lists the committed (tracked) paths, one per entry. Empty on any
// error: a tree git cannot read holds nothing git would vouch for.
func (g *Git) LsFiles() []string {
	out, err := g.run("ls-files")
	if err != nil {
		return nil
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files
}
