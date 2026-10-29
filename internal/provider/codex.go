package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Codex speaks to a ChatGPT/Codex subscription: the Responses API at
// chatgpt.com/backend-api/codex, signed in with OAuth instead of an API key.
// An opt-in provider beside the others, never a default — the person decides
// which seats spend the subscription.
//
// The wire differs from Chat Completions in every direction: the system
// prompt travels as instructions, the conversation as typed input items,
// tool calls and their results as items of their own, and the answer as a
// stream of typed events. Nothing above this file sees any of that.
type Codex struct {
	id           string
	baseURL      string
	creds        *CodexCredentials
	headers      map[string]string
	httpClient   *http.Client
	stallTimeout time.Duration
	version      string

	catalogMu sync.Mutex
	catalog   map[string]codexModel
	order     []string
	catalogAt time.Time
}

// CodexOption configures a Codex provider.
type CodexOption func(*Codex)

// WithCodexHTTPClient replaces the HTTP client (tests).
func WithCodexHTTPClient(c *http.Client) CodexOption { return func(p *Codex) { p.httpClient = c } }

// WithCodexStallTimeout overrides the stream-silence bound (tests).
func WithCodexStallTimeout(d time.Duration) CodexOption {
	return func(p *Codex) { p.stallTimeout = d }
}

// WithCodexHeaders adds configured headers to every request.
func WithCodexHeaders(h map[string]string) CodexOption {
	return func(p *Codex) {
		for k, v := range h {
			p.headers[k] = v
		}
	}
}

// WithCodexVersion is the version ducklab identifies itself with.
func WithCodexVersion(v string) CodexOption { return func(p *Codex) { p.version = v } }

// NewCodex creates a Codex subscription provider.
func NewCodex(id, baseURL string, creds *CodexCredentials, opts ...CodexOption) *Codex {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = CodexDefaultURL
	}
	p := &Codex{
		id:      id,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		creds:   creds,
		headers: map[string]string{},
		// No total timeout: the stall watchdog bounds silence (see stallWatch).
		httpClient: &http.Client{},
		version:    "dev",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ID returns the provider ID.
func (p *Codex) ID() string { return p.id }

// Credentials is the sign-in this provider uses.
func (p *Codex) Credentials() *CodexCredentials { return p.creds }

// Chat runs the request as a stream and returns the assembled answer: the
// subscription endpoint only streams.
func (p *Codex) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	ch := make(chan Delta, 64)
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	resp, err := p.ChatStream(ctx, req, ch)
	close(ch)
	<-done
	return resp, err
}

// ChatStream sends the request and streams the answer.
func (p *Codex) ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	tokens, err := p.signedIn(ctx)
	if err != nil {
		return ChatResponse{}, err
	}
	body, err := codexRequestBody(req)
	if err != nil {
		return ChatResponse{}, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	// A refused token is refreshed and the request sent once more: the
	// server can retire an access token before the expiry it carries.
	resp, out, err := p.stream(ctx, payload, tokens, ch)
	if resp == http.StatusUnauthorized {
		fresh, rerr := p.creds.ForceRefresh(ctx, tokens)
		if rerr != nil {
			return ChatResponse{}, p.signInError(rerr)
		}
		_, out, err = p.stream(ctx, payload, fresh, ch)
	}
	return out, err
}

// stream sends one request; status is the HTTP status when the server
// refused it.
func (p *Codex) stream(ctx context.Context, payload []byte, tokens CodexTokens, ch chan<- Delta) (int, ChatResponse, error) {
	ctx, watch, cancelStall := watchStall(ctx, p.stallTimeout)
	defer cancelStall()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/responses", strings.NewReader(string(payload)))
	if err != nil {
		return 0, ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, tokens)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if watch.tripped() {
			return 0, ChatResponse{}, watch.err(err)
		}
		return 0, ChatResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	watch.touch()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, ChatResponse{}, codexHTTPError(p.id, resp, raw)
	}
	out, err := readCodexStream(ctx, resp.Body, ch, watch.touch)
	if err != nil {
		if watch.tripped() {
			return 0, ChatResponse{}, watch.err(err)
		}
		return 0, ChatResponse{}, err
	}
	return 0, out, nil
}

