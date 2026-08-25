package xplat

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The T-075 night: four pytest orphans, one per stalled run, each surviving
// its 900s gate timeout AND engine restarts, each holding database
// connections that hung the next run's suite harder. This reproduces the
// exact shape — sh spawning a grandchild that sleeps — and pins both halves
// of the contract: the call RETURNS promptly at timeout, and the whole
// process GROUP is dead afterwards.
func TestShellContextKillsTheWholeGroupAtTimeout(t *testing.T) {
	marker := "xplat_killrepro_sleeper"
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	c := ShellContext(ctx, t.TempDir(), nil,
		"python3 -c 'import time; time.sleep(300) # "+marker+"'")
	start := time.Now()
	_, _ = c.CombinedOutput()
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("CombinedOutput blocked %s past a 1s timeout — the orphan held the pipe", took)
	}

	// The group must be dead: no survivor holding the tree or a database.
	// Under parallel gate load, scheduling the kill and reaping the children can
	// take longer than a single short sleep. Poll with a generous deadline so
	// this still checks the same contract without making CPU contention a flake.
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := exec.Command("pgrep", "-af", marker).Output()
		if s := strings.TrimSpace(string(out)); s == "" {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("orphan survived the group kill:\n%s", s)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
