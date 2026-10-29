package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCodexJWT is an unsigned token with the claims the subscription reads.
func fakeCodexJWT(exp time.Time) string {
	enc := func(v interface{}) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(map[string]interface{}{
		"exp":                            exp.Unix(),
		"https://api.openai.com/profile": map[string]string{"email": "duck@example.com"},
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "acct-1", "chatgpt_plan_type": "pro",
		},
	}) + ".sig"
}

func signedInCreds(t *testing.T, exp time.Time) *CodexCredentials {
	t.Helper()
	c := NewCodexCredentials(filepath.Join(t.TempDir(), "codex", "sub.json"))
	if err := c.Save(CodexTokens{AccessToken: fakeCodexJWT(exp), RefreshToken: "rt-1"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
	return b.String()
}

func codexServer(t *testing.T, handler http.HandlerFunc) (*Codex, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewCodex("sub", srv.URL, signedInCreds(t, time.Now().Add(time.Hour)), WithCodexStallTimeout(5*time.Second))
	return p, srv
}

func TestCodexRequestBodyTranslatesTheConversation(t *testing.T) {
	call := ToolCall{ID: "call_1", Type: "function"}
	call.Function.Name, call.Function.Arguments = "fs_read", `{"path":"a.go"}`
	req := ChatRequest{
		Model: "gpt-6.1-sol:high",
		Messages: []Message{
			{Role: "system", Content: "You are the architect."},
			{Role: "system", Content: "Be brief."},
			{Role: "user", Content: "Look at this.", Images: []string{"data:image/png;base64,AAAA"}},
			{Role: "assistant", Content: "Reading.", ToolCalls: []ToolCall{call}},
			{Role: "tool", ToolCallID: "call_1", Content: "package a"},
			{Role: "system", Content: "Budget: two turns left."},
		},
		Tools:       []Tool{{Type: "function", Function: ToolFunction{Name: "fs_read", Description: "read", Parameters: map[string]interface{}{"type": "object"}}}},
		ToolChoice:  map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "fs_read"}},
		MaxTokens:   intPtr(100),
		Temperature: floatPtr(0.2),
		Extra:       map[string]interface{}{"chat_template_kwargs": map[string]interface{}{"enable_thinking": false}},
	}
	body, err := codexRequestBody(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	var got map[string]interface{}
	_ = json.Unmarshal(raw, &got)

	if got["model"] != "gpt-6.1-sol" || got["store"] != false || got["stream"] != true {
		t.Fatalf("model/store/stream = %v/%v/%v", got["model"], got["store"], got["stream"])
	}
	if got["instructions"] != "You are the architect.\n\nBe brief." {
		t.Fatalf("instructions = %q", got["instructions"])
	}
	// The suffix wins over the seat's thinking-off request.
	if r := got["reasoning"].(map[string]interface{}); r["effort"] != "high" || r["summary"] != "auto" {
		t.Fatalf("reasoning = %v", r)
	}
	// Chat Completions controls the subscription refuses never reach it, nor
	// extras meant for other servers.
	for _, k := range []string{"max_tokens", "max_output_tokens", "temperature", "chat_template_kwargs", "messages"} {
		if _, ok := got[k]; ok {
			t.Errorf("request carries %q", k)
		}
	}
	if tc := got["tool_choice"].(map[string]interface{}); tc["type"] != "function" || tc["name"] != "fs_read" {
		t.Fatalf("tool_choice = %v", tc)
	}
	tool := got["tools"].([]interface{})[0].(map[string]interface{})
	if tool["name"] != "fs_read" || tool["type"] != "function" || tool["parameters"] == nil {
		t.Fatalf("tool = %v", tool)
	}
	want := `[{"content":[{"text":"Look at this.","type":"input_text"},{"image_url":"data:image/png;base64,AAAA","type":"input_image"}],"role":"user","type":"message"},` +
		`{"content":[{"text":"Reading.","type":"output_text"}],"role":"assistant","type":"message"},` +
		`{"arguments":"{\"path\":\"a.go\"}","call_id":"call_1","name":"fs_read","type":"function_call"},` +
		`{"call_id":"call_1","output":"package a","type":"function_call_output"},` +
		`{"content":[{"text":"Budget: two turns left.","type":"input_text"}],"role":"developer","type":"message"}]`
	if in, _ := json.Marshal(got["input"]); string(in) != want {
		t.Fatalf("input =\n%s\nwant\n%s", in, want)
	}
}

func TestCodexEffort(t *testing.T) {
	off := map[string]interface{}{"reasoning": map[string]interface{}{"enabled": false}}
	cases := []struct {
		model string
		extra map[string]interface{}
		want  string
	}{
		{"gpt-6-luna", nil, ""},
		{"gpt-6-luna:xhigh", nil, "xhigh"},
		{"gpt-6-luna", off, "low"},
		{"gpt-6-luna", map[string]interface{}{"reasoning": map[string]interface{}{"effort": "medium"}}, "medium"},
	}
	for _, c := range cases {
		_, suffix := splitCodexModel(c.model)
		if got := codexEffort(suffix, c.extra); got != c.want {
			t.Errorf("%s %v: effort %q, want %q", c.model, c.extra, got, c.want)
		}
	}
	body, _ := codexRequestBody(ChatRequest{Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "hi"}}})
	if r := body["reasoning"].(map[string]interface{}); r["effort"] != nil {
		t.Fatalf("no effort asked, sent %v — the model's own default should apply", r)
	}
	if body["instructions"] == "" {
		t.Fatal("instructions must never be empty")
	}
}