// signedIn returns fresh tokens or says how to get them.
func (p *Codex) signedIn(ctx context.Context) (CodexTokens, error) {
	if p.creds == nil {
		return CodexTokens{}, fmt.Errorf("%w: provider %s has no credential store", ErrAuth, p.id)
	}
	t, err := p.creds.Fresh(ctx)
	if err != nil {
		return CodexTokens{}, p.signInError(err)
	}
	return t, nil
}

// signInError says how to mend a missing or dead sign-in.
func (p *Codex) signInError(err error) error {
	switch {
	case errors.Is(err, ErrCodexNotLoggedIn):
		return fmt.Errorf("%w: provider %s is not signed in to a Codex subscription (%v) — run `ducklab provider login %s`", ErrAuth, p.id, err, p.id)
	case errors.Is(err, ErrAuth):
		return fmt.Errorf("%w — run `ducklab provider login %s`", err, p.id)
	}
	return err
}

func (p *Codex) setHeaders(req *http.Request, t CodexTokens) {
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	// Identity: OpenAI asks third-party harnesses to name themselves rather
	// than pose as the Codex CLI.
	req.Header.Set("originator", "ducklab")
	req.Header.Set("User-Agent", "ducklab/"+p.version)
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	if acct, err := t.Account(); err == nil {
		// Without the account id the per-account backend answers with an
		// empty catalog or a 401; residency-enforced workspaces also need
		// their region.
		if acct.AccountID != "" {
			req.Header.Set("ChatGPT-Account-ID", acct.AccountID)
		}
		if acct.Residency != "" {
			req.Header.Set("x-openai-internal-codex-residency", acct.Residency)
		}
	}
}

// codexHTTPError classifies a refused request. A spent usage window is a
// rate limit — resumable weather, with the reset time in the message — and a
// model the plan does not include is a chat route this duckling cannot use.
func codexHTTPError(id string, resp *http.Response, raw []byte) error {
	var env struct {
		Detail string `json:"detail"`
		Error  struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	msg := firstNonEmpty(env.Error.Message, env.Detail, strings.TrimSpace(string(raw)))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: codex usage limit (%s)%s", ErrRateLimit, msg, codexResetHint(resp.Header))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: codex refused the sign-in (%s: %s) — run `ducklab provider login %s`", ErrAuth, resp.Status, msg, id)
	case env.Error.Code == "model_not_found" || resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrChatUnavailable, msg)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: codex: %s: %s", ErrProviderUnavailable, resp.Status, msg)
	}
	return fmt.Errorf("codex: %s: %s", resp.Status, msg)
}

// codexResetHint names when the spent window reopens, from the headers the
// subscription sends with every answer.
func codexResetHint(h http.Header) string {
	for _, k := range []string{"x-codex-primary-reset-after-seconds", "x-codex-secondary-reset-after-seconds"} {
		if s, err := strconv.Atoi(strings.TrimSpace(h.Get(k))); err == nil && s > 0 {
			return fmt.Sprintf("; resets in %s", (time.Duration(s) * time.Second).Round(time.Minute))
		}
	}
	return ""
}

