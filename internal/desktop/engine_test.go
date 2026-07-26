package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeExe(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func engineName() string {
	if runtime.GOOS == "windows" {
		return "ducklab-engine.exe"
	}
	return "ducklab-engine"
}

// AC-27: the bundled engine wins over anything on PATH. A stale engine on a
// developer's PATH must never be picked up by the packaged app.
func TestBundledEngineBeatsPATH(t *testing.T) {
	// os.Executable() points at the test binary; put a sibling next to it.
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot resolve test executable")
	}
	bundled := filepath.Join(filepath.Dir(exe), engineName())
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { os.Remove(bundled) })

	onPath := writeExe(t, t.TempDir(), engineName())
	lookPath := func(string) (string, error) { return onPath, nil }

	got, err := ResolveEngine("", lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != bundled {
		t.Errorf("resolved %q, want the bundled copy %q — a stale PATH engine would be used", got, bundled)
	}
}

// With no bundled copy, PATH is the fallback.
func TestFallsBackToPATH(t *testing.T) {
	exe, _ := os.Executable()
	os.Remove(filepath.Join(filepath.Dir(exe), engineName())) // ensure absent

	onPath := writeExe(t, t.TempDir(), engineName())
	got, err := ResolveEngine("", func(string) (string, error) { return onPath, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != onPath {
		t.Errorf("resolved %q, want %q", got, onPath)
	}
}

// An explicit override wins over everything.
func TestConfiguredPathWins(t *testing.T) {
	configured := writeExe(t, t.TempDir(), "my-engine")
	got, err := ResolveEngine(configured, func(string) (string, error) {
		t.Error("PATH was consulted despite an explicit override")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != configured {
		t.Errorf("resolved %q, want %q", got, configured)
	}
}

// A broken override must fail loudly rather than silently using a different
// binary than the user asked for.
func TestBrokenConfiguredPathIsAnError(t *testing.T) {
	onPath := writeExe(t, t.TempDir(), engineName())
	_, err := ResolveEngine("/nonexistent/engine", func(string) (string, error) { return onPath, nil })
	if err == nil {
		t.Fatal("a missing configured engine silently fell through to PATH")
	}
}

// A directory named like the engine is not an engine.
func TestDirectoryIsNotAnExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, engineName()), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEngine(filepath.Join(dir, engineName()), nil); err == nil {
		t.Error("a directory was accepted as the engine")
	}
}

func TestNotFoundAnywhereIsAClearError(t *testing.T) {
	exe, _ := os.Executable()
	os.Remove(filepath.Join(filepath.Dir(exe), engineName()))

	_, err := ResolveEngine("", func(string) (string, error) {
		return "", fmt.Errorf("not found")
	})
	if err == nil {
		t.Fatal("expected an error when the engine is nowhere")
	}
}
