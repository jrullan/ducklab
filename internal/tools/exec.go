package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/verify"
	"github.com/jrullan/ducklab/internal/xplat"
)

// Shell runs a shell command.
type Shell struct{}

// Name returns the tool name.
func (t *Shell) Name() string   { return "shell" }
func (t *Shell) Mutating() bool { return true }

// Description returns the tool description.
func (t *Shell) Description() string {
	return "Run a shell command in the project root. Each call is a FRESH shell: cd, variables " +
		"and state do not carry over to the next call. Subject to policy."
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
func (t *VerifyRun) Name() string   { return "verify_run" }
func (t *VerifyRun) Mutating() bool { return false }

// Description returns the tool description.
func (t *VerifyRun) Description() string {
	return "Run the exact verification chain that decides this round: the task's Verification command, then the project's configured gate."
}

// Schema returns the argument schema.
func (t *VerifyRun) Schema() interface{} {
	return NewSchema()
}

// Execute runs the tool.
// GateFailLimit is the brake on a red-gate spiral: after this many
// consecutive failures the tool stops running the gate at all. Measured on
// the run that earned it: 45 verify_run calls, all red, 53 patches between
// them, 32KB of test output ballooning the context on every one — 8.7M
// tokens on a datepicker default, ended only by the wallclock. An approach
// that has failed this many times straight is not converging; running the
// gate again does not change the answer.
const GateFailLimit = 10

func (t *VerifyRun) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	if ectx.ConsecGateFails >= GateFailLimit {
		if ectx.OnDistress != nil {
			ectx.OnDistress("failure_streak", map[string]interface{}{"count": ectx.ConsecGateFails})
		}
		return &Result{IsError: true, Content: fmt.Sprintf(
			"REFUSED: the gate has failed %d times in a row in this run. Running it again will "+
				"not change the answer — this approach is not converging. Stop patching. State "+
				"plainly what you tried and why you believe it keeps failing, then end your reply "+
				"so a person (or the reviewer) can redirect the work.", ectx.ConsecGateFails)}, nil
	}
	// The same code path as the gate that decides the run, deliberately.
	//
	// This used to run "go test ./..." hardcoded. On a project whose gate was
	// "go build" a model called it, was told exit 0, and stopped — and the
	// real gate then failed on work it had been told was fine. A tool that
	// answers a different question from the one being asked is worse than no
	// tool.
	gate, result, err := RunVerificationGate(ctx, ectx)
	if err != nil {
		return ErrorResult("verify_run: %v", err), nil
	}
	if gate == "red" {
		ectx.ConsecGateFails++
		if ectx.ConsecGateFails >= GateFailLimit && ectx.OnDistress != nil {
			ectx.OnDistress("failure_streak", map[string]interface{}{"count": ectx.ConsecGateFails})
		}
		if left := GateFailLimit - ectx.ConsecGateFails; left >= 0 && left <= 3 {
			result += fmt.Sprintf("\n\n[gate brake: %d consecutive failure(s); %d attempt(s) left "+
				"before verify_run refuses — if the cause is not clear yet, stop and say so]",
				ectx.ConsecGateFails, left)
		}
		return &Result{Content: result, IsError: true}, nil
	}
	ectx.ConsecGateFails = 0
	// A green deterministic gate is the implementation turn's terminal state.
	// Continuing to mutate after it repeatedly destroyed verified repairs in a
	// small-seat build. Close tools and spend the conclusion call on the
	// required completion report; semantic objections still belong to the
	// independent reviewer and open the next implementation round normally.
	return &Result{
		Content: result + "\n\n[gate green: implementation tools are now closed for this reply; do not edit again. End with the required completion report.]",
		EndTurn: true,
	}, nil
}

// RunVerificationGate is the single verification path used by both
// verify_run and the automatic end-of-round gate. TaskVerification goes first:
// a task-local contract is part of the decision, not an optional hint.
func RunVerificationGate(ctx context.Context, ectx *ExecContext) (string, string, error) {
	taskGate, taskLog, err := RunTaskVerificationGate(ctx, ectx)
	if err != nil {
		return "none", "", err
	}
	if taskGate == "red" {
		return "red", taskLog, nil
	}

	identity := verify.Identity{RunID: ectx.RunID, ProjectID: ectx.ProjectID}
	res, err := verify.Run(ctx, ectx.ProjectRoot, ectx.Verify, identity)
	if err != nil {
		return "none", "", err
	}
	projectLog := formatGateResult(res)
	if taskLog != "" {
		projectLog = taskLog + "\nproject verification:\n" + projectLog
	}
	if verify.IsGreen(res) && ectx.WorkspaceDiff != nil && len(ectx.ActiveCapabilities) > 0 {
		diff, err := ectx.WorkspaceDiff()
		if err != nil {
			return "none", projectLog, fmt.Errorf("read candidate diff for gate coverage: %w", err)
		}
		findings := capability.DefaultRegistry().ObserveGate(capability.GateObservation{
			ProjectRoot: ectx.ProjectRoot, Diff: diff, Output: res.Output,
		}, ectx.ActiveCapabilities)
		blocking := false
		for _, finding := range findings {
			projectLog += fmt.Sprintf("\ncapability coverage [%s/%s, %s]:\n%s",
				finding.Capability, finding.Kind, finding.Enforcement, finding.Detail)
			if len(finding.Files) > 0 {
				projectLog += "\nfiles: " + strings.Join(finding.Files, ", ")
			}
			blocking = blocking || finding.Enforcement == capability.Required
		}
		if blocking {
			return "red", projectLog, nil
		}
	}
	switch {
	case verify.IsGreen(res):
		return "green", projectLog, nil
	case verify.IsRed(res):
		return "red", projectLog, nil
	default:
		return "none", projectLog, nil
	}
}

