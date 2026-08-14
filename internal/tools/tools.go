// Package tools defines the tool interface, registry, and all built-in tools.
// Every tool a duckling can use lives here. Models never mutate version control
// and never decide verdicts; they only call tools that Ducklab executes.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jrullan/ducklab/internal/verify"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/xplat"
)

// Tool is the interface every tool implements.
type Tool interface {
	// Name returns the tool name.
	Name() string

	// Description returns a human-readable description.
	Description() string

	// Schema returns the JSON Schema for the tool's arguments.
	Schema() interface{}

	// Mutating reports whether the tool can change the working tree.
	// This is what makes a "read-only" toolbelt a computed property rather
	// than a hand-maintained list that drifts as tools are added (01 §4.4).
	Mutating() bool

	// Execute runs the tool with the given arguments.
	Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error)
}

// ErrHumanNeeded signals that a run must pause for human input. It is not a
// failure: the run is waiting, and waiting indefinitely is correct behaviour.
var ErrHumanNeeded = errors.New("human input needed")

// PendingQuestion is a question waiting for a human.
type PendingQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// ExecContext is the execution context for a tool call.
type ExecContext struct {
	// Answers holds already-given answers, keyed by QuestionID. A resumed run
	// replays its turn with these available so the same question does not
	// pause it a second time.
	Answers map[string]string
	// Pending is set by ask_human when the run must stop for a human.
	Pending *PendingQuestion
	// NoHuman disables pausing: the tool returns an error result instead, for
	// --output json and non-interactive contexts.
	NoHuman bool

	ProjectRoot  string
	RunID        string
	Turn         int
	Role         config.Role
	Duckling     config.DucklingID
	Autonomy     config.Autonomy
	UnsafeWrites bool
	ShellPolicy  config.ShellPolicy
	// TestPathsOnly restricts writes to test files.
	//
	// Set for a test-first run, where a duckling writes the test that will
	// judge the implementation. Enforced here rather than asked for in a
	// prompt: a prompt is a request, and this is a rule. Without it the model
	// writes the implementation too, the gate goes green immediately, and the
	// test has proved nothing.
	TestPathsOnly bool
	// SeatContextTokens is the acting seat's declared context window, set
	// per turn. Tool results scale to it: a flat 32KB cap handed a 32k-token
	// model a quarter of its whole context in ONE result — two verify_runs
	// and the model had forgotten its task, which is what a "loop" is.
	SeatContextTokens int
	// ConsecGateFails counts verify_run reds with no green between them,
	// across the whole run — the loop's own I3, at gate level.
	ConsecGateFails int
	// Verify is the project's gate. verify_run runs this and nothing else:
	// a tool that runs a different command from the gate that decides tells a
	// model its work passes when it does not.
	Verify config.Verify
	// ShellEnv is the environment for shell and skill commands. Nil means the
	// engine's own, which is the default.
	ShellEnv []string
	// GlobalSkillsDir is the machine-wide skills directory. A project skill of
	// the same name shadows what is here (05 §7).
	GlobalSkillsDir string
	// AllowUnacceptedSkills lets a skill be read and run before the run that
	// wrote it is accepted.
	//
	// False everywhere a model is driving, which is the default so that a new
	// call site that forgets this gets the safe behaviour. True only for a
	// person at a terminal: `ducklab skill run` is how someone tests a skill
	// they just wrote, and requiring them to commit it first to try it would
	// make writing one miserable. They are the accepter; there is nobody left
	// to protect them from.
	AllowUnacceptedSkills bool
	Registry              *Registry
}

// Result is the result of a tool execution.
type Result struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ErrorResult creates an error result.
func ErrorResult(format string, args ...interface{}) *Result {
	return &Result{
		Content: fmt.Sprintf(format, args...),
		IsError: true,
	}
}

// SuccessResult creates a success result.
func SuccessResult(format string, args ...interface{}) *Result {
	return &Result{
		Content: fmt.Sprintf(format, args...),
	}
}

// Registry manages available tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	r.registerBuiltins()
	return r
}

