package prim

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/source"
)

// Output format contracts shared across roles.
const (
	FileFormat = "Return ONLY complete files in this exact format:\n" +
		"=== FILE: relative/path ===\n<full file content>\n" +
		"One block per file. No prose outside the blocks."

	SearchReplaceFormat = "Return ONLY surgical changes in this EXACT format:\n\n" +
		"=== FILE: relative/path/to/file ===\n" +
		"<<< SEARCH\n" +
		"<EXACT current code, including indentation>\n" +
		"===\n" +
		"<new code that replaces it>\n" +
		">>> REPLACE\n\n" +
		"Rules:\n" +
		"- To EDIT AN EXISTING FILE: SEARCH text must exist EXACTLY in the file\n" +
		"  (whitespace and indentation included). If not found, it is REJECTED.\n" +
		"  Only the portion that changes — never rewrite a whole existing file.\n" +
		"- To CREATE A NEW FILE: write the header and then the FULL file content\n" +
		"  directly, with NO SEARCH block:\n" +
		"    === FILE: path/to/new_file.html ===\n    <complete file content here>\n" +
		"  (A whole-file block is only allowed for files that don't exist yet.)\n" +
		"- Multiple blocks may target the same file; each needs its own\n" +
		"  === FILE: === header.\n" +
		"- No explanatory text outside the blocks.\n" +
		"- The closing delimiter for a SEARCH/REPLACE edit is >>> REPLACE."
)

// SolvePrompt asks a model to implement the requirement as full files.
func SolvePrompt(requirement, repo string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a senior software engineer. You implement the " +
			"requested task with minimal, correct changes. When you modify an existing file, " +
			"return it COMPLETE with your change integrated — never omit prior content. " + FileFormat},
		{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nRepo files:\n%s\n\n"+
			"Contents of relevant files:\n%s", requirement, RepoListing(repo),
			RelevantFiles(requirement, repo, ContextBudget))},
	}
}

// DriverPrompt asks the driver to implement the task by returning the complete
// updated file(s). Whole-file rewriting is far more robust than inline
// SEARCH/REPLACE markers on modest local models; the observer reviews the diff
// and a net loss of code is a blocking finding, so destruction is caught.
func DriverPrompt(requirement, repo, feedback string, round, maxRounds int) []source.Message {
	system := "You are a senior software engineer implementing the requested task. Make the " +
		"MINIMAL change needed and leave everything else intact. " + FencedEditFormat
	user := fmt.Sprintf("Task:\n%s\n\nRepo files:\n%s\n\nContents of relevant files:\n%s",
		requirement, RepoListing(repo), RelevantFiles(requirement, repo, ContextBudget))
	if feedback != "" {
		user += fmt.Sprintf("\n\nReviewer feedback (round %d/%d):\n%s\n\n"+
			"Address the feedback. Return the COMPLETE updated file(s).", round, maxRounds, feedback)
	}
	return []source.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}
}

// ObserverPrompt asks the reviewer for structured feedback on the current diff.
// The diff MUST be taken against base, not the current branch: the driver works
// on the scratch branch, so diffing against the current branch would compare it
// to itself and show nothing — the observer would then reject a correct change
// as "empty diff".
func ObserverPrompt(requirement, repo, base, testOutput string, round, maxRounds int) []source.Message {
	diff := TruncateMiddle(DiffAgainst(repo, base), 8000)
	return []source.Message{
		{Role: "system", Content: "You are a strict code reviewer. You evaluate the driver's " +
			"changes against the task and the test results. Your reply MUST have this structure:\n\n" +
			"1. Analysis: (2-3 sentences on what changed and whether it meets the task)\n" +
			"2. Tests: (do they pass, do they cover the change)\n" +
			"3. Verdict: APPROVED — if the changes meet the task and tests pass, or CORRECTIONS: " +
			"followed by a numbered list of specific problems (file, line, what is missing/wrong).\n\n" +
			"CRITICAL: the driver rewrites whole files. If the diff shows existing code being " +
			"REMOVED or omitted that the task did not ask to remove (a net loss of functions, lines, " +
			"or content), that is a serious regression — respond CORRECTIONS, never APPROVED. Losing " +
			"unrelated code is worse than not making the change.\n" +
			"NEVER reply just 'APPROVED' without the analysis. Without the analysis the verdict is invalid."},
		{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nTest result (round %d/%d):\n%s\n\nCurrent diff:\n%s",
			requirement, round, maxRounds, cap3000(testOutput), diff)},
	}
}

// JudgePrompt asks an independent judge to compare two anonymized solutions.
func JudgePrompt(requirement, solA, solB, verdicts string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a strict, independent technical judge. You evaluate two " +
			"solutions to the SAME task without knowing who wrote them. Be concrete: line, problem, why. " +
			"No praise. End with a 'DECISION' section stating: A, B, HYBRID (and what to take from each), " +
			"or NONE if neither is acceptable. NEVER declare a winner that has a BLOCKING FINDING — if the " +
			"only real solution cheats, the correct decision is NONE, not crowning the cheat.\n\n" +
			"BLOCKING FINDING (you must report it and never crown it): net coverage reduction — deleting, " +
			"rewriting, or shrinking existing tests the task did not ask to touch; deleting existing public " +
			"functions/modules unrelated to the task; emptying files. Green tests with reduced coverage is " +
			"CHEATING, not victory. If a diff looks truncated, judge by what is visible and by the test ground truth."},
		{Role: "user", Content: fmt.Sprintf("Original task:\n%s\n\nTest results (ground truth): %s\n\n"+
			"=== SOLUTION A ===\n```diff\n%s\n```\n\n=== SOLUTION B ===\n```diff\n%s\n```",
			requirement, verdicts, TruncateMiddle(solA, 6000), TruncateMiddle(solB, 6000))},
	}
}

