// Package agent implements the agentic loop: provider.Chat/ChatStream,
// tool dispatch, contract parsing, and repair. The loop is bounded by
// MaxTurns and enforces every bound.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/budget"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// Turn is one scheduled unit of conversation.
type Turn struct {
	Role      config.Role
	Duckling  config.DucklingID
	Prompt    string
	Toolbelt  []string
	Contract  string
	MaxTurns  int
	Anonymize bool
	// Persona narrows the role's system prompt to the situation ("critic" for
	// a document council's reviewer). Empty keeps the role's default.
	Persona string
	// Round and Index identify this turn within the run, so streamed tokens
	// can be attached to it rather than to whichever turn the same duckling
	// took last.
	Round int
	Index int
	// Images are data URLs shown to a vision model with the prompt — a bug's
	// screenshot in a triage turn. Set only when the duckling can see.
	Images []string
}

// Outcome is the result of a turn.
type Outcome struct {
	Text          string
	ToolCalls     []ToolCallRecord
	TokensIn      int
	TokensOut     int
	CostUSD       float64
	ContractError error
	// Parsed is the contract's typed value: *Verdict, *Choice, []Section,
	// map[string]interface{}, or nil for freeform/edits.
	Parsed interface{}
	// Pending is set when the turn stopped because a human must answer.
	Pending *tools.PendingQuestion
	// Repairs counts how many repair prompts this turn needed.
	Repairs int
	// Reasoning is the turn's accumulated thinking, joined across its model
	// calls. The deltas that streamed it are display state and are never
	// persisted (01 §5.3); this consolidated text IS the record's copy — it
	// was billed for, and without it a relaunched desktop showed a running
	// turn with its thinking gone and a finished one with none at all.
	Reasoning string
}

// ToolCallRecord records a tool call.
type ToolCallRecord struct {
	Name   string
	Args   json.RawMessage
	Result *tools.Result
	Digest string
}

// ErrTruncated is returned when a response is truncated.
var ErrTruncated = errors.New("response truncated")

// ErrContract is returned when contract parsing fails after repairs.
var ErrContract = errors.New("contract parse failed")

// ErrBudgetExceeded is returned when budget is exceeded.
var ErrBudgetExceeded = errors.New("budget exceeded")

// UncappedTurns stands in for "no cap" on the per-reply call loop. Finite,
// so I3 keeps its letter — but far beyond any use, so it never binds: the
// budget, checked before every call, is what actually guards an uncapped
// loop.
const UncappedTurns = 10000

// Loop runs the agentic loop for a single turn.
type Loop struct {
	Provider       provider.Provider
	Duckling       *DucklingConfig
	Registry       *tools.Registry
	Budget         *budget.Tracker
	MaxTurns       int
	RepairAttempts int
	// OnDelta, if set, receives streamed text as it arrives. Display only.
	OnDelta func(turn *Turn, text string)
	// OnReasoning, if set, receives streamed thinking as it arrives. Display
	// only, and deliberately a separate callback: appending deliberation to the
	// answer would make a transcript show a model's false starts as its reply.
	//
	// A model that reasons pays for those tokens whether or not anyone sees
	// them. The parser used to read only delta.content, so thinking was
	// generated, billed, and discarded before any event existed — which is why
	// a run that looked idle for two minutes had nothing on screen to explain
	// itself.
	OnReasoning func(turn *Turn, text string)
	// RunWriter, if set, receives LLM call records.
	RunWriter RunLogWriter
	// OnToolCall, if set, receives each tool call AS IT COMPLETES. The
	// strategy layer used to emit them in a batch when the whole turn ended,
	// so a thirty-call implementer turn showed an empty timeline for its
	// entire length and then eighty ticks at once.
	OnToolCall func(turn *Turn, duckling string, rec *ToolCallRecord)
	// OnCall, if set, fires as each model call of a reply begins, with the
	// call number and the effective cap. The budget card read "default"
	// while an architect sat at 19 calls of an invisible 24 — the loop is
	// the only thing that knows both numbers.
	OnCall func(turn *Turn, n, max int)
	// OnCapNear, if set, fires once as a reply is about to spend its LAST
	// allowed model call. The person watching can lift the cap live (the
	// budget card's calls/reply "no cap"), but only if they learn in time —
	// an intake died at 12/12 with the lift sitting unticked beside it.
	OnCapNear func(turn *Turn, used, max int)
	// OnToolStart, if set, fires as a tool BEGINS executing. Completion was
	// the only event, so a gate command that ran for its whole 900s ceiling
	// was fifteen minutes of unexplained silence — the person read it as a
	// hang and aborted healthy work. The lane can now say what is running.
	OnToolStart func(turn *Turn, duckling string, name string, args json.RawMessage)
	// OnRepetitionLoop, if set, fires when streaming detects a repeated n-gram.
	OnRepetitionLoop func(turn *Turn, repeated string)
	// OnRetry, if set, hears every transient provider failure AS IT HAPPENS.
	// The retry chain used to run in total silence: a stalled stream timed
	// out at 300s, three fallback attempts re-ran the wait, and the record
	// showed nothing until the whole chain died — up to twenty minutes in
	// which a person watching an idle run had every reason to abort healthy
	// work, and did, three times (T-075).
	OnRetry func(turn *Turn, attempt int, err error)
	// CapLift, if set, is consulted before every call: true removes this
	// turn's call cap for the rest of the reply. It exists so the person
	// watching a run circle toward its cap can lift it IN FLIGHT — a
	// reviewer once died on exactly its hundredth call, and the only remedy
	// was resuming into the same ceiling. The budget still guards.
	CapLift func() bool
}

// RunLogWriter is the interface for writing LLM call records.
type RunLogWriter interface {
	AppendLLM(call *LLMCallRecord) error
}

// LLMCallRecord is a record of an LLM call for logging.
type LLMCallRecord struct {
	Duckling string
	Provider string
	// Upstream is who OpenRouter actually routed the call to — the pool
	// member, not the gateway. Empty for direct endpoints.
	Upstream     string
	Model        string
	Role         string
	Request      map[string]interface{}
	Response     map[string]interface{}
	Usage        map[string]interface{}
	CostUSD      float64
	LatencyMs    int64
	Attempt      int
	Estimated    bool
	CostSource   string
	FinishReason string
}

// DucklingConfig holds the duckling's configuration for the loop.
type DucklingConfig struct {
	ID       config.DucklingID
	Provider config.ProviderID
	Model    string
	Params   config.SamplingParams
	Caps     provider.Capabilities
	Cost     config.Cost
}

