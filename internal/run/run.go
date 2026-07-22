// Package run owns the on-disk record of a single ducklab execution: a
// runs/<task_id>/ directory holding every artifact plus a resumable state.json.
// The state machine is the source of truth — a crashed or cancelled run resumes
// from exactly where it stopped.
package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Transition records one state-machine edge for the audit trail.
type Transition struct {
	From string `json:"from"`
	To   string `json:"to"`
	At   string `json:"at"`
}

// State is the serialized machine plus a free-form bag for strategy data.
type State struct {
	State   string         `json:"state"`
	History []Transition   `json:"history"`
	Data    map[string]any `json:"data"`
}

// Run is a handle to a run directory.
type Run struct {
	Dir   string
	State State
}

// Open loads (or initializes) the run at dir.
func Open(dir string) (*Run, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r := &Run{Dir: dir, State: State{State: "INTAKE", Data: map[string]any{}}}
	if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		if err := json.Unmarshal(data, &r.State); err != nil {
			return nil, err
		}
	}
	if r.State.Data == nil {
		r.State.Data = map[string]any{}
	}
	return r, nil
}

// Advance transitions to newState and persists.
func (r *Run) Advance(newState string) error {
	r.State.History = append(r.State.History, Transition{
		From: r.State.State, To: newState, At: time.Now().UTC().Format(time.RFC3339),
	})
	r.State.State = newState
	return r.Save()
}

// Set stores a strategy value and persists.
func (r *Run) Set(key string, val any) error {
	r.State.Data[key] = val
	return r.Save()
}

// Get returns a stored value.
func (r *Run) Get(key string) (any, bool) {
	v, ok := r.State.Data[key]
	return v, ok
}

// Save writes state.json.
func (r *Run) Save() error {
	data, err := json.MarshalIndent(r.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Dir, "state.json"), data, 0o644)
}

// Write drops an artifact file into the run directory.
func (r *Run) Write(name, content string) error {
	return os.WriteFile(filepath.Join(r.Dir, name), []byte(content), 0o644)
}

// Read returns an artifact's contents, or "" and false when absent.
func (r *Run) Read(name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(r.Dir, name))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// LogPath is the per-run JSONL sink for model-call observability.
func (r *Run) LogPath() string { return filepath.Join(r.Dir, "llm_log.jsonl") }