// registerBuiltins registers all built-in tools.
func (r *Registry) registerBuiltins() {
	// Filesystem
	r.Register(&FSList{})
	r.Register(&FSRead{})
	r.Register(&FSSearch{})
	r.Register(&FSWrite{})
	r.Register(&FSPatch{})
	r.Register(&FSDelete{})
	// Execution
	r.Register(&Shell{})
	r.Register(&VerifyRun{})
	// Human
	r.Register(&AskHuman{})
	// Lifecycle documents (read-only: a model proposes, it does not write)
	r.Register(&ArtifactRead{})
	r.Register(&TaskRead{})
	// Skills (05 §7). The documentation-only form is the default: reading a
	// skill cannot do anything to the project.
	r.Register(&SkillList{})
	r.Register(&SkillRead{})
	r.Register(&SkillRun{})
	// Version control (read-only)
	r.Register(&GitStatus{})
	r.Register(&GitDiff{})
	r.Register(&GitLog{})
	// The bug board. bug_read serves any belt that names it (the triager's
	// ceiling always did); bug_file is granted only by an explicit belt —
	// the chat's — and sits in no role's ceiling on purpose.
	r.Register(&BugRead{})
	r.Register(&RunListTool{})
	r.Register(&RunReadTool{})
	r.Register(&BugFile{})
}

// Register registers a tool.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return t, nil
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Execute executes a tool by name with the given arguments.
func (r *Registry) Execute(ctx context.Context, ectx *ExecContext, name string, args json.RawMessage) (*Result, error) {
	t, err := r.Get(name)
	if err != nil {
		return ErrorResult("unknown tool %q", name), nil
	}
	res, err := t.Execute(ctx, ectx, args)
	if res != nil {
		// Capped here rather than in each tool, so a tool cannot forget.
		//
		// Only the exec tools capped their own output. A triager searching a
		// project got a 290 KB result back — most of it the run's own
		// llm.jsonl — and the next request was refused for exceeding the
		// model's context by a factor of one. I3 says nothing is unbounded,
		// and a tool result is the largest thing a turn can pull in.
		res.Content = CapResult(res.Content, resultCapFor(ectx.SeatContextTokens))
	}
	return res, err
}

// resultCapFor scales the tool-result bound to the seat reading it: an
// eighth of the seat's context (tokens/8 ≈ bytes/2), floored so even a tiny
// model sees enough to act on, ceilinged at the flat cap big models keep.
func resultCapFor(contextTokens int) int {
	if contextTokens <= 0 {
		return MaxToolResultBytes
	}
	scaled := contextTokens / 2 // bytes: (tokens/8) * 4
	if scaled < 4096 {
		return 4096
	}
	if scaled > MaxToolResultBytes {
		return MaxToolResultBytes
	}
	return scaled
}

// MaxToolResultBytes bounds what one tool call can add to a conversation.
const MaxToolResultBytes = 32768

// IsHarnessPath reports whether a path belongs to ducklab's own record rather
// than to the project.
//
// .ducklab/runs holds every run's llm.jsonl: the full prompt and response of
// every duckling that has worked here. A tool that can read it hands a
// reviewer the implementer's reasoning transcript, which I7 exists to prevent,
// and hands any turn a file larger than the context it is running in — a
// triager pulled 290 KB of it into a search result and the next request was
// refused outright.
//
// The documents under .ducklab/docs stay readable: artifact_read is a tool,
// requirements and specs are meant to be read, and a duckling reading the plan
// it is implementing is the point.
func IsHarnessPath(root, abs string) bool {
	runs, err := filepath.EvalSymlinks(filepath.Join(root, ".ducklab", "runs"))
	if err != nil {
		runs = filepath.Join(root, ".ducklab", "runs")
	}
	return abs == runs || strings.HasPrefix(abs, runs+string(filepath.Separator))
}

// PathJail resolves a path within the project root and validates it doesn't escape.
// Returns the absolute path and whether it's inside the root.
// existingAncestor resolves the closest ancestor of a path that exists,
// following symlinks.
//
// It stops at the filesystem root, so it always terminates.
func existingAncestor(path string) (string, error) {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return resolved, nil
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", fmt.Errorf("no existing ancestor of %s", path)
		}
	}
}

