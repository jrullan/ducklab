package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/verify"
)

// The final gate's record (B-510). Several checks can turn the run red besides
// the project's command — task verification, acceptance probes, the product
// smoke, the visual check and capability coverage — and the gate event used
// to say so by replacing the command's exit code with a synthesised 1. A run
// whose `npm test` printed "# pass 174 # fail 0" was then recorded, and shown
// on the desktop, as "npm test failed". The record now keeps both: exit_code
// is what the command really returned, effective_exit_code is the gate's
// red/green as an exit code (what exit_code used to carry), and red_by names
// each check that made it red, in words.

// Gate check names, as red_by records them.
const (
	gateCheckCommand    = "command"
	gateCheckTask       = "task_verification"
	gateCheckProbes     = "acceptance_probes"
	gateCheckAppSmoke   = "app_smoke"
	gateCheckVisual     = "visual"
	gateCheckCapability = "capability_coverage"
)

// finalGateChecks are the outcomes of the checks around the project's gate
// command: "red", "green", "none" or "" (not run).
type finalGateChecks struct {
	Task, TaskLog         string
	Probe, ProbeLog       string
	Smoke, SmokeReason    string
	Visual, VisualSummary string
}

// settledGate is the final gate as recorded and judged.
type settledGate struct {
	// Gate and Exit are the gate's outcome: Exit is non-zero exactly when
	// the gate is red, whatever the command returned.
	Gate     string
	Exit     int
	Output   string
	Coverage []capability.GateFinding
	RedBy    []gateRedCause
}

// settleFinalGate decides the final gate from the command and every check
// around it. coverage is observed only over an otherwise green gate, as
// before (B-400); nil observes nothing.
func settleFinalGate(command *verify.Result, c finalGateChecks, output string, coverage func() []capability.GateFinding) settledGate {
	out := settledGate{Gate: string(command.Gate), Exit: command.ExitCode, Output: output}
	if c.Task == "red" || c.Probe == "red" || c.Smoke == "red" || c.Visual == "red" {
		out.Gate = "red"
		if out.Exit == 0 {
			out.Exit = 1
		}
	}
	if out.Exit == 0 && verify.IsGreen(command) && coverage != nil {
		out.Coverage = coverage()
		out.Gate, out.Exit, out.Output = applyFinalCapabilityCoverage(out.Gate, out.Exit, out.Output, out.Coverage)
	}
	out.RedBy = finalGateRedBy(command, c.Task, c.TaskLog, c.Probe, c.ProbeLog, c.Smoke, c.SmokeReason, c.Visual, c.VisualSummary, out.Coverage)
	return out
}

// gateRedCause is one check that made the final gate red.
type gateRedCause struct {
	Check   string `json:"check"`
	Summary string `json:"summary"`
}

// finalGateRedBy lists, in the order they ran, the checks that made the
// final gate red. Empty when the gate is not red.
func finalGateRedBy(command *verify.Result, task, taskLog, probe, probeLog, smoke, smokeReason, visual, visualSummary string, coverage []capability.GateFinding) []gateRedCause {
	var out []gateRedCause
	if task == "red" {
		out = append(out, gateRedCause{gateCheckTask, gateLogSummary(taskLog, "task verification failed")})
	}
	if command != nil && command.Gate != verify.GateNone && command.ExitCode != 0 {
		out = append(out, gateRedCause{gateCheckCommand, commandSummary(command.Command, command.ExitCode)})
	}
	if probe == "red" {
		out = append(out, gateRedCause{gateCheckProbes, gateLogSummary(probeLog, "an acceptance probe failed")})
	}
	if smoke == "red" {
		summary := "product smoke failed"
		if r := compactGateLine(smokeReason); r != "" {
			summary += ": " + r
		}
		out = append(out, gateRedCause{gateCheckAppSmoke, boundGateLine(summary)})
	}
	if visual == "red" {
		summary := compactGateLine(visualSummary)
		if summary == "" {
			summary = "visual check failed"
		}
		out = append(out, gateRedCause{gateCheckVisual, boundGateLine(summary)})
	}
	for _, f := range coverage {
		if f.Enforcement != capability.Required {
			continue
		}
		summary := fmt.Sprintf("capability coverage [%s/%s]: %s", f.Capability, f.Kind, compactGateLine(f.Detail))
		if len(f.Files) > 0 {
			summary += " (" + strings.Join(f.Files, ", ") + ")"
		}
		out = append(out, gateRedCause{gateCheckCapability, boundGateLine(summary)})
	}
	return out
}

