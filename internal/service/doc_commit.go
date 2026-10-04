package service

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jrullan/ducklab/internal/vcs"
)

// docSnapshot is the on-disk state of the accepted documents an engine
// operation is about to rewrite, kept so a refused commit can put them back.
//
// B-489: bug promotion appended a whole milestone to plan.md and committed
// nothing. Builds of the promoted tasks ran and were accepted against a plan
// that existed only in the checkout's working tree, worktrees cut from the
// branch did not have the tasks, and a reset of the checkout would have lost
// them. An operation that edits an accepted document now commits exactly what
// it wrote, and when it cannot, the documents go back to what HEAD knew.
type docSnapshot struct {
	root  string
	files []docSnapshotFile
}

type docSnapshotFile struct {
	path    string // absolute
	data    []byte
	existed bool
}

func snapshotDocs(root string, paths ...string) docSnapshot {
	snap := docSnapshot{root: root}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		snap.files = append(snap.files, docSnapshotFile{path: path, data: data, existed: err == nil})
	}
	return snap
}

// changed reports whether the file on disk differs from its snapshot.
func (f docSnapshotFile) changed() bool {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return f.existed
	}
	return !f.existed || !bytes.Equal(data, f.data)
}

// restore puts every snapshotted document back, removing the ones the
// operation created.
func (d docSnapshot) restore() error {
	var first error
	for _, f := range d.files {
		var err error
		if f.existed {
			err = os.WriteFile(f.path, f.data, 0o644)
		} else if rmErr := os.Remove(f.path); rmErr != nil && !os.IsNotExist(rmErr) {
			err = rmErr
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// docCommitSkip describes why a document edit was left uncommitted
// without failing the operation that made it.
type docCommitSkip string

// commitAcceptedDocs commits the snapshotted documents, and nothing else, with
// the given message and trailers.
//
// Only these paths are committed (`git commit --only`): the TI-36X checkout
// that hit B-489 had unrelated uncommitted files, and a promotion that swept
// them into its commit would have made the person's work the engine's.
//
// A project that is not a git repository, or one where git cannot attribute a
// commit, keeps the edit and is told so, the rule commitVisualSetup and
// commitRunRecord follow: there is no history to disagree with in the first
// case, and in the second refusing to promote would trade a missing commit for
// a missing feature. Any other commit failure is a real failure; the caller
// restores the snapshot and refuses.
func commitAcceptedDocs(snap docSnapshot, message string, trailers map[string]string) (string, docCommitSkip, error) {
	git := vcs.New(snap.root)
	if !git.HasGit() {
		return "", "not a git repository", nil
	}
	if !gitIdentityKnown(git) {
		return "", "git has no author identity configured", nil
	}
	var paths []string
	for _, f := range snap.files {
		// Only what this operation changed. A document it could have written
		// but did not (a spec acceptance that wired no plan task) may carry
		// the person's own pending edits, and those are not ours to commit.
		if !f.changed() {
			continue
		}
		rel, err := filepath.Rel(snap.root, f.path)
		if err != nil {
			return "", "", fmt.Errorf("resolve %s: %w", f.path, err)
		}
		rel = filepath.ToSlash(rel)
		// A never-tracked file that is absent (a proposal nobody committed,
		// now consumed) has nothing to record; naming it would fail the commit.
		if git.ExistsOrTracked(rel) {
			paths = append(paths, rel)
		}
	}
	if len(paths) == 0 {
		return "", "", nil
	}
	// Staged first so a document the history has never seen (a plan created by
	// the operation) can be named to --only, which accepts only known paths.
	if err := git.AddPaths(paths...); err != nil {
		return "", "", err
	}
	if staged, err := git.HasStagedChangesFor(paths); err == nil && !staged {
		return "", "", nil
	}
	sha, err := git.CommitPathsWithTrailer(message, trailers, paths)
	if err != nil {
		// Leave the index as the operation found it: the caller is about to
		// restore the files, and a staged copy of the refused edit would ride
		// the person's next commit.
		_ = git.UnstagePaths(paths...)
		return "", "", err
	}
	return sha, "", nil
}

// commitOrRestoreDocs is commitAcceptedDocs with the failure half done: on a
// refused commit the documents are restored and the error says what happened.
func commitOrRestoreDocs(snap docSnapshot, what, message string, trailers map[string]string) (string, docCommitSkip, error) {
	sha, skip, err := commitAcceptedDocs(snap, message, trailers)
	if err != nil {
		if restoreErr := snap.restore(); restoreErr != nil {
			return "", "", fmt.Errorf("commit %s: %v (also failed to restore the documents: %v)", what, err, restoreErr)
		}
		return "", "", fmt.Errorf("commit %s: %w", what, err)
	}
	return sha, skip, nil
}
