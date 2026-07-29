package strategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/conv"
	"github.com/jrullan/ducklab/internal/tools"
)

// TurnRunner executes one turn and returns its outcome.
//
// Injected rather than called directly so the round scheduler — the part that
// must be deterministic — can be tested without a provider, a repo, or a
// model. The default implementation calls agent.RunTurn.
// TurnContext is where a turn sits and where it works.
type TurnContext struct {
	// Round and Index identify WHICH turn this is, not just what kind.
	// Streamed tokens have to be attributable to one turn: keyed by duckling
	// alone, a council's second architect turn appended to the first one's.
	Round int
	Index int
	// Root is the tree this turn may touch. Empty means the project root.
	//
	// A tournament creates an isolated worktree per contestant and then had no
	// way to tell the runner about it, so every contestant edited the shared
	// tree instead. Their patches came back empty, the judge correctly found
	// nothing to choose between, and the work still landed in the repository
	// with no one having picked it.
	Root string
}

type TurnRunner func(ctx context.Context, t *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error)

// GateRunner runs the project's verification and reports green/red/none.
type GateRunner func(ctx context.Context) (gate string, log string, err error)

// ExecuteParams are the inputs to a script execution.
type ExecuteParams struct {
	ProjectRoot string
	TaskID      string
	Prompt      string
	AgentLoop   *agent.Loop
	ExecContext *tools.ExecContext
	Rounds      int

	// Runner executes a turn. Defaults to agent.RunTurn via AgentLoop.
	Runner TurnRunner
	// Gate runs verification between rounds. If nil the gate is "none" and
	// Until expressions referring to it evaluate accordingly — an honest
	// answer, never an assumed green (P3).
	Gate GateRunner
	// Diff returns the current working-tree diff, shown to the reviewer.
	Diff func() (string, error)
	// Roster maps a role to the duckling that plays it.
	Roster map[config.Role]config.DucklingID
	// OnEvent reports progress; optional.
	OnEvent func(kind string, data map[string]interface{})
}

// RoundRecord is what happened in one round.
type RoundRecord struct {
	Round   int
	Gate    string
	Verdict string
	Choice  string
}

// ExecuteResult is the result of executing a script.
type ExecuteResult struct {
	Text       string
	Rounds     int
	Outcome    *agent.Outcome
	Transcript *conv.Transcript
	State      conv.State
	Records    []RoundRecord
	Error      error
}

// ExecuteSolo executes the solo mode.
func ExecuteSolo(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, SoloScript(), params)
}

// ExecuteTestFirst writes the failing test for a task.
func ExecuteTestFirst(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, TestFirstScript(), params)
}

// ExecutePair executes the pair mode.
func ExecutePair(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, PairScript(), params)
}

