// Package desktop is the thin adapter the Wails app sits on. It resolves and
// supervises the engine, and exposes nothing the CLI cannot also do (01 §1.2).
//
// It imports engineclt, daemon and registry only: the desktop app is a client,
// and reaching into a domain package here would let the UI grow behaviour the
// CLI lacks.
package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jrullan/ducklab/internal/daemon"
)

// ResolveEngine finds the ducklab-engine binary to use.
//
// Order matters and is the whole point (07 §7.1): the copy shipped beside the
// app wins over anything on PATH. A developer with an old `ducklab-engine` on
// PATH must not have the packaged app silently talk to it — version skew
// between a bundled UI and a stale engine is the kind of bug that looks like
// data corruption.
//
//  1. the configured override, if set
//  2. next to the running executable (the bundled copy)
//  3. PATH
func ResolveEngine(configured string, lookPath func(string) (string, error)) (string, error) {
	name := "ducklab-engine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if configured != "" {
		if isExecutable(configured) {
			return configured, nil
		}
		// An explicit override that does not exist is an error, not a reason
		// to quietly fall through to a different binary.
		return "", fmt.Errorf("configured engine path %q is not executable", configured)
	}

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		bundled := filepath.Join(filepath.Dir(exe), name)
		if isExecutable(bundled) {
			return bundled, nil
		}
	}

	if lookPath == nil {
		return "", fmt.Errorf("engine %q not found beside the app and no PATH lookup available", name)
	}
	found, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("engine %q not found beside the app or on PATH", name)
	}
	return found, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// EnsureEngine returns a live engine, starting one if needed.
//
// Mirrors the CLI's discovery (07 §7.1) rather than reimplementing it: the
// desktop app and the CLI must reach the same engine, or a run started in one
// would be invisible in the other.
func EnsureEngine(enginePath string, wait time.Duration) (*daemon.EngineInfo, error) {
	if info, err := daemon.ReadEngineJSON(); err == nil && daemon.IsEngineRunning(info) {
		return info, nil
	}
	if enginePath == "" {
		return nil, fmt.Errorf("no engine binary found and none is running")
	}

	cmd := exec.Command(enginePath)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}
	// The engine outlives this process by design; do not wait on it.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if info, err := daemon.ReadEngineJSON(); err == nil && daemon.IsEngineRunning(info) {
			return info, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("engine did not become ready within %s", wait)
}
