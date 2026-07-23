package strategy

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/prim"
)

const gitID = "-c user.email=duck@ducklab.local -c user.name=ducklab"

// guardClean aborts if the repo has uncommitted changes — applying scratch
// solutions on a dirty tree would contaminate real work. It returns the dirty
// lines for display.
func guardClean(repo string) (bool, []string) {
	return prim.IsDirty(repo)
}

// finalBranch is the conventional branch a strategy leaves its result on.
func finalBranch(taskID string) string { return "ducklab/" + taskID + "/final" }

// checkoutFresh creates branch from base and hard-resets the tree, preserving
// runs/ so artifacts survive.
func checkoutFresh(repo, branch, base string) {
	prim.Git(fmt.Sprintf("checkout -q -B %s %s", branch, base), repo)
	prim.Git("reset -q --hard", repo)
	prim.Git("clean -qfd -e runs/", repo)
}

// stageWork stages every change EXCEPT ducklab's own runs/ artifacts. Those must
// never be tracked: if they were, the next `checkout -B final base` would delete
// them (tracked-but-absent on base) and destroy the solutions mid-run. This
// holds whether or not the target repo gitignores runs/.
func stageWork(repo string) {
	prim.Git("add -A", repo)
	prim.Git("reset -q -- runs .ducklab", repo)
}

// commitAll stages the work (never runs/) and commits with the ducklab identity.
func commitAll(repo, msg string) {
	stageWork(repo)
	prim.Git(fmt.Sprintf("%s commit -q -m %q", gitID, msg), repo)
}

// snapshotDiff returns the working-tree diff against base, excluding runs/, and
// leaves runs/ unstaged so a later commit cannot capture it.
func snapshotDiff(repo, base string) string {
	stageWork(repo)
	_, out := prim.Git("diff --cached "+base+" -- . ':(exclude)runs' ':(exclude).ducklab'", repo)
	return out
}

// runGate executes the verification gate. It returns:
//
//	ran  — whether an automated gate actually executed
//	pass — whether it passed (meaningless when ran is false)
//	out  — captured output
//
// A gate of kind "none" does not run: the caller produces an UNVERIFIED result
// rather than inventing a pass or fail.
func runGate(repo string, gate prim.Gate) (ran, pass bool, out string) {
	if !gate.Active() {
		return false, false, "(no automated gate — unverified)"
	}
	ok, output := prim.Shell(gate.Cmd, repo)
	return true, ok, output
}

// restore returns the repo to base (used in a deferred cleanup).
func restore(repo, base string) {
	prim.Git("checkout -q "+base, repo)
}
