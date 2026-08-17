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
	// TurnCaps overrides how many model calls one turn of a role may chain.
	// Absent leaves whatever the script or the mode carries.
	//
	// Needed because tournament and split build their turns themselves rather
	// than from a script, so walking a script's turns reaches four modes out of
	// six and a setting that applies to some modes is worse than none.
	TurnCaps map[config.Role]int
	// LiveToolEvents says the runner emits tool_call events itself, per call,
	// as they complete (agent.Loop.OnToolCall) — the post-turn batch here
	// would duplicate every one of them in the record.
	LiveToolEvents bool
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
	// RoleTexts holds every reply per role, in turn order — the memory Text
	// lacks. The stage layer falls back through an architect's earlier
	// drafts when the final revise carries no sections.
	RoleTexts map[string][]string
	Error     error
}

// ExecuteSolo executes the solo mode.
func ExecuteSolo(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, SoloScript(), params)
}

// ExecuteTestFirst writes the failing test for a task.
func ExecuteTestFirst(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, TestFirstScript(), params)
}

// ExecuteTestFirstMode executes the test-writing script for a mode.
func ExecuteTestFirstMode(ctx context.Context, mode string, params *ExecuteParams) (*ExecuteResult, error) {
	return ExecuteScript(ctx, TestFirstScriptFor(mode), params)
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
	var correctiveNotes []string

	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round
		state := conv.State{Round: round}
		verdictsThisRound := 0
		operational := ""

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

			prompt, err := buildPrompt(&turn, params, result.Transcript, findings, correctiveNotes, operational)
			if err != nil {
				result.Error = err
				return result, err
			}

			emit(params, "turn_start", map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role), "duckling": string(duckling),
			})

			outcome, err := runner(ctx, &turn, duckling, prompt, toolbelt, TurnContext{Round: round, Index: script.TurnIndexBase + i})
			if outcome != nil {
				result.Outcome = outcome
			}
			if err != nil {
				// What it managed to say and do before it died, recorded on the
				// way out. This used to return first, so a turn that failed took
				// its whole record with it: a run that patched a file seventeen
				// times left a transcript of four events, and the only way to
				// see the work was to read llm.jsonl by hand. The failure is
				// exactly when that record is worth most.
				if outcome != nil {
					emitMessage(params, round, i, turn.Role, duckling, outcome)
					emit(params, "turn_end", map[string]interface{}{
						"round": round, "turn": i, "role": string(turn.Role),
						"incomplete": true,
					})
				}
				// A pause propagates untouched: the caller checkpoints the run
				// and the loop resumes from the top once answered.
				result.Error = err
				return result, err
			}
			result.Outcome = outcome
			result.Text = outcome.Text
			// Every reply by role, in order. Text alone keeps only the LAST
			// turn, and for artifact councils that is the revise — a revise
			// that stands pat would otherwise erase the draft it stood on.
			if result.RoleTexts == nil {
				result.RoleTexts = map[string][]string{}
			}
			result.RoleTexts[string(turn.Role)] = append(result.RoleTexts[string(turn.Role)], outcome.Text)

			result.Transcript.Add(conv.Entry{
				Round: round, Index: script.TurnIndexBase + i, Role: turn.Role,
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
				// The WORST verdict of the round, not the last: a council seats
				// several critics now, and one request-changes among approvals
				// is a request for changes. Overwriting meant the last critic
				// to speak decided for everyone.
				if verdictsThisRound == 0 || state.Verdict == "approve" {
					state.Verdict = v.Verdict
				}
				verdictsThisRound++
				// Findings accumulate across the round's critics — each saw a
				// different blind spot, which is the reason to seat more than
				// one — and reset with the next round's fresh draft.
				if verdictsThisRound == 1 {
					findings = toConvFindings(v.Findings)
				} else {
					findings = append(findings, toConvFindings(v.Findings)...)
				}
				state.NoFindings = len(findings) == 0
			case *agent.Choice:
				state.Choice = v.Choice
			}

			emit(params, "turn_end", map[string]interface{}{
				"round": round, "turn": i, "role": string(turn.Role),
			})

			// The rubber duck: after the implementer's turn is closed on the
			// record, before the reviewer speaks, and only on measured
			// distress. See rubberduck.go.
			if turn.Role == config.RoleImplementer {
				if signals := measureDistress(outcome); signals.Distressed() {
					if summary, ok := operationalSummary(outcome); ok {
						operational = summary
					}
					note, stop, cerr := consultAdvisor(ctx, params, runner, registry, round, script.TurnIndexBase+i, duckling, outcome, signals)
					if cerr != nil {
						result.Error = cerr
						return result, cerr
					}
					if stop != nil {
						result.Error = stop
						return result, stop
					}
					if note != "" {
						correctiveNotes = append(correctiveNotes, note)
					}
				}
			}
		}

		// The gate runs after the round's turns, and it — not any model —
		// decides whether the work is green (I2).
		if params.Gate != nil {
			// Announced BEFORE it runs: a full suite can legally take minutes,
			// and a transcript whose reviewer just approved while nothing moved
			// read as a hang — the person could not see the harness working.
			emit(params, "gate_started", map[string]interface{}{"round": round})
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
		// Whether this round actually touched the tree. It used to be hardcoded
		// true, which was never read and never true in the interesting case.
		state.Changed = true
		if params.Diff != nil {
			if diff, err := params.Diff(); err == nil {
				state.Changed = strings.TrimSpace(diff) != ""
			}
		}

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

		// Nothing another round could change. The tree is untouched and the
		// gate is green, so the next implementer turn has nothing to write and
		// the next reviewer turn will read the same empty diff and object to it
		// again. T-007 burned three rounds this way: both ducklings agreed in
		// prose that the work was already present, and the reviewer returned
		// "request-changes" each time because its verdict contract has no way
		// to say "the code is right, the plan is wrong". The loop cannot
		// terminate on an objection the implementer cannot act on, so it is
		// terminated here instead.
		if !state.Changed && state.Gate == "green" {
			emit(params, "settled", map[string]interface{}{
				"round":  round,
				"detail": "no round changed the tree and the gate is green — further rounds cannot alter either",
			})
			break
		}
	}

	return result, nil
}

