package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// The rubber duck.
//
// The advisor used to run in a goroutine, racing the run it was advising: it
// answered a snapshot of a paused question while the implementer's line of
// thought moved on, and its reply landed too late or for a problem already
// solved. T-059 gave it a real turn between implementer and reviewer, but
// fired it on loose signals (four fs_read misses) to write a note nobody
// read, and emitted its events INSIDE the implementer's turn, so the desktop
// showed the two working in parallel.
//
// Now the duck listens at one deterministic moment — the implementer's turn
// is over, its full story is on the table, the reviewer has not yet spoken —
// and only when the turn carries structural distress. It reads what the
// reviewer must never read (the implementer's reasoning and its fight with
// the tools; I2 binds the judge, not the counselor) and answers with one of
// three things: nothing, a note for the next round, or STOP — the run is not
// converging and the person should reseat before spending more.

// distressSignals is what the harness itself measured about a turn. Every
// field is counted, never inferred from prose: an operator's own vocabulary
// ("stuck", "fighting") is exactly what a self-referential detector trips on.
type distressSignals struct {
	// Refusals counts tool results the harness's brakes refused ("REFUSED:"
	// prefix — the repeat brake, the fs_patch brake, the gate brake).
	Refusals int `json:"refusals"`
	// Streak is the longest run of consecutive failures of one tool.
	Streak int `json:"failure_streak"`
	// StreakTool names the tool behind Streak.
	StreakTool string `json:"failure_streak_tool,omitempty"`
	// GateReds counts verify_run calls that came back red this turn.
	GateReds int `json:"gate_reds"`
	// Failures is the per-tool count of failed calls, for the record.
	Failures map[string]int `json:"failures,omitempty"`
	// Undelivered lists deliverable ids the implementer ITSELF reported as
	// not done — the one self-reported signal, and structured: ids, not
	// prose. It catches what telemetry cannot: the model that gave up
	// quietly without fighting any tool.
	Undelivered []int `json:"undelivered,omitempty"`
	// Unreported: a deliverables contract was asked for and no report came.
	Unreported bool `json:"unreported,omitempty"`
}

const (
	distressStreak   = 5
	distressGateReds = 3
)

// Distressed is the trigger. Conservative on purpose: the duck must not cost
// a turn on a run that is merely working.
func (d distressSignals) Distressed() bool {
	return d.Refusals > 0 || d.Streak >= distressStreak || d.GateReds >= distressGateReds || len(d.Undelivered) > 0
}

// measureDistressWithReport folds the implementer's own report into the
// telemetry. Absence of a report is recorded, not treated as distress: a
// seat learning the contract must not summon the duck on every turn.
func measureDistressWithReport(outcome *agent.Outcome, rep *DeliverablesReport) distressSignals {
	d := measureDistress(outcome)
	if rep != nil {
		d.Undelivered = rep.Undelivered()
		d.Unreported = rep.Unreported
	}
	return d
}

