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
	"path"
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
	// DeterministicAnswers are facts the harness can answer without a person,
	// keyed by a lowercase phrase expected in the question. A plan architect
	// paused Neocapture to ask for its project root even though Ducklab had
	// already supplied that root to every filesystem tool.
	DeterministicAnswers map[string]string
	// Pending is set by ask_human when the run must stop for a human.
	Pending *PendingQuestion
	// NoHuman disables pausing: the tool returns an error result instead, for
	// --output json and non-interactive contexts.
	NoHuman bool

	ProjectRoot string
	// DocsRoot is where the project's documents live when the run works in
	// an isolated worktree: the worktree has the code, the project has
	// .ducklab/docs. Left empty, ProjectRoot is used. A build implementer
	// asked for its own task and the spec and was told neither existed
	// (T-001, benchmark run 5).
	DocsRoot     string
	RunID        string
	ProjectID    string
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
	// OnDistress reports operator-relevant brakes without coupling tools to the bus.
	OnDistress func(reason string, data map[string]interface{})
	// OnAskAdvisor answers an implementer's mid-turn consult (ask_advisor)
	// with the advisor seat's reply. Nil means no advisor is seated: the tool
	// says so and the model carries on with its own judgement.
	OnAskAdvisor func(ctx context.Context, question string) (string, error)
	// OnRosterRead renders the resolved team — seats, evidence, suggestions —
	// for roster_read. Injected by the service (tools is a leaf); nil means
	// the tool says the roster is not readable here.
	OnRosterRead func(ctx context.Context) (string, error)
	// RefPaths are the reference documents this run was launched with — the
	// only files ref_read may open. References live outside the project root
	// on purpose; the tool is a bridge to THEM, not out of the sandbox.
	RefPaths []string
	// OnRefRead marks a reference as opened, so the gate can name the ones
	// that never were.
	OnRefRead func(path string)
	// lastFailSig and lastFailCount track the most recent FAILING call's
	// tool+args, for the repetition brake: a small model that gets its
	// arguments wrong retries the identical call — six artifact_reads of
	// {"id":"plan"} on one run — because the error reads like disagreement,
	// not correction. The third identical failure is refused with orders to
	// change something.
	lastFailSig   string
	lastFailCount int
	// turnReads remembers the successful read-only calls of the current
	// turn, so an identical re-read is refused with directions (BeginTurn).
	turnReads map[string]int
	// searchMisses counts consecutive fs_search calls that found nothing.
	searchMisses int
	// ToolsClosed is set once a tool result asked to end the reply: the
	// loop offers no tools on the next call and refuses any further call.
	ToolsClosed bool
	// DraftUnderReview holds, per artifact kind, the draft a document
	// council is currently judging. It lives only in the conversation until
	// a person accepts it; a seat that asks artifact_read for it is served
	// the draft rather than told it does not exist (nineteen refusals in a
	// row on a spec review, benchmark run 4).
	DraftUnderReview map[string]string
	// fsPatchFailStreak tracks fuzzy, consecutive fs_patch failures by file.
	// Unlike the exact-call brake above, changing the search text does not
	// disguise a model fighting the same file.
	fsPatchFailStreak map[string]int
	// fsPatchRefusalStreak tracks repeated attempts after the per-file patch
	// brake has already named a rewrite as the remedy.
	fsPatchRefusalStreak map[string]int
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
	// EndTurn asks the loop to close tool use for this reply: the seat is
	// told so in Content and its next call carries no tools, so it answers
	// with what it has. Set by the repeat brake once refusal alone has
	// failed to change the seat's behaviour (29 identical failing calls in
	// a row, benchmark run 4).
	EndTurn bool `json:"end_turn,omitempty"`
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
	r.Register(&RefRead{})
	// Filesystem
	r.Register(&FSList{})
	r.Register(&FSRead{})
	r.Register(&FSSearch{})
	r.Register(&FSWrite{})
	r.Register(&FSWriteLines{})
	r.Register(&FSPatch{})
	r.Register(&FSDelete{})
	// Execution
	r.Register(&Shell{})
	r.Register(&VerifyRun{})
	// Human
	r.Register(&AskHuman{})
	// The rubber duck, on demand: a consult that never pauses the run.
	r.Register(&AskAdvisor{})
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
	r.Register(&RosterRead{})
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
	if ectx.ToolsClosed {
		return &Result{IsError: true, EndTurn: true, Content: "tool use is CLOSED for this reply: answer now, in text, with what you have."}, nil
	}
	sig := name + "\x00" + string(args)
	if name == "fs_patch" {
		if path := fsPatchPath(ectx.ProjectRoot, args); path != "" && ectx.fsPatchFailStreak != nil && ectx.fsPatchFailStreak[path] >= FSPatchFailLimit {
			if ectx.fsPatchRefusalStreak == nil {
				ectx.fsPatchRefusalStreak = make(map[string]int)
			}
			ectx.fsPatchRefusalStreak[path]++
			refusals := ectx.fsPatchRefusalStreak[path]
			count := ectx.fsPatchFailStreak[path]
			message := fmt.Sprintf("REFUSED: fs_patch has failed %d times on this file; stop patching. Use fs_read to see current line numbers, then fs_write_lines to replace the exact range (or fs_write for a full rewrite)", count)
			if refusals >= FSPatchRefusalLimit {
				message += "; end your reply so the next turn can use the rewrite remedy."
			}
			return &Result{IsError: true, Content: message}, nil
		}
	}
	// A document is read as an artifact, not as a file: artifact_read knows
	// the sections and the pending proposal; fs_read of the same .md is a
	// second copy of the same text in the context (a plan architect read
	// requirements and spec twice each, once per tool — Neocapture).
	if name == "fs_read" {
		if kind := artifactKindOfPath(args); kind != "" {
			return ErrorResult("%s is a project document — read it with artifact_read {\"kind\":%q} "+
				"(it knows the sections and any pending proposal). If you already did, the text is above: use it.", fsReadPath(args), kind), nil
		}
	}
	// Reading the same thing twice in one turn changes nothing: the first
	// answer is still in the conversation. Refused with directions instead
	// of served again — a seat that re-reads the documents it was given
	// spends its window and its minutes on nothing.
	// Refused ONCE, then served again with a reminder: a seat that cannot
	// use "it is above" was refused thirteen times at 37 s each and never
	// wrote a line (Neocapture plan, 2026-08-30). Teaching once is the
	// harness's job; stranding the turn is not.
	repeatedRead := false
	if readOnlyTool[name] {
		if ectx.turnReads == nil {
			ectx.turnReads = map[string]int{}
		}
		switch ectx.turnReads[sig] {
		case 0:
		case 1:
			ectx.turnReads[sig]++
			return ErrorResult("REPEATED READ: you already called %s with these exact arguments in this turn, "+
				"and its result is above in this conversation. Use it; do not read again.", name), nil
		case 2:
			// Served once more, with a reminder (a seat that cannot act on
			// "it is above" is not stranded).
			repeatedRead = true
		default:
			// A third identical read is reading as a way of thinking: a spec
			// critic re-read the same sections 25 times at 41 s each
			// (benchmark run 6). The reply closes; the seat answers.
			ectx.ToolsClosed = true
			return &Result{IsError: true, EndTurn: true, Content: fmt.Sprintf(
				"REFUSED, and tool use is now CLOSED for this reply: %s with these arguments has been served twice "+
					"already in this turn. You have everything; your next message must be your final reply.", name)}, nil
		}
	}
	if ectx.lastFailCount >= RepeatFailLimit && ectx.lastFailSig == sig {
		ectx.lastFailCount++
		if ectx.lastFailCount >= RepeatFailEndTurn {
			ectx.ToolsClosed = true
			return &Result{IsError: true, EndTurn: true, Content: fmt.Sprintf(
				"REFUSED, and tool use is now CLOSED for this reply: %s with these arguments has failed %d times "+
					"and you kept repeating it. Answer now with what you already have — your next message must be "+
					"your final reply, not a tool call.", name, ectx.lastFailCount)}, nil
		}
		return &Result{IsError: true, Content: fmt.Sprintf(
			"REFUSED: you have made this exact failing call %d times — %s with the same "+
				"arguments. Repeating it cannot change the answer. Re-read the tool's error and "+
				"its schema, CHANGE the arguments, or use a different tool.", ectx.lastFailCount, name)}, nil
	}
	// A seat that keeps searching and keeps finding nothing is looking for
	// something that is not there — 21 fs_search calls at 50 s each on an
	// empty tree (Neocapture spec, 2026-08-30). Different patterns dodge the
	// identical-call brake, so misses are counted in a row.
	if name == "fs_search" && ectx.searchMisses >= SearchMissLimit {
		ectx.searchMisses = 0
		return ErrorResult("STOP SEARCHING: %d searches in a row found nothing. What you are looking for is not in "+
			"the tree — either the project has no such code yet, or the id you search for is not a section "+
			"(sub-numbered ids like REQ-003.1 never are). Use what is in your prompt and reply.", SearchMissLimit), nil
	}
	res, err := t.Execute(ctx, ectx, args)
	if res != nil && res.EndTurn {
		ectx.ToolsClosed = true
	}
	// No gate configured: verify_run says so as an ERROR, so the identical
	// call brake escalates instead of serving 73 identical "nothing to
	// run" successes (T-001, benchmark run 5).
	if name == "verify_run" && res != nil && !res.IsError && (strings.HasPrefix(strings.TrimSpace(res.Content), "gate: none") || strings.Contains(res.Content, "no command configured for gate")) {
		res = ErrorResult("no verification gate is configured for this project, so verify_run has nothing to run — " +
			"it will answer this every time. Do not call it again in this reply; finish your work and report on your deliverables.")
	}
	if name == "fs_search" && res != nil {
		if !res.IsError && strings.TrimSpace(res.Content) == "no matches" {
			ectx.searchMisses++
		} else if !res.IsError {
			ectx.searchMisses = 0
		}
	}
	if name == "fs_read" && res != nil && !res.IsError {
		resetFSPatchFailureStreak(ectx, args)
	}
	if name == "fs_patch" {
		trackFSPatchFailure(ectx, args, res)
	}
	if res != nil && res.IsError {
		if ectx.lastFailSig == sig {
			ectx.lastFailCount++
		} else {
			ectx.lastFailSig, ectx.lastFailCount = sig, 1
		}
	} else {
		ectx.lastFailSig, ectx.lastFailCount = "", 0
		if readOnlyTool[name] && ectx.turnReads != nil {
			ectx.turnReads[sig]++
			if repeatedRead {
				res.Content = "(served again — you already had this result earlier in this turn; do not request it a third time)\n\n" + res.Content
			}
		}
	}
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