// RunTurn executes a single conversation turn.
func RunTurn(ctx context.Context, loop *Loop, turn *Turn, ectx *tools.ExecContext) (*Outcome, error) {
	outcome := &Outcome{}
	// A budget is a deadline, not merely a checkpoint between calls. The local
	// Qwen plan crossed its 30-minute cap inside one 139-second generation;
	// every provider path in this turn inherits the remaining run deadline.
	if loop.Budget != nil {
		budgetCtx, cancel := loop.Budget.Context(ctx)
		defer cancel()
		ctx = budgetCtx
	}
	if ectx != nil {
		ectx.BeginTurn()
	}

	// Determine dialect
	useNative := loop.Duckling.Caps.NativeTools

	// Build messages. Tool execution has always used loop.Registry, but the
	// text-protocol catalogue read only ExecContext.Registry. Production run
	// contexts do not populate that optional field, so local models were shown
	// bare tool names without descriptions or schemas and had to guess their
	// arguments. Keep execution state untouched and enrich only the prompt view.
	messageContext := ectx
	if ectx != nil && ectx.Registry == nil && loop.Registry != nil {
		copy := *ectx
		copy.Registry = loop.Registry
		messageContext = &copy
	}
	messages := BuildMessages(turn, messageContext, useNative)

	// Build tool definitions for native dialect
	var nativeTools []provider.Tool
	if useNative {
		for _, name := range turn.Toolbelt {
			t, err := loop.Registry.Get(name)
			if err != nil {
				continue
			}
			nativeTools = append(nativeTools, provider.Tool{
				Type: "function",
				Function: provider.ToolFunction{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Schema(),
				},
			})
		}
	}

	maxTurns := turn.MaxTurns
	if maxTurns <= 0 {
		maxTurns = loop.MaxTurns
	}
	if maxTurns <= 0 {
		maxTurns = 24
	}

	var conversation []provider.Message
	conversation = append(conversation, messages...)

	for turnNum := 1; turnNum <= maxTurns; turnNum++ {
		// A live lift lands between calls: the cap disappears mid-reply
		// instead of after the death it was about to cause.
		if maxTurns < UncappedTurns && loop.CapLift != nil && loop.CapLift() {
			maxTurns = UncappedTurns
		}
		if turnNum == maxTurns && maxTurns < UncappedTurns && loop.OnCapNear != nil {
			loop.OnCapNear(turn, turnNum-1, maxTurns)
		}
		if loop.OnCall != nil {
			loop.OnCall(turn, turnNum, maxTurns)
		}

		// Budget check
		// The outcome, not nil: by the time a budget runs out the turn has
		// usually done real work — tool calls, tokens, cost — and returning nil
		// threw all of it away. One run patched a file seventeen times and its
		// transcript recorded four events, not one of them naming a tool call.
		if msg, exceeded := loop.Budget.Check(); exceeded {
			return outcome, fmt.Errorf("%w: %s", ErrBudgetExceeded, msg)
		}

		// Build request
		req := provider.ChatRequest{
			Model:    loop.Duckling.Model,
			Messages: conversation,
		}
		// No tools once a result closed tool use for this reply: the seat
		// answers in text with what it has (tools.Result.EndTurn).
		if useNative && !(ectx != nil && ectx.ToolsClosed) {
			for _, tool := range nativeTools {
				if ectx == nil || ectx.ToolAvailable(tool.Function.Name) {
					req.Tools = append(req.Tools, tool)
				}
			}
		}
		if loop.Duckling.Params.Temperature != nil {
			req.Temperature = loop.Duckling.Params.Temperature
		}
		if loop.Duckling.Params.TopP != nil {
			req.TopP = loop.Duckling.Params.TopP
		}
		req.MaxTokens = outputCapForContract(loop.Duckling.Params.MaxTokens, turn.Contract)
		if len(loop.Duckling.Params.Stop) > 0 {
			req.Stop = loop.Duckling.Params.Stop
		}

		// Thinking suppression
		if loop.Duckling.Params.DisableThinking {
			applyThinkingSuppression(&req, loop.Duckling.Caps)
		}

		// Make the call. Thought-only replies are retried IN PLACE: a model
		// that returns seventy tokens of hidden reasoning and no answer has
		// glitched, not run out of room — and one bad sample among fifty good
		// calls used to kill the whole run, with the killing call absent from
		// llm.jsonl because the guard returned before the record was written.
		const thoughtOnlyAttempts = 3
		var resp provider.ChatResponse
		var err error
		var start time.Time
		var calc provider.CostCalculator
		var cost float64
		for attempt := 1; ; attempt++ {
			start = time.Now()
			resp, err = chatMaybeStreaming(ctx, loop, turn, req)
			if errors.Is(err, ErrRepetitionLoop) && attempt == 1 {
				if loop.OnRepetitionLoop != nil {
					loop.OnRepetitionLoop(turn, repetitionLoopText(err))
				}
				req.Messages = append(req.Messages, provider.Message{Role: "user", Content: fmt.Sprintf("A repetition loop was detected (%s). Stop repeating it and answer directly.", repetitionLoopText(err))})
				continue
			}

			if err != nil && provider.IsTransient(err) {
				// Retry with backoff — VISIBLY. Each transient failure lands
				// on the record before the next attempt starts, so a stalled
				// provider reads as "retrying (2)" instead of as death.
				if loop.OnRetry != nil {
					loop.OnRetry(turn, 1, err)
				}
				retryPolicy := provider.DefaultRetryPolicy()
				extra := 1
				err = provider.Retry(ctx, retryPolicy, func() error {
					var rerr error
					resp, rerr = loop.Provider.Chat(ctx, req)
					if rerr != nil && provider.IsTransient(rerr) && loop.OnRetry != nil {
						extra++
						loop.OnRetry(turn, extra, rerr)
					}
					return rerr
				})
			}
			if err != nil {
				callErr := fmt.Errorf("provider chat: %w", err)
				if errors.Is(err, context.DeadlineExceeded) && loop.Budget != nil {
					if msg, exceeded := loop.Budget.Check(); exceeded {
						callErr = fmt.Errorf("%w: %s", ErrBudgetExceeded, msg)
					}
				}
				// The call that failed, on the record. Only successful calls
				// were written, so a run that died on its third attempt left
				// two entries and no trace of the one that killed it — and
				// llm.jsonl is the one place that could have said what was
				// sent.
				if loop.RunWriter != nil {
					loop.RunWriter.AppendLLM(&LLMCallRecord{
						Duckling:     string(loop.Duckling.ID),
						Provider:     string(loop.Duckling.Provider),
						Model:        loop.Duckling.Model,
						Role:         string(turn.Role),
						Request:      requestMap(req),
						Response:     map[string]interface{}{"error": err.Error()},
						LatencyMs:    time.Since(start).Milliseconds(),
						Attempt:      attempt,
						FinishReason: "error",
					})
				}
				return outcome, callErr
			}

			// Record usage PER ATTEMPT: a glitched reply still billed its
			// reasoning tokens, and a budget that ignores them lies.
			calc = provider.CostCalculator{
				InputPerMTok:  loop.Duckling.Cost.InputPerMTok,
				OutputPerMTok: loop.Duckling.Cost.OutputPerMTok,
			}
			cost = calc.Cost(resp.Usage)
			loop.Budget.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cost)
			outcome.TokensIn += resp.Usage.PromptTokens
			outcome.TokensOut += resp.Usage.CompletionTokens
			outcome.CostUSD += cost

			if len(resp.Choices) == 0 {
				return outcome, fmt.Errorf("no response choices")
			}
			c := &resp.Choices[0]

			// Separate an inline reasoning block before anything reads the
			// content. Doing it here means every downstream path — contract
			// parsing, tool extraction, the transcript — sees the answer and
			// not the thinking.
			//
			// The thinking is kept rather than deleted: it was paid for, and
			// the run view has a place to show it. Discarding it here is why a
			// model that inlines its reasoning had an empty thinking section
			// while its deliberation filled the answer lane.
			if answer, thought := splitThinking(c.Message.Content); thought != "" {
				c.Message.Content = answer
				c.Message.Reasoning = joinReasoning(c.Message.Reasoning, thought)
			} else {
				c.Message.Content = answer
			}

			// A response with tokens spent and nothing to show. Two different
			// faults share this shape: a reply budget genuinely consumed by
			// thinking (thousands of tokens — retrying buys nothing), and a
			// stochastic empty reply (a handful of tokens — retrying is the
			// whole fix). Each gets its own advice, and neither escapes the
			// record.
			if c.Message.Content == "" && len(c.Message.ToolCalls) == 0 &&
				resp.Usage.CompletionTokens > 0 {
				if loop.RunWriter != nil {
					loop.RunWriter.AppendLLM(&LLMCallRecord{
						Duckling: string(loop.Duckling.ID),
						Provider: string(loop.Duckling.Provider),
						Model:    loop.Duckling.Model,
						Role:     string(turn.Role),
						Request:  requestMap(req),
						// The hidden reasoning is the only evidence of what the
						// model was doing for those tokens; a record that keeps
						// just the error cannot be diagnosed (Neocapture, a
						// 373 s thought-only revision turn, 2026-08-29).
						Response: map[string]interface{}{
							"error":           "thought-only reply: no content, no tool calls",
							"usage":           resp.Usage,
							"reasoning_chars": len(c.Message.Reasoning),
							"reasoning_head":  firstChars(c.Message.Reasoning, 2000),
							"reasoning_tail":  lastChars(c.Message.Reasoning, 1000),
						},
						LatencyMs:    time.Since(start).Milliseconds(),
						Attempt:      attempt,
						FinishReason: "thought_only",
					})
				}
				exhausted := 2000
				if req.MaxTokens != nil {
					exhausted = *req.MaxTokens * 9 / 10
				}
				if resp.Usage.CompletionTokens >= exhausted {
					return outcome, fmt.Errorf("%w: %s spent %d tokens on hidden reasoning and returned no answer; "+
						"raise max_tokens for this duckling, or disable thinking at the endpoint",
						ErrThoughtOnly, loop.Duckling.ID, resp.Usage.CompletionTokens)
				}
				if attempt < thoughtOnlyAttempts {
					continue
				}
				return outcome, fmt.Errorf("%w: %s returned an empty answer with only %d hidden reasoning tokens, "+
					"%d times in a row — the endpoint is misbehaving; relaunch the run, or disable thinking for this duckling",
					ErrThoughtOnly, loop.Duckling.ID, resp.Usage.CompletionTokens, attempt)
			}
			break
		}

		choice := resp.Choices[0]
		if choice.Message.Reasoning != "" {
			outcome.Reasoning = joinReasoning(outcome.Reasoning, choice.Message.Reasoning)
		}
		finishReason := choice.FinishReason

		// Log the LLM call
		if loop.RunWriter != nil {
			latencyMs := time.Since(start).Milliseconds()
			reqMap := requestMap(req)
			respMap := map[string]interface{}{
				"content":       choice.Message.Content,
				"finish_reason": finishReason,
			}
			if len(choice.Message.ToolCalls) > 0 {
				respMap["tool_calls"] = choice.Message.ToolCalls
			}
			loop.RunWriter.AppendLLM(&LLMCallRecord{
				Duckling:     string(loop.Duckling.ID),
				Provider:     string(loop.Duckling.Provider),
				Upstream:     resp.Upstream,
				Model:        loop.Duckling.Model,
				Role:         string(turn.Role),
				Request:      reqMap,
				Response:     respMap,
				Usage:        usageMap(resp.Usage),
				CostUSD:      cost,
				LatencyMs:    latencyMs,
				Attempt:      1,
				CostSource:   calc.CostSource(resp.Usage),
				FinishReason: finishReason,
			})
		}
		// Providers are expected to honor the context deadline, but a local
		// endpoint or transport may return a completed response after it. Record
		// that response, then stop before tools, repairs, or another model call.
		if msg, exceeded := loop.Budget.CheckWallclock(); exceeded {
			return outcome, fmt.Errorf("%w: %s", ErrBudgetExceeded, msg)
		}

		// Handle truncation
		//
		// The nudge used to be appended to `conversation` and then the *same*
		// req was resent. req.Messages was assigned before the append and
		// carries its own length, so the new message never reached the model:
		// the retry was the identical request, and it produced the identical
		// answer.
		//
		// That is how a run was lost. A model deliberating in circles — "let me
		// implement this now… actually, I just realized…" a dozen times over —
		// filled its output budget, got a retry that said nothing, filled it
		// again, and the run was marked FAILED.
		if provider.IsLength(finishReason) {
			// A document turn that hit the cap is not deliberating — the
			// document does not fit. "Be brief" cannot shrink a 36-section
			// spec into the same budget; the retry burned a full duplicate
			// call and died on the same wall, twice, for one real user. Fail
			// now, naming the wall and where the lever is.
			if strings.HasPrefix(turn.Contract, "markdown_sections") {
				cap := 0
				if req.MaxTokens != nil {
					cap = *req.MaxTokens
				}
				return outcome, fmt.Errorf("%w: the whole document did not fit in %s's output cap "+
					"(%d tokens). Raise max_tokens on this duckling (Ducklings → sampling params), "+
					"or draft with a duckling that has a higher cap", ErrTruncated, loop.Duckling.ID, cap)
			}
			retry := req
			// A fresh slice, so the nudge is actually in the request and the
			// original conversation is not mutated by an aliased append.
			retry.Messages = append(append([]provider.Message{}, conversation...), provider.Message{
				Role: "user",
				Content: "Your previous reply was cut off because it was too long. " +
					"Stop deliberating and act: call a tool, or give your final answer in " +
					"a few sentences. If you have concluded that the task needs no change, " +
					"say exactly that and stop.",
			})
			resp2, err2 := loop.Provider.Chat(ctx, retry)
			if err2 != nil || len(resp2.Choices) == 0 {
				return outcome, ErrTruncated
			}
			choice = resp2.Choices[0]
			finishReason = choice.FinishReason
			if provider.IsLength(finishReason) {
				cap := 0
				if req.MaxTokens != nil {
					cap = *req.MaxTokens
				}
				return outcome, fmt.Errorf("%w: %s filled its output cap (%d tokens) twice. "+
					"Raise max_tokens on this duckling (Ducklings → sampling params)",
					ErrTruncated, loop.Duckling.ID, cap)
			}
			// The retried answer is what the turn continues from, so the
			// conversation must carry the nudge that produced it.
			conversation = retry.Messages
		}

		// Handle content filter
		if finishReason == provider.FinishContentFilter {
			return outcome, fmt.Errorf("content filter blocked response")
		}

		// Handle tool calls (native dialect)
		//
		// Gated on the tool calls themselves, not only on finish_reason. A
		// streamed response does not always carry one — the chunk that would
		// have said "tool_calls" can be absent — and gating on the reason
		// alone meant the calls were dropped in silence and the turn ended
		// treating the model's own narration as its final answer.
		//
		// Measured as an A/B on the same task and the same ducklings: without
		// streaming the implementer made five calls, patched the file and
		// passed; with streaming it made two, never patched, and the reviewer
		// was handed nothing to review.
		if useNative && (provider.IsToolCalls(finishReason) || len(choice.Message.ToolCalls) > 0) {
			// Preserve narration accompanying tool calls as the partial draft so
			// a budget interruption can checkpoint what the model had concluded.
			if strings.TrimSpace(choice.Message.Content) != "" {
				outcome.Text = choice.Message.Content
			}
			toolCalls := choice.Message.ToolCalls
			conversation = append(conversation, choice.Message)
			for _, tc := range toolCalls {
				result, terr := executeToolCall(ctx, loop, ectx, tc, turn)
				if errors.Is(terr, tools.ErrHumanNeeded) {
					outcome.Pending = ectx.Pending
					return outcome, terr
				}
				conversation = append(conversation, provider.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result.Content,
				})
				rec := ToolCallRecord{
					Name:   tc.Function.Name,
					Args:   json.RawMessage(tc.Function.Arguments),
					Result: result,
					Digest: tools.Digest(json.RawMessage(tc.Function.Arguments)),
				}
				outcome.ToolCalls = append(outcome.ToolCalls, rec)
				if loop.OnToolCall != nil {
					loop.OnToolCall(turn, string(loop.Duckling.ID), &rec)
				}
			}
			continue
		}

		// Handle text protocol (Dialect B)
		if !useNative {
			text := choice.Message.Content
			// Check for tool call in text
			toolCall, remainingText := parseTextToolCall(text)
			if toolCall != nil {
				result, terr := executeTextToolCall(ctx, loop, ectx, toolCall, turn)
				if errors.Is(terr, tools.ErrHumanNeeded) {
					outcome.Pending = ectx.Pending
					return outcome, terr
				}
				conversation = append(conversation, provider.Message{
					Role:    "assistant",
					Content: text,
				})
				conversation = append(conversation, provider.Message{
					Role:    "user",
					Content: fmt.Sprintf("Tool result for %s:\n%s", toolCall.Name, result.Content),
				})
				rec := ToolCallRecord{
					Name:   toolCall.Name,
					Args:   toolCall.Args,
					Result: result,
					Digest: tools.Digest(toolCall.Args),
				}
				outcome.ToolCalls = append(outcome.ToolCalls, rec)
				if loop.OnToolCall != nil {
					loop.OnToolCall(turn, string(loop.Duckling.ID), &rec)
				}
				continue
			}
			// No tool call; this is the final answer
			outcome.Text = remainingText
			break
		}

		// Native dialect, no tool calls: final answer
		if provider.IsStop(finishReason) {
			outcome.Text = choice.Message.Content
			break
		}
	}

	// Record turn
	loop.Budget.RecordTurn()

	// The loop ended by running out of turns, not by the model answering.
	//
	// This used to fail the whole run — which threw away every OTHER turn's
	// work with it. A critic surveying a real codebase spent all 40 of its
	// calls reading and searching, legitimately, ran out mid-verification,
	// and the architect's perfectly good draft died with the run. Twice.
	// A model out of looking is not a model with nothing to say: it gets ONE
	// final call, tools withheld, to answer from what it has seen — a
	// reviewer's honest verdict at that point is request-changes naming what
	// it could not verify, which the loop can act on, unlike a corpse.
	if len(outcome.ToolCalls) > 0 && !substantiveAnswer(outcome.Text) {
		final := provider.ChatRequest{
			Model: loop.Duckling.Model,
			Messages: append(append([]provider.Message{}, conversation...), provider.Message{
				Role: "user",
				Content: "You are out of tool calls. Answer the original task NOW from " +
					"what you have already gathered — no more tools will be executed. " +
					"If your answer follows a contract (a verdict, a choice), emit it " +
					"now. Anything you could not verify, state as such inside the " +
					"answer rather than refusing to answer.",
			}),
		}
		final.MaxTokens = outputCapForContract(loop.Duckling.Params.MaxTokens, turn.Contract)
		if loop.Duckling.Params.DisableThinking {
			applyThinkingSuppression(&final, loop.Duckling.Caps)
		}
		if resp, err := loop.Provider.Chat(ctx, final); err == nil && len(resp.Choices) > 0 {
			calc := provider.CostCalculator{
				InputPerMTok:  loop.Duckling.Cost.InputPerMTok,
				OutputPerMTok: loop.Duckling.Cost.OutputPerMTok,
			}
			cost := calc.Cost(resp.Usage)
			loop.Budget.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cost)
			outcome.TokensIn += resp.Usage.PromptTokens
			outcome.TokensOut += resp.Usage.CompletionTokens
			outcome.CostUSD += cost
			answer, _ := splitThinking(resp.Choices[0].Message.Content)
			outcome.Text = answer
		}
	}
	if len(outcome.ToolCalls) > 0 && !substantiveAnswer(outcome.Text) {
		return outcome, fmt.Errorf(
			"%w: %s used all %d of its turns calling tools and never gave a substantive answer "+
				"(%d tool calls), even when asked to conclude without them. "+
				"Raise the turn cap for this role, or the task needs narrowing",
			ErrNoAnswer, turn.Role, maxTurns, len(outcome.ToolCalls))
	}
	if !substantiveAnswer(outcome.Text) {
		return outcome, fmt.Errorf("%w: %s returned only whitespace or Markdown fences", ErrNoAnswer, turn.Role)
	}

	// Parse contract. The parsed value is kept: pair needs the reviewer's
	// findings and tournament needs the judge's choice.
	parsed, err := ParseContract(turn.Contract, outcome.Text)
	if err != nil {
		repairedText, repairedVal, attempts, rerr := repairContract(ctx, loop, turn, messages, outcome.Text, err)
		outcome.Repairs = attempts
		if rerr != nil {
			// Name the contract and the original parse failure: "contract
			// parse failed" alone gives no way to tell a malformed verdict
			// from a malformed choice.
			outcome.ContractError = fmt.Errorf("%s contract (role %s): %w", turn.Contract, turn.Role, err)
			return outcome, fmt.Errorf("%w: %v", ErrContract, outcome.ContractError)
		}
		outcome.Text = repairedText
		parsed = repairedVal
	}
	outcome.Parsed = parsed

	return outcome, nil
}

