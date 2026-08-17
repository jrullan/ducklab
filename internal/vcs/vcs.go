// Package vcs handles all git operations. Models never call these directly;
// only the orchestrator does.
package vcs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	cmdLine := "git " + strings.Join(args, " ")
	cmd := xplat.Shell(g.Root, nil, cmdLine)
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

// HeadSHA returns the HEAD commit SHA.
func (g *Git) HeadSHA() (string, error) {
	out, err := g.run("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
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
	_, err := g.run("add", "-A")
	return err
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
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	// The harness's own state never rides a task's diff. project.toml is a
	// tracked file, and flipping the project's autonomy in settings dirtied
	// it — T-097's reviewer then flagged, CRITICAL, that the diff "loosens
	// harness safety constraints without task justification". It was right
	// about everything except whose change it was.
	return g.run("diff", "HEAD", "--", ".", ":^.ducklab")
}

// DiffStat returns the diff stat, including untracked files.
func (g *Git) DiffStat() (string, error) {
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	return g.run("diff", "--stat", "HEAD", "--", ".", ":^.ducklab")
}

// WorktreeAdd creates a worktree.
func (g *Git) WorktreeAdd(path, branch string) error {
	_, err := g.run("worktree", "add", path, "-b", branch)
	return err
}

// WorktreeAddDetached checks out a commit without changing a branch. It is
// used when a caller must verify precisely what a commit contains, rather than
// the potentially dirtier working tree that produced it.
func (g *Git) WorktreeAddDetached(path, rev string) error {
	_, err := g.run("worktree", "add", "--detach", path, rev)
	return err
}

// WorktreeRemove removes a worktree.
func (g *Git) WorktreeRemove(path string) error {
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
	_, err := g.run("worktree", "prune")
	return err
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
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

// RestoreTree puts the working tree back to a snapshot: files the run changed
// are rewritten, files it created are removed. Everything under .ducklab is
// left alone, as is anything .gitignore hides.
//
// A person's own edits made WHILE the run was going are rolled back with it —
// unavoidable in a shared tree, and the reason to keep hands off a project
// while a run is working in it.
func (g *Git) RestoreTree(snapshot string) error {
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
