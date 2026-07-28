// Package runlog manages the run directory and JSONL writers.
// Every run gets a directory under .ducklab/runs/<id>/ with state, events,
// llm calls, verify output, diff, and transcript.
package runlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/xplat"
)

// Run represents a run's persistent state.
type Run struct {
	ID            string                 `json:"id"`
	ProjectID     string                 `json:"project_id"`
	Stage         string                 `json:"stage"`
	Mode          string                 `json:"mode"`
	TaskID        string                 `json:"task_id"`
	Roster        map[string]string      `json:"roster"`
	Gate          string                 `json:"gate"`
	Status        string                 `json:"status"` // running|paused|done|failed|queued
	Verdict       string                 `json:"verdict"`
	Accepted      bool                   `json:"accepted"`
	CommitSHA     string                 `json:"commit_sha"`
	StartedAt     string                 `json:"started_at"`
	EndedAt       string                 `json:"ended_at"`
	WallclockMs   int64                  `json:"wallclock_ms"`
	PendingSince  string                 `json:"pending_since,omitempty"`
	PendingKind   string                 `json:"pending_kind,omitempty"` // gate|question
	PendingData   map[string]interface{} `json:"pending_data,omitempty"`
	UnsafeWrites  bool                   `json:"unsafe_writes"`
	Stream        bool                   `json:"stream"`
	DryRun        bool                   `json:"dry_run"`
	Autonomy      string                 `json:"autonomy"`
	Budget        BudgetState            `json:"budget"`
	Resolution    string                 `json:"resolution,omitempty"` // tournament resolution
	TestsModified bool                   `json:"tests_modified"`
	Warning       string                 `json:"warning,omitempty"`
}

// BudgetState tracks budget spend in a run.
type BudgetState struct {
	USD        float64 `json:"usd"`
	Tokens     int64   `json:"tokens"`
	Turns      int     `json:"turns"`
	WallclockS float64 `json:"wallclock_s"`
}