// readOnlyTool names the tools whose identical call in one turn returns the
// same answer, so the second call is refused with directions.
var readOnlyTool = map[string]bool{
	"fs_read": true, "fs_list": true, "fs_search": true, "artifact_read": true, "task_read": true,
	"skill_list": true, "skill_read": true, "ref_read": true, "git_log": true, "git_diff": true, "git_status": true,
}

// Docs returns the root that holds .ducklab/docs for this run.
func (e *ExecContext) Docs() string {
	if e.DocsRoot != "" {
		return e.DocsRoot
	}
	return e.ProjectRoot
}

// BeginTurn resets what is remembered per turn: the repeated-read brake. A
// new turn is a new conversation, so the earlier answers are gone and a read
// is legitimate again.
func (e *ExecContext) BeginTurn() {
	e.turnReads = map[string]int{}
	e.searchMisses = 0
	e.ToolsClosed = false
	e.lastFailSig, e.lastFailCount = "", 0
}

// SearchMissLimit is how many fs_search calls may find nothing in a row
// before the next one is refused with directions.
// Five, not three: a strong seat checking that a symbol is ABSENT from a
// large repository legitimately searches several patterns in a row.
const SearchMissLimit = 5

// fsReadPath extracts the path argument of an fs_read call, if any.
func fsReadPath(args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &a)
	return strings.TrimSpace(a.Path)
}

