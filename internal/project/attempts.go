package project

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Attempt is one failed try at a task goal, remembered so the next run of the
// same goal doesn't repeat the dead end. Persisted to .ducklab/attempts.jsonl
// so the memory survives the session (the effort was never committed, but the
// lesson from it is).
type Attempt struct {
	Goal   string `json:"goal"`
	Mode   string `json:"mode"`
	Reason string `json:"reason"` // why it failed (outcome message)
	Detail string `json:"detail"` // reviewer's issues or test failure tail
	Diff   string `json:"diff"`   // the change that was tried (truncated)
}

const (
	maxAttemptsInContext = 3
	maxDetailChars       = 1000
	maxDiffChars         = 1500
)

func attemptsPath(repo string) string {
	return filepath.Join(repo, ".ducklab", "attempts.jsonl")
}

// normalizeGoal collapses whitespace and case so a re-typed goal matches.
func normalizeGoal(goal string) string {
	return strings.ToLower(strings.Join(strings.Fields(goal), " "))
}

// AddAttempt records a failed try.
func AddAttempt(repo string, a Attempt) error {
	if err := os.MkdirAll(filepath.Dir(attemptsPath(repo)), 0o755); err != nil {
		return err
	}
	a.Detail = trunc(a.Detail, maxDetailChars)
	a.Diff = trunc(a.Diff, maxDiffChars)
	line, _ := json.Marshal(a)
	f, err := os.OpenFile(attemptsPath(repo), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// AttemptsFor returns the recorded failed attempts for a goal (most recent last).
func AttemptsFor(repo, goal string) []Attempt {
	f, err := os.Open(attemptsPath(repo))
	if err != nil {
		return nil
	}
	defer f.Close()
	key := normalizeGoal(goal)
	var out []Attempt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var a Attempt
		if json.Unmarshal(sc.Bytes(), &a) == nil && normalizeGoal(a.Goal) == key {
			out = append(out, a)
		}
	}
	return out
}

// ClearAttempts drops all recorded attempts for a goal (call once it succeeds).
func ClearAttempts(repo, goal string) error {
	f, err := os.Open(attemptsPath(repo))
	if err != nil {
		return nil
	}
	key := normalizeGoal(goal)
	var kept [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var a Attempt
		if json.Unmarshal(sc.Bytes(), &a) == nil && normalizeGoal(a.Goal) == key {
			continue
		}
		kept = append(kept, append([]byte{}, sc.Bytes()...))
	}
	f.Close()
	if len(kept) == 0 {
		return os.Remove(attemptsPath(repo))
	}
	return os.WriteFile(attemptsPath(repo), append([]byte(strings.Join(bytesToStrings(kept), "\n")), '\n'), 0o644)
}

// AttemptsContext formats the prior failures for injection, or "" if none.
func AttemptsContext(repo, goal string) string {
	attempts := AttemptsFor(repo, goal)
	if len(attempts) == 0 {
		return ""
	}
	if len(attempts) > maxAttemptsInContext {
		attempts = attempts[len(attempts)-maxAttemptsInContext:]
	}
	var b strings.Builder
	b.WriteString("IMPORTANT: earlier attempts at THIS task already FAILED. Do NOT repeat these " +
		"approaches — diagnose why they failed and try a genuinely different one.\n")
	for i, a := range attempts {
		b.WriteString("\n--- Failed attempt ")
		b.WriteString(itoa(i + 1))
		b.WriteString(" (" + a.Mode + ") ---\n")
		if a.Reason != "" {
			b.WriteString("Why it failed: " + a.Reason + "\n")
		}
		if a.Detail != "" {
			b.WriteString("Details: " + a.Detail + "\n")
		}
		if a.Diff != "" {
			b.WriteString("The change that was tried (do NOT reproduce it):\n" + a.Diff + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Count returns how many failed attempts are on record for a goal.
func Count(repo, goal string) int { return len(AttemptsFor(repo, goal)) }

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func bytesToStrings(bs [][]byte) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(b)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