func TestCodexStreamsAToolCall(t *testing.T) {
	var gotHeaders http.Header
	var gotBody map[string]interface{}
	p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotHeaders = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`{"type":"response.created","response":{"id":"resp_1"}}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"add","call_id":"call_9","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{\"a\":2"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"add","arguments":"{\"a\":2,\"b\":3}","status":"completed"}}`,
			`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-6-luna","status":"completed","output":[],"usage":{"input_tokens":141,"output_tokens":21,"total_tokens":162,"output_tokens_details":{"reasoning_tokens":7}}}}`,
		))
	})
	ch := make(chan Delta, 16)
	resp, err := p.ChatStream(context.Background(), ChatRequest{Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "add"}}}, ch)
	if err != nil {
		t.Fatal(err)
	}
	if gotHeaders.Get("Authorization") == "" || gotHeaders.Get("ChatGPT-Account-ID") != "acct-1" || gotHeaders.Get("originator") != "ducklab" {
		t.Fatalf("headers: account %q originator %q", gotHeaders.Get("ChatGPT-Account-ID"), gotHeaders.Get("originator"))
	}
	if gotBody["store"] != false {
		t.Fatalf("store = %v", gotBody["store"])
	}
	if resp.FinishReason != FinishToolCalls || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("finish %q calls %v", resp.FinishReason, resp.Choices[0].Message.ToolCalls)
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call_9" || tc.Function.Name != "add" || tc.Function.Arguments != `{"a":2,"b":3}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if u := resp.Usage; u.PromptTokens != 141 || u.CompletionTokens != 21 || u.TotalTokens != 162 || u.ReasoningTokens != 7 {
		t.Fatalf("usage = %+v", u)
	}
	close(ch)
	var sawTool, sawDone bool
	for d := range ch {
		sawTool = sawTool || d.ToolName == "add"
		sawDone = sawDone || d.Done
	}
	if !sawTool || !sawDone {
		t.Fatalf("deltas: tool %v done %v", sawTool, sawDone)
	}
}