// ExecuteScript runs a conversation script.
//
// The loop is: for each round, run every turn in order, then evaluate the
// script's Until expression against the round's state. Turn order and round
// count are data; no model influences either (05 §3.1).
func ExecuteScript(ctx context.Context, script *Script, params *ExecuteParams) (*ExecuteResult, error) {
	result := &ExecuteResult{Transcript: &conv.Transcript{}}

	registry := registryFrom(params)
	if err := script.Validate(registry); err != nil {
		result.Error = err
		return result, err
	}
	until, err := conv.Compile(script.Until)
	if err != nil {
		result.Error = err
		return result, err
	}

	maxRounds := params.Rounds
	if maxRounds <= 0 {
		maxRounds = script.MaxRounds
	}
	if maxRounds <= 0 {
		maxRounds = 3
	}

	runner := params.Runner
	if runner == nil {
		runner = defaultRunner(params)
	}

	// findings carry the previous round's review into this round's implementer
	// prompt; this is what makes pair an iteration rather than two monologues.
	var findings []conv.Finding

	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round
		state := conv.State{Round: round}

		for i := range script.Turns {
			turn := script.Turns[i]

			if turn.Role == config.RoleHuman {
				// A human turn is scheduled by the stage runner, not here.
				continue
			}

			toolbelt, err := turn.ResolveToolbelt(registry)
			if err != nil {
				result.Error = err
				return result, err
			}
			duckling := resolveDuckling(params, turn)

			prompt, err := buildPrompt(&turn, params, result.Transcript, findings)
			if err != nil {
				result.Error = err
				return result, err
			}

			emit(params, "turn_start", map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role), "duckling": string(duckling),
			})

			outcome, err := runner(ctx, &turn, duckling, prompt, toolbelt, TurnContext{Round: round, Index: i})
			if outcome != nil {
				result.Outcome = outcome
			}
			if err != nil {
				// A pause propagates untouched: the caller checkpoints the run
				// and the loop resumes from the top once answered.
				result.Error = err
				return result, err
			}
			result.Outcome = outcome
			result.Text = outcome.Text

			result.Transcript.Add(conv.Entry{
				Round: round, Index: i, Role: turn.Role,
				Duckling: duckling, Text: transcriptText(outcome),
			})

			// What the model actually said, and what it did.
			//
			// turn_start and turn_end bracketed a turn whose content was never
			// recorded anywhere: the run log held eleven events and not one
			// carried a message, /transcript answered an empty document, and
			// the desktop's conversation lanes had nothing to render. The text
			// existed the whole time — it fed the internal transcript one line
			// above — it just never left the process.
			emitMessage(params, round, i, turn.Role, duckling, outcome)

			// Fold the turn's parsed contract value into the round state.
			switch v := outcome.Parsed.(type) {
			case *agent.Verdict:
				state.Verdict = v.Verdict
				state.NoFindings = len(v.Findings) == 0
				findings = toConvFindings(v.Findings)
			case *agent.Choice:
				state.Choice = v.Choice
			}

			emit(params, "turn_end", map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role),
			})
		}

		// The gate runs after the round's turns, and it — not any model —
		// decides whether the work is green (I2).
		if params.Gate != nil {
			gate, log, err := params.Gate(ctx)
			if err != nil {
				result.Error = err
				return result, err
			}
			state.Gate = gate
			// round_gate, not gate: the two carry different things under the same
			// name otherwise. The service's "gate" reports a verification —
			// which command ran and what it exited with. This reports a round's
			// outcome, green or red. A consumer taking the latest "gate" event
			// got whichever happened to come last, and the desktop's gate card
			// showed the right thing only because of event ordering.
			emit(params, "round_gate", map[string]interface{}{"result": gate, "round": round})
			_ = log
		}
		state.Changed = true

		result.State = state
		result.Records = append(result.Records, RoundRecord{
			Round: round, Gate: state.Gate, Verdict: state.Verdict, Choice: state.Choice,
		})

		done, err := until.Eval(&state)
		if err != nil {
			result.Error = err
			return result, err
		}
		if done {
			break
		}
	}

	return result, nil
}

// buildPrompt assembles the turn's user prompt: the task, the previous round's
// review if this is an implementer, and the diff if this is a reviewer.
func buildPrompt(turn *Turn, params *ExecuteParams, tr *conv.Transcript, findings []conv.Finding) (string, error) {
	var b strings.Builder
	b.WriteString(params.Prompt)

	switch turn.Role {
	case config.RoleArchitect:
		// A revision that cannot see the critique is just a second draft.
		if rendered := tr.Render(false, ""); rendered != "" {
			b.WriteString("\n\n")
			b.WriteString(rendered)
		}
	case config.RoleImplementer:
		if rendered := conv.RenderFindings(findings); rendered != "" {
			b.WriteString("\n\n")
			b.WriteString(rendered)
		}
	case config.RoleReviewer:
		// The reviewer gets the diff and the conversation with the author's
		// own turns removed (I7).
		if params.Diff != nil {
			diff, err := params.Diff()
			if err != nil {
				return "", fmt.Errorf("diff for reviewer: %w", err)
			}
			b.WriteString("\n\n## The change under review\n\n```diff\n")
			b.WriteString(strings.TrimSpace(diff))
			b.WriteString("\n```\n")
		}
		if rendered := tr.Render(turn.Anonymize, turn.OmitRole); rendered != "" {
			b.WriteString("\n")
			b.WriteString(rendered)
		}
	}
	return b.String(), nil
}

// transcriptText renders a turn for the next reader.
//
// A verdict's raw JSON is the wire format, not the content. An architect asked
// to revise against `{"verdict":"request-changes"}` has been told nothing;
// against the rendered findings it has been told what to change.
func transcriptText(outcome *agent.Outcome) string {
	if v, ok := outcome.Parsed.(*agent.Verdict); ok && v != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "Verdict: %s\n", v.Verdict)
		if rendered := conv.RenderFindings(toConvFindings(v.Findings)); rendered != "" {
			b.WriteString(rendered)
		}
		return strings.TrimSpace(b.String())
	}
	return outcome.Text
}

