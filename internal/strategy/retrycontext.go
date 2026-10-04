package strategy

import (
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// B-496. An implementer retry (a missing completion report, or an advisor
// note) is a new conversation: the model sees only the task prompt. It was
// told "continue from the CURRENT tree; do not restart research" with nothing
// to continue from, so it re-read the files it had just read. In TI-36X T-014
// the first attempt had already landed the right patch and diagnosed the
// remaining red correctly ("So maybe the test is wrong?"); the retry never
// saw either, and the advisor sent it after other suspects. The attempt's own
// conclusion and evidence ride into the retry here, bounded.

const (
	maxPriorMessageRunes = 2000
	maxPriorGateRunes    = 1500
	maxPriorReads        = 20
)

// previousAttempt renders what an implementer turn did, for the retry of that
// turn: its final message, the files it changed, its last verification and
// what it read. Empty when the turn did nothing worth carrying.
func previousAttempt(outcome *agent.Outcome) string {
	if outcome == nil {
		return ""
	}
	var changed, reads []string
	seenChanged, seenRead := map[string]bool{}, map[string]bool{}
	gate := ""
	for _, c := range outcome.ToolCalls {
		if c.Result == nil {
			continue
		}
		switch c.Name {
		case "fs_write", "fs_write_lines", "fs_patch", "fs_delete":
			if c.Result.IsError {
				continue
			}
			if d := argDigest(c.Args); d != "" && !seenChanged[d] {
				seenChanged[d] = true
				changed = append(changed, d+" ("+c.Name+")")
			}
		case "fs_read", "fs_search", "fs_list":
			if c.Result.IsError {
				continue
			}
			if d := argDigest(c.Args); d != "" && !seenRead[d] {
				seenRead[d] = true
				reads = append(reads, c.Name+" "+d)
			}
		case "verify_run":
			gate = c.Result.Content
		}
	}
	message := strings.TrimSpace(outcome.Text)
	if message == "" && len(changed) == 0 && gate == "" && len(reads) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Your previous attempt at this turn — continue from it\n\n")
	b.WriteString("This already happened on the CURRENT tree; it is evidence, not a draft. Start from its conclusion " +
		"instead of re-reading. Re-read only the exact lines you are about to patch.\n")
	if message != "" {
		b.WriteString("\n### Its final message\n\n" + lastRunes(message, maxPriorMessageRunes) + "\n")
	}
	b.WriteString("\n### Files it changed\n\n")
	if len(changed) == 0 {
		b.WriteString("- none\n")
	}
	for _, c := range changed {
		b.WriteString("- " + c + "\n")
	}
	if gate != "" {
		b.WriteString("\n### Its last verify_run\n\n```\n" + gateExcerpt(gate) + "\n```\n")
	}
	if len(reads) > 0 {
		b.WriteString("\n### What it read\n\n")
		for i, r := range reads {
			if i == maxPriorReads {
				b.WriteString("- … and more\n")
				break
			}
			b.WriteString("- " + r + "\n")
		}
	}
	return b.String()
}

// gateExcerpt keeps what a retry needs from a verify_run result: the outcome
// line and the failing assertions, not the whole passing log.
func gateExcerpt(gate string) string {
	var keep []string
	for _, line := range strings.Split(gate, "\n") {
		t := strings.TrimSpace(line)
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "exit:"), strings.HasPrefix(low, "cmd:"),
			strings.HasPrefix(low, "not ok"), strings.HasPrefix(low, "# fail"), strings.HasPrefix(low, "# pass"),
			strings.Contains(low, "fail"), strings.Contains(low, "error"),
			strings.Contains(low, "expected"), strings.Contains(low, "actual"):
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return lastRunes(strings.TrimSpace(gate), maxPriorGateRunes)
	}
	return lastRunes(strings.Join(keep, "\n"), maxPriorGateRunes)
}

// lastRunes keeps the end of s: a model's conclusion and a gate's failures
// come last.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}