// ErrRepetitionLoop reports a token repetition loop detected in a stream.
var ErrRepetitionLoop = errors.New("repetition loop")

type repetitionError struct{ text string }

func (e *repetitionError) Error() string { return fmt.Sprintf("%s: %s", ErrRepetitionLoop, e.text) }
func (e *repetitionError) Unwrap() error { return ErrRepetitionLoop }
func repetitionLoopText(err error) string {
	var e *repetitionError
	if errors.As(err, &e) {
		return e.text
	}
	return "repeated output"
}

// chatMaybeStreaming streams when the caller asked for it and the provider can,
// and falls back to a plain call otherwise.
//
// Streaming is a DISPLAY concern only (01 §5.2): contract parsing, tool
// dispatch and logging always operate on the assembled final response, never
// on deltas. A dropped subscriber therefore cannot affect a run.
func chatMaybeStreaming(ctx context.Context, loop *Loop, turn *Turn, req provider.ChatRequest) (provider.ChatResponse, error) {
	if loop.OnDelta == nil && loop.OnReasoning == nil {
		resp, err := loop.Provider.Chat(ctx, req)
		if err == nil && len(resp.Choices) > 0 {
			d := newRepetitionDetector()
			if d.Add(resp.Choices[0].Message.Content) {
				return provider.ChatResponse{}, &repetitionError{text: d.Repeated()}
			}
		}
		return resp, err
	}

	ch := make(chan provider.Delta, 64)
	done := make(chan struct{})
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var loopErr error
	go func() {
		defer close(done)
		// Deltas used to go to the answer untouched, so a model that inlines its
		// thinking filled the transcript lane with deliberation and bare
		// `</think>` markers for the whole run. One splitter per call: a marker
		// can be split across two chunks.
		var split thinkSplitter
		detector := newRepetitionDetector()
		emit := func(answer, reasoning string) {
			if answer != "" && detector.Add(answer) && loopErr == nil {
				loopErr = &repetitionError{text: detector.Repeated()}
				cancel()
			}
			if answer != "" && loop.OnDelta != nil {
				loop.OnDelta(turn, answer)
			}
			if reasoning != "" && loop.OnReasoning != nil {
				loop.OnReasoning(turn, reasoning)
			}
		}
		for d := range ch {
			if d.Text != "" {
				emit(split.Feed(d.Text))
			}
			// Already separated by the endpoint; nothing to parse.
			if d.Reasoning != "" && loop.OnReasoning != nil {
				loop.OnReasoning(turn, d.Reasoning)
			}
		}
		emit(split.Flush())
	}()

	resp, err := loop.Provider.ChatStream(streamCtx, req, ch)
	close(ch)
	<-done

	if errors.Is(err, provider.ErrUnsupported) {
		// The endpoint cannot stream. Emit the assembled text as a single
		// delta so a watching client still sees output appear, rather than
		// silently showing nothing for the whole turn.
		resp, err = loop.Provider.Chat(ctx, req)
		if err == nil && len(resp.Choices) > 0 {
			d := newRepetitionDetector()
			if d.Add(resp.Choices[0].Message.Content) {
				return provider.ChatResponse{}, &repetitionError{text: d.Repeated()}
			}
			if r := resp.Choices[0].Message.Reasoning; r != "" && loop.OnReasoning != nil {
				loop.OnReasoning(turn, r)
			}
			if text := resp.Choices[0].Message.Content; text != "" {
				answer, thought := splitThinking(text)
				if thought != "" && loop.OnReasoning != nil {
					loop.OnReasoning(turn, thought)
				}
				if answer != "" && loop.OnDelta != nil {
					loop.OnDelta(turn, answer)
				}
			}
		}
	}
	if loopErr != nil {
		return provider.ChatResponse{}, loopErr
	}
	return resp, err
}

