package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/xplat"
)

// Shell runs a shell command.
type Shell struct{}

// Name returns the tool name.
func (t *Shell) Name() string { return "shell" }

// Description returns the tool description.
func (t *Shell) Description() string {
	return "Run a shell command in the project root. Subject to policy."
}

// Schema returns the argument schema.
func (t *Shell) Schema() interface{} {
	return NewSchema().
		AddString("cmd", "Command to run", true).
		AddInt("timeout_s", "Timeout in seconds", false)
}

type shellArgs struct {
	Cmd      string `json:"cmd"`
	TimeoutS int    `json:"timeout_s"`
}

// Execute runs the tool.
func (t *Shell) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a shellArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	// Policy check
	if guard := ShellPolicyCheck(ectx, a.Cmd); guard != nil {
		return guard, nil
	}
	timeoutS := a.TimeoutS
	if timeoutS <= 0 {
		timeoutS = ectx.ShellPolicy.TimeoutS
	}
	if timeoutS <= 0 {
		timeoutS = 120
	}
	output, exitCode, err := RunShell(ctx, ectx, a.Cmd, timeoutS)
	if err != nil {
		return ErrorResult("shell: %v", err), nil
	}
	result := fmt.Sprintf("exit code: %d\n%s", exitCode, CapResult(output, 32768))
	if exitCode != 0 {
		return &Result{Content: result, IsError: true}, nil
	}
	return SuccessResult("%s", result), nil
}

// VerifyRun runs the project's verification gate.
type VerifyRun struct{}

// Name returns the tool name.
func (t *VerifyRun) Name() string { return "verify_run" }

// Description returns the tool description.
func (t *VerifyRun) Description() string {
	return "Run the project's configured verification gate command."
}

// Schema returns the argument schema.
func (t *VerifyRun) Schema() interface{} {
	return NewSchema()
}

// Execute runs the tool.
func (t *VerifyRun) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	// The verify command is stored in the exec context by the orchestrator
	// For now, we look for common verification commands
	// This is a placeholder; the real implementation will use the project's verify config
	cmd := "go test ./..."
	output, exitCode, err := RunShell(ctx, ectx, cmd, 900)
	if err != nil {
		return ErrorResult("verify_run: %v", err), nil
	}
	result := fmt.Sprintf("gate: tests\ncmd: %s\nexit: %d\n%s", cmd, exitCode, CapResult(output, 32768))
	if exitCode != 0 {
		return &Result{Content: result, IsError: true}, nil
	}
	return SuccessResult("%s", result), nil
}

// GitStatus shows git status.
type GitStatus struct{}

// Name returns the tool name.
func (t *GitStatus) Name() string { return "git_status" }

// Description returns the tool description.
func (t *GitStatus) Description() string {
	return "Show git status (read-only)."
}

// Schema returns the argument schema.
func (t *GitStatus) Schema() interface{} {
	return NewSchema()
}

// Execute runs the tool.
func (t *GitStatus) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	cmd := xplat.Shell(ectx.ProjectRoot, nil, "git status --porcelain")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ErrorResult("git status: %v", err), nil
	}
	if len(output) == 0 {
		return SuccessResult("clean"), nil
	}
	return SuccessResult("%s", string(output)), nil
}

// GitDiff shows a git diff.
type GitDiff struct{}

// Name returns the tool name.
func (t *GitDiff) Name() string { return "git_diff" }

// Description returns the tool description.
func (t *GitDiff) Description() string {
	return "Show git diff (read-only)."
}

// Schema returns the argument schema.
func (t *GitDiff) Schema() interface{} {
	return NewSchema().
		AddString("ref", "Git ref to diff against", false).
		AddString("path", "Path to limit diff to", false)
}

type gitDiffArgs struct {
	Ref  string `json:"ref"`
	Path string `json:"path"`
}

// Execute runs the tool.
func (t *GitDiff) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a gitDiffArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	cmdStr := "git diff"
	if a.Ref != "" {
		cmdStr += " " + a.Ref
	}
	if a.Path != "" {
		cmdStr += " -- " + a.Path
	}
	cmd := xplat.Shell(ectx.ProjectRoot, nil, cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ErrorResult("git diff: %v", err), nil
	}
	return SuccessResult("%s", CapResult(string(output), 32768)), nil
}

// GitLog shows git log.
type GitLog struct{}

// Name returns the tool name.
func (t *GitLog) Name() string { return "git_log" }

// Description returns the tool description.
func (t *GitLog) Description() string {
	return "Show git log (read-only)."
}

// Schema returns the argument schema.
func (t *GitLog) Schema() interface{} {
	return NewSchema().
		AddInt("n", "Number of commits to show", false).
		AddString("path", "Path to limit log to", false)
}

type gitLogArgs struct {
	N    int    `json:"n"`
	Path string `json:"path"`
}

// Execute runs the tool.
func (t *GitLog) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a gitLogArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	n := a.N
	if n <= 0 {
		n = 10
	}
	cmdStr := fmt.Sprintf("git log --oneline -n %d", n)
	if a.Path != "" {
		cmdStr += " -- " + a.Path
	}
	cmd := xplat.Shell(ectx.ProjectRoot, nil, cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ErrorResult("git log: %v", err), nil
	}
	return SuccessResult("%s", string(output)), nil
}

// AskHuman asks the human a question. This pauses the run.
type AskHuman struct{}

// Name returns the tool name.
func (t *AskHuman) Name() string { return "ask_human" }

// Description returns the tool description.
func (t *AskHuman) Description() string {
	return "Ask the human a question. This pauses the run until answered."
}

// Schema returns the argument schema.
func (t *AskHuman) Schema() interface{} {
	return NewSchema().
		AddString("question", "The question to ask", true).
		AddArray("options", "Optional answer choices", &Property{Type: "string"}, false)
}

type askHumanArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// Execute runs the tool.
func (t *AskHuman) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a askHumanArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	// Under --yes or auto/yolo autonomy, return the no-human response
	if ectx.Autonomy == config.AutonomyAuto || ectx.Autonomy == config.AutonomyYolo {
		return ErrorResult("no human available; proceed with your best judgement and state the assumption"), nil
	}
	// This tool's execution is intercepted by the agent loop, which pauses the run.
	// Returning here means the loop didn't intercept it, which is an error.
	return ErrorResult("ask_human should be intercepted by the agent loop"), nil
}
