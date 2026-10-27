// Package daemon manages the engine process lifecycle: engine.json,
// pidfile, lock, auto-start, and graceful stop.
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// EngineInfo is the engine.json content.
//
// Token is the AGENT credential (B-530). engine.json is the file every client
// discovers the engine through — the CLI, `ducklab mcp serve`, a renderer, and
// any agent with a shell that goes looking — so the token in it must never be
// able to act as the person. An OpenCode operator was refused a human-only
// lane widening over MCP, read this file, POSTed the accept itself with an
// empty actor and was recorded as "human". The person's credential lives
// apart, in human.token (HumanTokenPath). The field keeps its name so a client
// from before the split still connects after an engine restart, as an agent.
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

// WriteEngineFiles publishes a starting engine: the person's credential
// first, then engine.json. A client discovers the engine through engine.json,
// so by the time it can see this engine the human.token beside it is already
// this engine's. The live-owner check runs before either write: an engine
// that loses the race must not clobber the running engine's human.token.
func WriteEngineFiles(info *EngineInfo, humanToken string) error {
	if existing, err := ReadEngineJSON(); err == nil {
		if processAlive(existing.PID) {
			return fmt.Errorf("engine.json belongs to live process %d", existing.PID)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing engine.json: %w", err)
	}
	if err := WriteHumanToken(humanToken); err != nil {
		return fmt.Errorf("write human.token: %w", err)
	}
	return WriteEngineJSON(info)
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

// ErrEngineLocked is returned when another engine holds the state directory.
var ErrEngineLocked = errors.New("another ducklab-engine holds the state directory")

// EngineLock is the engine's exclusive hold on its state directory.
//
// B-530 review: two engines started at once could each find no engine.json,
// each write its own human.token, and then race to engine.json — Codex
// reproduced engine.json naming engine B's agent token beside engine A's
// human.token, so the desktop lost every person-only door until a restart.
// The credential pair is only a pair if one engine at a time publishes it.
//
// The old engine.lock was an O_EXCL marker that nothing ever acquired: a
// marker file outlives the process that made it, so the first crash would
// have locked every later start out. This lock is an OS file lock (flock /
// LockFileEx) on engine.lock, held for the engine's whole life. The kernel
// drops it when the holder exits, cleanly or not, so a dead engine's lock
// never blocks a fresh start, and the file's mere existence means nothing.
type EngineLock struct{ f *os.File }

// AcquireEngineLock takes the state directory's engine lock, waiting up to
// wait for a holder that is on its way out (a restart starts the new engine
// as soon as the old one stops answering, while it may still be running its
// deferred cleanup). It returns ErrEngineLocked if the lock is still held.
func AcquireEngineLock(wait time.Duration) (*EngineLock, error) {
	path, err := LockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		ok, err := tryLockFile(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return &EngineLock{f: f}, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, ErrEngineLocked
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Release drops the lock. Closing the descriptor releases it; so does the
// process exiting.
func (l *EngineLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// GenerateToken generates a random bearer token.
//
// 256 bits from crypto/rand. It used to be the start time in hex nanoseconds,
// which engine.json's own started_at narrows to a guessable range: splitting
// the person's credential from the agent's is worth nothing if the person's
// can be computed (B-530).
func GenerateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// HumanTokenPath is where the engine writes the person's credential, the one
// the desktop and an interactive CLI present. It is a different file from
// engine.json (which carries the agent credential), mode 0600, rewritten at
// every engine start and removed at shutdown.
//
// Same-user processes can read it: a file mode cannot tell a person's
// terminal from an agent's shell. What the split buys is that no agent
// channel is HANDED it — the MCP server, the renderer and a non-interactive
// CLI read engine.json — so presenting it is a deliberate impersonation, not
// the default path B-530's operator took.
func HumanTokenPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "human.token"), nil
}

// WriteHumanToken writes the person's credential atomically, mode 0600.
func WriteHumanToken(token string) error {
	path, err := HumanTokenPath()
	if err != nil {
		return err
	}
	return xplat.AtomicWrite(path, []byte(token+"\n"), 0o600)
}

// ReadHumanToken reads the person's credential.
func ReadHumanToken() (string, error) {
	path, err := HumanTokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return token, nil
}

// DeleteHumanToken removes the person's credential file.
func DeleteHumanToken() error {
	path, err := HumanTokenPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// AsHuman returns a copy of info carrying the person's credential, for the
// desktop and an interactive CLI. An engine from before the credential split
// writes no human.token; its single token is then the only one there is, and
// the copy keeps it, so a new client still reaches an old engine.
func AsHuman(info *EngineInfo) *EngineInfo {
	if info == nil {
		return nil
	}
	out := *info
	if token, err := ReadHumanToken(); err == nil {
		out.Token = token
	}
	return &out
}

// StartEngine spawns the engine binary detached: the engine outlives whoever
// started it by design, so nothing waits on the process.
func StartEngine(path string) error {
	_, err := StartEnginePID(path)
	return err
}

// StartEnginePID starts the engine and returns its process id, so a caller
// can tell "still recovering" from "died".
func StartEnginePID(path string) (int, error) {
	cmd := exec.Command(path)
	prepareDetachedCommand(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start engine: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// StartCap bounds WaitReadyWhileAlive: generous, because it only matters when
// the engine is alive yet never serves.
const StartCap = 5 * time.Minute

// WaitReadyWhileAlive waits for the engine that StartEnginePID spawned. An
// engine that is still recovering its runs is not a failure — a state dir
// with a thousand runs took 25 s, and a fixed 15 s wait reported a healthy
// engine as "did not become ready" (B-298). So the wait is bounded by the
// process, not by a guess: it returns as soon as the engine serves, fails at
// once if the process exits first, and gives up only at the generous cap.
func WaitReadyWhileAlive(pid int, cap time.Duration) (*EngineInfo, error) {
	deadline := time.Now().Add(cap)
	for time.Now().Before(deadline) {
		if info, err := ReadEngineJSON(); err == nil && info.PID == pid && IsEngineRunning(info) {
			return info, nil
		}
		if !processAlive(pid) {
			return nil, fmt.Errorf("engine (pid %d) exited before it became ready", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("engine (pid %d) still starting after %s", pid, cap)
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