func PathJail(root, path string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("project root is required")
	}
	// Clean the path
	clean := filepath.Clean(path)
	// If it's absolute, check it's inside root
	if filepath.IsAbs(clean) {
		abs, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return "", fmt.Errorf("path escapes root: %v", err)
		}
		rootAbs, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("root error: %v", err)
		}
		if !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) && abs != rootAbs {
			return "", fmt.Errorf("path escapes root: %s", path)
		}
		if IsHarnessPath(root, abs) {
			return "", fmt.Errorf("%s is ducklab's own run log, not project content", path)
		}
		return abs, nil
	}
	// Relative path: join with root and evaluate
	joined := filepath.Join(root, clean)
	abs, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// The path does not exist yet, which is the normal case for a write.
		// Resolve the nearest ancestor that does exist and check that instead.
		//
		// Checking only the immediate parent was wrong twice over: a file in a
		// directory that also did not exist was refused outright — though
		// fs_write creates parents and says so — and it was refused as "path
		// escapes root", which is a claim about a path that was always inside
		// it. Walking up is still safe: a symlink can only redirect through a
		// component that exists, so the nearest existing ancestor is where any
		// escape would have to happen.
		absAncestor, err2 := existingAncestor(joined)
		if err2 != nil {
			return "", fmt.Errorf("path escapes root: %v", err2)
		}
		rootAbs, _ := filepath.EvalSymlinks(root)
		if !strings.HasPrefix(absAncestor, rootAbs+string(filepath.Separator)) && absAncestor != rootAbs {
			return "", fmt.Errorf("path escapes root: %s", path)
		}
		if IsHarnessPath(root, joined) {
			return "", fmt.Errorf("%s is ducklab's own run log, not project content", path)
		}
		return joined, nil
	}
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("root error: %v", err)
	}
	if !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) && abs != rootAbs {
		return "", fmt.Errorf("path escapes root: %s", path)
	}
	if IsHarnessPath(root, abs) {
		return "", fmt.Errorf("%s is ducklab's own run log, not project content", path)
	}
	return abs, nil
}

// WriteGuard checks all write guard rules before a mutating filesystem call.
// Returns nil if the write is allowed, or an error result naming the rule.
func WriteGuard(ectx *ExecContext, path string, content []byte, isWrite bool) *Result {
	// 1. Jail check
	absPath, err := PathJail(ectx.ProjectRoot, path)
	if err != nil {
		return ErrorResult("jail: %v", err)
	}

	// 2. Test-first runs write tests and nothing else.
	if ectx.TestPathsOnly && !verify.IsTestPath(path, ectx.Verify.TestGlobs) {
		return ErrorResult("this run writes tests only, and %s is not one. "+
			"Write the failing test; the implementation is the next run's job.", path)
	}

	// 3. Denylist
	denylist := []string{
		".git",
		".ducklab/runs",
		".ducklab/ducklab.db",
		".ducklab/ducklab.db-wal",
		".ducklab/ducklab.db-shm",
		".ducklab/lock",
	}
	for _, denied := range denylist {
		// At a path boundary, not a string prefix: ".git" must refuse .git
		// and .git/config and never .gitignore — the string-prefix version
		// walled off the one file a task about ignoring things has to edit
		// (T-068), and the error blamed a directory the write never touched.
		deniedAbs := filepath.Join(ectx.ProjectRoot, denied)
		if absPath == deniedAbs || strings.HasPrefix(absPath, deniedAbs+string(filepath.Separator)) {
			return ErrorResult("denylist: write to %s is refused", denied)
		}
	}
	// User protected paths
	for _, protected := range ectx.ShellPolicy.Deny {
		if matched, _ := filepath.Match(protected, path); matched {
			return ErrorResult("denylist: write to %s is refused (protected path)", path)
		}
	}

	// 3. Marker guard (can be disabled with --unsafe-writes)
	if !ectx.UnsafeWrites {
		contentStr := string(content)
		lines := strings.Split(contentStr, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "<<<<<<<") ||
				strings.HasPrefix(trimmed, ">>>>>>>") ||
				(strings.HasPrefix(trimmed, "=======") && !isConflictedFile(absPath)) {
				return ErrorResult("marker guard: line %d contains conflict/protocol marker: %s", i+1, trimmed)
			}
			if strings.Contains(line, "```ducklab") || strings.Contains(line, "```payload:") {
				return ErrorResult("marker guard: line %d contains protocol marker", i+1)
			}
		}
	}

	// 4. Truncation guard (only for existing files, only for fs_write)
	if isWrite && !ectx.UnsafeWrites {
		if existing, err := os.ReadFile(absPath); err == nil {
			oldSize := len(existing)
			newSize := len(content)
			if oldSize > 200 && newSize < oldSize*40/100 {
				return ErrorResult("truncation guard: refused: this would shrink %s from %d to %d bytes. If that is intended, use fs_patch, or write the file in full.",
					path, oldSize, newSize)
			}
		}
	}

	// 5. Binary guard
	for _, b := range content {
		if b == 0 {
			return ErrorResult("binary guard: content contains NUL bytes")
		}
	}

	// 6. Size guard
	if len(content) > 1024*1024 {
		return ErrorResult("size guard: write over 1 MB refused")
	}

	return nil
}