// toolCatalogue lists the tools this turn may actually call.
//
// Dialect B was told the syntax and never the vocabulary. The preamble says
// "you act only through the tools you are given" and then gave none, so a
// text-protocol duckling had to guess names — and guessed from the only name
// in front of it, the fs_write in the @payload example. A reviewer, whose
// ceiling is read-only, was measured asking for fs_write for exactly that
// reason.
//
// Native tool calling never had this problem: the request carries the schemas.
func toolCatalogue(turn *Turn, ectx *tools.ExecContext) string {
	if len(turn.Toolbelt) == 0 {
		// Said out loud. A turn with no tools that is not told so will spend
		// its budget trying to call one.
		return "\n\nYou have no tools for this turn. Answer from what you have been given."
	}
	registry := ectx.Registry
	var b strings.Builder
	b.WriteString("\n\n## Your tools\n\nThese are the only tools you may call. " +
		"Anything else is refused.\n\n")
	for _, name := range turn.Toolbelt {
		if registry == nil {
			fmt.Fprintf(&b, "- `%s`\n", name)
			continue
		}
		t, err := registry.Get(name)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", name, t.Description())
		if schema, err := json.Marshal(t.Schema()); err == nil {
			fmt.Fprintf(&b, "  Arguments: `%s`\n", schema)
		}
	}
	return b.String()
}

// substantiveAnswer distinguishes an answer from protocol debris. A local
// implementer spent a full turn constructing a patch in hidden reasoning and
// returned one bare closing fence; accepting it advanced an empty diff to the
// reviewer. Fences surrounding actual content remain valid.
func substantiveAnswer(text string) bool {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		fenceOnly := len(fields) == 1 && (strings.HasPrefix(fields[0], "```") || strings.HasPrefix(fields[0], "~~~"))
		if line == "" || fenceOnly {
			continue
		}
		return true
	}
	return false
}

