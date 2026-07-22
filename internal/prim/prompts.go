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
		"- SEARCH text must exist EXACTLY in the file (whitespace and indentation\n" +
		"  included). If not found, the change is REJECTED.\n" +
		"- Multiple === FILE: ... >>> REPLACE blocks may target the same file;\n" +
		"  each needs its own === FILE: === header.\n" +
		"- Do NOT rewrite whole files — only the portion that changes.\n" +
		"- No explanatory text outside the blocks.\n" +
		"- The closing delimiter is >>> REPLACE."
)

// SolvePrompt asks a model to implement the requirement as full files.
func SolvePrompt(requirement, repo string) []source.Message {
	return []source.Message{
		{Role: "system", Content: "You are a senior software engineer. You implement the " +
			"requested task with minimal, correct changes. When you modify an existing file, " +
			"return it COMPLETE with your change integrated — never omit prior content. " + FileFormat},
		{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nRepo files:\n%s\n\n"+
			"Contents of relevant files:\n%s", requirement, RepoListing(repo),
			RelevantFiles(requirement, repo, 12000))},
	}
}

// DriverPrompt asks the driver for surgical SEARCH/REPLACE edits, optionally
// incorporating the observer's feedback from a prior round.
func DriverPrompt(requirement, repo, feedback string, round, maxRounds int) []source.Message {
	system := "You are a senior software engineer. You implement the requested task with " +
		"minimal, surgical changes. You read the existing code and propose ONLY the portions " +
		"that change using SEARCH/REPLACE. SEARCH text must be EXACT (indentation included) — " +
		"if not found the change is auto-rejected. " + SearchReplaceFormat +
		"\nCRITICAL: only modify files whose content is shown below under 'Contents of relevant " +
		"files'. Do NOT invent code in files you have not read. Copy the EXACT file text into " +
		"your SEARCH blocks."
	user := fmt.Sprintf("Task:\n%s\n\nRepo files:\n%s\n\nContents of relevant files (only modify these):\n%s",
		requirement, RepoListing(repo), RelevantFiles(requirement, repo, 12000))
	if feedback != "" {
		user += fmt.Sprintf("\n\nObserver feedback (round %d/%d):\n%s\n\n"+
			"Fix ONLY what the observer flagged. If already correct, return an empty SEARCH/REPLACE block.",
			round, maxRounds, feedback)
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
			RelevantFiles(requirement, repo, 12000))},
	}
}

func cap3000(s string) string {
	if len(s) > 3000 {
		return s[:3000]
	}
	return s
}