func resolveDuckling(params *ExecuteParams, turn Turn) config.DucklingID {
	if turn.Duckling != "" {
		return turn.Duckling
	}
	if params.Roster != nil {
		if id, ok := params.Roster[turn.Role]; ok && id != "" {
			return id
		}
	}
	if params.AgentLoop != nil {
		return params.AgentLoop.Duckling.ID
	}
	return ""
}

func registryFrom(params *ExecuteParams) *tools.Registry {
	if params.AgentLoop != nil && params.AgentLoop.Registry != nil {
		return params.AgentLoop.Registry
	}
	return tools.NewRegistry()
}

func defaultRunner(params *ExecuteParams) TurnRunner {
	return func(ctx context.Context, t *Turn, duckling config.DucklingID, prompt string, toolbelt []string, tc TurnContext) (*agent.Outcome, error) {
		return agent.RunTurn(ctx, params.AgentLoop, &agent.Turn{
			Round:     tc.Round,
			Index:     tc.Index,
			Role:      t.Role,
			Duckling:  duckling,
			Prompt:    prompt,
			Toolbelt:  toolbelt,
			Contract:  t.Contract,
			MaxTurns:  t.MaxTurns,
			Anonymize: t.Anonymize,
		}, params.ExecContext)
	}
}

func toConvFindings(in []agent.Finding) []conv.Finding {
	out := make([]conv.Finding, len(in))
	for i, f := range in {
		out[i] = conv.Finding{Severity: f.Severity, File: f.File, Line: f.Line, Issue: f.Issue, Fix: f.Fix}
	}
	return out
}

func emit(params *ExecuteParams, kind string, data map[string]interface{}) {
	if params.OnEvent != nil {
		params.OnEvent(kind, data)
	}
}

// emitMessage records a turn's content and its tool calls.
//
// Tool calls are separate events rather than a field on the message: the
// timeline renders them in order, and a turn that made forty fs_read calls
// must not put forty payloads inside one record.
func emitMessage(params *ExecuteParams, round, turn int, role config.Role, duckling config.DucklingID, outcome *agent.Outcome) {
	if outcome == nil {
		return
	}
	if text := strings.TrimSpace(outcome.Text); text != "" {
		data := map[string]interface{}{
			"round": round, "turn": turn,
			"role": string(role), "duckling": string(duckling),
			"content":    text,
			"tokens_in":  outcome.TokensIn,
			"tokens_out": outcome.TokensOut,
			"repairs":    outcome.Repairs,
		}
		// A reviewer's turn is a verdict, not prose. Sending only the raw text
		// left the lane showing `{"verdict":"approve", "findings":[]}` — the
		// one turn whose content is already structured, displayed as a blob.
		// The raw text stays: it is what the model actually returned.
		if v, ok := outcome.Parsed.(*agent.Verdict); ok && v != nil {
			data["verdict"] = v.Verdict
			data["findings"] = v.Findings
		}
		emit(params, "message", data)
	}
	for _, tc := range outcome.ToolCalls {
		data := map[string]interface{}{
			"round": round, "turn": turn,
			"role": string(role), "duckling": string(duckling),
			// "tool", not "name": both existing consumers — the CLI's stream
			// printer and the desktop's timeline — already read this key.
			"tool": tc.Name,
			"args": string(tc.Args),
		}
		if tc.Result != nil {
			data["ok"] = !tc.Result.IsError
			// The result is summarised, not stored whole: a single fs_read can
			// return an entire file, and forty of them would make the run log
			// larger than the repository it describes.
			data["result"] = summariseToolResult(tc.Result.Content)
		}
		if tc.Digest != "" {
			data["digest"] = tc.Digest
		}
		emit(params, "tool_call", data)
	}
}

// maxToolResultBytes bounds what a tool result contributes to the log (I3).
const maxToolResultBytes = 512

func summariseToolResult(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxToolResultBytes {
		return s
	}
	return s[:maxToolResultBytes] + fmt.Sprintf("\n… %d bytes truncated", len(s)-maxToolResultBytes)
}
