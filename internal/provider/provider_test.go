package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCostCalculator(t *testing.T) {
	c := CostCalculator{InputPerMTok: 0.20, OutputPerMTok: 0.60}
	usage := Usage{PromptTokens: 1000000, CompletionTokens: 500000}
	cost := c.Cost(usage)
	expected := 0.20 + 0.30
	if cost != expected {
		t.Errorf("Cost() = %f, want %f", cost, expected)
	}
}

func TestCostCalculatorProviderCost(t *testing.T) {
	c := CostCalculator{InputPerMTok: 0.20, OutputPerMTok: 0.60}
	usage := Usage{PromptTokens: 1000000, CompletionTokens: 500000, CostUSD: 0.99}
	cost := c.Cost(usage)
	if cost != 0.99 {
		t.Errorf("Cost() = %f, want 0.99 (provider cost wins)", cost)
	}
}

func TestEstimateTokens(t *testing.T) {
	text := "hello world this is a test"
	est := EstimateTokens(text)
	if est != len(text)/4 {
		t.Errorf("EstimateTokens() = %d, want %d", est, len(text)/4)
	}
}

func TestIsTransient(t *testing.T) {
	if !IsTransient(ErrRateLimit) {
		t.Error("ErrRateLimit should be transient")
	}
	if !IsTransient(ErrProviderUnavailable) {
		t.Error("ErrProviderUnavailable should be transient")
	}
	if IsTransient(ErrAuth) {
		t.Error("ErrAuth should not be transient")
	}
	if IsTransient(nil) {
		t.Error("nil should not be transient")
	}
}

func TestRetry(t *testing.T) {
	ctx := context.Background()
	policy := RetryPolicy{MaxAttempts: 3, InitialWait: 1 * time.Millisecond, MaxWait: 10 * time.Millisecond}

	// Success on first try
	calls := 0
	err := Retry(ctx, policy, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("Retry should succeed: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}

	// Success after transient failure
	calls = 0
	err = Retry(ctx, policy, func() error {
		calls++
		if calls < 2 {
			return ErrRateLimit
		}
		return nil
	})
	if err != nil {
		t.Errorf("Retry should succeed after transient: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}

	// Non-transient error fails immediately
	calls = 0
	err = Retry(ctx, policy, func() error {
		calls++
		return ErrAuth
	})
	if !errors.Is(err, ErrAuth) {
		t.Errorf("Retry should fail with ErrAuth: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on non-transient)", calls)
	}

	// Exhausted attempts
	calls = 0
	err = Retry(ctx, policy, func() error {
		calls++
		return ErrRateLimit
	})
	if err == nil {
		t.Error("Retry should fail after exhausted attempts")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestRedactHeaders(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Bearer sk-secret",
		"Content-Type":  "application/json",
		"X-API-Key":     "secret",
	}
	redacted := RedactHeaders(headers)
	if redacted["Authorization"] != "[redacted]" {
		t.Errorf("Authorization should be redacted: %s", redacted["Authorization"])
	}
	if redacted["Content-Type"] != "application/json" {
		t.Errorf("Content-Type should not be redacted: %s", redacted["Content-Type"])
	}
	if redacted["X-API-Key"] != "[redacted]" {
		t.Errorf("X-API-Key should be redacted: %s", redacted["X-API-Key"])
	}
}

func TestFinishReasonHelpers(t *testing.T) {
	if !IsToolCalls(FinishToolCalls) {
		t.Error("IsToolCalls(FinishToolCalls) should be true")
	}
	if !IsStop(FinishStop) {
		t.Error("IsStop(FinishStop) should be true")
	}
	if !IsLength(FinishLength) {
		t.Error("IsLength(FinishLength) should be true")
	}
}

func TestFakeProvider(t *testing.T) {
	fake := NewFake("test")
	fake.AddTextResponse("hello")

	ctx := context.Background()
	resp, err := fake.Chat(ctx, ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Errorf("Content = %q, want %q", resp.Choices[0].Message.Content, "hello")
	}
	if fake.CallCount() != 1 {
		t.Errorf("CallCount = %d, want 1", fake.CallCount())
	}
	if len(fake.Requests()) != 1 {
		t.Errorf("Requests = %d, want 1", len(fake.Requests()))
	}
}

func TestFakeProviderStream(t *testing.T) {
	fake := NewFake("test")
	fake.AddTextResponse("hi")

	ctx := context.Background()
	ch := make(chan Delta, 10)
	resp, err := fake.ChatStream(ctx, ChatRequest{Model: "test"}, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)
	var deltas []Delta
	for d := range ch {
		deltas = append(deltas, d)
	}
	if len(deltas) < 2 {
		t.Errorf("deltas = %d, want at least 2", len(deltas))
	}
	if resp.Choices[0].Message.Content != "hi" {
		t.Errorf("Content = %q, want %q", resp.Choices[0].Message.Content, "hi")
	}
}
