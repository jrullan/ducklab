// Package tools defines the tool interface, registry, and all built-in tools.
// Every tool a duckling can use lives here. Models never mutate version control
// and never decide verdicts; they only call tools that Ducklab executes.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

// ExecContext is the execution context for a tool call.
type ExecContext struct {
	ProjectRoot  string
	RunID        string
	Turn         int
	Role         config.Role
	Duckling     config.DucklingID
	Autonomy     config.Autonomy
	UnsafeWrites bool
	ShellPolicy  config.ShellPolicy
	Registry     *Registry
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
	// Version control (read-only)
	r.Register(&GitStatus{})
	r.Register(&GitDiff{})
	r.Register(&GitLog{})
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
	return t.Execute(ctx, ectx, args)
}

// PathJail resolves a path within the project root and validates it doesn't escape.
// Returns the absolute path and whether it's inside the root.
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
		return abs, nil
	}
	// Relative path: join with root and evaluate
	joined := filepath.Join(root, clean)
	abs, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// If the file doesn't exist yet, check the parent
		parent := filepath.Dir(joined)
		absParent, err2 := filepath.EvalSymlinks(parent)
		if err2 != nil {
			return "", fmt.Errorf("path escapes root: %v", err)
		}
		rootAbs, _ := filepath.EvalSymlinks(root)
		if !strings.HasPrefix(absParent, rootAbs+string(filepath.Separator)) && absParent != rootAbs {
			return "", fmt.Errorf("path escapes root: %s", path)
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

	// 2. Denylist
	denylist := []string{
		".git",
		".ducklab/runs",
		".ducklab/ducklab.db",
		".ducklab/ducklab.db-wal",
		".ducklab/ducklab.db-shm",
		".ducklab/lock",
	}
	for _, denied := range denylist {
		if strings.HasPrefix(absPath, filepath.Join(ectx.ProjectRoot, denied)) {
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
	// Tail-biased: keep the end
	return result[len(result)-maxBytes:]
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
func TruncateLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
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

	shellCmd := xplat.Shell(ectx.ProjectRoot, nil, cmd)
	output, err := shellCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(interface{ ExitCode() int }); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return string(output), -1, err
		}
	}
	return string(output), exitCode, nil
}
