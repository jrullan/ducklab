package strategy

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
)

func decomp(subtasks ...agent.Subtask) *agent.Decomposition {
	return &agent.Decomposition{Subtasks: subtasks}
}

func TestOwnershipAcceptsDisjointFiles(t *testing.T) {
	got, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "api", Files: []string{"src/api.go", "src/api_test.go"}},
		agent.Subtask{Title: "store", Files: []string{"src/store.go"}},
	))
	if err != nil {
		t.Fatalf("a disjoint decomposition was rejected: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 2 || got[1][0] != "src/store.go" {
		t.Errorf("ownership = %v", got)
	}
}

// The whole mode rests on this. Phase 4 copies each subtask's files out of its
// worktree, which is only safe because no two subtasks touched the same file.
func TestOwnershipRejectsATwiceClaimedFile(t *testing.T) {
	_, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "api", Files: []string{"src/api.go", "src/shared.go"}},
		agent.Subtask{Title: "store", Files: []string{"src/shared.go"}},
	))
	if err == nil {
		t.Fatal("two subtasks were allowed to claim the same file")
	}
	// The architect gets one retry, which is only useful if it is told what
	// was wrong and by whom.
	for _, want := range []string{"src/shared.go", "api", "store"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict does not name %q: %v", want, err)
		}
	}
}

// The same file spelled two ways is still the same file.
func TestOwnershipSeesThroughPathSpelling(t *testing.T) {
	_, err := ValidateOwnership(decomp(
		agent.Subtask{Title: "a", Files: []string{"src/api.go"}},
		agent.Subtask{Title: "b", Files: []string{"./src/../src/api.go"}},
	))
	if err == nil {
		t.Error("the same file written two ways was treated as two files")
	}
}

// Checked here rather than trusted to the tool path jail: phase 4 copies these
// paths afterwards, and a copy driven by an unchecked "../" escapes long after
// any tool had a say.
func TestOwnershipRefusesPathsOutsideTheRepo(t *testing.T) {
	for _, bad := range []string{"../secrets.env", "/etc/passwd", "src/../../x.go", ".", ""} {
		if _, err := ValidateOwnership(decomp(
			agent.Subtask{Title: "a", Files: []string{bad}},
			agent.Subtask{Title: "b", Files: []string{"ok.go"}},
		)); err == nil {
			t.Errorf("%q was accepted as an owned file", bad)
		}
	}
}

func TestOwnershipRejectsAnEmptyDecomposition(t *testing.T) {
	if _, err := ValidateOwnership(nil); err == nil {
		t.Error("a nil decomposition was accepted")
	}
	if _, err := ValidateOwnership(decomp()); err == nil {
		t.Error("a decomposition with no subtasks was accepted")
	}
}

// Integration is a copy, not a merge, and that is the entire point of the
// mode: a weak model asked to reconcile whole files destroys code that was
// working. Disjoint ownership turns the reconciliation into a file copy that
// no model takes part in.
func TestIntegrateCopiesFromTheOwningWorkspaceOnly(t *testing.T) {
	var moves []string
	copier := func(from, to string) (bool, error) {
		moves = append(moves, from+" -> "+to)
		return true, nil
	}
	written, err := Integrate("/repo",
		[][]string{{"src/api.go"}, {"src/store.go"}},
		[]string{"/ws/a", "/ws/b"},
		copier,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Errorf("written = %v", written)
	}
	want := []string{
		"/ws/a/src/api.go -> /repo/src/api.go",
		"/ws/b/src/store.go -> /repo/src/store.go",
	}
	for i, w := range want {
		if moves[i] != w {
			t.Errorf("copy %d = %q, want %q", i, moves[i], w)
		}
	}
}

// Deciding a file was unnecessary is a legitimate outcome. Treating its
// absence as an error would fail a run that succeeded, and deleting the
// target copy would silently discard whatever was there before.
func TestIntegrateToleratesAFileNeverCreated(t *testing.T) {
	copier := func(from, to string) (bool, error) {
		return !strings.HasSuffix(from, "unused.go"), nil
	}
	written, err := Integrate("/repo",
		[][]string{{"src/api.go", "src/unused.go"}},
		[]string{"/ws/a"},
		copier,
	)
	if err != nil {
		t.Fatalf("a claimed-but-uncreated file failed the integration: %v", err)
	}
	if len(written) != 1 || written[0] != "src/api.go" {
		t.Errorf("written = %v, want only the file that exists", written)
	}
}

// A mismatch means the caller lost track of which workspace belongs to which
// subtask, and copying anything at that point would put a subtask's work under
// another's name.
func TestIntegrateRefusesMismatchedWorkspaces(t *testing.T) {
	_, err := Integrate("/repo", [][]string{{"a.go"}, {"b.go"}}, []string{"/ws/a"},
		func(string, string) (bool, error) { return true, nil })
	if err == nil {
		t.Error("integration ran with fewer workspaces than ownership lists")
	}
}
