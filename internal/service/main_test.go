package service

import (
	"fmt"
	"os"
	"testing"

	"github.com/jrullan/ducklab/internal/gittest"
)

// TestMain keeps the package's tests off the developer's real data
// directory. Capability probes and run image evidence (B-515) write
// caps.json under XDG_DATA_HOME; tests that built a loop without setting it
// wrote fake providers ("fake:m-pato-uno") into the person's own cache.
// A test that needs its own directory still sets XDG_DATA_HOME itself.
//
// It also makes every git process the tests start ignore the developer's
// global and system git config (B-526): remote fixtures pushed `master` after
// a bare `git init` and failed wherever init.defaultBranch was `main`.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ducklab-service-data-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_DATA_HOME", dir)
	cleanupGit, err := gittest.Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanupGit()
	os.RemoveAll(dir)
	os.Exit(code)
}
