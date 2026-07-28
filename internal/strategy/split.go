package strategy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// OwnershipError is a decomposition the mode refuses to run.
//
// It carries the conflict in words the architect can act on, because the one
// retry it gets is only useful if it is told what was wrong (05 §4.5).
type OwnershipError struct {
	Detail string
}

func (e *OwnershipError) Error() string { return e.Detail }

// ValidateOwnership checks a decomposition's file ownership. Phase 2, and
// deterministic on purpose (05 §4.5).
//
// This is the check the whole mode rests on. Phase 4 integrates by copying each
// subtask's files out of its worktree, which is only safe because no two
// subtasks can have touched the same file. Take this away and integration
// becomes a merge — and a weak model asked to merge whole files destroys
// working code, which is the failure the mode exists to avoid.
//
// It returns the cleaned, repo-relative ownership per subtask, in the order
// given, so callers do not re-derive paths that were already normalised here.
func ValidateOwnership(d *agent.Decomposition) ([][]string, error) {
	if d == nil || len(d.Subtasks) == 0 {
		return nil, &OwnershipError{Detail: "the decomposition has no subtasks"}
	}

	owner := map[string]string{} // clean path -> owning subtask title
	out := make([][]string, len(d.Subtasks))

	for i, st := range d.Subtasks {
		var files []string
		for _, raw := range st.Files {
			clean, err := repoRelative(raw)
			if err != nil {
				return nil, &OwnershipError{Detail: fmt.Sprintf(
					"subtask %q claims %q, which is outside the repository", st.Title, raw)}
			}
			if prev, taken := owner[clean]; taken {
				return nil, &OwnershipError{Detail: fmt.Sprintf(
					"%q is claimed by both %q and %q; each file may have exactly one owner",
					clean, prev, st.Title)}
			}
			owner[clean] = st.Title
			files = append(files, clean)
		}
		sort.Strings(files)
		out[i] = files
	}
	return out, nil
}

// repoRelative normalises a claimed path and rejects anything that leaves the
// repository.
//
// Checked here rather than trusted to the tool path jail: the jail stops a
// contestant from writing outside its worktree at execution time, but phase 4
// copies these paths afterwards, and a copy driven by an unchecked "../" would
// escape long after any tool had a say.
func repoRelative(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute path")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("escapes the repository")
	}
	if clean == "." {
		return "", fmt.Errorf("the repository itself is not a file")
	}
	return filepath.ToSlash(clean), nil
}

// Integrate copies each subtask's owned files out of its workspace into the
// target tree. Phase 4, and deterministic on purpose (05 §4.5).
//
// This is a copy and not a merge, and that is the entire point of the mode. A
// weak model asked to reconcile whole files destroys code that was working;
// disjoint ownership, established in phase 2, turns the reconciliation into a
// file copy that no model takes part in.
//
// A file a subtask claimed but never created is not an error: deciding a file
// was unnecessary is a legitimate outcome. Deleting the target copy in that
// case would be, because it would silently discard whatever was there before.
func Integrate(target string, owned [][]string, roots []string, copyFile FileCopier) ([]string, error) {
	if len(owned) != len(roots) {
		return nil, fmt.Errorf("integrate: %d ownership lists for %d workspaces", len(owned), len(roots))
	}
	var written []string
	for i, files := range owned {
		for _, rel := range files {
			from := filepath.Join(roots[i], filepath.FromSlash(rel))
			to := filepath.Join(target, filepath.FromSlash(rel))
			copied, err := copyFile(from, to)
			if err != nil {
				return written, fmt.Errorf("integrate %s: %w", rel, err)
			}
			if copied {
				written = append(written, rel)
			}
		}
	}
	sort.Strings(written)
	return written, nil
}

// FileCopier copies one file, reporting whether it existed. Injected so the
// integration is testable without a filesystem, and so the real one can be the
// only place that knows about permissions and parent directories.
type FileCopier func(from, to string) (copied bool, err error)
