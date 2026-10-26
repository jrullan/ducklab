// Package gittest makes git-using tests hermetic against the developer's own
// git configuration (B-526).
//
// Fixtures that ran a bare `git init` and then pushed `master` passed on a
// machine whose init.defaultBranch was unset and failed on one where it was
// `main`: the result depended on ~/.gitconfig, not on the code. The same door
// lets hooks, commit signing, templates, credential helpers or a pager change
// what a test observes.
//
// Isolate, called from a package's TestMain, points every git process the
// tests start at a private global config and ignores the system config. That
// private config deliberately names an initial branch that is neither `main`
// nor `master` (DefaultBranch), so a fixture that silently assumes either one
// fails on every machine instead of only on some. A fixture names its branch
// (`git init -b main`) or reads the current one.
//
// Only test files import this package.
package gittest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultBranch is the initial branch a bare `git init` produces under
// Isolate. It is intentionally a name no fixture or production default uses.
const DefaultBranch = "ducklab-test-unnamed-branch"

// Identity committed under Isolate, so fixtures can commit without each one
// configuring user.name/user.email (the developer's global config used to
// provide it silently).
const (
	UserName  = "ducklab test"
	UserEmail = "test@ducklab.invalid"
)

// Isolate replaces the process's git configuration environment with a
// hermetic one and returns a cleanup function that removes its temporary
// directory. It must run before any test starts (TestMain): it mutates the
// process environment that every exec'd git inherits.
func Isolate() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "ducklab-gitconfig-")
	if err != nil {
		return nil, err
	}
	global := filepath.Join(dir, "gitconfig")
	body := fmt.Sprintf("[init]\n\tdefaultBranch = %s\n[user]\n\tname = %s\n\temail = %s\n[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n",
		DefaultBranch, UserName, UserEmail)
	if err := os.WriteFile(global, []byte(body), 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// Repository discovery and command-scope overrides inherited from the
	// caller (a `make` run from inside a git hook, or GIT_CONFIG_COUNT /
	// GIT_CONFIG_PARAMETERS exported by a wrapper) leak exactly as
	// ~/.gitconfig did, and outrank the global file.
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
		"GIT_CEILING_DIRECTORIES", "GIT_TEMPLATE_DIR", "GIT_CONFIG",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_SYSTEM",
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	} {
		os.Unsetenv(key)
	}
	if n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")); err == nil {
		for i := 0; i < n; i++ {
			os.Unsetenv("GIT_CONFIG_KEY_" + strconv.Itoa(i))
			os.Unsetenv("GIT_CONFIG_VALUE_" + strconv.Itoa(i))
		}
	}
	os.Unsetenv("GIT_CONFIG_COUNT")
	os.Setenv("GIT_CONFIG_GLOBAL", global)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_TERMINAL_PROMPT", "0")
	return func() { os.RemoveAll(dir) }, nil
}

// Main is the whole TestMain for a package that needs nothing else:
// gittest.Main(m.Run) isolates git, runs the tests and exits with their
// status.
func Main(run func() int) {
	cleanup, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gittest:", err)
		os.Exit(1)
	}
	code := run()
	cleanup()
	os.Exit(code)
}
