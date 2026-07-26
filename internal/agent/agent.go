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
}

// Outcome is the result of a turn.
type Outcome struct {
	Text          string
	ToolCalls     []ToolCallRecord
	TokensIn      int
	TokensOut     int
	CostUSD       float64
	ContractError error
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
	Provider    provider.Provider
	Duckling    *DucklingConfig
	Registry    *tools.Registry
	Budget      *budget.Tracker
	MaxTurns    int
	RepairAttempts int
	// RunWriter, if set, receives LLM call records.
	RunWriter   RunLogWriter
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
		if msg, exceeded := loop.Budget.Check(); exceeded {
			return nil, fmt.Errorf("%w: %s", ErrBudgetExceeded, msg)
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
		if loop.Duckling.Params.MaxTokens != nil {
			req.MaxTokens = loop.Duckling.Params.MaxTokens
		}
		if len(loop.Duckling.Params.Stop) > 0 {
			req.Stop = loop.Duckling.Params.Stop
		}

		// Thinking suppression
		if loop.Duckling.Params.DisableThinking {
			applyThinkingSuppression(&req, loop.Duckling.Caps)
		}

		// Make the call
		start := time.Now()
		resp, err := loop.Provider.Chat(ctx, req)
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
				return nil, fmt.Errorf("provider chat: %w", err)
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
			return nil, fmt.Errorf("no response choices")
		}
		choice := resp.Choices[0]
		finishReason := choice.FinishReason

		// Log the LLM call
		if loop.RunWriter != nil {
			latencyMs := time.Since(start).Milliseconds()
			reqMap := map[string]interface{}{
				"model":    req.Model,
				"messages": req.Messages,
			}
			if len(req.Tools) > 0 {
				reqMap["tools"] = req.Tools
			}
			respMap := map[string]interface{}{
				"content":      choice.Message.Content,
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
				Usage:        map[string]interface{}{"prompt_tokens": resp.Usage.PromptTokens, "completion_tokens": resp.Usage.CompletionTokens},
				CostUSD:      cost,
				LatencyMs:    latencyMs,
				Attempt:      1,
				CostSource:   calc.CostSource(resp.Usage),
				FinishReason: finishReason,
			})
		}

		// Handle truncation
		if provider.IsLength(finishReason) {
			// Retry once with terse instruction
			conversation = append(conversation, provider.Message{
				Role:    "user",
				Content: "Be terse. Your previous response was truncated.",
			})
			resp2, err2 := loop.Provider.Chat(ctx, req)
			if err2 != nil || len(resp2.Choices) == 0 {
				return nil, ErrTruncated
			}
			choice = resp2.Choices[0]
			finishReason = choice.FinishReason
			if provider.IsLength(finishReason) {
				return nil, ErrTruncated
			}
		}

		// Handle content filter
		if finishReason == provider.FinishContentFilter {
			return nil, fmt.Errorf("content filter blocked response")
		}

		// Handle tool calls (native dialect)
		if useNative && provider.IsToolCalls(finishReason) {
			toolCalls := choice.Message.ToolCalls
			conversation = append(conversation, choice.Message)
			for _, tc := range toolCalls {
				result := executeToolCall(ctx, loop, ectx, tc, turn)
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
				result := executeTextToolCall(ctx, loop, ectx, toolCall, turn)
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

	// Parse contract
	if err := parseContract(turn.Contract, outcome.Text); err != nil {
		// Repair loop
		repaired, err := repairContract(ctx, loop, turn, outcome.Text, err)
		if err != nil {
			outcome.ContractError = err
			return outcome, ErrContract
		}
		outcome.Text = repaired
	}

	return outcome, nil
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

// getRolePrompt returns the prompt for a role.
func getRolePrompt(role config.Role) string {
	switch role {
	case config.RoleImplementer:
		return `You are the implementer. You change the code so the task is done and the
verification command passes.

Method:
1. Read before you write. Use fs_read on every file you intend to change.
2. Make the smallest change that satisfies the task.
3. Use fs_patch for edits to existing files and fs_write only for new files or
   a full rewrite you can justify.
4. Run verify_run yourself before you finish. If it is red, keep working.
5. When you finish, reply with a 3-line summary: what changed, why, and what you
   did not do.

Do not: reformat untouched code, rename things not named in the task, add
dependencies without saying so in your summary, or claim tests pass without
having run verify_run.`
	default:
		return "You are a duckling in ducklab."
	}
}

// readProjectMemory reads .ducklab/docs/project.md.
func readProjectMemory(root string) string {
	// Simplified; real implementation reads the file
	return ""
}

// applyThinkingSuppression applies thinking-token suppression.
func applyThinkingSuppression(req *provider.ChatRequest, caps provider.Capabilities) {
	// Step 1: vLLM/Qwen family
	if req.Extra == nil {
		req.Extra = make(map[string]interface{})
	}
	req.Extra["chat_template_kwargs"] = map[string]interface{}{
		"enable_thinking": false,
	}
	// Step 2: OpenRouter
	req.Extra["reasoning"] = map[string]interface{}{
		"exclude": true,
	}
	// Step 3: stop sequences (always)
	req.Stop = append(req.Stop, "</think>")
}

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
func executeToolCall(ctx context.Context, loop *Loop, ectx *tools.ExecContext, tc provider.ToolCall, turn *Turn) *tools.Result {
	// Check tool is in toolbelt
	allowed := false
	for _, name := range turn.Toolbelt {
		if name == tc.Function.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ErrorResult("tool %q not in toolbelt", tc.Function.Name)
	}
	result, err := loop.Registry.Execute(ctx, ectx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		return tools.ErrorResult("execute: %v", err)
	}
	return result
}

// executeTextToolCall executes a text-protocol tool call.
func executeTextToolCall(ctx context.Context, loop *Loop, ectx *tools.ExecContext, tc *TextToolCall, turn *Turn) *tools.Result {
	allowed := false
	for _, name := range turn.Toolbelt {
		if name == tc.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return tools.ErrorResult("tool %q not in toolbelt", tc.Name)
	}
	result, err := loop.Registry.Execute(ctx, ectx, tc.Name, tc.Args)
	if err != nil {
		return tools.ErrorResult("execute: %v", err)
	}
	return result
}

// parseContract parses the outcome text against the contract.
func parseContract(contract, text string) error {
	switch contract {
	case "freeform", "edits":
		return nil
	case "verdict":
		return parseVerdictContract(text)
	case "choice":
		return parseChoiceContract(text)
	default:
		if strings.HasPrefix(contract, "json:") {
			return parseJSONContract(text)
		}
		if strings.HasPrefix(contract, "markdown_sections:") {
			return parseMarkdownSectionsContract(contract, text)
		}
		return nil
	}
}

func parseVerdictContract(text string) error {
	// Strip fences
	text = stripFences(text)
	var v struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return fmt.Errorf("verdict contract: %w", err)
	}
	if v.Verdict != "approve" && v.Verdict != "request-changes" {
		return fmt.Errorf("verdict must be approve or request-changes")
	}
	return nil
}

func parseChoiceContract(text string) error {
	text = stripFences(text)
	var c struct {
		Choice string `json:"choice"`
	}
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return fmt.Errorf("choice contract: %w", err)
	}
	return nil
}