// SynthesizePrompt asks the judge to produce a final merged solution.
func SynthesizePrompt(requirement, judgeNotes, repo string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a senior engineer. From your own comparative evaluation, produce " +
			"the FINAL solution taking the best-evaluated parts. When you modify an existing file, return it " +
			"COMPLETE with all prior content intact — omitting existing content is a serious error. " + FileFormat},
		{Role: "user", Content: fmt.Sprintf("Original task:\n%s\n\nYour evaluation:\n%s\n\n"+
			"Current contents of relevant files:\n%s", requirement, judgeNotes,
			RelevantFiles(requirement, repo, ContextBudget))},
	}
}

func cap3000(s string) string {
	if len(s) > 3000 {
		return s[:3000]
	}
	return s
}

// ─── plan mode (peer dialogue) ────────────────────────────────────

// PlanHandoffPrompt is Model A: round 1 drafts a plan and hands it to B; later
// rounds respond to B's observations by either revising or standing pat.
func PlanHandoffPrompt(requirement, repo, feedback string, round, maxRounds int) []source.Message {
	if round == 1 {
		return []source.Message{
			{Role: "system", Content: "You are Model A, a software architect. Present an execution plan " +
				"to your colleague Model B for review.\n\nStructure your message:\n" +
				"1. A short framing: 'B, here is my plan for the user's requirement — tell me if it meets the " +
				"goals and where you'd change it.'\n" +
				"2. The plan: a numbered list of concrete IMPLEMENTATION steps. For each step name the " +
				"file to touch, the specific change, and why. 5-10 steps is typical; keep each to 1-2 sentences.\n" +
				"3. Close by asking if B has observations.\n\n" +
				"A plan is a sequence of implementation actions — NOT a restatement of the requirements, NOT " +
				"acceptance criteria, and NOT the finished code. Do not write actual file contents here; that " +
				"happens in the execution step. Be concrete about files and changes so it is executable."},
			{Role: "user", Content: fmt.Sprintf("User requirement:\n%s\n\nRepo files:\n%s\n\nContents of relevant files:\n%s",
				requirement, RepoListing(repo), RelevantFiles(requirement, repo, ContextBudget))},
		}
	}
	return []source.Message{
		{Role: "system", Content: "You are Model A. Your colleague B reviewed your plan and gave observations. " +
			"You have TWO options:\n\n" +
			"OPTION 1 — Revise: 'B, thanks — here is my revised plan incorporating your points.' Then the full " +
			"revised plan as a numbered list.\n\n" +
			"OPTION 2 — Stand by it: 'B, I appreciate the observations but I'm keeping my plan because <concrete " +
			"reason>.' Do NOT include a new plan if you choose this — just the reason.\n\n" +
			"Do not combine the options. If you revise, give the complete plan. If you stand by it, give only the reason."},
		{Role: "user", Content: fmt.Sprintf("User requirement:\n%s\n\nB's observations (round %d/%d):\n%s\n\n"+
			"Decide: revise, or stand by your plan?", requirement, round, maxRounds, feedback)},
	}
}

// PlanReviewPrompt is Model B: observations only — B never approves or rejects,
// so A keeps final say over its own plan.
func PlanReviewPrompt(requirement, handoff, repo string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are Model B, a software reviewer. Model A has shown you an execution plan. " +
			"Give constructive observations and suggestions — do NOT approve or reject. A decides whether to " +
			"incorporate them.\n\nStructure:\n1. A one-line read: 'A, the plan looks solid / promising / needs work.'\n" +
			"2. Observations: specific points — what's good, what's missing, what could go wrong. Be concrete.\n" +
			"3. Close with either 'a revised version incorporating these would help' or 'I think it's ready to execute.'\n\n" +
			"Do NOT use APPROVED/REJECTED. Only observations and suggestions."},
		{Role: "user", Content: fmt.Sprintf("User requirement:\n%s\n\nA's message (contains the plan):\n%s\n\nRepo files:\n%s",
			requirement, handoff, RepoListing(repo))},
	}
}

// PlanExecutePrompt is Model A implementing the ratified plan by returning the
// complete updated file(s).
func PlanExecutePrompt(requirement, plan, repo string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a senior engineer. Implement the ratified plan, step by step, " +
			"making the minimal changes needed. " + FencedEditFormat},
		{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nPlan (follow it exactly):\n%s\n\nContents of relevant files:\n%s",
			requirement, plan, RelevantFiles(requirement, repo, ContextBudget))},
	}
}

// PlanVerifyPrompt is Model B checking the execution against the plan and the
// requirement. Advisory when a gate ran; the decisive check when none did.
func PlanVerifyPrompt(requirement, plan, repo, diff, gateOutput string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a verifier. Compare the execution against the original plan and the " +
			"requirement. Reply with:\n1. Plan vs execution: were all steps completed?\n2. Requirement: satisfied?\n" +
			"3. Gate: did the automated check pass (if any)?\n4. Regression check: did the diff REMOVE or omit " +
			"existing code the plan did not ask to remove? A net loss of unrelated functions/lines is a serious " +
			"problem.\n5. Verdict: APPROVED, or ISSUES: followed by a list. Do not APPROVE if code was lost."},
		{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nPlan:\n%s\n\nDiff:\n%s\n\nGate output:\n%s",
			requirement, plan, TruncateMiddle(diff, 6000), cap3000(gateOutput))},
	}
}