// codexRequestBody translates a chat request into a Responses request.
func codexRequestBody(req ChatRequest) (map[string]interface{}, error) {
	model, effort := splitCodexModel(req.Model)
	if model == "" {
		return nil, errors.New("codex request needs a model")
	}
	var instructions []string
	input := []interface{}{}
	leading := true
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			// The leading system prompt is the instructions; a later system
			// note keeps its place in the conversation as a developer message.
			if leading {
				instructions = append(instructions, m.Content)
				continue
			}
			input = append(input, codexMessage("developer", "input_text", m.Content, nil))
		case "user":
			leading = false
			input = append(input, codexMessage("user", "input_text", m.Content, m.Images))
		case "assistant":
			leading = false
			if m.Content != "" {
				input = append(input, codexMessage("assistant", "output_text", m.Content, nil))
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]interface{}{
					"type": "function_call", "call_id": tc.ID,
					"name": tc.Function.Name, "arguments": tc.Function.Arguments,
				})
			}
		case "tool":
			leading = false
			input = append(input, map[string]interface{}{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content,
			})
		default:
			return nil, fmt.Errorf("codex request: unknown message role %q", m.Role)
		}
	}
	body := map[string]interface{}{
		"model":        model,
		"instructions": firstNonEmpty(strings.TrimSpace(strings.Join(instructions, "\n\n")), "You are a helpful assistant."),
		"input":        input,
		// The subscription keeps nothing server-side: every turn carries the
		// whole conversation, and it only answers as a stream.
		"store":  false,
		"stream": true,
	}
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Function.Parameters
			if params == nil {
				params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
			tools = append(tools, map[string]interface{}{
				"type": "function", "name": t.Function.Name,
				"description": t.Function.Description, "strict": false, "parameters": params,
			})
		}
		body["tools"] = tools
		if choice := codexToolChoice(req.ToolChoice); choice != nil {
			body["tool_choice"] = choice
		}
	}
	if e := codexEffort(effort, req.Extra); e != "" {
		body["reasoning"] = map[string]interface{}{"effort": e, "summary": "auto"}
	} else {
		body["reasoning"] = map[string]interface{}{"summary": "auto"}
	}
	return body, nil
}

func codexMessage(role, partType, text string, images []string) map[string]interface{} {
	parts := []interface{}{}
	if text != "" || len(images) == 0 {
		parts = append(parts, map[string]interface{}{"type": partType, "text": text})
	}
	for _, img := range images {
		parts = append(parts, map[string]interface{}{"type": "input_image", "image_url": img})
	}
	return map[string]interface{}{"type": "message", "role": role, "content": parts}
}

// splitCodexModel reads an effort suffix off a duckling's model id:
// "gpt-6.1-sol:high" runs gpt-6.1-sol at high reasoning effort. Without one
// the model's own default applies.
func splitCodexModel(model string) (string, string) {
	model = strings.TrimSpace(model)
	if i := strings.LastIndex(model, ":"); i > 0 {
		return model[:i], strings.TrimSpace(model[i+1:])
	}
	return model, ""
}

// codexEffort picks the reasoning effort: the model suffix, else a seat that
// asks for thinking off runs at the lowest effort the models offer.
func codexEffort(suffix string, extra map[string]interface{}) string {
	if suffix != "" {
		return suffix
	}
	if r, ok := extra["reasoning"].(map[string]interface{}); ok {
		if e, ok := r["effort"].(string); ok && e != "" {
			return e
		}
		if enabled, ok := r["enabled"].(bool); ok && !enabled {
			return "low"
		}
	}
	if k, ok := extra["chat_template_kwargs"].(map[string]interface{}); ok {
		if enabled, ok := k["enable_thinking"].(bool); ok && !enabled {
			return "low"
		}
	}
	return ""
}

func codexToolChoice(choice interface{}) interface{} {
	switch c := choice.(type) {
	case nil:
		return nil
	case string:
		return c
	case map[string]interface{}:
		if fn, ok := c["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				return map[string]interface{}{"type": "function", "name": name}
			}
		}
	}
	return nil
}

// codexEvent is one streamed event; only the fields ducklab reads.
type codexEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Response *struct {
		ID                string            `json:"id"`
		Model             string            `json:"model"`
		Status            string            `json:"status"`
		Output            []json.RawMessage `json:"output"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *codexFailure `json:"error"`
		Usage *struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			TotalTokens         int `json:"total_tokens"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
	// A bare error event carries its fields at the top level.
	Code    string `json:"code"`
	Message string `json:"message"`
}

type codexFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f codexFailure) err() error {
	lower := strings.ToLower(f.Code + " " + f.Message)
	msg := firstNonEmpty(f.Message, f.Code, "response failed")
	switch {
	case strings.Contains(lower, "rate_limit") || strings.Contains(lower, "usage_limit"):
		return fmt.Errorf("%w: codex: %s", ErrRateLimit, msg)
	case strings.Contains(lower, "server_error") || strings.Contains(lower, "overloaded") || strings.Contains(lower, "unavailable"):
		return fmt.Errorf("%w: codex: %s", ErrProviderUnavailable, msg)
	case strings.Contains(lower, "content_filter") || strings.Contains(lower, "content_policy"):
		return fmt.Errorf("%w: codex: %s", ErrContentFilter, msg)
	}
	return fmt.Errorf("codex: %s", msg)
}

// codexItem is one output item: a message, a function call, or reasoning.
type codexItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

// readCodexStream assembles the answer from the event stream. The finished
// items (output_item.done) are authoritative; the deltas are what a live view
// shows meanwhile. The completed event's own output list is only a fallback —
// the subscription has been seen to send it empty.
func readCodexStream(ctx context.Context, body io.Reader, ch chan<- Delta, touch func()) (ChatResponse, error) {
	send := func(d Delta) error {
		select {
		case ch <- d:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var items []codexItem
	var deltaText, deltaReasoning strings.Builder
	var out ChatResponse
	terminal := false
	incomplete := ""

	scanner := newSSEScanner(body)
	for scanner.scan() {
		touch()
		var ev codexEvent
		if err := json.Unmarshal([]byte(scanner.event()), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			deltaText.WriteString(ev.Delta)
			if err := send(Delta{Text: ev.Delta}); err != nil {
				return ChatResponse{}, err
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			deltaReasoning.WriteString(ev.Delta)
			if err := send(Delta{Reasoning: ev.Delta}); err != nil {
				return ChatResponse{}, err
			}
		case "response.reasoning_summary_part.done":
			deltaReasoning.WriteString("\n\n")
		case "response.output_item.added":
			var it codexItem
			if json.Unmarshal(ev.Item, &it) == nil && it.Type == "function_call" && it.Name != "" {
				if err := send(Delta{ToolName: it.Name}); err != nil {
					return ChatResponse{}, err
				}
			}
		case "response.output_item.done":
			var it codexItem
			if json.Unmarshal(ev.Item, &it) == nil {
				items = append(items, it)
			}
		case "response.completed", "response.incomplete":
			terminal = true
			if r := ev.Response; r != nil {
				out.ID, out.Model = r.ID, r.Model
				if r.Usage != nil {
					out.Usage = Usage{
						PromptTokens:     r.Usage.InputTokens,
						CompletionTokens: r.Usage.OutputTokens,
						TotalTokens:      r.Usage.TotalTokens,
						ReasoningTokens:  r.Usage.OutputTokensDetails.ReasoningTokens,
					}
				}
				if r.IncompleteDetails != nil {
					incomplete = r.IncompleteDetails.Reason
				} else if ev.Type == "response.incomplete" {
					incomplete = "incomplete"
				}
				if len(items) == 0 {
					for _, raw := range r.Output {
						var it codexItem
						if json.Unmarshal(raw, &it) == nil {
							items = append(items, it)
						}
					}
				}
			}
		case "response.failed":
			var f codexFailure
			if ev.Response != nil && ev.Response.Error != nil {
				f = *ev.Response.Error
			}
			return ChatResponse{}, f.err()
		case "error":
			return ChatResponse{}, codexFailure{Code: ev.Code, Message: ev.Message}.err()
		}
		if terminal {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() == nil {
			return ChatResponse{}, fmt.Errorf("%w: stream read: %v", ErrProviderUnavailable, err)
		}
		return ChatResponse{}, fmt.Errorf("stream read: %w", err)
	}
	if !terminal {
		// A stream that ends without its completion event was cut, not
		// finished: never hand back half an answer as a whole one.
		return ChatResponse{}, fmt.Errorf("%w: codex stream ended before the response completed", ErrProviderUnavailable)
	}

	msg := Message{Role: "assistant"}
	var text, reasoning strings.Builder
	for _, it := range items {
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			tc := ToolCall{ID: it.CallID, Type: "function"}
			tc.Function.Name, tc.Function.Arguments = it.Name, it.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		case "reasoning":
			for _, s := range it.Summary {
				if reasoning.Len() > 0 {
					reasoning.WriteString("\n\n")
				}
				reasoning.WriteString(s.Text)
			}
		}
	}
	msg.Content = firstNonEmpty(text.String(), deltaText.String())
	msg.Reasoning = firstNonEmpty(reasoning.String(), strings.TrimSpace(deltaReasoning.String()))

	finish := FinishStop
	switch {
	case len(msg.ToolCalls) > 0:
		finish = FinishToolCalls
	case incomplete == "max_output_tokens":
		finish = FinishLength
	case incomplete == "content_filter":
		finish = FinishContentFilter
	case incomplete != "":
		finish = FinishLength
	}
	if err := send(Delta{Done: true}); err != nil {
		return ChatResponse{}, err
	}
	out.Choices = []Choice{{Message: msg, FinishReason: finish}}
	out.FinishReason = finish
	return out, nil
}

// codexModel is one entry of the subscription's model catalog.
type codexModel struct {
	Slug            string   `json:"slug"`
	Visibility      string   `json:"visibility"`
	ContextWindow   int      `json:"context_window"`
	InputModalities []string `json:"input_modalities"`
	Priority        int      `json:"priority"`
}

// codexCatalogTTL bounds how stale the catalog may be: it changes when a plan
// does, not per request.
const codexCatalogTTL = 10 * time.Minute

func (p *Codex) loadCatalog(ctx context.Context) (map[string]codexModel, []string, error) {
	p.catalogMu.Lock()
	defer p.catalogMu.Unlock()
	if p.catalog != nil && time.Since(p.catalogAt) < codexCatalogTTL {
		return p.catalog, p.order, nil
	}
	tokens, err := p.signedIn(ctx)
	if err != nil {
		return nil, nil, err
	}
	// The catalog is gated by client version; a high one lists every model
	// the plan includes.
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/models?client_version=99.0.0", nil)
	if err != nil {
		return nil, nil, err
	}
	p.setHeaders(req, tokens)
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: codex models: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, nil, codexHTTPError(p.id, resp, raw)
	}
	var got struct {
		Models []codexModel `json:"models"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, nil, fmt.Errorf("%w: codex models: %v", ErrInvalidResponse, err)
	}
	catalog := map[string]codexModel{}
	var order []string
	for _, m := range got.Models {
		if m.Slug == "" {
			continue
		}
		catalog[m.Slug] = m
		if v := strings.ToLower(m.Visibility); v == "hide" || v == "hidden" {
			continue
		}
		order = append(order, m.Slug)
	}
	p.catalog, p.order, p.catalogAt = catalog, order, time.Now()
	return catalog, order, nil
}

// Models lists the models the signed-in plan offers.
func (p *Codex) Models(ctx context.Context) ([]string, error) {
	_, order, err := p.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), order...), nil
}

// ModelInfo reports a model's context window and whether it sees images, so a
// duckling saved without them is filled from what the plan declares.
func (p *Codex) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	catalog, _, err := p.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	slug, _ := splitCodexModel(model)
	m, ok := catalog[slug]
	if !ok {
		return nil, fmt.Errorf("%w: model %q is not in this Codex plan", ErrNotFound, slug)
	}
	info := &ModelInfo{ContextTokens: m.ContextWindow}
	if m.InputModalities != nil {
		vision := false
		for _, mod := range m.InputModalities {
			if mod == "image" {
				vision = true
			}
		}
		info.Vision = &vision
	}
	return info, nil
}