// artifactKindOfPath maps .ducklab/docs/<kind>.md (or .md.proposed) to the
// artifact kind, or "" when the path is not a project document.
func artifactKindOfPath(args json.RawMessage) string {
	p := filepath.ToSlash(filepath.Clean(fsReadPath(args)))
	p = strings.TrimPrefix(p, "./")
	if !strings.HasPrefix(p, ".ducklab/docs/") {
		return ""
	}
	base := strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".proposed"), ".md")
	switch base {
	case "requirements", "spec", "plan", "project", "intent":
		return base
	}
	return ""
}

const FSPatchFailLimit = 5

// FSPatchRefusalLimit is the number of attempts allowed after fs_patch's
// per-file brake has named fs_write_lines or fs_write as the remedy.
const FSPatchRefusalLimit = 5

func fsPatchPath(root string, args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil || a.Path == "" {
		return ""
	}
	if _, err := PathJail(root, a.Path); err != nil {
		return filepath.Clean(a.Path)
	}
	return filepath.Clean(a.Path)
}

// resetFSPatchFailureStreak reopens a braked file after fs_read provides
// the prescribed opportunity to refresh patch anchors.
func resetFSPatchFailureStreak(ectx *ExecContext, args json.RawMessage) {
	path := fsPatchPath(ectx.ProjectRoot, args)
	if path == "" || ectx.fsPatchFailStreak == nil || ectx.fsPatchFailStreak[path] < FSPatchFailLimit {
		return
	}
	delete(ectx.fsPatchFailStreak, path)
	delete(ectx.fsPatchRefusalStreak, path)
}

