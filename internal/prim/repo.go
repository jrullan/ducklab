package prim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const gitIdentity = "-c user.email=duck@ducklab.local -c user.name=ducklab"

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	ok, out := Git("rev-parse --is-inside-work-tree", dir)
	return ok && strings.TrimSpace(out) == "true"
}

// EnsureRepo makes dir usable by ducklab's branch-based isolation. If dir is
// already a git repo it does nothing. Otherwise — the greenfield case — it
// runs `git init`, ignores ducklab's own runs/, and makes an initial commit so
// strategies have a base branch to fork from. Returns whether it initialized.
func EnsureRepo(dir string) (initialized bool, err error) {
	if IsRepo(dir) {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if ok, out := Git("init -q", dir); !ok {
		return false, fmt.Errorf("git init failed: %s", strings.TrimSpace(out))
	}
	ensureIgnored(dir, "runs/")
	ensureIgnored(dir, ".ducklab/")
	Git("add -A", dir)
	// --allow-empty so a truly empty folder still gets a base commit.
	if ok, out := Git(gitIdentity+` commit -q --allow-empty -m "ducklab: initialize repository"`, dir); !ok {
		return false, fmt.Errorf("initial commit failed: %s", strings.TrimSpace(out))
	}
	return true, nil
}

// ensureIgnored makes sure a pattern is present in the repo's .gitignore so
// ducklab's own directories never enter the user's history.
func ensureIgnored(dir, pattern string) {
	p := filepath.Join(dir, ".gitignore")
	data, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return
		}
	}
	body := string(data)
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += pattern + "\n"
	_ = os.WriteFile(p, []byte(body), 0o644)
}