func parseJSONContract(text string) error {
	text = stripFences(text)
	var v interface{}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return fmt.Errorf("json contract: %w", err)
	}
	return nil
}

func parseMarkdownSectionsContract(contract, text string) error {
	prefix := strings.TrimPrefix(contract, "markdown_sections:")
	// Check at least one section starts with the prefix
	if !strings.Contains(text, "## "+prefix+"-") {
		return fmt.Errorf("no markdown sections starting with ## %s-", prefix)
	}
	return nil
}

func stripFences(text string) string {
	// Remove ```json and ``` fences
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```json") {
		text = strings.TrimPrefix(text, "```json")
	} else if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
	}
	if strings.HasSuffix(text, "```") {
		text = strings.TrimSuffix(text, "```")
	}
	return strings.TrimSpace(text)
}

// repairContract attempts to repair a contract violation.
func repairContract(ctx context.Context, loop *Loop, turn *Turn, text string, parseErr error) (string, error) {
	repairs := loop.RepairAttempts
	if repairs <= 0 {
		repairs = 2
	}

	for i := 0; i < repairs; i++ {
		repairPrompt := fmt.Sprintf(`Your previous response did not satisfy the output contract.

Contract: %s
Error: %v

Please reply again with the correct format.`, turn.Contract, parseErr)

		req := provider.ChatRequest{
			Model: loop.Duckling.Model,
			Messages: []provider.Message{
				{Role: "system", Content: "You are a duckling in ducklab. Follow the output contract exactly."},
				{Role: "user", Content: repairPrompt},
			},
		}
		resp, err := loop.Provider.Chat(ctx, req)
		if err != nil {
			continue
		}
		if len(resp.Choices) == 0 {
			continue
		}
		newText := resp.Choices[0].Message.Content
		if err := parseContract(turn.Contract, newText); err == nil {
			return newText, nil
		}
	}
	return "", fmt.Errorf("%w: after %d repair attempts", ErrContract, repairs)
}