func trackFSPatchFailure(ectx *ExecContext, args json.RawMessage, res *Result) {
	path := fsPatchPath(ectx.ProjectRoot, args)
	if path == "" {
		return
	}
	if ectx.fsPatchFailStreak == nil {
		ectx.fsPatchFailStreak = make(map[string]int)
	}
	if res != nil && res.IsError {
		ectx.fsPatchFailStreak[path]++
		if ectx.OnDistress != nil {
			ectx.OnDistress("fs_patch_failure_streak", map[string]interface{}{
				"tool": "fs_patch", "path": path, "count": ectx.fsPatchFailStreak[path],
			})
		}
	} else {
		delete(ectx.fsPatchFailStreak, path)
	}
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

// RepeatFailLimit is how many times the SAME failing call runs before the
// executor refuses the repeat: identical inputs cannot produce a different
// answer, and a model that has not changed anything is not going to.
const RepeatFailLimit = 3

// RepeatFailEndTurn is the identical-failure count at which refusal gives
// way to closing tool use for the reply.
const RepeatFailEndTurn = 6

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

	// 2. A run may not alter project governance through filesystem tools.
	// Project settings are changed only through the project API, where the
	// change is recorded and surfaced at the human gate.
	if ectx.Role == config.RoleImplementer && isProjectGovernancePath(ectx.ProjectRoot, absPath) {
		if ectx.OnDistress != nil {
			ectx.OnDistress("governance_write_refused", map[string]interface{}{"path": path})
		}
		return ErrorResult("governance config %s cannot be changed by a run; use PATCH /v1/projects", path)
	}

	// 3. Test-first runs write tests and nothing else.
	if ectx.TestPathsOnly && !verify.IsTestPath(path, ectx.Verify.TestGlobs) {
		return ErrorResult("this run writes tests only, and %s is not one. "+
			"Write the failing test; the implementation is the next run's job.", path)
	}

	// 4. Denylist
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

	// 5. Marker guard (can be disabled with --unsafe-writes)
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
		}
		// Protocol-marker guard: refuse only the markers this write INTRODUCES.
		//
		// A blanket refusal on any ```ducklab / ```payload: line walled off every
		// file that legitimately contains the text protocol's own fences — agent.go
		// defines them, the docs quote them, the service mocks embed them — so no
		// duckling could edit them at all: it hit "marker guard: protocol marker"
		// on content it never authored. The guard's real job is to catch a model
		// accidentally echoing a ```ducklab fence into a source file, which shows
		// up as a NET-NEW marker line. Compare against what the file already holds
		// and only refuse when the result carries more.
		if newN := countProtocolMarkers(contentStr); newN > 0 {
			oldN := 0
			if existing, err := os.ReadFile(absPath); err == nil {
				oldN = countProtocolMarkers(string(existing))
			}
			if newN > oldN {
				for i, line := range lines {
					if strings.Contains(line, "```ducklab") || strings.Contains(line, "```payload:") {
						return ErrorResult("marker guard: line %d introduces a protocol marker "+
							"(```ducklab / ```payload:) not already in the file", i+1)
					}
				}
			}
		}
	}

	// 6. Truncation guard (only for existing files, only for fs_write)
	if isWrite && !ectx.UnsafeWrites {
		if existing, err := os.ReadFile(absPath); err == nil {
			oldSize := len(existing)
			newSize := len(content)
			if oldSize > 200 && newSize < oldSize*40/100 {
				return ErrorResult("truncation guard: refused: this would shrink %s from %d to %d bytes. If the shorter file IS what you want, "+
					"say so by removing first — fs_delete the file, then fs_write it — or replace only the lines that change with fs_patch / fs_write_lines.",
					path, oldSize, newSize)
			}
		}
	}

	// 7. Binary guard
	for _, b := range content {
		if b == 0 {
			return ErrorResult("binary guard: content contains NUL bytes")
		}
	}

	// 8. Size guard
	if len(content) > 1024*1024 {
		return ErrorResult("size guard: write over 1 MB refused")
	}

	return nil
}