// BuildMessages builds the message list for a turn.
func BuildMessages(turn *Turn, ectx *tools.ExecContext, useNative bool) []provider.Message {
	var messages []provider.Message

	// 1. System: preamble + role prompt + gate description
	preamble := `You are a duckling in ducklab, a multi-model software development harness.

Ground rules, which you cannot change:
- ducklab, not you, runs git, runs the tests, and decides whether work passed.
  Do not attempt to commit, branch, merge, or push. Do not claim work is
  verified; you will be told the verification result.
- You act only through the tools you are given. If a tool you want is absent,
  say so plainly instead of pretending or improvising a shell workaround.
- Paths are relative to the project root. You cannot read or write outside it.
- Copy file text EXACTLY when editing: the line numbers fs_read shows are not
  part of the file, and whitespace must match byte-for-byte.
- Tool errors teach: each one states what was wrong and what to do instead.
  Read the error and change your call — resending the same call cannot help.
- If you are uncertain, say what you are uncertain about. A stated unknown is
  more useful here than a confident guess, because another model will read this.
- Be terse. Prose is not the deliverable.`

	rolePrompt := getRolePrompt(turn.Role)
	if turn.Contract == "verdict:native" && turn.Role == config.RoleReviewer {
		rolePrompt += `

For a native-code diff, your JSON object MUST also contain:
"native_checks":{"completion":"concrete function/path evidence","resources":"concrete allocation/handle evidence","threads":"concrete ownership/join/unref/blocking evidence","representation":"concrete masks/width/byte-order/stride/alpha evidence","cleanup":"concrete null/error-path evidence"}
Each value names what you inspected in the final code. Bare words such as "ok", "pass", "verified", "none", or "n/a" are invalid. Findings still go in findings; native_checks records the sweep that supports either verdict.`
	}
	if turn.Persona == "critic" && turn.Role == config.RoleReviewer {
		rolePrompt = criticPrompt
	}
	if turn.Persona == "consultant" {
		rolePrompt = consultantPrompt
	}
	if turn.Persona == "plan_manifest" && turn.Role == config.RoleArchitect {
		rolePrompt = planManifestPrompt
	}
	gateDesc := gateDescFor(turn)

	system := preamble + "\n\n" + rolePrompt + "\n\n" + gateDesc
	if ectx.HarnessContext != "" && (turn.Role == config.RoleImplementer || turn.Role == config.RoleReviewer || turn.Role == config.RoleAdvisor) {
		system += "\n\n" + ectx.HarnessContext
	}
	// Dialect B: append fenced text protocol instructions
	if !useNative {
		system += `

## How to use tools

When you want to use a tool, end your message with exactly one fenced block
tagged ` + "```ducklab" + ` containing a single JSON object:

` + "```ducklab" + `
{"tool": "fs_read", "args": {"path": "src/main.go"}}
` + "```" + `

Rules:
- One tool call per message. Nothing after the closing fence.
- To pass a large or multi-line string (like file content), write the value as
  "@payload:N", then add a fenced block that OPENS with ` + "```payload:N" + ` and
  CLOSES with ` + "```payload:N:end" + ` on its own line — an id-tagged terminator,
  NOT a bare fence. That closer is what lets the payload safely contain code
  fences, Markdown, or ` + "```" + ` sequences of its own:

` + "```ducklab" + `
{"tool": "fs_write", "args": {"path": "src/main.go", "content": "@payload:1"}}
` + "```" + `
` + "```payload:1" + `
package main

func main() {}
` + "```payload:1:end" + `

- When you are finished and have no tool to call, reply with your answer and no
  ` + "```ducklab" + ` block at all.`
		system += toolCatalogue(turn, ectx)
	}
	messages = append(messages, provider.Message{Role: "system", Content: system})

	// 2. System: project memory (capped at 8 KB)
	projectMemory := readProjectMemory(ectx.ProjectRoot)
	if projectMemory != "" {
		if len(projectMemory) > 8192 {
			projectMemory = projectMemory[:8192]
		}
		messages = append(messages, provider.Message{Role: "system", Content: projectMemory})
	}

	// 3. User: stage context (artifacts)
	// This is filled in by the stage runner

	// 4. User: the turn's rendered task prompt
	messages = append(messages, provider.Message{Role: "user", Content: turn.Prompt, Images: turn.Images})

	// 5. User: prior turns (rendered)
	// This is filled in by the conversation engine

	return messages
}

// gateDescFor tells the seat what ducklab does after its turn. A document
// turn has no test gate — ducklab checks the document's structure — and
// telling it "tests will run" sent a reviewer to call verify_run twice on a
// requirements draft ("no command configured", Neocapture 2026-08-29).
func gateDescFor(turn *Turn) string {
	if turn != nil && (strings.HasPrefix(turn.Contract, "markdown_sections:") || turn.Persona == "critic" ||
		turn.Role == config.RoleArchitect || turn.Role == config.RoleScribe || turn.Role == config.RoleTriager) {
		return "This is a document turn: no tests run after it. ducklab checks the " +
			"document's structure and the person decides at the gate. verify_run has nothing to run here."
	}
	return "The verification gate will run tests after you finish."
}

// firstChars and lastChars bound a string for the record without cutting
// inside a rune.
func firstChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// getRolePrompt returns the system prompt for a role (04 §6).
//
// The prompt is one third of what a role IS; the other two are its toolbelt
// ceiling (tools.RoleToolbelt) and its output contract. All three must agree:
// a reviewer told to judge correctness, given read-only tools, and parsed with
// the verdict contract.
func getRolePrompt(role config.Role) string {
	switch role {
	case config.RoleImplementer:
		return implementerPrompt
	case config.RoleReviewer:
		return reviewerPrompt
	case config.RoleJudge:
		return judgePrompt
	case config.RoleArchitect:
		return architectPrompt
	case config.RoleScribe:
		return scribePrompt
	case config.RoleTriager:
		return triagerPrompt
	case config.RoleAdvisor:
		return advisorPrompt
	default:
		return "You are a duckling in ducklab."
	}
}

const implementerPrompt = `You are the implementer. You change the code so the task is done and the
verification command passes.

Method:
1. Read before you write. Use fs_read on every file you intend to change.
2. Make the smallest change that satisfies the task.
3. Use fs_patch for edits to existing files and fs_write only for new files or
   a full rewrite you can justify.
4. Run verify_run yourself before you finish. If it is red, keep working.
5. If the same tool keeps failing on you, or the gate stays red after several
   different attempts, call ask_advisor with what you tried and what you are
   stuck on. The advisor sees your situation and answers inline; the run does
   not pause. Consulting once beats twenty more failed calls.
6. When you finish, reply with a 3-line summary: what changed, why, and what you
   did not do.

If the task underdetermines a decision a user would notice — a boundary (where
does a "week" start?), a format, an external contract — do not guess and do not
spend turns deliberating: call ask_human once, with concrete options. Ask for
the needed outcome or decision, never approval to run a shell command: approval
cannot change shell policy. Decisions the task does determine, and internals
nobody outside would notice, are yours — never ask about those.

Do not: reformat untouched code, rename things not named in the task, add
dependencies without saying so in your summary, or claim tests pass without
having run verify_run.`

const advisorPrompt = `You are the advisor — the rubber duck. An implementer working alongside you is
allowed to bring you what it is stuck on: repeated tool failures, a gate that
stays red, a fight it cannot win. You listen to the whole story, then answer as
a senior colleague would: concretely, briefly, and about the NEXT move.

You are not the reviewer. You do not grade the work and you never see the diff
as a judge would; you see the implementer's reasoning and its trace, which the
reviewer must never read. Use that. Name the tool to use instead, the file to
read first, the assumption to drop. Cite the project's own documents when they
decide the matter.

You may read files (fs_read, fs_search, artifact_read) to ground your advice.
Do not attempt to do the work yourself. When a consult asks for a JSON answer,
reply with exactly that JSON object and nothing else.`

