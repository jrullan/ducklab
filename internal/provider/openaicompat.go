package provider

import (
	"sync/atomic"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompat is an OpenAI-compatible provider.
type OpenAICompat struct {
	id         string
	baseURL    string
	apiKey     string
	headers    map[string]string
	httpClient *http.Client
	// stallTimeout bounds silence WITHIN a streaming call: first byte and
	// every gap between chunks. Zero means defaultStallTimeout.
	stallTimeout time.Duration
}

// OpenAICompatOption configures an OpenAICompat provider.
type OpenAICompatOption func(*OpenAICompat)

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) OpenAICompatOption {
	return func(p *OpenAICompat) {
		p.httpClient = c
	}
}

// WithHeaders sets extra headers.
func WithHeaders(headers map[string]string) OpenAICompatOption {
	return func(p *OpenAICompat) {
		p.headers = headers
	}
}

// defaultStallTimeout is how long a streaming call may be silent — no first
// byte, no next chunk — before it is declared stalled and retried. Distinct
// from the 300s client timeout, which bounds the WHOLE call and is what a
// legitimately long generation needs.
const defaultStallTimeout = 120 * time.Second

// WithStallTimeout overrides the stream-silence bound (tests, mostly).
func WithStallTimeout(d time.Duration) OpenAICompatOption {
	return func(p *OpenAICompat) { p.stallTimeout = d }
}

