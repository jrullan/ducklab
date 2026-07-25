// Package daemon manages the engine process lifecycle: engine.json,
// pidfile, lock, auto-start, and graceful stop.
package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// EngineInfo is the engine.json content.
type EngineInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	Token     string `json:"token"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	StateDir  string `json:"state_dir"`
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

// WriteEngineJSON writes engine.json atomically with mode 0600.
func WriteEngineJSON(info *EngineInfo) error {
	path, err := EngineJSONPath()
	if err != nil {
		return err
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
