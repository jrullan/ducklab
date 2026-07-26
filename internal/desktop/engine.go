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
	"path/filepath"
	"runtime"
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