const reviewerPrompt = `You are the reviewer. You did not write this code and you are not here to be
agreeable. The tests have already been run; their result is given to you and is
not yours to dispute.

Judge only: correctness against the task, obvious defects, security, and whether
the change does something it was not asked to do. Style is not a finding unless
the project's conventions are written down and violated.

Round discipline — a review that converges is worth more than one that is right
in pieces:
1. Before any finding, state the one or two INVARIANTS this change must hold
   (what the task really requires, in one sentence each: "the published ref is
   the ref acceptance advances", "every accept path goes through the policy").
   Every finding cites the invariant or task criterion it violates.
2. Sweep the WHOLE contract every round, not the delta. A defect that was
   visible last round and that you did not name is your defect, not the
   implementer's; a second round is not the time to discover what round one
   could have seen.
3. When the same rule is broken in several places, make ONE class-level
   finding — file "*", the invariant named — not one local symptom per file.
   The implementer fixes the class; you do not dribble it out over rounds.
4. In a later round, first re-verify each finding you made, then re-sweep. If
   a new finding contradicts a fix you prescribed earlier, say so in the fix
   ("this replaces my round-1 guidance to …") and give the corrected rule. The
   implementer followed you; do not review it against the opposite rule
   without saying you changed it.

Reply with one JSON object:
{"verdict":"approve"|"request-changes",
 "findings":[{"severity":"critical"|"major"|"minor","file":"path","line":N,
              "issue":"one sentence","fix":"one sentence",
              "invariant":"the rule this violates (optional on anchored findings)"},
             {"severity":"major","file":"*","invariant":"the rule, in one sentence",
              "issue":"where and how it is broken, as a class","fix":"one sentence"}]}

If the gate result you were given is red, "approve" is not available to you.
An empty findings list with "approve" is a legitimate answer.

If the diff is empty because the work the task asks for is already in the tree,
the task is satisfied and "approve" with an empty findings list is the correct
verdict. An empty diff is not itself an actionable defect; run history records
that the turn delivered no change.`

// consultantPrompt frames a chat turn: investigate and advise, change
// nothing. The closing duty matters most — the person acts with the buttons
// they already have, so the advice must end in their menu's terms.
const consultantPrompt = `You are a consultant in a conversation with the human about one subject — a
bug, a task, or a lifecycle document section — whose dossier and history you have been given. Investigate the
code and the record, then answer plainly.

You do not touch the code or configuration. You diagnose, explain what actually happened, and
advise. When the dossier includes Configuration findings, treat them as a priority-ordered
read-only diagnosis: explain each finding's reason and its consequence before recommending
it. You may draft a configuration amendment only as a clearly labelled proposal containing
key, old, new, and why. The why is prose for the scribe seat. Proposals are data, never an
instruction to apply a change: only the human can apply one through the desktop control.
One act is yours to perform, and only when the human explicitly asks
for it in their message: filing a bug with bug_file. Never file one on your
own initiative — draft the report in your reply and let the human decide.
When asked to file, check bug_read first for an existing bug covering the
same problem, then file and report the new bug's id back.

End every reply with a short "Suggested next step:" line choosing from the
human's real options: reopen the bug, file a new bug (say what its title
should be — or, if the human already told you to, file it and name the id),
relaunch the task with a note (say what the note should say), propose a focused
document change, mark it verified, or keep investigating (say what you would look at next).`

// criticPrompt replaces the code-review framing for a document council's
// critique turn. The code framing told the reviewer to examine "the diff" and
// "the tests" — so it spent half its turns calling git_diff (empty by design:
// a proposal never touches the tree before acceptance), artifact_read (the
// OLD approved document) and fs_read (no such file), and its tools truthfully
// corroborated the wrong story: "there is no draft anywhere". It kept the
// opening words "You are the reviewer" on purpose — the e2e fake provider
// recognises reviewer turns by them.
const criticPrompt = `You are the reviewer on a document council. Another model has drafted or
revised a project document, and you are not here to be agreeable.

The draft is IN this conversation, under "The draft under review". It exists
nowhere else: it has not been written to the tree or the artifact store, and
will not be unless a person accepts it. An empty git_diff and an artifact_read
that returns the previous document are therefore the expected state, not a
finding. Spend your turns reading the draft, not searching for it.

Judge only: does the draft do what the brief asks, does it keep every approved
section it was told to keep, are ids, priorities and cross-references coherent,
and is anything missing or invented. Style is not a finding.

Reply with one JSON object:
{"verdict":"approve"|"request-changes",
 "findings":[{"severity":"critical"|"major"|"minor","file":"path","line":N,
              "issue":"one sentence","fix":"one sentence"}]}

An empty findings list with "approve" is a legitimate answer.`

const architectPrompt = `You are the architect. You turn intent into a written artifact that another
model, with no memory of this conversation, can act on.

How the artifact is delivered: your final reply IS the document. ducklab
captures your reply and writes the file itself. There is no write tool, by
design — do not ask for one, do not ask how to save, do not describe the
document instead of writing it. When you have read what you need (often
nothing, on a new project), reply with the complete document and stop.

Rules:
- Every section starts with an H2 line: "## <ID> — <short title>".
- IDs are assigned by ducklab; reuse the IDs you are given and only allocate new
  ones from the next-free number you are told.
- An ID is PREFIX-NNN, three digits, nothing else: REQ-004, SPEC-012, T-031.
  Never sub-number (REQ-003.1, REQ-003-1, REQ-003a): ducklab does not see a
  sub-numbered id as a section, its traceability is lost, and the next seat
  cannot find it. To group related items, give each its own H2 id and say
  in the body what it belongs with, or use plain bullets inside one section.
- Prefer fewer, sharper items over exhaustive lists.
- State what is OUT of scope as explicitly as what is in.
- Where you had to assume something, add "**Assumption:**" and say it.
- Never write implementation code in this artifact.`

const planManifestPrompt = `You are the plan topology architect. Before anyone writes a long plan,
produce the compact dependency manifest that constrains it.

Reply with exactly one JSON object:
{"milestones":[{"id":"M-01","title":"short title","tasks":[
 {"id":"T-001","title":"short action","implements":["SPEC-001"],
  "produces":["file:path/or/capability"],"consumes":[],
  "verification":"executable command"}]}]}

Rules:
- Each task belongs to exactly one milestone and each produced artifact has one producer.
- Use exact file, directory, build-target, or capability names.
- A consumer names the producer's artifact byte-for-byte; ducklab derives Depends on.
- Keep tasks small: at most three top-level deliverables when rendered.
- Prefer 5–8 tasks and keep the total at 10 or fewer unless the specification makes that impossible.
- This is topology only. No prose, markdown, Owns lanes, or implementation code.
- The next architect turn receives this validated manifest and renders the full plan.`

const scribePrompt = `You are the scribe. You write the release notes and changelog entries from the
list of accepted work you are given.

Your final reply IS the document: ducklab captures it and writes the file.
There is no write tool, by design — do not ask for one.

Write for the user of the software, not for the developer. One bullet per
user-visible change, in plain language, no ticket numbers in the text. Omit
internal refactors entirely unless they change behaviour. If the list contains
nothing user-visible, say exactly: "No user-visible changes."`

const judgePrompt = `You are the judge. You are given several candidate solutions labelled A, B, …
and, for each, the result of running the project's verification command. You do
not know who wrote them and you must not ask.

Rules that bind you:
- A candidate whose gate is green beats any candidate whose gate is red,
  regardless of how the code reads.
- If exactly one candidate is green, choose it. Do not look for reasons to
  prefer a red one.
- If several are green, choose the one that changes least while satisfying the
  task.
- If all are red, answer "none" and say in one sentence what they all got wrong.
- You may not rewrite, improve, or merge candidates. Choose or refuse.

Reply with one JSON object: {"choice":"A"|"B"|…|"none","reason":"one sentence"}`

// readProjectMemory reads .ducklab/docs/project.md.
func readProjectMemory(root string) string {
	// Simplified; real implementation reads the file
	return ""
}

// applyThinkingSuppression asks a provider not to spend the budget on hidden
// reasoning.
//
// It deliberately does NOT add "</think>" as a stop sequence, which is what
// this used to do and which caused the exact failure it was meant to prevent.
// A server that separates reasoning from content (llama.cpp with a Qwen3
// model) puts the think block in reasoning_content and the answer in content;
// stopping generation at </think> ends the request precisely when the answer
// was about to start, so content comes back EMPTY with hundreds of tokens
// spent. Measured against a live endpoint: with the stop, content ""; without
// it, the requested JSON.
//
// Suppression is therefore only ever a request. What makes this safe is
// stripThinking below, which removes an inline think block after the fact
// without truncating anything.
// ApplyThinkingSuppression is exported for the service's one-shot calls
// (advisor drafts, reference digestion): a one-shot that skips it hands a
// disable_thinking seat its whole token cap to reason in, and the visible
// answer comes back empty (B-123).
func ApplyThinkingSuppression(req *provider.ChatRequest, caps provider.Capabilities) {
	applyThinkingSuppression(req, caps)
}