// Event is a single event in events.jsonl.
type Event struct {
	TS       string                 `json:"ts"`
	Seq      int                    `json:"seq"`
	Type     string                 `json:"type"`
	RunID    string                 `json:"run_id"`
	Round    int                    `json:"round,omitempty"`
	Turn     int                    `json:"turn,omitempty"`
	Role     string                 `json:"role,omitempty"`
	Duckling string                 `json:"duckling,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// LLMCall is a single LLM call record in llm.jsonl.
type LLMCall struct {
	TS           string                 `json:"ts"`
	Seq          int                    `json:"seq"`
	Duckling     string                 `json:"duckling"`
	Provider     string                 `json:"provider"`
	Model        string                 `json:"model"`
	Role         string                 `json:"role"`
	Request      map[string]interface{} `json:"request"`
	Response     map[string]interface{} `json:"response"`
	Usage        map[string]interface{} `json:"usage"`
	CostUSD      float64                `json:"cost_usd"`
	LatencyMs    int64                  `json:"latency_ms"`
	Attempt      int                    `json:"attempt"`
	Estimated    bool                   `json:"estimated"`
	CostSource   string                 `json:"cost_source"`
	FinishReason string                 `json:"finish_reason"`
}

// Writer manages writing run files.
type Writer struct {
	runDir    string
	run       *Run
	eventsMu  sync.Mutex
	eventsSeq int
	closed    bool
	eventsF   *os.File
	llmMu     sync.Mutex
	llmSeq    int
	llmF      *os.File

	// OnEvent, if set, is called with every event after it has been durably
	// appended to events.jsonl. This is the single point where persisted
	// events reach the bus, so a subscriber can never see an event that is
	// not also on disk. Must not block.
	OnEvent func(*Event)
}

// NewWriter creates a writer for a new run.
func NewWriter(projectRoot string, run *Run) (*Writer, error) {
	return openWriter(projectRoot, run, false)
}

// OpenWriter creates a writer for an existing run, continuing its sequence
// numbers from what is already on disk. Used when a run is rehydrated after
// an engine restart: seq must never restart, or SSE resume breaks.
func OpenWriter(projectRoot string, run *Run) (*Writer, error) {
	return openWriter(projectRoot, run, true)
}

func openWriter(projectRoot string, run *Run, resume bool) (*Writer, error) {
	runDir := filepath.Join(projectRoot, ".ducklab", "runs", run.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	w := &Writer{
		runDir: runDir,
		run:    run,
	}
	if resume {
		w.eventsSeq = lastSeq(filepath.Join(runDir, "events.jsonl"))
		w.llmSeq = lastSeq(filepath.Join(runDir, "llm.jsonl"))
	}
	// Open events.jsonl
	eventsPath := filepath.Join(runDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events.jsonl: %w", err)
	}
	w.eventsF = f
	// Open llm.jsonl
	llmPath := filepath.Join(runDir, "llm.jsonl")
	f2, err := os.OpenFile(llmPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		w.eventsF.Close()
		return nil, fmt.Errorf("open llm.jsonl: %w", err)
	}
	w.llmF = f2
	// Write initial state
	if err := w.WriteState(); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// Close closes the writer.
func (w *Writer) Close() error {
	w.eventsMu.Lock()
	w.closed = true
	w.eventsMu.Unlock()
	if w.eventsF != nil {
		w.eventsF.Close()
	}
	if w.llmF != nil {
		w.llmF.Close()
	}
	return nil
}

// Closed reports whether this writer has been closed.
//
// Close left eventsF non-nil, so a later AppendEvent wrote to a closed
// descriptor and returned an error that callers ignored. State kept updating —
// WriteState writes by path — while the events that explain the state
// vanished. Accepting a run recorded the commit and never recorded the accept.
func (w *Writer) Closed() bool {
	w.eventsMu.Lock()
	defer w.eventsMu.Unlock()
	return w.closed
}

// WriteState writes state.json atomically.
func (w *Writer) WriteState() error {
	data, err := json.MarshalIndent(w.run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	path := filepath.Join(w.runDir, "state.json")
	return xplat.AtomicWrite(path, data, 0o644)
}

// AppendEvent appends an event to events.jsonl.
func (w *Writer) AppendEvent(eventType string, data map[string]interface{}) error {
	w.eventsMu.Lock()
	defer w.eventsMu.Unlock()
	if w.closed {
		// Named rather than surfaced as a raw "file already closed", because
		// the caller's mistake is reusing the writer, not the write itself.
		return fmt.Errorf("append %s: run log is closed", eventType)
	}
	w.eventsSeq++
	e := Event{
		TS:    time.Now().UTC().Format(time.RFC3339Nano),
		Seq:   w.eventsSeq,
		Type:  eventType,
		RunID: w.run.ID,
		Data:  data,
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := w.eventsF.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if w.OnEvent != nil {
		// Published only after the durable append, and while still holding
		// eventsMu, so bus delivery order matches on-disk seq order.
		w.OnEvent(&e)
	}
	return nil
}

// AppendLLM appends an LLM call to llm.jsonl.
func (w *Writer) AppendLLM(call *LLMCall) error {
	w.llmMu.Lock()
	defer w.llmMu.Unlock()
	w.llmSeq++
	call.Seq = w.llmSeq
	call.TS = time.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("marshal llm call: %w", err)
	}
	if _, err := w.llmF.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write llm call: %w", err)
	}
	return nil
}

// WriteVerify writes verify.log.
func (w *Writer) WriteVerify(output string) error {
	path := filepath.Join(w.runDir, "verify.log")
	return os.WriteFile(path, []byte(output), 0o644)
}

// WriteDiff writes diff.patch.
func (w *Writer) WriteDiff(diff string) error {
	path := filepath.Join(w.runDir, "diff.patch")
	return os.WriteFile(path, []byte(diff), 0o644)
}

// WriteTranscript writes transcript.md.
func (w *Writer) WriteTranscript(content string) error {
	path := filepath.Join(w.runDir, "transcript.md")
	return os.WriteFile(path, []byte(content), 0o644)
}

// WriteCandidate writes a candidate patch.
func (w *Writer) WriteCandidate(label, diff string) error {
	dir := filepath.Join(w.runDir, "candidates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, label+".patch")
	return os.WriteFile(path, []byte(diff), 0o644)
}

// RunDir returns the run directory path.
func (w *Writer) RunDir() string {
	return w.runDir
}

// ReadState reads a run's state.json.
func ReadState(runDir string) (*Run, error) {
	path := filepath.Join(runDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// ReadEvents reads all events from events.jsonl.
func ReadEvents(runDir string) ([]*Event, error) {
	path := filepath.Join(runDir, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []*Event
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// Torn tail: stop reading
			break
		}
		events = append(events, &e)
	}
	return events, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// lastSeq returns the highest seq recorded in a JSONL file, or 0.
// A torn final line is ignored, matching ReadEvents.
func lastSeq(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	max := 0
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var probe struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			break
		}
		if probe.Seq > max {
			max = probe.Seq
		}
	}
	return max
}

// RunDirFor returns the directory for a run without needing an open writer.
func RunDirFor(projectRoot, runID string) string {
	return filepath.Join(projectRoot, ".ducklab", "runs", runID)
}

// ListRuns lists all run IDs for a project.
func ListRuns(projectRoot string) ([]string, error) {
	runsDir := filepath.Join(projectRoot, ".ducklab", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// GenerateRunID generates a run ID: r-<YYYYMMDD>-<HHMMSS>-<4 base32>.
func GenerateRunID() string {
	now := time.Now().UTC()
	ts := now.Format("20060102-150405")
	// Simple 4-char base32 suffix from nanoseconds
	ns := now.Nanosecond()
	chars := "abcdefghijklmnopqrstuvwxyz234567"
	suffix := make([]byte, 4)
	for i := 0; i < 4; i++ {
		suffix[i] = chars[ns%32]
		ns /= 32
	}
	return fmt.Sprintf("r-%s-%s", ts, string(suffix))
}

// ReadJSONL reads a JSONL file as generic objects, skipping records at or
// below fromSeq. A torn final line is ignored, matching ReadEvents.
func ReadJSONL(path string, fromSeq int) ([]map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []map[string]interface{}
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			break
		}
		if seq, ok := rec["seq"].(float64); ok && int(seq) <= fromSeq {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}