// NewOpenAICompat creates a new OpenAI-compatible provider.
func NewOpenAICompat(id, baseURL, apiKey string, opts ...OpenAICompatOption) *OpenAICompat {
	p := &OpenAICompat{
		id:      id,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		headers: make(map[string]string),
		httpClient: &http.Client{
			// No total-request timeout. Client.Timeout bounds the WHOLE
			// exchange including a streaming body, so an architect actively
			// writing a long spec was guillotined mid-word at five minutes —
			// twice, by different models, both healthy. Silence is what kills
			// a stream, and the stall watchdog already bounds silence
			// (first byte and every gap). Non-streaming calls, which have no
			// watchdog, carry their own per-request deadline in Chat.
			Timeout: 0,
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ID returns the provider ID.
func (p *OpenAICompat) ID() string {
	return p.id
}

// Chat sends a non-streaming chat request.
func (p *OpenAICompat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	req.Stream = false
	p.askForBilledCost(&req)
	// A non-streaming call has no stall watchdog: its deadline lives here,
	// bounding this one exchange rather than every stream the client makes.
	ctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	return p.doChat(ctx, req)
}

// askForBilledCost requests OpenRouter's usage accounting, which carries the
// actually-billed cost — caching discounts included — where the configured
// flat rates can only approximate. OpenRouter only: OpenAI proper rejects
// unknown top-level parameters, and plainer servers have nothing to report.
func (p *OpenAICompat) askForBilledCost(req *ChatRequest) {
	if req.UsageDetail == nil && strings.Contains(strings.ToLower(p.baseURL), "openrouter") {
		req.UsageDetail = &UsageDetail{Include: true}
	}
}

// ChatStream sends a streaming chat request.
func (p *OpenAICompat) ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	req.Stream = true
	// Ask for usage explicitly. An OpenAI-compatible endpoint omits it from a
	// streamed response unless told to include it, so without this every
	// streamed run recorded zero tokens and zero cost — and a budget that
	// counts zero is a budget that never stops anything (I3).
	//
	// Measured against vLLM: no usage in any chunk without the flag; a final
	// usage chunk with prompt, completion and total with it.
	if req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	p.askForBilledCost(&req)
	return p.doChatStream(ctx, req, ch)
}

// Models returns available models.
// ModelInfo is what a provider's model listing knows about one model beyond
// its name. OpenRouter reports context_length and per-token pricing; plainer
// OpenAI-compatible servers report neither, and absence is not an error.
type ModelInfo struct {
	ContextTokens int
	// MaxOutputTokens is the model's own reply ceiling. Left unset, a
	// duckling runs on ducklab's conservative default — which truncated a
	// whole-document draft twice for a model that could have emitted eight
	// times as much.
	MaxOutputTokens   int
	PromptPerMTok     float64
	CompletionPerMTok float64
}

// ModelInfo looks one model up in the provider's listing.
//
// Best-effort by design: it exists so a duckling saved without a context size
// or costs can be filled from what the provider itself declares, instead of
// silently running on a 32k default that starves a 200k model.
func (p *OpenAICompat) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: %s", resp.Status)
	}
	var result struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			TopProvider   struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
			Pricing struct {
				// OpenRouter sends USD per TOKEN as strings ("0.000003").
				Prompt     json.Number `json:"prompt"`
				Completion json.Number `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	for _, m := range result.Data {
		if m.ID != model {
			continue
		}
		info := &ModelInfo{
			ContextTokens:   m.ContextLength,
			MaxOutputTokens: m.TopProvider.MaxCompletionTokens,
		}
		if v, err := m.Pricing.Prompt.Float64(); err == nil {
			info.PromptPerMTok = v * 1e6
		}
		if v, err := m.Pricing.Completion.Float64(); err == nil {
			info.CompletionPerMTok = v * 1e6
		}
		return info, nil
	}
	return nil, fmt.Errorf("model %q not in the provider's listing", model)
}

func (p *OpenAICompat) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models: %s: %s", resp.Status, string(body))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

func (p *OpenAICompat) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
}

func (p *OpenAICompat) doChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq)
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ChatResponse{}, ErrAuth
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return ChatResponse{}, ErrRateLimit
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("chat: %s: %s", resp.Status, string(body))
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return ChatResponse{}, fmt.Errorf("read response: %w", readErr)
	}
	// Checked before decoding: an empty body is not a malformed one, and
	// "unexpected end of JSON input" describes our parser rather than the fact
	// that the server sent nothing.
	if len(bytes.TrimSpace(body)) == 0 {
		return ChatResponse{}, fmt.Errorf("%w: the server answered %s with an empty body",
			ErrInvalidResponse, resp.Status)
	}
	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		// What the provider actually said, and what kind of failure it is.
		//
		// OpenRouter answers 200 with an `error` object, so an upstream gateway
		// timeout arrives looking like a well-formed response. Classed as
		// ErrInvalidResponse it was not transient, so the retry policy never
		// fired and a 504 — the thing retries exist for — killed the run on the
		// first try.
		msg, kind := providerFailure(body)
		return ChatResponse{}, fmt.Errorf("%w: %s", kind, msg)
	}
	chatResp.FinishReason = chatResp.Choices[0].FinishReason
	chatResp.Usage.Normalize()
	applyReasoning(body, &chatResp)
	return chatResp, nil
}

// applyReasoning folds an endpoint's thinking fields into the response.
//
// A second pass rather than tags on Message, because Message is also what gets
// sent back in a request: a field that decodes reasoning would also encode it,
// and replaying a turn's deliberation to the model is not a conversation.
//
// The two names are the same thing under different vendors — DeepSeek's own API
// and vLLM say reasoning_content, OpenRouter says reasoning. Handled here so
// nothing above this layer has to know which endpoint answered.
func applyReasoning(body []byte, out *ChatResponse) {
	var side struct {
		Choices []struct {
			Message struct {
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &side); err != nil {
		return
	}
	for i := range side.Choices {
		if i >= len(out.Choices) {
			break
		}
		if r := firstNonEmpty(
			side.Choices[i].Message.ReasoningContent,
			side.Choices[i].Message.Reasoning,
		); r != "" {
			out.Choices[i].Message.Reasoning = r
		}
	}
	out.Usage.ReasoningTokens = side.Usage.CompletionTokensDetails.ReasoningTokens
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (p *OpenAICompat) doChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	// The stall watchdog. An upstream that accepts the request and then sends
	// NOTHING held a run for the full 300s client timeout — and the retry
	// chain behind it re-ran the same wait three more times, so one stalled
	// provider cost twenty silent minutes (T-075, twice, two models). A
	// healthy stream delivers its first byte in seconds and never goes quiet
	// for two minutes between chunks; one that does is weather, and weather
	// is retried — fast.
	stall := p.stallTimeout
	if stall <= 0 {
		stall = defaultStallTimeout
	}
	ctx, cancelStall := context.WithCancel(ctx)
	defer cancelStall()
	activity := make(chan struct{}, 1)
	var stalled atomic.Bool
	go func() {
		t := time.NewTimer(stall)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(stall)
			case <-t.C:
				stalled.Store(true)
				cancelStall()
				return
			}
		}
	}()
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	// stallErr dresses a watchdog trip as what it is: the provider's
	// silence, transient, retryable — not a mysterious context error.
	stallErr := func(err error) error {
		if stalled.Load() {
			return fmt.Errorf("%w: provider sent nothing for %s (stalled stream)", ErrProviderUnavailable, stall)
		}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if stalled.Load() {
			return ChatResponse{}, stallErr(err)
		}
		return ChatResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	touch()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("chat stream: %s: %s", resp.Status, string(body))
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Not actually streaming; fall back to non-streaming
		var chatResp ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			return ChatResponse{}, fmt.Errorf("decode non-stream response: %w", err)
		}
		if len(chatResp.Choices) > 0 {
			chatResp.FinishReason = chatResp.Choices[0].FinishReason
		}
		return chatResp, nil
	}

	var fullText strings.Builder
	var fullReasoning strings.Builder
	var acc toolCallAccumulator
	var usage Usage
	var finishReason string
	var upstream string

	scanner := newSSEScanner(resp.Body)
	for scanner.scan() {
		touch()
		event := scanner.event()
		if event == "done" || event == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// Two vendors, one thing: vLLM and DeepSeek's own API say
					// reasoning_content, OpenRouter says reasoning. Read both
					// so a duckling's endpoint does not decide whether its
					// thinking is visible.
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					// Streamed tool calls arrive in fragments identified by
					// index: the name in one chunk, the arguments split across
					// several more. They are not whole ToolCalls.
					ToolCalls []streamToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage    *Usage `json:"usage"`
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal([]byte(event), &chunk); err != nil {
			continue
		}
		if chunk.Provider != "" {
			upstream = chunk.Provider
		}
		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			// Thinking first: it is produced before the answer, and emitting
			// it in that order is what makes a live view show deliberation
			// turning into a reply.
			if r := firstNonEmpty(c.Delta.ReasoningContent, c.Delta.Reasoning); r != "" {
				fullReasoning.WriteString(r)
				select {
				case ch <- Delta{Reasoning: r}:
				case <-ctx.Done():
					return ChatResponse{}, ctx.Err()
				}
			}
			if c.Delta.Content != "" {
				fullText.WriteString(c.Delta.Content)
				select {
				case ch <- Delta{Text: c.Delta.Content}:
				case <-ctx.Done():
					return ChatResponse{}, ctx.Err()
				}
			}
			for _, frag := range c.Delta.ToolCalls {
				acc.add(frag)
			}
			if c.FinishReason != "" {
				finishReason = c.FinishReason
			}
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
			usage.Normalize()
		}
	}
	if err := scanner.Err(); err != nil {
		if stalled.Load() {
			return ChatResponse{}, stallErr(err)
		}
		// The server went away mid-stream — a connection reset from a proxy on
		// a long stream is the most ordinary transient there is, and classed
		// as a plain error it skipped the retry policy entirely: one TCP reset
		// from Cloudflare killed a forty-minute run.
		if ctx.Err() == nil {
			return ChatResponse{}, fmt.Errorf("%w: stream read: %v", ErrProviderUnavailable, err)
		}
		return ChatResponse{}, fmt.Errorf("stream read: %w", err)
	}

	select {
	case ch <- Delta{Done: true}:
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	}

	return ChatResponse{
		Upstream: upstream,
		Choices: []Choice{{
			Message: Message{
				Role:      "assistant",
				Content:   fullText.String(),
				Reasoning: fullReasoning.String(),
				ToolCalls: acc.result(),
			},
			FinishReason: finishReason,
		}},
		Usage:        usage,
		FinishReason: finishReason,
	}, nil
}

// streamToolCall is one fragment of a tool call as it arrives over SSE.
type streamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolCallAccumulator reassembles streamed tool calls.
//
// The fragments used to be appended as if each were a complete call, which
// produced one tool call per chunk: the first with a name and no arguments,
// the rest with a slice of JSON and no name. Sent back in the next request,
// the server rejected it — vLLM answered 400 "Expecting value: line 1 column
// 10" while trying to parse a truncated arguments string. A run therefore
// streamed its first turn beautifully and then died.
type toolCallAccumulator struct {
	order []int
	byIdx map[int]*ToolCall
}

func (a *toolCallAccumulator) add(f streamToolCall) {
	if a.byIdx == nil {
		a.byIdx = map[int]*ToolCall{}
	}
	tc, ok := a.byIdx[f.Index]
	if !ok {
		tc = &ToolCall{}
		a.byIdx[f.Index] = tc
		a.order = append(a.order, f.Index)
	}
	// Identity arrives once, in whichever fragment carries it; arguments
	// arrive in pieces and must be concatenated in order.
	if f.ID != "" {
		tc.ID = f.ID
	}
	if f.Type != "" {
		tc.Type = f.Type
	}
	if f.Function.Name != "" {
		tc.Function.Name = f.Function.Name
	}
	tc.Function.Arguments += f.Function.Arguments
}

func (a *toolCallAccumulator) result() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(a.order))
	for _, i := range a.order {
		tc := *a.byIdx[i]
		if tc.Type == "" {
			tc.Type = "function"
		}
		out = append(out, tc)
	}
	return out
}

// sseScanner scans SSE events.
type sseScanner struct {
	r        io.Reader
	buf      []byte
	eventVal string
	err      error
	// eof records that the reader is exhausted. It is separate from err
	// because a reader can hand back data and io.EOF together, and the data
	// still has to be parsed.
	eof bool
}

func newSSEScanner(r io.Reader) *sseScanner {
	// Length zero, capacity 4096 — NOT length 4096.
	//
	// With a full-length buffer, s.buf[len(s.buf):cap(s.buf)] is an empty
	// slice, Read into it returns (0, nil) immediately, and scan() spins
	// forever over 4096 zero bytes without ever consuming the body. Streaming
	// therefore never worked: a run that asked for it hung on its first turn
	// while the endpoint sat idle, burning a core.
	return &sseScanner{r: r, buf: make([]byte, 0, 4096)}
}

func (s *sseScanner) scan() bool {
	for {
		if s.err != nil {
			return false
		}
		idx := strings.Index(string(s.buf), "\n\n")
		if idx < 0 && s.eof {
			// No delimiter left and the stream is finished: whatever remains
			// is the final frame, terminator or not.
			rest := strings.TrimSpace(string(s.buf))
			s.buf = nil
			if rest == "" {
				return false
			}
			for _, line := range strings.Split(rest, "\n") {
				if after, ok := strings.CutPrefix(line, "data: "); ok {
					s.eventVal = after
					return true
				}
			}
			return false
		}
		if idx >= 0 {
			event := string(s.buf[:idx])
			s.buf = s.buf[idx+2:]
			lines := strings.Split(event, "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "data: ") {
					s.eventVal = strings.TrimPrefix(line, "data: ")
					return true
				}
			}
			continue
		}
		// Grow when the buffer is full, or the read window is empty again and
		// a single event larger than the buffer would deadlock the same way.
		if len(s.buf) == cap(s.buf) {
			grown := make([]byte, len(s.buf), 2*cap(s.buf)+1)
			copy(grown, s.buf)
			s.buf = grown
		}
		n, err := s.r.Read(s.buf[len(s.buf):cap(s.buf)])
		if n > 0 {
			s.buf = s.buf[:len(s.buf)+n]
		}
		if err == io.EOF {
			// A reader may return data AND io.EOF in the same call — http
			// bodies routinely do, strings.Reader never does. Emitting the
			// whole remaining buffer as one event here handed the parser
			// several concatenated frames as a single malformed one, and it
			// dropped the lot.
			//
			// That is why a live run streamed content correctly and still
			// recorded zero tokens: usage is the last chunk before EOF, so it
			// was always in the part that got swallowed.
			s.eof = true
			if len(s.buf) == 0 {
				return false
			}
			continue
		}
		if err != nil {
			s.err = err
			return false
		}
	}
}

func (s *sseScanner) event() string {
	return s.eventVal
}

func (s *sseScanner) Err() error {
	return s.err
}

// Anthropic is an Anthropic provider.
type Anthropic struct {
	id         string
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(id, baseURL, apiKey string) *Anthropic {
	return &Anthropic{
		id:      id,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

// ID returns the provider ID.
func (p *Anthropic) ID() string {
	return p.id
}

// Chat sends a chat request.
func (p *Anthropic) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Convert to Anthropic format
	messages := make([]map[string]interface{}, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // Anthropic uses a separate system parameter
		}
		msg := map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
		messages = append(messages, msg)
	}
	body := map[string]interface{}{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": 4096,
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	// Extract system message
	var system string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			break
		}
	}
	if system != "" {
		body["system"] = system
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", strings.NewReader(string(jsonBody)))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ChatResponse{}, ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("chat: %s: %s", resp.Status, string(body))
	}
	var result struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	var text string
	for _, c := range result.Content {
		if c.Type == "text" {
			text = c.Text
			break
		}
	}
	finishReason := result.StopReason
	if finishReason == "end_turn" {
		finishReason = FinishStop
	}
	return ChatResponse{
		ID: result.ID,
		Choices: []Choice{{
			Message: Message{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: finishReason,
		}},
		Usage: Usage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
		},
		FinishReason: finishReason,
	}, nil
}

// ChatStream sends a streaming chat request.
func (p *Anthropic) ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	// Anthropic streaming is more complex; fall back to non-streaming for now
	return p.Chat(ctx, req)
}

// Models returns available models.
func (p *Anthropic) Models(ctx context.Context) ([]string, error) {
	// Anthropic doesn't have a models endpoint; return a static list
	return []string{
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
	}, nil
}

// Fake is a fake provider for testing.
type Fake struct {
	ModelInfoFn func(model string) *ModelInfo
	id        string
	responses []ChatResponse
	requests  []ChatRequest
	callCount int
	models    []string
	// ScriptFunc is called when responses are exhausted. If it returns a
	// response, it is used; otherwise an error is returned.
	ScriptFunc func(req ChatRequest, callCount int) *ChatResponse
}

// NewFake creates a new fake provider.
func NewFake(id string) *Fake {
	return &Fake{id: id}
}

// ID returns the provider ID.
// ModelInfoFn, when set, makes the fake answer model lookups — the hook
// enrichment tests use.
func (p *Fake) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	if p.ModelInfoFn == nil {
		return nil, fmt.Errorf("no model info scripted")
	}
	info := p.ModelInfoFn(model)
	if info == nil {
		return nil, fmt.Errorf("model %q not in the provider's listing", model)
	}
	return info, nil
}

func (p *Fake) ID() string {
	return p.id
}

// AddResponse adds a scripted response.
func (p *Fake) AddResponse(resp ChatResponse) {
	p.responses = append(p.responses, resp)
}

// AddTextResponse adds a simple text response.
func (p *Fake) AddTextResponse(text string) {
	p.responses = append(p.responses, ChatResponse{
		Choices: []Choice{{
			Message:      Message{Role: "assistant", Content: text},
			FinishReason: FinishStop,
		}},
		Usage: Usage{
			PromptTokens:     EstimateTokens(text),
			CompletionTokens: EstimateTokens(text),
		},
	})
}

// AddToolCallResponse adds a response with tool calls.
func (p *Fake) AddToolCallResponse(toolCalls []ToolCall) {
	p.responses = append(p.responses, ChatResponse{
		Choices: []Choice{{
			Message:      Message{Role: "assistant", ToolCalls: toolCalls},
			FinishReason: FinishToolCalls,
		}},
	})
}

// Requests returns all recorded requests.
func (p *Fake) Requests() []ChatRequest {
	return p.requests
}

// CallCount returns the number of calls made.
func (p *Fake) CallCount() int {
	return p.callCount
}

// Chat sends a chat request.
func (p *Fake) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	p.requests = append(p.requests, req)
	p.callCount++
	// ScriptFunc takes priority over pre-scripted responses
	if p.ScriptFunc != nil {
		if resp := p.ScriptFunc(req, p.callCount); resp != nil {
			return *resp, nil
		}
	}
	if len(p.responses) == 0 {
		return ChatResponse{}, errors.New("no more scripted responses")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

// ChatStream sends a streaming chat request.
//
// The same answer as Chat, delivered a piece at a time. It used to serve only
// the pre-scripted queue and ignore ScriptFunc — which is how every test that
// scripts by prompt (reviewer, judge, triager) silently stopped working the
// moment a run streamed, and why nothing caught that five of the six run kinds
// never wired their streaming callbacks at all. A double that behaves
// differently on the path under test is not a double.
func (p *Fake) ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error) {
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(resp.Choices) > 0 {
		// Reasoning first, then the answer: that is the order a model produces
		// them in, and a test of the split depends on it.
		for _, d := range []Delta{
			{Reasoning: resp.Choices[0].Message.Reasoning},
			{Text: resp.Choices[0].Message.Content},
		} {
			if d.Reasoning == "" && d.Text == "" {
				continue
			}
			select {
			case ch <- d:
			case <-ctx.Done():
				return ChatResponse{}, ctx.Err()
			}
		}
	}
	select {
	case ch <- Delta{Done: true}:
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	}
	return resp, nil
}

// Models returns available models.
func (p *Fake) Models(ctx context.Context) ([]string, error) {
	if len(p.models) > 0 {
		return p.models, nil
	}
	return []string{"fake-model"}, nil
}

// SetModels sets the available models.
func (p *Fake) SetModels(models []string) {
	p.models = models
}

// providerComplaint extracts why a response carried no choices.
//
// The shapes differ and none of them are documented together: OpenAI-compatible
// servers use {"error":{"message":...}}, some return a bare {"error":"..."},
// and OpenRouter adds a provider-specific {"metadata":{"raw":...}} underneath.
// Whichever arrived beats "no choices", which named our own parser rather than
// the cause and sent the reader looking in the wrong program.
func providerComplaint(body []byte) string {
	msg, _ := providerFailure(body)
	return msg
}

// providerFailure extracts why a response carried no choices, and which kind of
// failure it is so the retry policy can act on it.
//
// The status the upstream reported is inside the body, not on the HTTP response:
// a 504 from the model's own provider reaches us as a 200 from OpenRouter. It is
// the difference between a run that waits a moment and one that dies.
func providerFailure(body []byte) (string, error) {
	kind := error(ErrInvalidResponse)
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Error) > 0 {
		var obj struct {
			Message  string          `json:"message"`
			Code     json.RawMessage `json:"code"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		}
		if json.Unmarshal(envelope.Error, &obj) == nil && obj.Message != "" {
			msg := obj.Message
			if len(obj.Code) > 0 && string(obj.Code) != "null" {
				code := strings.Trim(string(obj.Code), `"`)
				msg += " (" + code + ")"
				kind = classifyUpstream(code, kind)
			}
			if obj.Metadata.Raw != "" {
				msg += ": " + truncateForError(obj.Metadata.Raw)
			}
			// Bounded here too: a provider that returns a novel must not put the
			// whole thing in a run's failure banner.
			return truncateForError(msg), kind
		}
		var plain string
		if json.Unmarshal(envelope.Error, &plain) == nil && plain != "" {
			return plain, kind
		}
		return truncateForError(string(envelope.Error)), kind
	}
	// No error object either. The body is the only evidence there is, and an
	// empty one is itself worth saying out loud.
	if len(bytes.TrimSpace(body)) == 0 {
		return "no choices and an empty body", kind
	}
	return "no choices; the server said: " + truncateForError(string(body)), kind
}

// classifyUpstream turns the status the provider reported into the kind of
// failure it is. Anything else keeps the caller's classification: a code we do
// not recognise is not a reason to retry forever.
func classifyUpstream(code string, fallback error) error {
	switch code {
	case "429":
		return ErrRateLimit
	case "500", "502", "503", "504", "408", "522", "524":
		// The upstream was reachable and could not answer in time. That is what
		// the retry policy exists for.
		return ErrProviderUnavailable
	}
	return fallback
}

func truncateForError(s string) string {
	const max = 400
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