func applyThinkingSuppression(req *provider.ChatRequest, caps provider.Capabilities) {
	if req.Extra == nil {
		req.Extra = make(map[string]interface{})
	}
	// vLLM and the Qwen family.
	req.Extra["chat_template_kwargs"] = map[string]interface{}{
		"enable_thinking": false,
	}
	// OpenRouter.
	req.Extra["reasoning"] = map[string]interface{}{
		"exclude": true,
	}
}

var thinkBlockRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// stripThinking removes an inline reasoning block from a response.
//
// Servers differ: some return the block inside content, some put it in a
// separate field and leave content clean. Stripping post-hoc handles both, and
// unlike a stop sequence it can never truncate the answer.
func stripThinking(content string) string {
	cleaned := thinkBlockRe.ReplaceAllString(content, "")
	// An unterminated block means generation was cut mid-thought; everything
	// from the marker on is reasoning, not answer.
	if i := strings.Index(cleaned, "<think>"); i >= 0 {
		cleaned = cleaned[:i]
	}
	// A closing tag with no opening one: the provider suppressed the marker
	// that starts the block but not the one that ends it, so the response
	// arrives as reasoning, then </think>, then the real answer.
	//
	// Seen against a live endpoint: a message began "Tests pass.\n</think>\n\n
	// **Changed:** add.go …", and the model's private reasoning was recorded
	// verbatim as its answer.
	//
	// The tag must be alone on its line. Prose that merely mentions the tag —
	// documentation about this very parser, say — keeps it, because a rule
	// that cut at any occurrence would silently delete the first half of a
	// legitimate answer. That is not hypothetical: it is what the first
	// version of this did, and a test caught it.
	if rest, ok := afterDanglingClose(cleaned); ok {
		cleaned = rest
	}
	return strings.TrimSpace(cleaned)
}

func afterDanglingClose(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "</think>" {
			return strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", false
}

// DefaultMaxOutputTokens caps a single completion when a duckling declares no
// limit of its own.
//
// I3 says nothing is unbounded, and a request with no max_tokens is exactly
// that: a model that starts looping generates until the context window fills,
// which on a fast endpoint still took over ten minutes of wall clock and then
// multiplied by the transient-retry policy. The cap is high enough for a long
// artifact and low enough that a loop is caught in seconds, not hours.
const DefaultMaxOutputTokens = 8192

// outputCap returns the per-call token limit, never nil.
func outputCap(declared *int) *int {
	return outputCapForContract(declared, "")
}

func outputCapForContract(declared *int, contract string) *int {
	if contract == "json:triage" {
		n := 2048
		if declared != nil && *declared > 0 && *declared < n {
			n = *declared
		}
		return &n
	}
	if declared != nil && *declared > 0 {
		return declared
	}
	n := DefaultMaxOutputTokens
	return &n
}

// ErrNoAnswer reports a turn that spent every one of its turns calling tools and
// never produced the answer it was asked for.
//
// Worth its own error: the contract failure it used to become said "empty
// response", which describes the parser's input rather than what the model did,
// and points at the wrong fix — a repair prompt cannot help a model that has run
// out of room to reply in.
var ErrNoAnswer = errors.New("turn ended without an answer")

// ErrThoughtOnly reports that a model spent its whole budget on hidden
// reasoning and returned no answer.
//
// Worth its own error: "empty response" sends the reader looking for a
// transport fault, when the cause is a token budget consumed before the answer
// began and the fix is to raise max_tokens or turn thinking off at the server.
var ErrThoughtOnly = errors.New("model returned only hidden reasoning")

// TextToolCall is a parsed text-protocol tool call.
type TextToolCall struct {
	Name       string
	Args       json.RawMessage
	ParseError string
}

var ducklabBlockRe = regexp.MustCompile("(?s)```ducklab\\s*\\n(.*?)\\n```")
var ducklabLooseBlockRe = regexp.MustCompile("(?s)```ducklab\\s*\\n(.*?)```")

// A payload block opens with ```payload:N and closes with an explicit,
// id-bearing terminator ```payload:N:end on its own line — NOT a bare ```.
//
// A bare closer let payload CONTENT terminate the block early: any value that
// itself contained a ``` fence — a Markdown code block, or ducklab's own
// protocol text when editing agent.go — was truncated at the first fence, and
// a ```ducklab line inside that content spawned a phantom second envelope that
// made parseTextToolCall drop the whole call. Editing any file that mentions
// the protocol was therefore impossible. The id in the terminator makes an
// accidental collision with file content effectively impossible.
//
// RE2 has no backreferences, so this generic strip lets the open/close ids
// differ; per-id extraction in parseTextToolCall pins them to the same value.
var payloadBlockRe = regexp.MustCompile("(?s)```payload:\\d+\\s*\\n.*?\\n```payload:\\d+:end")

// parseTextToolCall parses a Dialect B tool call from text.
func parseTextToolCall(text string) (*TextToolCall, string) {
	// Locate the envelope on a copy with payload blocks removed. Payload CONTENT
	// can legitimately contain a ```ducklab line or a bare ``` fence (editing
	// agent.go, or any Markdown), and scanning the raw text let that content
	// masquerade as a second envelope (dropped) or truncate the first. The
	// values are still read from the ORIGINAL text, by id, below.
	envelope := payloadBlockRe.ReplaceAllString(text, "")

	blockRe := ducklabBlockRe
	matches := blockRe.FindAllStringSubmatch(envelope, -1)
	if len(matches) == 0 {
		// Small models sometimes leave the JSON string unterminated, so the
		// final fence follows a literal `\\n` escape instead of a real newline.
		// It is still visibly a tool envelope. Capture it as a malformed call and
		// return repair feedback instead of treating the whole turn as finished.
		blockRe = ducklabLooseBlockRe
		matches = blockRe.FindAllStringSubmatch(envelope, -1)
	}
	if len(matches) == 0 {
		// A small text-protocol implementer wrote a perfectly shaped tool call
		// under ```duckdb. Treating that typo as its final answer advanced an
		// empty diff to review. We do not guess and execute it; we return a tool
		// result that teaches the exact envelope and lets the same turn recover.
		if raw, err := extractJSONObject(envelope); err == nil {
			var candidate struct {
				Tool string          `json:"tool"`
				Args json.RawMessage `json:"args"`
			}
			if json.Unmarshal([]byte(raw), &candidate) == nil && strings.TrimSpace(candidate.Tool) != "" && len(bytes.TrimSpace(candidate.Args)) > 0 {
				return &TextToolCall{
					Name:       "ducklab_protocol",
					Args:       json.RawMessage(`{}`),
					ParseError: fmt.Sprintf("tool call for %q used a missing or incorrect fence tag", candidate.Tool),
				}, ""
			}
		}
	}
	if len(matches) != 1 {
		// Zero envelopes: not a tool call. More than one: ambiguous — refuse
		// rather than guess which the model meant.
		return nil, text
	}

	block := matches[0][1]
	var call struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(block), &call); err != nil {
		return &TextToolCall{Name: "ducklab_protocol", Args: json.RawMessage(`{}`), ParseError: fmt.Sprintf("malformed ducklab tool call JSON: %v", err)}, ""
	}
	if strings.TrimSpace(call.Tool) == "" {
		return &TextToolCall{Name: "ducklab_protocol", Args: json.RawMessage(`{}`), ParseError: "malformed ducklab tool call: missing non-empty tool name"}, ""
	}
	// Zero-argument tools are a frequent small-model edge: omitting `args`
	// carries the same meaning as an empty object. The registry remains the
	// authority for tools that actually require parameters.
	if len(bytes.TrimSpace(call.Args)) == 0 || string(bytes.TrimSpace(call.Args)) == "null" {
		call.Args = json.RawMessage(`{}`)
	}

	// Handle payload substitution
	var args map[string]interface{}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return &TextToolCall{Name: call.Tool, ParseError: fmt.Sprintf("malformed arguments for %s: %v", call.Tool, err)}, ""
	}
	for k, v := range args {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, "@payload:") {
			continue
		}
		payloadID := strings.TrimPrefix(s, "@payload:")
		// Open and close both carry the id, so file content with a bare ``` — or
		// even another payload's fence — cannot terminate this block early.
		id := regexp.QuoteMeta(payloadID)
		payloadRe := regexp.MustCompile("(?s)```payload:" + id + "\\s*\\n(.*?)\\n```payload:" + id + ":end")
		payloadMatch := payloadRe.FindStringSubmatch(text)
		if len(payloadMatch) < 2 {
			return &TextToolCall{
				Name:       "ducklab_protocol",
				Args:       json.RawMessage(`{}`),
				ParseError: fmt.Sprintf("tool %s references @payload:%s but the matching ```payload:%s ... ```payload:%s:end block is missing", call.Tool, payloadID, payloadID, payloadID),
			}, ""
		}
		args[k] = payloadMatch[1]
	}
	newArgs, err := json.Marshal(args)
	if err != nil {
		return nil, text
	}

	// Strip the payload blocks first, then the envelope, so payload content that
	// contains a ```ducklab fence isn't left behind as stray prose.
	remaining := payloadBlockRe.ReplaceAllString(text, "")
	remaining = blockRe.ReplaceAllString(remaining, "")
	remaining = strings.TrimSpace(remaining)

	return &TextToolCall{Name: call.Tool, Args: newArgs}, remaining
}