// isProjectGovernancePath reports whether abs is the project's governance file.
func isProjectGovernancePath(root, abs string) bool {
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootAbs = root
	}
	return filepath.Clean(abs) == filepath.Join(rootAbs, ".ducklab", "project.toml")
}

// countProtocolMarkers counts lines carrying a Dialect B text-protocol fence
// (```ducklab or ```payload:). The marker guard compares this between the
// existing file and the write to tell a net-new marker (a model leaking the
// protocol into source) from one the file already legitimately contains.
func countProtocolMarkers(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "```ducklab") || strings.Contains(line, "```payload:") {
			n++
		}
	}
	return n
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
		// File operations have their own tools; a seat denied `mkdir -p`
		// was told to ask a person (T-001, benchmark run 5).
		first := strings.Fields(cmd)
		if len(first) > 0 {
			switch first[0] {
			case "mkdir", "touch", "cp", "mv", "rm", "cat", "echo", "tee", "sed", "chmod":
				return ErrorResult("shell policy: %q is not in the allowlist, and file operations are not done through the shell here: "+
					"fs_write creates a file AND its directories, fs_patch/fs_write_lines edit, fs_delete removes, fs_read/fs_list inspect. Use those.", first[0])
			}
		}
		return ErrorResult("shell policy: command not in allowlist. This command is not model-runnable under this policy; ask for the needed outcome or decision instead, or use a different command.")
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

func runIdentity(id string) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("manual-%d", time.Now().Unix())
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

	env := ectx.ShellEnv
	if env == nil {
		env = os.Environ()
	}
	projectID := ectx.ProjectID
	if projectID == "" {
		projectID = "manual"
	}
	env = append(env, "DUCKLAB_RUN_ID="+runIdentity(ectx.RunID), "DUCKLAB_PROJECT_ID="+projectID)
	shellCmd := xplat.ShellContext(ctx, ectx.ProjectRoot, env, cmd)
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
