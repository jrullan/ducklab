// Package xplat provides OS abstraction for ducklab: shell invocation,
// config/state/data directories, atomic file writes, and platform-specific
// helpers. All platform-dependent behavior lives here.
package xplat

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OS represents the operating system.
type OS string

const (
	Linux   OS = "linux"
	Darwin  OS = "darwin"
	Windows OS = "windows"
)

// CurrentOS returns the current operating system.
func CurrentOS() OS {
	switch runtime.GOOS {
	case "linux":
		return Linux
	case "darwin":
		return Darwin
	case "windows":
		return Windows
	default:
		return OS(runtime.GOOS)
	}
}

// ConfigDir returns the platform config directory for ducklab.
func ConfigDir() (string, error) {
	base, err := configBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ducklab"), nil
}

// DataDir returns the platform data directory for ducklab.
func DataDir() (string, error) {
	base, err := dataBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ducklab"), nil
}

// StateDir returns the platform state directory for ducklab.
func StateDir() (string, error) {
	base, err := stateBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ducklab"), nil
}

func configBase() (string, error) {
	switch CurrentOS() {
	case Linux:
		if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
			return dir, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config dir: %w", err)
		}
		return filepath.Join(home, ".config"), nil
	case Darwin:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case Windows:
		if dir := os.Getenv("AppData"); dir != "" {
			return dir, nil
		}
		return "", errors.New("config dir: %AppData% not set")
	default:
		return "", fmt.Errorf("config dir: unsupported OS %s", runtime.GOOS)
	}
}

func dataBase() (string, error) {
	switch CurrentOS() {
	case Linux:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return dir, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("data dir: %w", err)
		}
		return filepath.Join(home, ".local", "share"), nil
	case Darwin:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("data dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case Windows:
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return "", errors.New("data dir: %LocalAppData% not set")
	default:
		return "", fmt.Errorf("data dir: unsupported OS %s", runtime.GOOS)
	}
}

func stateBase() (string, error) {
	switch CurrentOS() {
	case Linux:
		if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
			return dir, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("state dir: %w", err)
		}
		return filepath.Join(home, ".local", "state"), nil
	case Darwin:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("state dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case Windows:
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir, nil
		}
		return "", errors.New("state dir: %LocalAppData% not set")
	default:
		return "", fmt.Errorf("state dir: unsupported OS %s", runtime.GOOS)
	}
}

// Shell runs a command through the platform shell. On Unix it uses /bin/sh -c;
// on Windows it uses cmd /C. The command runs with the given working directory
// and environment.
func Shell(workdir string, env []string, cmd string) *exec.Cmd {
	var c *exec.Cmd
	if CurrentOS() == Windows {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("/bin/sh", "-c", cmd)
	}
	c.Dir = workdir
	if env != nil {
		c.Env = env
	}
	return c
}

// ShellName returns the shell name for display purposes.
func ShellName() string {
	if CurrentOS() == Windows {
		return "cmd"
	}
	return "sh"
}

// AtomicWrite writes data to a file atomically: write to tmp, fsync, rename.
// The file gets the given mode. Parent directories are created.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("atomic write: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("atomic write: open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("atomic write: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("atomic write: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("atomic write: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("atomic write: rename: %w", err)
	}
	return nil
}

// ExpandHome expands a leading ~ or ~user to the user's home directory.
func ExpandHome(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if path[1] == '/' || path[1] == '\\' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	// ~user form
	sep := strings.IndexAny(path, "/\\")
	if sep < 0 {
		return "", fmt.Errorf("cannot expand %q", path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[sep+1:]), nil
}
