package strategy

import (
	"fmt"
	"regexp"
	"strings"
)

// maxToolResultBytes bounds what a tool result contributes to the log (I3).
const maxToolResultBytes = 512

// An error's summary is bounded too, by these: the header lines, the failure
// lines rescued from the omitted middle, and the tail.
const (
	maxErrorHeaderLines  = 4
	maxErrorFailureLines = 12
	maxErrorLineBytes    = 200
	maxErrorTailBytes    = 1536
)

// errorHeaderLine is a leading "key: value" line — verify_run's "gate:",
// "cmd:", "exit:" — that says what ran and how it ended.
var errorHeaderLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z_ ]{0,20}: `)

// failureLine is a line that names a failure, wherever the runner put it.
var failureLine = regexp.MustCompile(`(?i)(\bFAIL(ED|URE|S)?\b|\berror\b|\bTraceback\b|\bassert|\bexpected\b|\bpanic\b|✗|✕)`)

// SummariseToolResult is what the run log keeps of a tool result: the log is
// read by the person, the full result went to the model.
//
// An ordinary result keeps its first maxToolResultBytes. An error keeps what
// explains it (B-544). A failed verify_run used to show the gate header, the
// first PASS lines and "… 29751 bytes truncated": a test runner's head is
// its passing tests, and the failure is at the end — or in the middle, for a
// runner that keeps going. The model saw the error (tools.CapResult is
// tail-biased); the person, reading the log, never did. So an error keeps its
// header, the failure lines from the middle, and its tail, each cut said out
// loud.
func SummariseToolResult(s string, isError bool) string {
	s = strings.TrimSpace(s)
	if !isError {
		if len(s) <= maxToolResultBytes {
			return s
		}
		return s[:maxToolResultBytes] + fmt.Sprintf("\n… %d bytes truncated", len(s)-maxToolResultBytes)
	}
	if len(s) <= maxErrorTailBytes {
		return s
	}

	// The tail starts at a line boundary when one is near, so it does not
	// open mid-word.
	tailStart := len(s) - maxErrorTailBytes
	if nl := strings.IndexByte(s[tailStart:], '\n'); nl >= 0 && nl < maxErrorTailBytes/4 {
		tailStart += nl + 1
	}

	var header []string
	headEnd := 0
	for headEnd < tailStart && len(header) < maxErrorHeaderLines {
		line, next := nextLine(s, headEnd)
		if !errorHeaderLine.MatchString(line) || next > tailStart {
			break
		}
		header = append(header, clipLine(line))
		headEnd = next
	}

	var failures []string
	omitted := 0
	for pos := headEnd; pos < tailStart; {
		line, next := nextLine(s, pos)
		if next > tailStart {
			next = tailStart
			line = s[pos:next]
		}
		if len(failures) < maxErrorFailureLines && failureLine.MatchString(line) {
			if omitted > 0 {
				failures = append(failures, fmt.Sprintf("… %d bytes omitted …", omitted))
				omitted = 0
			}
			failures = append(failures, clipLine(line))
		} else {
			omitted += next - pos
		}
		pos = next
	}

	var b strings.Builder
	for _, l := range header {
		b.WriteString(l + "\n")
	}
	for _, l := range failures {
		b.WriteString(l + "\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "… %d bytes omitted …\n", omitted)
	}
	b.WriteString(s[tailStart:])
	return b.String()
}

// nextLine returns the line starting at pos (without its newline) and the
// position after it.
func nextLine(s string, pos int) (string, int) {
	if nl := strings.IndexByte(s[pos:], '\n'); nl >= 0 {
		return strings.TrimRight(s[pos:pos+nl], "\r"), pos + nl + 1
	}
	return s[pos:], len(s)
}

func clipLine(l string) string {
	if len(l) <= maxErrorLineBytes {
		return l
	}
	return l[:maxErrorLineBytes] + "…"
}