// RunTaskVerificationGate executes the task-local Verification contract, when
// present. The build worker uses it again at the final gate so a green project
// build cannot overwrite a red task gate after the strategy exhausts its
// rounds.
func RunTaskVerificationGate(ctx context.Context, ectx *ExecContext) (string, string, error) {
	command := strings.TrimSpace(ectx.TaskVerification)
	if command == "" {
		return "none", "", nil
	}
	if missing := missingProducedFiles(ectx.ProjectRoot, ectx.TaskProducedFiles); len(missing) > 0 {
		return "red", "task artifact contract:\ngate: red\nmissing declared Produces files: " + strings.Join(missing, ", "), nil
	}
	res, err := verify.Run(ctx, ectx.ProjectRoot, config.Verify{
		Mode: "custom", Custom: command, TimeoutS: ectx.Verify.TimeoutS,
	}, verify.Identity{RunID: ectx.RunID, ProjectID: ectx.ProjectID})
	if err != nil {
		return "none", "", err
	}
	log := "task verification:\n" + formatGateResult(res)
	if !verify.IsGreen(res) {
		return "red", log, nil
	}

	checks, err := capability.DefaultRegistry().ResolveChecks(capability.Context{
		ProjectRoot:      ectx.ProjectRoot,
		TaskVerification: command,
		ProducedFiles:    ectx.TaskProducedFiles,
		ConsumedFiles:    ectx.TaskConsumedFiles,
		Policies:         ectx.Capabilities.Policy,
	}, ectx.Capabilities.Auto, ectx.Capabilities.Enabled, ectx.Capabilities.Disabled)
	if err != nil {
		return "none", "", fmt.Errorf("resolve harness capabilities: %w", err)
	}
	for _, check := range checks {
		strictRes, err := verify.Run(ctx, ectx.ProjectRoot, config.Verify{
			Mode: "custom", Custom: check.Command, TimeoutS: ectx.Verify.TimeoutS,
		}, verify.Identity{RunID: ectx.RunID, ProjectID: ectx.ProjectID})
		if err != nil {
			return "none", "", err
		}
		log += fmt.Sprintf("\ncapability diagnostic [%s/%s, %s]:\n%s",
			check.Capability, check.Name, check.Enforcement, formatGateResult(strictRes))
		if check.Enforcement == capability.Required && !verify.IsGreen(strictRes) {
			return "red", log, nil
		}
	}
	inspections, err := capability.DefaultRegistry().ResolveInspections(capability.Context{
		ProjectRoot:      ectx.ProjectRoot,
		TaskVerification: command,
		ProducedFiles:    ectx.TaskProducedFiles,
		ConsumedFiles:    ectx.TaskConsumedFiles,
		Policies:         ectx.Capabilities.Policy,
	}, ectx.Capabilities.Auto, ectx.Capabilities.Enabled, ectx.Capabilities.Disabled)
	if err != nil {
		return "none", "", fmt.Errorf("run harness capability inspections: %w", err)
	}
	for _, finding := range inspections {
		log += fmt.Sprintf("\ncapability diagnostic [%s/%s, %s]:\ngate: red\n%s",
			finding.Capability, finding.Name, finding.Enforcement, finding.Detail)
		if finding.Enforcement == capability.Required {
			return "red", log, nil
		}
	}
	return "green", log, nil
}

func missingProducedFiles(root string, files []string) []string {
	var missing []string
	for _, relative := range files {
		clean := filepath.Clean(relative)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			missing = append(missing, relative+" (invalid path)")
			continue
		}
		info, err := os.Stat(filepath.Join(root, clean))
		if err != nil || info.IsDir() {
			missing = append(missing, relative)
		}
	}
	return missing
}

func formatGateResult(res *verify.Result) string {
	return fmt.Sprintf("gate: %s\ncmd: %s\nexit: %d\n%s",
		res.Gate, res.Command, res.ExitCode, CapResult(res.Output, MaxToolResultBytes))
}

// GitStatus shows git status.
type GitStatus struct{}