func isConflictedFile(path string) bool {
	// A file is considered conflicted if it already contains conflict markers
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "<<<<<<<") && strings.Contains(content, ">>>>>>>")
}

// ShellPolicyCheck validates a shell command against the policy.
func ShellPolicyCheck(ectx *ExecContext, cmd string) *Result {
	policy := ectx.ShellPolicy

	if policy.Mode == "off" {
		return ErrorResult("shell tool is disabled by policy")
	}

	// Check denylist
	for _, deny := range policy.Deny {
		if strings.Contains(cmd, deny) {
			return ErrorResult("shell policy: command matches denylist entry: %s", deny)
		}
	}

	if policy.Mode == "free" {
		return nil // only denylist applies
	}

	// Guarded mode: must match an allow prefix
	matched := false
	for _, prefix := range policy.AllowPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			matched = true
			break
		}
	}
	if !matched {
		return ErrorResult("shell policy: command not in allowlist. Ask the human for approval, or use a different command.")
	}

	return nil
}

// ScrubEnv returns the process environment minus secrets.
func ScrubEnv(providers map[config.ProviderID]config.Provider) []string {
	env := os.Environ()
	scrubbed := make([]string, 0, len(env))
	secretNames := make(map[string]bool)
	for _, p := range providers {
		if p.APIKeyEnv != "" {
			secretNames[p.APIKeyEnv] = true
		}
	}
	for _, e := range env {
		name := strings.SplitN(e, "=", 2)[0]
		if secretNames[name] {
			continue
		}
		if strings.HasPrefix(name, "DUCKLAB_") {
			continue
		}
		scrubbed = append(scrubbed, e)
	}
	return scrubbed
}

// Digest computes a SHA256 digest of args for logging.
func Digest(args json.RawMessage) string {
	h := sha256.Sum256(args)
	return "sha256:" + hex.EncodeToString(h[:8])
}

// ToolSchema is a helper for creating JSON schemas.
type ToolSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property is a JSON schema property.
type Property struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Items       *Property   `json:"items,omitempty"`
	Default     interface{} `json:"default,omitempty"`
}

// NewSchema creates a new tool schema.
func NewSchema() *ToolSchema {
	return &ToolSchema{
		Type:       "object",
		Properties: make(map[string]Property),
	}
}

