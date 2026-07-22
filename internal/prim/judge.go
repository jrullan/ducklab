package prim

import (
	"regexp"
	"strings"
)

var decisionRe = regexp.MustCompile(`(?i)DECISION[^A-Za-z0-9]{0,20}\b(HYBRID|NONE|A|B)\b`)
var sectionRe = regexp.MustCompile(`(?i)^\s*(?:#{1,6}|\*\*)\s*(?:SOLUTION\s+)?([AB])\b`)
var blockingRe = regexp.MustCompile(`(?i)BLOCKING\s+FINDING`)
var inlineLetterRe = regexp.MustCompile(`(?i)\bSOLUTION\s+([AB])\b|\b([AB])\s*:`)

// Decision is the judge's verdict: "A", "B", "HYBRID", or "NONE". Unparseable
// output defaults to HYBRID — synthesis is the safe default when the winner is
// unclear (a declared winner triggers a cheaper short-circuit downstream).
func Decision(judgeText string) string {
	m := decisionRe.FindStringSubmatch(judgeText)
	if m == nil {
		return "HYBRID"
	}
	return strings.ToUpper(m[1])
}

// JudgeReport is the structured read of a judge's output.
type JudgeReport struct {
	Decision string          // A | B | HYBRID | NONE
	Blocking map[string]bool // letters carrying a blocking finding
}

// ParseJudge extracts the decision and any solution flagged with a BLOCKING
// FINDING. A blocking finding on the same line names its letter; otherwise the
// nearest preceding section header attributes it. This is what stops the judge
// from crowning a solution that passes tests by deleting them.
func ParseJudge(judgeText string) JudgeReport {
	rep := JudgeReport{Decision: Decision(judgeText), Blocking: map[string]bool{}}
	lines := strings.Split(judgeText, "\n")

	headers := map[int]string{}
	for i, line := range lines {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			headers[i] = strings.ToUpper(m[1])
		}
	}
	for i, line := range lines {
		if !blockingRe.MatchString(line) {
			continue
		}
		if m := inlineLetterRe.FindStringSubmatch(line); m != nil {
			letter := m[1]
			if letter == "" {
				letter = m[2]
			}
			rep.Blocking[strings.ToUpper(letter)] = true
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if h, ok := headers[j]; ok {
				rep.Blocking[h] = true
				break
			}
		}
	}
	return rep
}