func TestCodexStreamsTextAndReasoning(t *testing.T) {
	p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`{"type":"response.reasoning_summary_text.delta","delta":"Thinking about sums."}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"Thinking about sums."}]}}`,
			`{"type":"response.output_text.delta","delta":"2 + 3 "}`,
			`{"type":"response.output_text.delta","delta":"= 5."}`,
			`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"2 + 3 = 5."}]}}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		))
	})
	ch := make(chan Delta, 16)
	resp, err := p.ChatStream(context.Background(), ChatRequest{Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "sum"}}}, ch)
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Choices[0].Message
	if m.Content != "2 + 3 = 5." || m.Reasoning != "Thinking about sums." || resp.FinishReason != FinishStop {
		t.Fatalf("content %q reasoning %q finish %q", m.Content, m.Reasoning, resp.FinishReason)
	}
	close(ch)
	var text, reasoning string
	for d := range ch {
		text += d.Text
		reasoning += d.Reasoning
	}
	if text != "2 + 3 = 5." || reasoning != "Thinking about sums." {
		t.Fatalf("streamed text %q reasoning %q", text, reasoning)
	}
	// Chat is the same stream, assembled.
	resp, err = p.Chat(context.Background(), ChatRequest{Model: "gpt-6-luna", Messages: []Message{{Role: "user", Content: "sum"}}})
	if err != nil || resp.Choices[0].Message.Content != "2 + 3 = 5." {
		t.Fatalf("Chat = %v, %v", resp, err)
	}
}

func TestCodexIncompleteIsTruncation(t *testing.T) {
	p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`{"type":"response.output_text.delta","delta":"half"}`,
			`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		))
	})
	resp, err := p.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil || resp.FinishReason != FinishLength || resp.Choices[0].Message.Content != "half" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func TestCodexErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		stream string
		want   error
		says   string
	}{
		{name: "usage window spent", status: 429, header: map[string]string{"x-codex-primary-reset-after-seconds": "7200"},
			body: `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`, want: ErrRateLimit, says: "resets in 2h0m0s"},
		{name: "model not in plan", status: 400, body: `{"detail":"The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.","error":{"code":"model_not_found","message":"The 'gpt-5.5' model is not supported"}}`, want: ErrChatUnavailable},
		{name: "sign-in refused", status: 401, body: `{"error":{"message":"token revoked"}}`, want: ErrAuth, says: "ducklab provider login sub"},
		{name: "server weather", status: 502, body: `bad gateway`, want: ErrProviderUnavailable},
		{name: "failed mid-stream", status: 200, stream: sse(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`), want: ErrProviderUnavailable},
		{name: "failed on a limit", status: 200, stream: sse(`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"slow down"}}}`), want: ErrRateLimit},
		{name: "cut before completion", status: 200, stream: sse(`{"type":"response.output_text.delta","delta":"hal"}`), want: ErrProviderUnavailable, says: "before the response completed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				if c.stream != "" {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, c.stream)
					return
				}
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			})
			_, err := p.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.says != "" && !strings.Contains(err.Error(), c.says) {
				t.Fatalf("err = %v, want it to say %q", err, c.says)
			}
		})
	}
}

func TestCodexNotSignedInSaysHowToSignIn(t *testing.T) {
	p := NewCodex("sub", "http://127.0.0.1:1", NewCodexCredentials(filepath.Join(t.TempDir(), "none.json")))
	_, err := p.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "ducklab provider login sub") {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Models(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("Models err = %v", err)
	}
}

func TestCodexRefreshesAnExpiringTokenAndKeepsTheRotation(t *testing.T) {
	var refreshes atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-1" || r.Form.Get("client_id") != codexClientID {
			t.Errorf("refresh form = %v", r.Form)
		}
		refreshes.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"access_token": fakeCodexJWT(time.Now().Add(time.Hour)), "refresh_token": "rt-2"})
	}))
	defer issuer.Close()
	creds := signedInCreds(t, time.Now().Add(time.Minute)) // inside the refresh margin
	creds.issuer = issuer.URL

	got, err := creds.Fresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "rt-2" {
		t.Fatalf("refresh token = %q, want the rotated one", got.RefreshToken)
	}
	// Stored, owner-only, and read back by a fresh store (another process).
	st, err := os.Stat(creds.Path())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", st, err)
	}
	again, err := NewCodexCredentials(creds.Path()).Load()
	if err != nil || again.RefreshToken != "rt-2" {
		t.Fatalf("reloaded = %+v, %v", again, err)
	}
	// Fresh now: no second refresh.
	if _, err := creds.Fresh(context.Background()); err != nil || refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, err %v", refreshes.Load(), err)
	}
}

func TestCodexDeadGrantAsksForANewSignIn(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"refresh_token_reused"}`)
	}))
	defer issuer.Close()
	creds := signedInCreds(t, time.Now().Add(-time.Hour))
	creds.issuer = issuer.URL
	p := NewCodex("sub", "http://127.0.0.1:1", creds)
	_, err := p.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "ducklab provider login sub") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexCatalog(t *testing.T) {
	var calls atomic.Int32
	p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("ChatGPT-Account-ID") != "acct-1" {
			t.Errorf("catalog request %s account %q", r.URL.Path, r.Header.Get("ChatGPT-Account-ID"))
		}
		calls.Add(1)
		io.WriteString(w, `{"models":[
			{"slug":"gpt-6.1-sol","visibility":"list","context_window":500000,"input_modalities":["text","image"]},
			{"slug":"gpt-reserve","visibility":"hide","context_window":272000},
			{"slug":"gpt-6-luna","visibility":"list","context_window":272000,"input_modalities":["text"]}]}`)
	})
	models, err := p.Models(context.Background())
	if err != nil || strings.Join(models, ",") != "gpt-6.1-sol,gpt-6-luna" {
		t.Fatalf("models = %v, %v", models, err)
	}
	info, err := p.ModelInfo(context.Background(), "gpt-6.1-sol:high")
	if err != nil || info.ContextTokens != 500000 || info.Vision == nil || !*info.Vision {
		t.Fatalf("info = %+v, %v", info, err)
	}
	info, err = p.ModelInfo(context.Background(), "gpt-6-luna")
	if err != nil || info.Vision == nil || *info.Vision {
		t.Fatalf("luna info = %+v, %v", info, err)
	}
	if _, err := p.ModelInfo(context.Background(), "gpt-5.5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown model err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("catalog fetched %d times, want once (cached)", calls.Load())
	}
}