// buildPrompt assembles the turn's user prompt: the task, the previous round's
// review if this is an implementer, and the diff if this is a reviewer.
func buildPrompt(turn *Turn, params *ExecuteParams, tr *conv.Transcript, findings []conv.Finding, correctiveNotes []string, operational string) (string, error) {
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
		if len(correctiveNotes) > 0 {
			b.WriteString("\n\n## Advisor corrective note\n\n" + strings.Join(correctiveNotes, "\n\n"))
		}
	case config.RoleReviewer:
		if operational != "" {
			b.WriteString("\n\n## Operational summary\n\n```json\n" + operational + "\n```\n")
		}
		// A document critic gets the draft under its own heading, with the
		// mechanism spelled out. Presented only as "Conversation so far", the
		// draft read as chat history and the reviewer went looking for the
		// real thing with tools — which truthfully reported a world without
		// it, since a proposal touches nothing until a person accepts it.
		if turn.Persona == PersonaCritic {
			b.WriteString("\n\n## The draft under review\n\n" +
				"The proposal you are critiquing is in the conversation below. " +
				"It exists only there — not in the tree, not in the artifact " +
				"store — until a person accepts it, so do not go looking for " +
				"it with tools.\n")
		}
		// The reviewer gets the diff and the conversation with the author's
		// own turns removed (I7). Compacted per file: a tracked build
		// artifact once rode this prompt at 644KB and the reviewer re-read
		// it on all 22 calls of its loop — 4.7M tokens for one minified
		// bundle (T-067).
		if params.Diff != nil {
			diff, err := params.Diff()
			if err != nil {
				return "", fmt.Errorf("diff for reviewer: %w", err)
			}
			b.WriteString("\n\n## The change under review\n\n```diff\n")
			b.WriteString(strings.TrimSpace(conv.CompactDiff(diff)))
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
	EmitTurnRecord(func(kind string, data map[string]interface{}) {
		emit(params, kind, data)
	}, round, turn, role, duckling, outcome, params.LiveToolEvents)
}

// EmitTurnRecord writes what a turn said and did, through whatever writer the
// caller has.
//
// Exported because the operate loop drives its own turns: triage runs one per
// bug outside a script, and it wrote turn_start and turn_end around nothing at
// all. The run showed a participant with an empty bubble, and the model's
// reasoning — which is the whole content of a triage — never left the process.
// Duplicating the event shapes here would have been two places to keep in step.
// liveToolEvents says the runner already emitted each tool_call as it
// completed (agent.Loop.OnToolCall); the batch below would duplicate them.
func EmitTurnRecord(emitFn func(kind string, data map[string]interface{}), round, turn int, role config.Role, duckling config.DucklingID, outcome *agent.Outcome, liveToolEvents bool) {
	emit := func(_ *ExecuteParams, kind string, data map[string]interface{}) { emitFn(kind, data) }
	var params *ExecuteParams
	_ = params
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
		if outcome.Reasoning != "" {
			data["reasoning"] = outcome.Reasoning
		}
		emit(params, "message", data)
	}
	// Live emission (agent.Loop.OnToolCall) supersedes this batch: a
	// thirty-call turn showed an empty timeline for its whole length and
	// then every tick at once. The batch remains for callers that wire no
	// callback, so nothing loses its record.
	if liveToolEvents {
		return
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
			data["result"] = SummariseToolResult(tc.Result.Content)
		}
		if tc.Digest != "" {
			data["digest"] = tc.Digest
		}
		emit(params, "tool_call", data)
	}
}

// maxToolResultBytes bounds what a tool result contributes to the log (I3).
const maxToolResultBytes = 512

func SummariseToolResult(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxToolResultBytes {
		return s
	}
	return s[:maxToolResultBytes] + fmt.Sprintf("\n… %d bytes truncated", len(s)-maxToolResultBytes)
}

// CapFor returns the call cap for a role: the configured one, else the fallback
// the caller was going to use.
func CapFor(caps map[config.Role]int, role config.Role, fallback int) int {
	if n, ok := caps[role]; ok && n > 0 {
		return n
	}
	return fallback
}