// AddString adds a string property.
func (s *ToolSchema) AddString(name, description string, required bool) *ToolSchema {
	s.Properties[name] = Property{Type: "string", Description: description}
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// AddInt adds an integer property.
func (s *ToolSchema) AddInt(name, description string, required bool) *ToolSchema {
	s.Properties[name] = Property{Type: "integer", Description: description}
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// AddBool adds a boolean property.
func (s *ToolSchema) AddBool(name, description string, required bool) *ToolSchema {
	s.Properties[name] = Property{Type: "boolean", Description: description}
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// AddObject adds an object property.
func (s *ToolSchema) AddObject(name, description string, required bool) *ToolSchema {
	s.Properties[name] = Property{Type: "object", Description: description}
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// AddArray adds an array property.
func (s *ToolSchema) AddArray(name, description string, items *Property, required bool) *ToolSchema {
	s.Properties[name] = Property{Type: "array", Description: description, Items: items}
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// CapResult caps a tool result to the maximum size.
func CapResult(result string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 32768
	}
	if len(result) <= maxBytes {
		return result
	}
	// Tail-biased: an error or a failing assertion is at the end of output,
	// and that is what a turn most often needs.
	//
	// Said out loud. Truncating in silence hands a model half a file and lets
	// it reason about the half as though it were the whole: it will conclude
	// a function is missing that was simply cut off.
	dropped := len(result) - maxBytes
	return fmt.Sprintf("[%d bytes truncated; showing the last %d]\n%s",
		dropped, maxBytes, result[dropped:])
}

// ParseArgs parses JSON arguments into a struct.
func ParseArgs(args json.RawMessage, v interface{}) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// ValidateAgainstSchema validates args against a tool's schema.
// This is a simplified validation; full JSON Schema validation is not implemented.
func ValidateAgainstSchema(tool Tool, args json.RawMessage) error {
	// For now, just check that args is valid JSON
	var raw interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// LineNumbers adds line numbers to content.
func LineNumbers(content string, start int) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%4d\t%s\n", start+i, line)
	}
	return b.String()
}

// TruncateLines truncates content to a line range.
//
// Each bound used to be clamped on its own, and nothing checked that the range
// ran forwards. A model asked for lines 93 to 78 — a range that reads backwards
// — and `lines[92:78]` panicked, which killed the goroutine running the turn
// and took the whole run down with it. A helper that formats text must not be
// able to do that whatever it is handed.
func TruncateLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) || start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// GlobMatch matches a path against a glob pattern.
func GlobMatch(pattern, path string) bool {
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	return matched
}

// SearchInContent searches for a regex pattern in content.
func SearchInContent(pattern, content string, maxResults int) []string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []string{fmt.Sprintf("invalid regex: %v", err)}
	}
	lines := strings.Split(content, "\n")
	var results []string
	for i, line := range lines {
		if re.MatchString(line) {
			results = append(results, fmt.Sprintf("%d: %s", i+1, line))
			if len(results) >= maxResults {
				break
			}
		}
	}
	return results
}

// RunShell runs a shell command and returns the result.
func RunShell(ctx context.Context, ectx *ExecContext, cmd string, timeoutS int) (string, int, error) {
	if timeoutS <= 0 {
		timeoutS = ectx.ShellPolicy.TimeoutS
	}
	if timeoutS <= 0 {
		timeoutS = 120
	}

	// I3: nothing is unbounded. This took a context and a timeout and used
	// neither, so `sleep 999` — or an install waiting on a prompt nobody was
	// there to answer — held the run open until a person noticed. The policy's
	// timeout_s was decorative.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()

	shellCmd := xplat.ShellContext(ctx, ectx.ProjectRoot, ectx.ShellEnv, cmd)
	output, err := shellCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(interface{ ExitCode() int }); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return string(output), -1, err
		}
	}
	// Say which limit ended it. A model reading "exit code: -1" and no output
	// concludes the command failed and tries a different one; reading that it
	// ran out of time, it can ask for longer or do less.
	if ctxErr := ctx.Err(); ctxErr != nil {
		reason := fmt.Sprintf("\n[command timed out after %ds and was killed]", timeoutS)
		if errors.Is(ctxErr, context.Canceled) {
			reason = "\n[run cancelled; command was killed]"
		}
		if exitCode == 0 {
			exitCode = -1
		}
		return string(output) + reason, exitCode, nil
	}
	return string(output), exitCode, nil
}