func TestCodexDeviceLogin(t *testing.T) {
	var polls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			io.WriteString(w, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":"5"}`)
		case "/api/accounts/deviceauth/token":
			if polls.Add(1) < 2 {
				w.WriteHeader(http.StatusForbidden) // not approved yet
				return
			}
			io.WriteString(w, `{"authorization_code":"auth-1","code_verifier":"ver-1"}`)
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "auth-1" || r.Form.Get("code_verifier") != "ver-1" {
				t.Errorf("exchange form = %v", r.Form)
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": fakeCodexJWT(time.Now().Add(time.Hour)), "refresh_token": "rt-new"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	creds := NewCodexCredentials(filepath.Join(t.TempDir(), "codex", "sub.json"))
	creds.issuer = issuer.URL

	code, err := creds.StartDeviceLogin(context.Background())
	if err != nil || code.UserCode != "ABCD-1234" || code.interval != 5*time.Second || !strings.HasSuffix(code.VerificationURL, "/codex/device") {
		t.Fatalf("code = %+v, %v", code, err)
	}
	code.interval = 10 * time.Millisecond
	tokens, err := creds.PollDeviceLogin(context.Background(), code)
	if err != nil || tokens.RefreshToken != "rt-new" {
		t.Fatalf("tokens = %+v, %v", tokens, err)
	}
	acct, err := tokens.Account()
	if err != nil || acct.Email != "duck@example.com" || acct.Plan != "pro" {
		t.Fatalf("account = %+v, %v", acct, err)
	}
	if stored, err := creds.Load(); err != nil || stored.RefreshToken != "rt-new" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }

// Review: a provider rebuilt by a config edit shares the store of the one a
// run still holds, so a refresh token is spent once, however many ask.
func TestCodexStoresAreSharedAndRefreshOnce(t *testing.T) {
	var refreshes atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		time.Sleep(20 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]string{"access_token": fakeCodexJWT(time.Now().Add(time.Hour)), "refresh_token": "rt-2"})
	}))
	defer issuer.Close()
	a := signedInCreds(t, time.Now().Add(time.Minute))
	a.WithIssuer(issuer.URL)
	b := NewCodexCredentials(a.Path())
	if a != b {
		t.Fatal("two stores for one file")
	}
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := b.Fresh(context.Background()); errs <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshed %d times, want once", refreshes.Load())
	}
}