// Name returns the tool name.
func (t *GitStatus) Name() string   { return "git_status" }
func (t *GitStatus) Mutating() bool { return false }

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
func (t *GitDiff) Name() string   { return "git_diff" }
func (t *GitDiff) Mutating() bool { return false }

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
func (t *GitLog) Name() string   { return "git_log" }
func (t *GitLog) Mutating() bool { return false }

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
func (t *AskHuman) Name() string   { return "ask_human" }
func (t *AskHuman) Mutating() bool { return false }

// Description returns the tool description.
//
// The economics are stated because the old one-liner ("this pauses the run")
// read as a deterrent: a model burned two million tokens deliberating where a
// "week" starts rather than ask the person who knew. The description is where
// a model decides whether a tool is for it — it must say when asking WINS.
func (t *AskHuman) Description() string {
	return "Ask the human ONE precise question when the task leaves a decision you cannot " +
		"infer from the repo or its docs — a boundary (where does a week start?), a format, " +
		"an external contract. Ask for a needed outcome or decision, not approval to run a " +
		"shell command: approval cannot change shell policy. Offer 2-4 concrete options when " +
		"you can. One question costs a pause; a wrong guess costs the whole run. Decisions " +
		"the task does determine, and internals nobody outside would notice, are yours — never " +
		"ask about those."
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

// Execute either returns a stored answer or signals that the run must pause.
//
// It never blocks. A blocking wait would hold a goroutine for as long as the
// human takes — which may be days — and would make a paused run indistinguishable
// from a hung one. Instead it returns ErrHumanNeeded, the agent loop unwinds,
// and the engine checkpoints the run as paused (01 §7.1).
func (t *AskHuman) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a askHumanArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if strings.TrimSpace(a.Question) == "" {
		return ErrorResult("question is required"), nil
	}

	id := QuestionID(a.Question)

	// Environment facts are not decisions. Resolve them inline before looking
	// for a human answer so a small seat cannot turn known workspace metadata
	// into a pause (or invite an advisor to invent a different path).
	normalizedQuestion := strings.ToLower(a.Question)
	for phrase, answer := range ectx.DeterministicAnswers {
		if strings.Contains(normalizedQuestion, strings.ToLower(strings.TrimSpace(phrase))) {
			return SuccessResult("Harness fact (no human decision required): %s", answer), nil
		}
	}

	// A resumed run replays the turn with the answer already available, so the
	// same question resolves instead of pausing again.
	if ans, ok := ectx.Answers[id]; ok {
		return SuccessResult("%s", ans), nil
	}

	// Nobody is there to answer: say so plainly rather than stalling forever.
	// Yolo is the exception now — its question pauses like anyone else's, and
	// the advisor's drafted answer is submitted automatically (advisor.go):
	// a considered second opinion beats forcing the asker to guess.
	if ectx.NoHuman || ectx.Autonomy == config.AutonomyAuto {
		return ErrorResult("no human available; proceed with your best judgement and state the assumption"), nil
	}

	ectx.Pending = &PendingQuestion{ID: id, Question: a.Question, Options: a.Options}
	return nil, ErrHumanNeeded
}

// AskAdvisor is the implementer's door to the rubber duck. ask_human pauses
// the run and waits for a person; ask_advisor asks the advisor seat and
// returns its reply inline, in the same turn. Built for the small seat that
// knows it is stuck but not what to do about it: one consult in place of
// twenty more failed calls.
type AskAdvisor struct{}

func (t *AskAdvisor) Name() string   { return "ask_advisor" }
func (t *AskAdvisor) Mutating() bool { return false }

func (t *AskAdvisor) Description() string {
	return "Consult the advisor duckling when you are stuck: the same tool keeps failing, the gate " +
		"stays red after several attempts, or you cannot decide between approaches. Say what you " +
		"tried and what you are stuck on. The advisor answers inline — the run does NOT pause and " +
		"no human is involved. Use it once when stuck rather than retrying the same thing."
}

func (t *AskAdvisor) Schema() interface{} {
	return NewSchema().
		AddString("question", "What you are stuck on and what you have tried so far", true)
}

type askAdvisorArgs struct {
	Question string `json:"question"`
}

func (t *AskAdvisor) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a askAdvisorArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	if strings.TrimSpace(a.Question) == "" {
		return ErrorResult("question is required: say what you tried and what you are stuck on"), nil
	}
	if ectx.OnAskAdvisor == nil {
		return ErrorResult("no advisor is seated for this run; proceed with your best judgement — " +
			"if you are stuck on a tool, read the whole section with fs_read and rewrite it with fs_write_lines"), nil
	}
	answer, err := ectx.OnAskAdvisor(ctx, a.Question)
	if err != nil {
		return ErrorResult("the advisor could not answer (%v); proceed with your best judgement", err), nil
	}
	if strings.TrimSpace(answer) == "" {
		return ErrorResult("the advisor had nothing to add; proceed with your best judgement"), nil
	}
	return SuccessResult("advisor: %s", strings.TrimSpace(answer)), nil
}

// QuestionID is a stable identifier for a question, so a resumed run can match
// an answer to the question that produced it without persisting message state.
func QuestionID(question string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(question)))
	return hex.EncodeToString(sum[:8])
}