// executeToolCall executes a native tool call.
func executeToolCall(ctx context.Context, loop *Loop, ectx *tools.ExecContext, tc provider.ToolCall, turn *Turn) (*tools.Result, error) {
	// Check tool is in toolbelt
	allowed := false
	for _, name := range turn.Toolbelt {
		if name == tc.Function.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ErrorResult("tool %q not in toolbelt", tc.Function.Name), nil
	}
	if loop.OnToolStart != nil {
		loop.OnToolStart(turn, string(loop.Duckling.ID), tc.Function.Name, json.RawMessage(tc.Function.Arguments))
	}
	result, err := loop.Registry.Execute(ctx, ectx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		// A pause is not a tool error: turning it into an error result would
		// feed "human input needed" back to the model as if it were an answer.
		if errors.Is(err, tools.ErrHumanNeeded) {
			return nil, err
		}
		return tools.ErrorResult("execute: %v", err), nil
	}
	return result, nil
}

// executeTextToolCall executes a text-protocol tool call.
func executeTextToolCall(ctx context.Context, loop *Loop, ectx *tools.ExecContext, tc *TextToolCall, turn *Turn) (*tools.Result, error) {
	if tc.ParseError != "" {
		return tools.ErrorResult("%s; return exactly one valid ```ducklab JSON envelope and try again", tc.ParseError), nil
	}
	allowed := false
	for _, name := range turn.Toolbelt {
		if name == tc.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ErrorResult("tool %q not in toolbelt", tc.Name), nil
	}
	if loop.OnToolStart != nil {
		loop.OnToolStart(turn, string(loop.Duckling.ID), tc.Name, tc.Args)
	}
	result, err := loop.Registry.Execute(ctx, ectx, tc.Name, tc.Args)
	if err != nil {
		if errors.Is(err, tools.ErrHumanNeeded) {
			return nil, err
		}
		return tools.ErrorResult("execute: %v", err), nil
	}
	return result, nil
}

// repairContract attempts to repair a contract violation.
func repairContract(ctx context.Context, loop *Loop, turn *Turn, msgs []provider.Message, text string, parseErr error) (string, interface{}, int, error) {
	repairs := loop.RepairAttempts
	if repairs <= 0 {
		repairs = 2
	}

	// The repair conversation KEEPS the original exchange and appends the bad
	// answer plus the correction. Sending only the correction, as this used to,
	// asks a model to fix a response it can no longer see — which is why weak
	// models tended to produce a second, differently-malformed answer.
	base := append([]provider.Message{}, msgs...)

	attempts := 0
	for i := 0; i < repairs; i++ {
		attempts++

		conv := append([]provider.Message{}, base...)
		conv = append(conv,
			provider.Message{Role: "assistant", Content: text},
			provider.Message{Role: "user", Content: repairInstruction(turn.Contract, parseErr)},
		)

		req := provider.ChatRequest{
			Model:    loop.Duckling.Model,
			Messages: conv,
		}
		applySampling(&req, loop.Duckling, turn.Contract)

		resp, err := loop.Provider.Chat(ctx, req)
		if err != nil {
			// A transport failure is not the model failing the contract.
			// Burning a repair attempt on it would spend the budget the
			// model needs to actually correct itself.
			return "", nil, attempts, fmt.Errorf("repair attempt %d: %w", attempts, err)
		}
		if len(resp.Choices) == 0 {
			parseErr = fmt.Errorf("empty response")
			text = ""
			continue
		}
		newText := resp.Choices[0].Message.Content
		if val, perr := ParseContract(turn.Contract, newText); perr == nil {
			return newText, val, attempts, nil
		} else {
			parseErr = perr
			text = newText
		}
	}
	return "", nil, attempts, fmt.Errorf("%w: after %d repair attempts: %v", ErrContract, attempts, parseErr)
}

func repairInstruction(contract string, parseErr error) string {
	return fmt.Sprintf(`Your reply did not satisfy the required output format.

Contract: %s
What was wrong: %v

Reply again with ONLY the required format. No prose before or after it.`, contract, parseErr)
}

// applySampling copies the duckling's sampling parameters onto a request, so a
// repair uses the same settings as the turn it is repairing.
func applySampling(req *provider.ChatRequest, d *DucklingConfig, contract string) {
	if d == nil {
		return
	}
	if d.Params.Temperature != nil {
		req.Temperature = d.Params.Temperature
	}
	req.MaxTokens = outputCapForContract(d.Params.MaxTokens, contract)
}

// triagerPrompt is 04 §6.6, verbatim.
//
// The instruction to answer null when unsure is the important line: a missed
// duplicate costs a second look, a wrongly closed one loses a real report.
const triagerPrompt = `You are the triager. Classify one bug report.

Reply with one JSON object:
{"severity":"critical"|"high"|"normal"|"low",
 "duplicate_of": "B-012" | null,
 "component": "short name or empty",
 "suspected_files": ["path", …],
 "reproducible": true|false|null,
 "task_title": "imperative one-liner for the fix task, or empty if not actionable",
 "test_strategy": "test-first" | "build-only",
 "test_reason": "one line",
 "deliverables": ["2-5 concrete, verifiable outcomes for the fix task", …],
 "proposal": [{"title":"imperative portion", "acceptance":["1-2 verifiable criteria"], "owns":["disjoint/path"]}],
 "reason": "one sentence"}

Base "duplicate_of" only on the open bugs you were given. If you are unsure,
answer null; a missed duplicate is cheaper than a wrongly closed bug.

"test_strategy" is your judgment on the HONEST verification for the fix:
- "test-first" when the bug is reproducible as an automated test (behaviour,
  crash, wrong data). Then "test_reason" sketches the reproduction the
  test-writer starts from, e.g. "POST /profile with empty name expects 422".
- "build-only" when the honest check is eyes (visual, cosmetic, layout,
  config): a forced test degenerates into grepping the source, which pins the
  implementation and not the bug. Then "test_reason" says why in one line.
You recommend; a person decides.

"deliverables" become the fix task's numbered work contract: each one a
concrete outcome a reviewer can check against the diff ("the brake resets
after a successful fs_read of the braked path", "a test asserts the reset"),
not steps and not vague goals. 2-5 of them; empty only if not actionable.

When the bug spans multiple concerns, you may add "proposal": portions with a
short title, no more than two acceptance criteria, and an Owns lane per portion.
The proposal is advice only; it never creates tasks until a person promotes it.`

// usageMap is what goes to llm.jsonl for one call.
//
// reasoning_tokens is recorded when the endpoint reports it, and it is a share
// of completion_tokens rather than an addition to them — summing the two would
// double-count. "The run spent 400k tokens" and "the run spent 400k tokens, 380k
// of them thinking" call for different actions, and only the second explains a
// budget that ran out with nothing written.
func usageMap(u provider.Usage) map[string]interface{} {
	out := map[string]interface{}{
		"prompt_tokens":     u.PromptTokens,
		"completion_tokens": u.CompletionTokens,
	}
	if u.ReasoningTokens > 0 {
		out["reasoning_tokens"] = u.ReasoningTokens
	}
	return out
}

// requestMap is what goes to llm.jsonl for one call's request.
//
// Shared with the failure path: a call that died has to record the same thing a
// call that worked does, or the one entry anybody would want to read would be
// the one written differently.
func requestMap(req provider.ChatRequest) map[string]interface{} {
	out := map[string]interface{}{
		"model":    req.Model,
		"messages": req.Messages,
	}
	if len(req.Tools) > 0 {
		out["tools"] = req.Tools
	}
	return out
}
