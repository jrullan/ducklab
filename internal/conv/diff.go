package conv

import (
	"fmt"
	"strings"
)

// maxFileDiff bounds what ONE FILE may contribute to a prompt-borne diff,
// in bytes (roughly 5k tokens).
//
// T-067's review: frontend/dist's minified bundle rode the diff at 644KB —
// 98% of the change — and the reviewer re-read it on every call of its
// loop. 4.7M of the run's 6M tokens were one generated file, sent 22 times,
// contributing nothing a reviewer could use. Real source diffs this large
// exist but are rare; the stub names the file and the counts, and the
// reviewer keeps its tools to read anything it truly needs.
const maxFileDiff = 20_000

// CompactDiff returns the diff with any single file's section larger than
// maxFileDiff replaced by an honest one-line stub. Small diffs pass through
// byte-identical.
func CompactDiff(diff string) string {
	if len(diff) <= maxFileDiff {
		return diff
	}
	lines := strings.Split(diff, "\n")
	var starts []int
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		// An undelimited blob too big to inline: stub the whole thing rather
		// than guess at file boundaries a producer did not mark.
		return diffStub("the change", diff, lines)
	}

	var b strings.Builder
	if starts[0] > 0 {
		b.WriteString(strings.Join(lines[:starts[0]], "\n"))
		b.WriteString("\n")
	}
	for k, start := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		section := strings.Join(lines[start:end], "\n")
		if len(section) <= maxFileDiff {
			b.WriteString(section)
			if !strings.HasSuffix(section, "\n") {
				b.WriteString("\n")
			}
			continue
		}
		b.WriteString(lines[start] + "\n")
		b.WriteString(diffStub(fileOf(lines[start]), section, lines[start:end]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func diffStub(name, section string, lines []string) string {
	adds, dels := 0, 0
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			adds++
		case strings.HasPrefix(l, "-"):
			dels++
		}
	}
	return fmt.Sprintf(
		"[%s omitted from this prompt: %d bytes, +%d/-%d lines — too large to inline. Use your tools to read it if the review needs it.]",
		name, len(section), adds, dels)
}

// fileOf pulls the target path out of a "diff --git a/x b/y" header.
func fileOf(header string) string {
	fields := strings.Fields(header)
	if len(fields) >= 4 {
		return strings.TrimPrefix(fields[3], "b/")
	}
	return "a file"
}