// The server can retire an access token before its expiry: a 401 refreshes
// it and sends the request once more.
func TestCodexRefreshesOnARefusedTokenAndRetries(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"access_token": fakeCodexJWT(time.Now().Add(2 * time.Hour)), "refresh_token": "rt-2"})
	}))
	defer issuer.Close()
	var calls atomic.Int32
	var firstAuth string
	p, _ := codexServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			firstAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"token expired"}}`)
			return
		}
		if r.Header.Get("Authorization") == firstAuth {
			t.Error("retried with the refused token")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"ok"}]}}`, `{"type":"response.completed","response":{}}`))
	})
	p.Credentials().WithIssuer(issuer.URL)
	resp, err := p.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil || resp.Choices[0].Message.Content != "ok" || calls.Load() != 2 {
		t.Fatalf("resp %v err %v calls %d", resp, err, calls.Load())
	}
}

// A refused refresh marks the grant dead: it reads as not signed in, with
// the reason, rather than as signed in until the next call fails.
func TestCodexDeadGrantReadsAsSignedOut(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer issuer.Close()
	creds := signedInCreds(t, time.Now().Add(-time.Hour))
	creds.WithIssuer(issuer.URL)
	if _, err := creds.Fresh(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("refresh err = %v", err)
	}
	_, err := creds.Load()
	if !errors.Is(err, ErrCodexNotLoggedIn) || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("load after a dead grant = %v", err)
	}
}

// A token that does not say when it expires is used until the server refuses
// it — never refreshed (and rotated) on every call.
func TestCodexTokenWithoutExpiryIsNotRefreshed(t *testing.T) {
	creds := NewCodexCredentials(filepath.Join(t.TempDir(), "noexp.json"))
	creds.WithIssuer("http://127.0.0.1:1") // any refresh would fail loudly
	noExp := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	if err := creds.Save(CodexTokens{AccessToken: "e30." + noExp + ".sig", RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Fresh(context.Background()); err != nil {
		t.Fatalf("Fresh = %v", err)
	}
}

// A sign-out that cancels a login while its token exchange is in flight is
// not undone by the save behind it.
func TestCodexCancelledLoginDoesNotSave(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/token":
			io.WriteString(w, `{"authorization_code":"a","code_verifier":"v"}`)
		case "/oauth/token":
			cancel() // the sign-out lands while the exchange answers
			json.NewEncoder(w).Encode(map[string]string{"access_token": fakeCodexJWT(time.Now().Add(time.Hour)), "refresh_token": "rt"})
		}
	}))
	defer issuer.Close()
	creds := NewCodexCredentials(filepath.Join(t.TempDir(), "race.json"))
	creds.WithIssuer(issuer.URL)
	_, err := creds.PollDeviceLogin(ctx, CodexDeviceCode{UserCode: "X", deviceAuthID: "d", interval: time.Millisecond})
	if err == nil {
		t.Fatal("a cancelled login reported success")
	}
	if _, err := os.Stat(creds.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a cancelled login stored tokens: %v", err)
	}
}
