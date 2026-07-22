package prim

import (
	"os/exec"
	"runtime"
	"strings"
)

// Shell runs cmd through the platform's shell (sh -c on Unix, cmd /C on
// Windows) inside dir, capturing combined stdout+stderr. It returns whether the
// command exited zero and the captured output. Errors are folded into the
// (false, output) result so callers can decide whether a non-zero exit matters.
func Shell(cmd, dir string) (bool, string) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Dir = dir
	out, err := c.CombinedOutput()
	return err == nil, string(out)
}

// Git runs a git subcommand string in dir and returns (ok, trimmedOutput).
func Git(args, dir string) (bool, string) {
	ok, out := Shell("git "+args, dir)
	return ok, strings.TrimSpace(out)
}

// CurrentBranch returns the checked-out branch name, or "main" when detached
// or otherwise unavailable.
func CurrentBranch(dir string) string {
	ok, out := Git("branch --show-current", dir)
	if !ok || out == "" {
		return "main"
	}
	return out
}

// IsDirty reports whether the working tree has uncommitted changes, returning
// the porcelain status lines for display.
func IsDirty(dir string) (bool, []string) {
	_, out := Git("status --porcelain", dir)
	out = strings.TrimSpace(out)
	if out == "" {
		return false, nil
	}
	return true, strings.Split(out, "\n")
}

// DiffAgainst stages everything and returns the diff of the working tree
// against base (used to snapshot a candidate solution as a patch).
func DiffAgainst(dir, base string) string {
	_, out := Git("add -A && git diff --cached "+base, dir)
	return out
}
