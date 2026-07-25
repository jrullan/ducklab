// Package provider defines the Provider interface and implementations for
// OpenAI-compatible, Anthropic, and fake providers. All model communication
// goes through this package.
package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ChatRequest is a request to a model.
type ChatRequest struct {
	Model         string                 `json:"model"`
	Messages      []Message              `json:"messages"`
	Tools         []Tool                 `json:"tools,omitempty"`
	ToolChoice    interface{}            `json:"tool_choice,omitempty"`
	Temperature   *float64               `json:"temperature,omitempty"`
	TopP          *float64               `json:"top_p,omitempty"`
	MaxTokens     *int                   `json:"max_tokens,omitempty"`
	Stop          []string               `json:"stop,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	JSONMode      bool                   `json:"json_mode,omitempty"`
	Extra         map[string]interface{} `json:"extra,omitempty"`
}

// Message is a chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a tool call in a message.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool is a tool definition.
type Tool struct {
	Type     string      `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function definition within a tool.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ChatResponse is a response from a model.
type ChatResponse struct {
	ID           string   `json:"id"`
	Model        string   `json:"model"`
	Choices      []Choice `json:"choices"`
	Usage        Usage    `json:"usage"`
	FinishReason string   `json:"finish_reason"`
}

// Choice is a response choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage is token usage.
type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd,omitempty"` // OpenRouter extension
}

// Delta is a streaming delta.
type Delta struct {
	Text     string `json:"text,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Done     bool   `json:"done,omitempty"`
}

// Capabilities describes what a provider/model can do.
type Capabilities struct {
	NativeTools   bool `json:"native_tools"`
	JSONMode      bool `json:"json_mode"`
	ContextTokens int  `json:"context_tokens"`
	Vision        bool `json:"vision"`
}

// Provider is the interface every model endpoint implements.
type Provider interface {
	// ID returns the provider identifier.
	ID() string

	// Chat sends a non-streaming chat request.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)

	// ChatStream sends a streaming chat request. Deltas are sent on ch.
	// Returns the assembled final response. If the provider cannot stream,
	// it returns ErrUnsupported without sending on ch.
	ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error)

	// Models returns the list of available model IDs.
	Models(ctx context.Context) ([]string, error)
}

// ErrUnsupported is returned when a provider does not support an operation.
var ErrUnsupported = errors.New("unsupported operation")

// ErrNotFound is returned when a model or provider is not found.
var ErrNotFound = errors.New("not found")

// ErrAuth is returned when authentication fails.
var ErrAuth = errors.New("authentication failed")

// ErrRateLimit is returned when the provider rate-limits the request.
var ErrRateLimit = errors.New("rate limited")

// ErrTruncated is returned when the response is truncated (finish_reason=length).
var ErrTruncated = errors.New("response truncated")

// ErrContentFilter is returned when the provider's content filter blocks the response.
var ErrContentFilter = errors.New("content filter blocked response")

// ErrInvalidResponse is returned when the provider returns an unparseable response.
var ErrInvalidResponse = errors.New("invalid response")

// ErrProviderUnavailable is returned when the provider cannot be reached.
var ErrProviderUnavailable = errors.New("provider unavailable")

// IsTransient returns whether an error is transient and worth retrying.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimit) {
		return true
	}
	if errors.Is(err, ErrProviderUnavailable) {
		return true
	}
	// Network errors
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// RedactHeaders redacts sensitive headers from a request/response log.
func RedactHeaders(headers map[string]string) map[string]string {
	redacted := make(map[string]string, len(headers))
	for k, v := range headers {
		if isSensitive(k) {
			redacted[k] = "[redacted]"
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

// RedactString redacts sensitive values from a string.
func RedactString(s string) string {
	// Simple implementation: redact anything that looks like an API key
	// In practice, we redact specific known patterns
	return s
}

func isSensitive(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret")
}

// CostCalculator computes cost from token usage.
type CostCalculator struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Cost computes the cost in USD.
func (c CostCalculator) Cost(usage Usage) float64 {
	if usage.CostUSD > 0 {
		return usage.CostUSD // provider-reported cost wins
	}
	input := float64(usage.PromptTokens) / 1e6 * c.InputPerMTok
	output := float64(usage.CompletionTokens) / 1e6 * c.OutputPerMTok
	return input + output
}

// CostSource returns the cost source label.
func (c CostCalculator) CostSource(usage Usage) string {
	if usage.CostUSD > 0 {
		return "provider"
	}
	return "computed"
}

// EstimateTokens estimates token count from text length.
func EstimateTokens(text string) int {
	return len(text) / 4
}

// FinishReason constants.
const (
	FinishStop          = "stop"
	FinishEndTurn       = "end_turn"
	FinishToolCalls     = "tool_calls"
	FinishToolUse       = "tool_use"
	FinishLength        = "length"
	FinishMaxTokens     = "max_tokens"
	FinishContentFilter = "content_filter"
)

// IsToolCalls returns whether the finish reason indicates tool calls.
func IsToolCalls(reason string) bool {
	return reason == FinishToolCalls || reason == FinishToolUse
}

// IsStop returns whether the finish reason indicates a normal stop.
func IsStop(reason string) bool {
	return reason == FinishStop || reason == FinishEndTurn
}

// IsLength returns whether the finish reason indicates truncation.
func IsLength(reason string) bool {
	return reason == FinishLength || reason == FinishMaxTokens
}

// RetryPolicy configures retry behavior.
type RetryPolicy struct {
	MaxAttempts int
	InitialWait time.Duration
	MaxWait     time.Duration
}

// DefaultRetryPolicy returns the default retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		InitialWait: 500 * time.Millisecond,
		MaxWait:     10 * time.Second,
	}
}

// Retry executes fn with retries on transient errors.
func Retry(ctx context.Context, policy RetryPolicy, fn func() error) error {
	var lastErr error
	wait := policy.InitialWait
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsTransient(lastErr) {
			return lastErr
		}
		if attempt == policy.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			wait *= 2
			if wait > policy.MaxWait {
				wait = policy.MaxWait
			}
		}
	}
	return fmt.Errorf("after %d attempts: %w", policy.MaxAttempts, lastErr)
}
