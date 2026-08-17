// Package daemon manages the engine process lifecycle: engine.json,
// pidfile, lock, auto-start, and graceful stop.
package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// EngineInfo is the engine.json content.
type EngineInfo struct {
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	Token      string `json:"token"`
	Version    string `json:"version"`
	Provenance string `json:"provenance,omitempty"`
	StartedAt  string `json:"started_at"`
	StateDir   string `json:"state_dir"`
}

// StateDir returns the engine state directory.
func StateDir() (string, error) {
	return xplat.StateDir()
}

// EngineJSONPath returns the path to engine.json.
func EngineJSONPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "engine.json"), nil
}

// ReadEngineJSON reads engine.json.
func ReadEngineJSON() (*EngineInfo, error) {
	path, err := EngineJSONPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info EngineInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// WriteEngineJSON writes engine.json atomically with mode 0600. It refuses to
// replace a record belonging to a process that is still alive.
func WriteEngineJSON(info *EngineInfo) error {
	path, err := EngineJSONPath()
	if err != nil {
		return err
	}
	existing, err := ReadEngineJSON()
	if err == nil {
		if processAlive(existing.PID) {
			return fmt.Errorf("engine.json belongs to live process %d", existing.PID)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing engine.json: %w", err)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return xplat.AtomicWrite(path, data, 0o600)
}

// DeleteEngineJSON deletes engine.json.
func DeleteEngineJSON() error {
	path, err := EngineJSONPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// IsEngineRunning checks if the engine is running by hitting the health endpoint.
func IsEngineRunning(info *EngineInfo) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/health", info.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// LockPath returns the path to engine.lock.
func LockPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "engine.lock"), nil
}

// TryLock attempts to create the lock file exclusively.
// Returns true if the lock was acquired, false if another process holds it.
func TryLock() (bool, error) {
	path, err := LockPath()
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	f.Close()
	return true, nil
}

// Unlock removes the lock file.
func Unlock() error {
	path, err := LockPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// GenerateToken generates a random bearer token.
func GenerateToken() (string, error) {
	// Use crypto/rand via xplat or a simple approach
	// For now, use a timestamp-based token; in production use crypto/rand
	return fmt.Sprintf("%x", time.Now().UnixNano()), nil
}

// StartEngine spawns the engine binary detached: the engine outlives whoever
// started it by design, so nothing waits on the process.
func StartEngine(path string) error {
	cmd := exec.Command(path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// WaitReady polls until a healthy engine has written its connection details,
// or the deadline passes.
func WaitReady(wait time.Duration) (*EngineInfo, error) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if info, err := ReadEngineJSON(); err == nil && IsEngineRunning(info) {
			return info, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("engine did not become ready within %s", wait)
}

// WaitGone polls until the recorded engine process has exited, or the
// deadline passes. A restart that spawns before the old process released its
// port and state file races both.
func WaitGone(wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		info, err := ReadEngineJSON()
		if err != nil || !IsEngineRunning(info) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("engine still running after %s", wait)
}
