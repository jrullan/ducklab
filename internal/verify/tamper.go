package verify

import (
	"path"
	"regexp"
	"strings"
)

// DefaultTestGlobs are the paths a project's tests live in unless it says
// otherwise (05 §5.3).
var DefaultTestGlobs = []string{"*_test.go", "test_*.py", "*_test.py", "*.test.ts", "*.test.tsx", "*.spec.ts", "tests/**", "test/**", "__tests__/**"}

// Tampering is what a run's diff did to the project's tests.
//
// A gate is only worth what the tests are worth, and a change that edits both
// at once can turn any diff green. This does not judge that — sometimes tests
// must change, and a model that fixes a genuinely wrong assertion is doing its
// job. It reports, so a human reads those hunks before accepting rather than
// after (05 §5.3, P3).
type Tampering struct {
	// Files are the test files the diff touches, in the order the diff lists
	// them.
	Files []string
	// Hunks is the diff restricted to those files, ready to show first.
	Hunks string
	// Asked is true when the task itself mentioned tests, which makes the
	// change expected rather than a surprise.
	Asked bool
}

// Flagged reports whether a human should be shown the test hunks separately.
//
// Only when the task did not ask. Flagging every test edit in a task that says
// "add tests for X" would train the reader to dismiss the flag, and a warning
// that is always on is a warning nobody reads.
func (t Tampering) Flagged() bool { return len(t.Files) > 0 && !t.Asked }

// Message is what the human gate says about the change.
const TamperMessage = "this change edits tests; read these hunks before accepting"

// CheckTampering inspects a unified diff for changes to test files.
//
// globs may be empty, in which case DefaultTestGlobs applies.
func CheckTampering(diff, taskText string, globs []string) Tampering {
	if len(globs) == 0 {
		globs = DefaultTestGlobs
	}
	t := Tampering{Asked: MentionsTests(taskText)}

	var kept []string
	for _, section := range splitDiff(diff) {
		file := diffFile(section)
		if file == "" || !matchesAny(file, globs) {
			continue
		}
		t.Files = append(t.Files, file)
		kept = append(kept, strings.TrimRight(section, "\n"))
	}
	t.Hunks = strings.Join(kept, "\n")
	if t.Hunks != "" {
		t.Hunks += "\n"
	}
	return t
}

// splitDiff cuts a unified diff at each file header.
func splitDiff(diff string) []string {
	if strings.TrimSpace(diff) == "" {
		return nil
	}
	lines := strings.Split(diff, "\n")
	var out []string
	var cur []string
	for _, l := range lines {
		if strings.HasPrefix(l, "diff --git ") && len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

var gitHeaderRe = regexp.MustCompile(`^diff --git a/(.+?) b/(.+)$`)

// diffFile reads the path a diff section is about, preferring the new name.
//
// A test renamed out of the way is still a test being moved, so a rename is
// reported under whichever of its two names is a test.
func diffFile(section string) string {
	for _, l := range strings.Split(section, "\n") {
		if m := gitHeaderRe.FindStringSubmatch(l); m != nil {
			if matchesAny(m[2], DefaultTestGlobs) {
				return m[2]
			}
			if matchesAny(m[1], DefaultTestGlobs) {
				return m[1]
			}
			return m[2]
		}
		if strings.HasPrefix(l, "+++ b/") {
			return strings.TrimPrefix(l, "+++ b/")
		}
	}
	return ""
}

// matchesAny reports whether a path matches any of the globs.
//
// A `dir/**` glob matches everything under dir at any depth; anything else is
// matched against the base name, so `*_test.go` catches a test wherever in the
// tree it lives.
func matchesAny(file string, globs []string) bool {
	file = path.Clean(strings.TrimPrefix(file, "./"))
	for _, g := range globs {
		if strings.HasSuffix(g, "/**") {
			prefix := strings.TrimSuffix(g, "/**")
			if file == prefix {
				continue
			}
			if strings.HasPrefix(file, prefix+"/") {
				return true
			}
			// A tests/ directory nested anywhere, not only at the root.
			if strings.Contains(file, "/"+prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(g, path.Base(file)); ok {
			return true
		}
		if ok, _ := path.Match(g, file); ok {
			return true
		}
	}
	return false
}

// testWord matches the ways a task asks for test work without matching words
// that merely contain them, like "latest" or "contest".
var testWord = regexp.MustCompile(`(?i)\b(tests?|testing|spec|specs|assertions?|coverage|fixtures?)\b`)

// spineID matches a traceability reference: PREFIX-<digits> (02 §3).
var spineID = regexp.MustCompile(`\b[A-Za-z]+-\d+\b`)

// MentionsTests reports whether a task text asks for test work.
//
// Traceability references are removed first. Every properly traced task
// carries `**Implements:** SPEC-001` in its body, and `spec` is one of the
// words that says a human asked for test work — so without this the guard
// switched itself off on exactly the tasks that are best documented. A real
// run found it: the model edited the test that would have caught its change,
// and nothing was flagged.
func MentionsTests(taskText string) bool {
	return testWord.MatchString(spineID.ReplaceAllString(taskText, " "))
}