func operationalSummaryWithReport(outcome *agent.Outcome, rep *DeliverablesReport) (string, bool) {
	d := measureDistressWithReport(outcome, rep)
	if !d.Distressed() {
		return "", false
	}
	data, err := json.Marshal(d)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func measureDistress(outcome *agent.Outcome) distressSignals {
	d := distressSignals{Failures: map[string]int{}}
	if outcome == nil {
		return d
	}
	curTool, cur := "", 0
	for _, call := range outcome.ToolCalls {
		failed := call.Result != nil && call.Result.IsError
		if !failed {
			curTool, cur = "", 0
			continue
		}
		d.Failures[call.Name]++
		if strings.HasPrefix(strings.TrimSpace(call.Result.Content), "REFUSED:") {
			d.Refusals++
		}
		if call.Name == "verify_run" {
			d.GateReds++
		}
		if call.Name == curTool {
			cur++
		} else {
			curTool, cur = call.Name, 1
		}
		if cur > d.Streak {
			d.Streak, d.StreakTool = cur, curTool
		}
	}
	if len(d.Failures) == 0 {
		d.Failures = nil
	}
	return d
}

// operationalSummary is what the REVIEWER gets: telemetry as data, never
// prose, so its verdict can tell wounded execution from wrong design without
// reading a rationalisation.
func operationalSummary(outcome *agent.Outcome) (string, bool) {
	d := measureDistress(outcome)
	if !d.Distressed() {
		return "", false
	}
	data, err := json.Marshal(d)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// AdvisorStop is the duck's third answer: the run should end here.
type AdvisorStop struct {
	Advisor   config.DucklingID
	Reason    string
	Reshuffle string
}

func (e *AdvisorStop) Error() string {
	msg := fmt.Sprintf("stopped by advisor %s: %s", e.Advisor, e.Reason)
	if strings.TrimSpace(e.Reshuffle) != "" {
		msg += " — for the re-run: " + e.Reshuffle
	}
	return msg
}

// advice is the duck's contract.
type advice struct {
	Action    string `json:"action"`
	Note      string `json:"note,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Reshuffle string `json:"reshuffle,omitempty"`
}

// The contract name the agent parses generically (json: prefix); the shape is
// enforced here, where a bad reply degrades to "none" instead of failing the
// run — advice is optional, the work is not.
const adviceContract = "json:advice"

// consultAdvisorDefaultTurns allows the advisor enough calls to investigate the
// implementer's distress before it replies. TurnCaps may override or lift it.
const consultAdvisorDefaultTurns = 6

func parseAdvice(outcome *agent.Outcome) advice {
	if outcome == nil {
		return advice{Action: "none"}
	}
	var a advice
	if m, ok := outcome.Parsed.(map[string]interface{}); ok {
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &a)
	}
	if a.Action == "" {
		// A duck that answered in prose still said something; treat it as a
		// note rather than lose it — but never as a stop.
		if text := strings.TrimSpace(outcome.Text); text != "" && outcome.Parsed == nil {
			return advice{Action: "note", Note: text}
		}
		return advice{Action: "none"}
	}
	switch a.Action {
	case "none", "note", "stop":
	default:
		a.Action = "none"
	}
	if a.Action == "note" && strings.TrimSpace(a.Note) == "" {
		a.Action = "none"
	}
	if a.Action == "stop" && strings.TrimSpace(a.Reason) == "" {
		a.Reason = "the advisor judged this run is not converging"
	}
	return a
}

// consultAdvisor runs the duck's turn. Returns the corrective note (if any)
// and *AdvisorStop when the duck says stop. A missing advisor seat is not an
// error: the consult is skipped and the record says so.
func consultAdvisor(ctx context.Context, params *ExecuteParams, runner TurnRunner, registry *tools.Registry, round, index int, implementer config.DucklingID, outcome *agent.Outcome, signals distressSignals) (string, *AdvisorStop, error) {
	turn := Turn{
		Role: config.RoleAdvisor, Toolbelt: "full", Contract: adviceContract,
		MaxTurns: CapFor(params.TurnCaps, config.RoleAdvisor, consultAdvisorDefaultTurns),
	}
	advisor := resolveDuckling(params, turn)
	if advisor == "" {
		emit(params, "advisor_consult", map[string]interface{}{
			"round": round, "outcome": "skipped", "detail": "no advisor seated", "signals": signals,
		})
		return "", nil, nil
	}
	belt, err := turn.ResolveToolbelt(registry)
	if err != nil {
		return "", nil, err
	}
	prompt := rubberDuckPrompt(params, implementer, outcome, signals)
	emit(params, "turn_start", map[string]interface{}{
		"round": round, "turn": index, "role": string(config.RoleAdvisor), "duckling": string(advisor),
	})
	res, err := runner(ctx, &turn, advisor, prompt, belt, TurnContext{Round: round, Index: index})
	if res != nil {
		emitMessage(params, round, index, config.RoleAdvisor, advisor, res)
	}
	emit(params, "turn_end", map[string]interface{}{
		"round": round, "turn": index, "role": string(config.RoleAdvisor), "incomplete": err != nil,
	})
	if err != nil {
		// The duck failing must not fail the run it was helping: record and
		// carry on to the reviewer as if it had said nothing.
		emit(params, "advisor_consult", map[string]interface{}{
			"round": round, "advisor": string(advisor), "outcome": "failed", "detail": err.Error(), "signals": signals,
		})
		return "", nil, nil
	}
	a := parseAdvice(res)
	event := map[string]interface{}{
		"round": round, "advisor": string(advisor), "outcome": a.Action, "signals": signals,
	}
	if a.Reason != "" {
		event["reason"] = a.Reason
	}
	if a.Reshuffle != "" {
		event["reshuffle"] = a.Reshuffle
	}
	emit(params, "advisor_consult", event)
	switch a.Action {
	case "note":
		return strings.TrimSpace(a.Note), nil, nil
	case "stop":
		return "", &AdvisorStop{Advisor: advisor, Reason: strings.TrimSpace(a.Reason), Reshuffle: strings.TrimSpace(a.Reshuffle)}, nil
	}
	return "", nil, nil
}

// rubberDuckPrompt lays the implementer's whole turn in front of the duck:
// its final words, its reasoning, and the trace of what it tried — the story
// the reviewer is forbidden to hear.
func rubberDuckPrompt(params *ExecuteParams, implementer config.DucklingID, outcome *agent.Outcome, signals distressSignals) string {
	var b strings.Builder
	b.WriteString(params.Prompt)
	b.WriteString("\n\n## Rubber-duck consult\n\n")
	fmt.Fprintf(&b, "The implementer (%s) has just finished a turn that the harness measured as distressed. ", implementer)
	b.WriteString("You are not reviewing the work — the reviewer does that next, blind to what you read here. " +
		"You are the duck: listen to what the implementer went through, then decide whether it needs advice for the next round, or whether this run should stop.\n\n")
	if params.Roster != nil {
		var seats []string
		for role, id := range params.Roster {
			if id != "" {
				seats = append(seats, fmt.Sprintf("%s=%s", role, id))
			}
		}
		sort.Strings(seats)
		if len(seats) > 0 {
			b.WriteString("Seats this run: " + strings.Join(seats, ", ") + "\n\n")
		}
	}
	if data, err := json.Marshal(signals); err == nil {
		b.WriteString("### What the harness measured\n\n```json\n" + string(data) + "\n```\n\n")
	}
	if len(params.Deliverables) > 0 {
		b.WriteString("### The deliverables and what the implementer reports\n\n" + renderDeliverables(params.Deliverables))
		if rep := ParseDeliverablesReport(outcomeText(outcome), len(params.Deliverables)); rep != nil && !rep.Unreported {
			if data, err := json.Marshal(rep.Items); err == nil {
				b.WriteString("\nReported: " + string(data) + "\n")
			}
			if gap := rep.Undelivered(); len(gap) > 0 {
				fmt.Fprintf(&b, "\nThe implementer itself reports %v undelivered — ask why, and what would unblock it.\n", gap)
			}
		} else {
			b.WriteString("\nThe implementer filed no report on these.\n")
		}
		b.WriteString("\n")
	}
	if trace := toolTrace(outcome, 40); trace != "" {
		b.WriteString("### What the implementer tried (tool trace, oldest first)\n\n" + trace + "\n")
	}
	if outcome != nil && strings.TrimSpace(outcome.Reasoning) != "" {
		b.WriteString("### The implementer's reasoning (tail)\n\n" + lastN(strings.TrimSpace(outcome.Reasoning), 4000) + "\n\n")
	}
	if outcome != nil && strings.TrimSpace(outcome.Text) != "" {
		b.WriteString("### What the implementer said at the end\n\n" + firstNStr(strings.TrimSpace(outcome.Text), 3000) + "\n\n")
	}
	b.WriteString(`### Answer with ONE JSON object

{"action":"none"}
  — the turn was rough but the implementer got through; the next round needs nothing from you.
{"action":"note","note":"..."}
  — concrete, actionable advice for the NEXT implementer round: which tool to use instead, what to read first, what to stop doing. 2-6 sentences, imperative voice. It rides the next round's prompt verbatim.
{"action":"stop","reason":"...","reshuffle":"..."}
  — the run is not converging and more rounds will only burn budget: the same failure repeats, the model cannot operate the tools it needs, or the task is beyond this seat. Give the reason in one or two sentences, and in "reshuffle" say what to change for the re-run (a different implementer seat, a note, a smaller task).

Prefer "none" when in doubt. Choose "stop" only when the evidence above shows repetition without progress.`)
	return b.String()
}

// toolTrace renders the last n tool calls, one line each: name, a short
// argument digest, OK or the first line of the error.
func toolTrace(outcome *agent.Outcome, n int) string {
	if outcome == nil || len(outcome.ToolCalls) == 0 {
		return ""
	}
	calls := outcome.ToolCalls
	skipped := 0
	if len(calls) > n {
		skipped = len(calls) - n
		calls = calls[skipped:]
	}
	var b strings.Builder
	if skipped > 0 {
		fmt.Fprintf(&b, "(%d earlier calls omitted)\n", skipped)
	}
	for _, c := range calls {
		status := "ok"
		if c.Result != nil && c.Result.IsError {
			first := strings.TrimSpace(c.Result.Content)
			if i := strings.IndexByte(first, '\n'); i >= 0 {
				first = first[:i]
			}
			status = "ERR " + firstNStr(first, 160)
		}
		fmt.Fprintf(&b, "- %s %s → %s\n", c.Name, argDigest(c.Args), status)
	}
	return b.String()
}

func argDigest(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return firstNStr(string(raw), 80)
	}
	for _, k := range []string{"path", "cmd", "command", "pattern", "question", "id"} {
		if v, ok := m[k]; ok {
			s := fmt.Sprint(v)
			if k == "path" {
				if start, ok := m["start"]; ok {
					s += fmt.Sprintf(":%v", start)
					if end, ok := m["end"]; ok {
						s += fmt.Sprintf("-%v", end)
					}
				}
			}
			return firstNStr(s, 80)
		}
	}
	return ""
}

func outcomeText(o *agent.Outcome) string {
	if o == nil {
		return ""
	}
	return o.Text
}

func firstNStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// stoppedByAdvisor lets callers recognise the duck's stop through wrapping.
func stoppedByAdvisor(err error) (*AdvisorStop, bool) {
	var stop *AdvisorStop
	if errors.As(err, &stop) {
		return stop, true
	}
	return nil, false
}

// StoppedByAdvisor is the exported form for the service.
func StoppedByAdvisor(err error) (*AdvisorStop, bool) { return stoppedByAdvisor(err) }
