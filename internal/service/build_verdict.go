package service

// adjudicateBuildVerdict combines the deterministic project gate, the task's
// own Verification contract, and the final reviewer. None may be overwritten
// by another green signal: pair means gate AND reviewer, not gate OR reviewer.
func adjudicateBuildVerdict(projectVerdict, taskGate string, reviewerDissent bool) string {
	if projectVerdict != "PASSED" {
		return projectVerdict
	}
	if taskGate == "red" || reviewerDissent {
		return "FAILED"
	}
	return projectVerdict
}
