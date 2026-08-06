// Package agent implements the agentic loop: provider.Chat/ChatStream,
// tool dispatch, contract parsing, and repair. The loop is bounded by
// MaxTurns and enforces every bound.
package agent

import (
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
}

// RunLogWriter is the interface for writing LLM call records.
type RunLogWriter interface {
	AppendLLM(call *LLMCallRecord) error
}

// LLMCallRecord is a record of an LLM call for logging.
type LLMCallRecord struct {
	Duckling     string
	Provider     string
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

	// Determine dialect
	useNative := loop.Duckling.Caps.NativeTools

	// Build messages
	messages := BuildMessages(turn, ectx, useNative)

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
		if useNative {
			req.Tools = nativeTools
		}
		if loop.Duckling.Params.Temperature != nil {
			req.Temperature = loop.Duckling.Params.Temperature
		}
		if loop.Duckling.Params.TopP != nil {
			req.TopP = loop.Duckling.Params.TopP
		}
		req.MaxTokens = outputCap(loop.Duckling.Params.MaxTokens)
		if len(loop.Duckling.Params.Stop) > 0 {
			req.Stop = loop.Duckling.Params.Stop
		}

		// Thinking suppression
		if loop.Duckling.Params.DisableThinking {
			applyThinkingSuppression(&req, loop.Duckling.Caps)
		}

		// Make the call
		start := time.Now()
		resp, err := chatMaybeStreaming(ctx, loop, turn, req)
		_ = time.Since(start) // latency recorded in llm.jsonl by the orchestrator

		if err != nil {
			if provider.IsTransient(err) {
				// Retry with backoff
				retryPolicy := provider.DefaultRetryPolicy()
				err = provider.Retry(ctx, retryPolicy, func() error {
					resp, err = loop.Provider.Chat(ctx, req)
					return err
				})
			}
			if err != nil {
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
						Attempt:      1,
						FinishReason: "error",
					})
				}
				return outcome, fmt.Errorf("provider chat: %w", err)
			}
		}

		// Record usage
		calc := provider.CostCalculator{
			InputPerMTok:  loop.Duckling.Cost.InputPerMTok,
			OutputPerMTok: loop.Duckling.Cost.OutputPerMTok,
		}
		cost := calc.Cost(resp.Usage)
		loop.Budget.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cost)
		outcome.TokensIn += resp.Usage.PromptTokens
		outcome.TokensOut += resp.Usage.CompletionTokens
		outcome.CostUSD += cost

		// Check finish reason
		if len(resp.Choices) == 0 {
			return outcome, fmt.Errorf("no response choices")
		}
		choice := resp.Choices[0]
		finishReason := choice.FinishReason

		// Separate an inline reasoning block before anything reads the content.
		// Doing it here means every downstream path — contract parsing, tool
		// extraction, the transcript — sees the answer and not the thinking.
		//
		// The thinking is kept rather than deleted: it was paid for, and the run
		// view has a place to show it. Discarding it here is why a model that
		// inlines its reasoning had an empty thinking section while its
		// deliberation filled the answer lane.
		if answer, thought := splitThinking(choice.Message.Content); thought != "" {
			choice.Message.Content = answer
			choice.Message.Reasoning = joinReasoning(choice.Message.Reasoning, thought)
		} else {
			choice.Message.Content = answer
		}

		// A response with tokens spent and nothing to show is a model that
		// thought until its budget ran out. Saying so beats "empty response",
		// which sends the reader hunting for a transport fault.
		if choice.Message.Content == "" && len(choice.Message.ToolCalls) == 0 &&
			resp.Usage.CompletionTokens > 0 {
			return outcome, fmt.Errorf("%w: %s spent %d tokens on hidden reasoning and returned no answer; "+
				"raise max_tokens for this duckling, or disable thinking at the endpoint",
				ErrThoughtOnly, loop.Duckling.ID, resp.Usage.CompletionTokens)
		}

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
				outcome.ToolCalls = append(outcome.ToolCalls, ToolCallRecord{
					Name:   tc.Function.Name,
					Args:   json.RawMessage(tc.Function.Arguments),
					Result: result,
					Digest: tools.Digest(json.RawMessage(tc.Function.Arguments)),
				})
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
				outcome.ToolCalls = append(outcome.ToolCalls, ToolCallRecord{
					Name:   toolCall.Name,
					Args:   toolCall.Args,
					Result: result,
					Digest: tools.Digest(toolCall.Args),
				})
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
	if len(outcome.ToolCalls) > 0 && strings.TrimSpace(outcome.Text) == "" {
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
		final.MaxTokens = outputCap(loop.Duckling.Params.MaxTokens)
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
	if len(outcome.ToolCalls) > 0 && strings.TrimSpace(outcome.Text) == "" {
		return outcome, fmt.Errorf(
			"%w: %s used all %d of its turns calling tools and never answered "+
				"(%d tool calls, no text), even when asked to conclude without them. "+
				"Raise the turn cap for this role, or the task needs narrowing",
			ErrNoAnswer, turn.Role, maxTurns, len(outcome.ToolCalls))
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

// chatMaybeStreaming streams when the caller asked for it and the provider can,
// and falls back to a plain call otherwise.
//
// Streaming is a DISPLAY concern only (01 §5.2): contract parsing, tool
// dispatch and logging always operate on the assembled final response, never
// on deltas. A dropped subscriber therefore cannot affect a run.
func chatMaybeStreaming(ctx context.Context, loop *Loop, turn *Turn, req provider.ChatRequest) (provider.ChatResponse, error) {
	if loop.OnDelta == nil && loop.OnReasoning == nil {
		return loop.Provider.Chat(ctx, req)
	}

	ch := make(chan provider.Delta, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Deltas used to go to the answer untouched, so a model that inlines its
		// thinking filled the transcript lane with deliberation and bare
		// `</think>` markers for the whole run. One splitter per call: a marker
		// can be split across two chunks.
		var split thinkSplitter
		emit := func(answer, reasoning string) {
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

	resp, err := loop.Provider.ChatStream(ctx, req, ch)
	close(ch)
	<-done

	if errors.Is(err, provider.ErrUnsupported) {
		// The endpoint cannot stream. Emit the assembled text as a single
		// delta so a watching client still sees output appear, rather than
		// silently showing nothing for the whole turn.
		resp, err = loop.Provider.Chat(ctx, req)
		if err == nil && len(resp.Choices) > 0 {
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
	}
	return b.String()
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
- If you are uncertain, say what you are uncertain about. A stated unknown is
  more useful here than a confident guess, because another model will read this.
- Be terse. Prose is not the deliverable.`

	rolePrompt := getRolePrompt(turn.Role)
	if turn.Persona == "critic" && turn.Role == config.RoleReviewer {
		rolePrompt = criticPrompt
	}
	gateDesc := "The verification gate will run tests after you finish."

	system := preamble + "\n\n" + rolePrompt + "\n\n" + gateDesc
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
  "@payload:N" and add a fenced block tagged ` + "```payload:N" + ` after the ducklab block:

` + "```ducklab" + `
{"tool": "fs_write", "args": {"path": "src/main.go", "content": "@payload:1"}}
` + "```" + `
` + "```payload:1" + `
package main

func main() {}
` + "```" + `

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
	messages = append(messages, provider.Message{Role: "user", Content: turn.Prompt})

	// 5. User: prior turns (rendered)
	// This is filled in by the conversation engine

	return messages
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
5. When you finish, reply with a 3-line summary: what changed, why, and what you
   did not do.

If the task underdetermines a decision a user would notice — a boundary (where
does a "week" start?), a format, an external contract — do not guess and do not
spend turns deliberating: call ask_human once, with concrete options. Decisions
the task does determine, and internals nobody outside would notice, are yours —
never ask about those.

Do not: reformat untouched code, rename things not named in the task, add
dependencies without saying so in your summary, or claim tests pass without
having run verify_run.`

const reviewerPrompt = `You are the reviewer. You did not write this code and you are not here to be
agreeable. The tests have already been run; their result is given to you and is
not yours to dispute.

Judge only: correctness against the task, obvious defects, security, and whether
the change does something it was not asked to do. Style is not a finding unless
the project's conventions are written down and violated.

Reply with one JSON object:
{"verdict":"approve"|"request-changes",
 "findings":[{"severity":"critical"|"major"|"minor","file":"path","line":N,
              "issue":"one sentence","fix":"one sentence"}]}

If the gate result you were given is red, "approve" is not available to you.
An empty findings list with "approve" is a legitimate answer.

If the diff is empty because the work the task asks for is already in the tree,
the task is satisfied and "approve" is the correct verdict. Say so, and record
what you noticed as a finding with severity "minor" — that this task delivered
nothing is worth a human knowing, but it is not a defect the implementer can
fix by writing code, and "request-changes" only asks it to try again against
the same empty diff.`

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

Rules:
- Every section starts with an H2 line: "## <ID> — <short title>".
- IDs are assigned by ducklab; reuse the IDs you are given and only allocate new
  ones from the next-free number you are told.
- Prefer fewer, sharper items over exhaustive lists.
- State what is OUT of scope as explicitly as what is in.
- Where you had to assume something, add "**Assumption:**" and say it.
- Never write implementation code in this artifact.`

const scribePrompt = `You are the scribe. You write the release notes and changelog entries from the
list of accepted work you are given.

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
	Name string
	Args json.RawMessage
}

var ducklabBlockRe = regexp.MustCompile("(?s)```ducklab\\s*\\n(.*?)\\n```")
var payloadBlockRe = regexp.MustCompile("(?s)```payload:(\\d+)\\s*\\n(.*?)\\n```")

// parseTextToolCall parses a Dialect B tool call from text.
func parseTextToolCall(text string) (*TextToolCall, string) {
	matches := ducklabBlockRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	if len(matches) > 1 {
		// Multiple ducklab blocks; error
		return nil, text
	}

	block := matches[0][1]
	var call struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(block), &call); err != nil {
		return nil, text
	}

	// Handle payload substitution
	var args map[string]interface{}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, text
	}
	for k, v := range args {
		if s, ok := v.(string); ok {
			if strings.HasPrefix(s, "@payload:") {
				payloadID := strings.TrimPrefix(s, "@payload:")
				payloadRe := regexp.MustCompile("(?s)```payload:" + payloadID + "\\s*\\n(.*?)\\n```")
				payloadMatch := payloadRe.FindStringSubmatch(text)
				if len(payloadMatch) < 2 {
					return nil, text
				}
				args[k] = payloadMatch[1]
			}
		}
	}
	newArgs, err := json.Marshal(args)
	if err != nil {
		return nil, text
	}

	// Remove the ducklab block and payload blocks from the text
	remaining := ducklabBlockRe.ReplaceAllString(text, "")
	remaining = payloadBlockRe.ReplaceAllString(remaining, "")
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
		applySampling(&req, loop.Duckling)

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
func applySampling(req *provider.ChatRequest, d *DucklingConfig) {
	if d == nil {
		return
	}
	if d.Params.Temperature != nil {
		req.Temperature = d.Params.Temperature
	}
	req.MaxTokens = outputCap(d.Params.MaxTokens)
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
 "reason": "one sentence"}

Base "duplicate_of" only on the open bugs you were given. If you are unsure,
answer null; a missed duplicate is cheaper than a wrongly closed bug.`

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
