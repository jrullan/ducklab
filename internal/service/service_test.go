package service

import (
	"os"
	"path/filepath"
	"testing"
)

// A path typed by a person is not a path the process can use.
//
// `~/dev/calculator` produced a directory literally named "~", nested under
// wherever the engine happened to be started. The project was created, the
// runs worked, and it was somewhere nobody would ever look. Reported from a
// real session.
func TestResolveProjectPathExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for in, want := range map[string]string{
		"~/dev/calculator": filepath.Join(home, "dev", "calculator"),
		"~":                home,
		"~/":               home,
	} {
		got, err := resolveProjectPath(in)
		if err != nil {
			t.Errorf("resolveProjectPath(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("resolveProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A relative path resolved against the engine's working directory, which is
// wherever someone happened to launch the daemon — arbitrary, invisible, and
// different for every client that asked.
func TestResolveProjectPathRefusesRelativePaths(t *testing.T) {
	for _, in := range []string{"dev/calculator", "./calculator", "../calculator", ""} {
		if _, err := resolveProjectPath(in); err == nil {
			t.Errorf("resolveProjectPath(%q) was accepted", in)
		}
	}
}

// A path that is already absolute is left exactly as it is.
func TestResolveProjectPathLeavesAbsolutePathsAlone(t *testing.T) {
	got, err := resolveProjectPath("/tmp/calculator")
	if err != nil || got != "/tmp/calculator" {
		t.Errorf("got %q, %v", got, err)
	}
}

// "~user/x" is a shell feature this does not implement, and silently treating
// it as a relative directory would repeat the original bug in a new shape.
func TestResolveProjectPathRefusesOtherUsersHomes(t *testing.T) {
	if _, err := resolveProjectPath("~someone/dev"); err == nil {
		t.Error("~someone/dev was accepted")
	}
}
