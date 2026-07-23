// Package source models an OpenAI-compatible endpoint (llama.cpp, vLLM, …).
// A Client turns a list of messages into a completion; HTTPSource is the real
// implementation and any fake satisfying Client drops in for tests.
package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message is one turn in a chat completion request.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Options tunes a single completion.
type Options struct {
	Temperature     float64
	MaxTokens       int
	DisableThinking bool // some local models burn MaxTokens on hidden reasoning
	LogPath         string
	// OnDone, if set, is called with the Result right after each completion —
	// the seam through which the UI reports per-phase tokens and latency.
	OnDone func(Result)
	// OnRetry, if set, is called before each retry of a transient failure
	// (5xx / connection error) so the UI can show that a blip is being ridden out.
	OnRetry func(attempt int, reason string)
}

// maxAttempts is how many times a completion is tried before giving up. Local
// model servers (vLLM/llama.cpp) occasionally throw a transient 5xx or drop a
// connection; a couple of backed-off retries ride that out instead of losing a
// multi-minute run.
const maxAttempts = 3

// Result is a completion plus the observability every ducklab call records.
type Result struct {
	Source           string
	Content          string
	PromptTokens     int
	CompletionTokens int
	FinishReason     string
	ReasoningChars   int
	Elapsed          time.Duration
}

// Tokens is the total (prompt + completion) tokens for the call.
func (r Result) Tokens() int { return r.PromptTokens + r.CompletionTokens }

// Client is the single seam strategies depend on. Real endpoints and test
// fakes both implement it.
type Client interface {
	Name() string
	Complete(ctx context.Context, msgs []Message, opts Options) (Result, error)
}

// HTTPSource is a live OpenAI-compatible endpoint.
type HTTPSource struct {
	SrcName string
	BaseURL string // e.g. http://localhost:8081/v1
	ModelID string // empty => resolved from /v1/models on first use
	APIKey  string
	Timeout time.Duration
}

func (s *HTTPSource) Name() string { return s.SrcName }

// ResolveModel returns the configured model id, discovering it from the
// endpoint's /models list when none was configured.
func (s *HTTPSource) ResolveModel(ctx context.Context) (string, error) {
	if s.ModelID != "" {
		return s.ModelID, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/models", nil)
	if err != nil {
		return "", err
	}
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Data) == 0 {
		return "", fmt.Errorf("source %q advertised no models", s.SrcName)
	}
	s.ModelID = body.Data[0].ID
	return s.ModelID, nil
}

// doOnce performs one chat-completion HTTP attempt, returning the body, status
// code, and any transport error. A fresh timeout and request body are used each
// call so retries are independent.
func (s *HTTPSource) doOnce(ctx context.Context, raw []byte, timeout time.Duration) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		s.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

func (s *HTTPSource) Complete(ctx context.Context, msgs []Message, opts Options) (Result, error) {
	model, err := s.ResolveModel(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("resolve model: %w", err)
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 16384
	}
	payload := map[string]any{
		"model":       model,
		"messages":    msgs,
		"temperature": opts.Temperature,
		"max_tokens":  opts.MaxTokens,
	}
	if opts.DisableThinking {
		payload["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	raw, _ := json.Marshal(payload)

	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	start := time.Now()
	var data []byte
	var status int
	for attempt := 1; ; attempt++ {
		data, status, err = s.doOnce(ctx, raw, timeout)
		transient := err != nil || status >= 500
		if !transient || attempt >= maxAttempts {
			break
		}
		reason := s.SrcName + " "
		if err != nil {
			reason += "connection error: " + prim60(err.Error())
		} else {
			reason += fmt.Sprintf("HTTP %d", status)
		}
		if opts.OnRetry != nil {
			opts.OnRetry(attempt, reason)
		}
		time.Sleep(time.Duration(attempt) * 2 * time.Second) // 2s, 4s backoff
	}
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return Result{}, fmt.Errorf("%s returned %d: %s", s.SrcName, status, prim60(string(data)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Result{}, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Result{}, fmt.Errorf("%s returned no choices", s.SrcName)
	}
	ch := parsed.Choices[0]
	r := Result{
		Source:           s.SrcName,
		Content:          ch.Message.Content,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		FinishReason:     ch.FinishReason,
		ReasoningChars:   len(ch.Message.ReasoningContent),
		Elapsed:          time.Since(start),
	}
	s.log(opts.LogPath, model, r)
	if opts.OnDone != nil {
		opts.OnDone(r)
	}
	return r, nil
}

// log appends a JSONL record per call: the HTTP-level visibility that makes an
// empty/truncated completion diagnosable instead of mysterious.
func (s *HTTPSource) log(path, model string, r Result) {
	if path == "" {
		return
	}
	entry := map[string]any{
		"source":            s.SrcName,
		"model":             model,
		"finish_reason":     r.FinishReason,
		"elapsed_s":         r.Elapsed.Seconds(),
		"prompt_tokens":     r.PromptTokens,
		"completion_tokens": r.CompletionTokens,
		"content_chars":     len(r.Content),
		"reasoning_chars":   r.ReasoningChars,
	}
	if r.FinishReason == "length" && strings.TrimSpace(r.Content) == "" {
		entry["warning"] = "EMPTY CONTENT with finish_reason=length — cut by max_tokens (reasoning burn?)."
	}
	line, _ := json.Marshal(entry)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func prim60(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