// finalGateEvent is the final `gate` event. `gate` and the verdict keep
// their meaning; exit_code is the command's own.
func finalGateEvent(gate string, effectiveExit int, command *verify.Result, output string, redBy []gateRedCause) map[string]interface{} {
	event := map[string]interface{}{
		"gate":                gate,
		"command_gate":        string(command.Gate),
		"command":             command.Command,
		"exit_code":           command.ExitCode,
		"effective_exit_code": effectiveExit,
		"output":              output,
		"duration_s":          command.Duration,
	}
	if len(redBy) > 0 {
		// Plain maps, as the record reads back: an in-process subscriber sees
		// the same shape as one reading events.jsonl.
		causes := make([]interface{}, 0, len(redBy))
		for _, c := range redBy {
			causes = append(causes, map[string]interface{}{"check": c.Check, "summary": c.Summary})
		}
		event["red_by"] = causes
	}
	return event
}

// finalGateFailure is the run's failure line for a red final gate: the
// command's own result, then what else made it red.
func finalGateFailure(command *verify.Result, redBy []gateRedCause) string {
	var others []string
	for _, c := range redBy {
		if c.Check != gateCheckCommand {
			others = append(others, c.Summary)
		}
	}
	if len(others) == 0 {
		return "gate failed (exit " + strconv.Itoa(command.ExitCode) + ")"
	}
	head := "gate red"
	switch {
	case command.Gate == verify.GateNone:
	case command.ExitCode == 0:
		head += ": " + string(command.Gate) + " passed (exit 0)"
	default:
		head += ": " + string(command.Gate) + " failed (exit " + strconv.Itoa(command.ExitCode) + ")"
	}
	return head + "; " + strings.Join(others, "; ")
}

func commandSummary(command string, exit int) string {
	command = compactGateLine(command)
	if command == "" {
		command = "the gate command"
	}
	return boundGateLine(fmt.Sprintf("%s exited %d", command, exit))
}

// gateLogSummary is one line for a check's log: its heading, then the
// command it ran and its exit, or else the first line that explains it.
func gateLogSummary(log, fallback string) string {
	var head, cmd, exit, detail string
lines:
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case head == "":
			head = strings.TrimSuffix(strings.TrimPrefix(line, "blocking "), ":")
		case strings.HasPrefix(line, "cmd: ") && cmd == "":
			cmd = strings.TrimPrefix(line, "cmd: ")
		case strings.HasPrefix(line, "exit: ") && exit == "":
			exit = strings.TrimPrefix(line, "exit: ")
		case strings.HasPrefix(line, "gate: "):
		case strings.HasPrefix(line, "prior successful"):
			// What follows is earlier, green evidence, not this check's.
			break lines
		case detail == "" && cmd == "":
			detail = line
		}
	}
	if head == "" {
		return fallback
	}
	switch {
	case cmd != "" && exit != "":
		return boundGateLine(head + ": " + compactGateLine(cmd) + " exited " + exit)
	case detail != "":
		return boundGateLine(head + ": " + detail)
	}
	return boundGateLine(head)
}

func compactGateLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func boundGateLine(s string) string {
	const limit = 240
	if len(s) <= limit {
		return s
	}
	cut := limit - len("…")
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
