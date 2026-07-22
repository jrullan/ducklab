package prim

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Gate is a run's verification tier: a command whose exit code is the objective
// ground truth, plus the kind of check it is. ducklab treats verification as a
// spectrum — real tests are strongest, a build/compile is shallower-but-real,
// and "none" means the reviewer and the human are the only gate. The kind is
// reported so a run never hides how much objective checking it actually had.
type Gate struct {
	Cmd  string // command to run; "" means no automated gate
	Kind string // "tests" | "build" | "custom" | "none"
}

// Active reports whether an automated gate will run.
func (g Gate) Active() bool { return g.Cmd != "" && g.Kind != "none" }

// Label is a human description for /config and /show.
func (g Gate) Label() string {
	if !g.Active() {
		return "unverified — no automated gate (reviewer + human only)"
	}
	return g.Kind + " (" + g.Cmd + ")"
}

// DetectGate picks the strongest available verification for a repo: real tests
// first, then a build/compile fallback, then none. Explicit user commands
// bypass this (see GateFromCmd).
func DetectGate(repo string) Gate {
	// Go: `go test ./...` compiles every package and runs any tests; with no
	// tests it still passes, so it doubles as a compile gate. Strongest for Go.
	if fileExists(repo, "go.mod") {
		return Gate{"go test ./...", "tests"}
	}
	// Python tests — only when test files actually exist (pytest exits non-zero
	// when it collects nothing, which would masquerade as a failure).
	if py := pythonPytest(repo); py != "" && hasPyTests(repo) {
		return Gate{py, "tests"}
	}
	// JS/TS tests via package.json.
	if npmHasTest(repo) {
		return Gate{"npm test", "tests"}
	}
	// Build/compile fallbacks (shallow but real).
	if fileExists(repo, "tsconfig.json") {
		return Gate{"npx --no-install tsc --noEmit", "build"}
	}
	if hasPyFiles(repo) {
		return Gate{"python3 -m compileall -q .", "build"}
	}
	return Gate{"", "none"}
}

// GateFromCmd wraps an explicit user command. Its kind is "custom" — the user
// vouched for it; ducklab does not second-guess whether it's tests or a build.
func GateFromCmd(cmd string) Gate {
	if strings.TrimSpace(cmd) == "" {
		return Gate{"", "none"}
	}
	return Gate{strings.TrimSpace(cmd), "custom"}
}

func fileExists(repo, name string) bool {
	_, err := os.Stat(filepath.Join(repo, name))
	return err == nil
}

func pythonPytest(repo string) string {
	for _, v := range []string{".venv", "venv"} {
		if fileExists(repo, filepath.Join(v, "bin", "pytest")) {
			return v + "/bin/pytest -q"
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".venv")); err == nil {
		return ""
	}
	// pytest on PATH
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "pytest")); err == nil {
			return "pytest -q"
		}
	}
	return ""
}

func hasPyTests(repo string) bool {
	for _, pat := range []string{"test_*.py", "*_test.py", "tests/*.py", "tests/**/*.py"} {
		if m, _ := filepath.Glob(filepath.Join(repo, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

func npmHasTest(repo string) bool {
	data, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	t, ok := pkg.Scripts["test"]
	return ok && !strings.Contains(t, "no test specified")
}

// hasPyFiles reports whether the repo contains any .py file (bounded walk,
// skipping vendored/venv/vcs dirs).
func hasPyFiles(repo string) bool {
	found := false
	skip := map[string]bool{".git": true, ".venv": true, "venv": true, "node_modules": true, "runs": true}
	_ = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
