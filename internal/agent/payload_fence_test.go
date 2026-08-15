package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// A payload whose CONTENT contains the protocol's own fences — the exact case
// of editing agent.go — used to break the parser two ways: a bare ``` inside
// the value truncated it at the first fence, and a ```ducklab line inside it
// looked like a second envelope, so the whole tool call was dropped. The
// id-tagged terminator (```payload:N:end) plus stripping payloads before
// locating the envelope fix both. This is the regression that aborted every
// build that touched agent.go.
func TestPayloadCarryingProtocolFencesIsExtractedWhole(t *testing.T) {
	// The kind of content a duckling writes back into agent.go: it mentions a
	// ```ducklab fence and has a bare ``` fence line of its own.
	payload := strings.Join([]string{
		"package agent",
		"",
		"// The protocol opens with ```ducklab and the model must not stop early.",
		"const example = `raw string with one backtick pair`",
		"```", // a bare fence line — the old early terminator
		"func main() {}",
	}, "\n")

	text := "I'll rewrite it.\n" +
		"```ducklab\n" +
		`{"tool": "fs_write", "args": {"path": "internal/agent/agent.go", "content": "@payload:1"}}` + "\n" +
		"```\n" +
		"```payload:1\n" +
		payload + "\n" +
		"```payload:1:end\n"

	tc, remaining := parseTextToolCall(text)
	if tc == nil {
		t.Fatal("parser returned nil: it tripped on the protocol fences inside the payload")
	}
	if tc.Name != "fs_write" {
		t.Fatalf("tool = %q, want fs_write", tc.Name)
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("args did not decode: %v", err)
	}
	if got, _ := args["content"].(string); got != payload {
		t.Errorf("payload was altered.\n got: %q\nwant: %q", got, payload)
	}
	if strings.Contains(remaining, "```ducklab") || strings.Contains(remaining, "payload:1") {
		t.Errorf("remaining prose still holds protocol blocks: %q", remaining)
	}
}

// The old failure mode in isolation: with a bare closer, a bare ``` line inside
// the value ended the block early. With the id-tagged terminator the value
// survives intact even when it is nothing but fences.
func TestPayloadTerminatorIsNotABareFence(t *testing.T) {
	payload := "line one\n```\nline after a bare fence\n"

	text := "```ducklab\n" +
		`{"tool": "fs_write", "args": {"path": "a.md", "content": "@payload:1"}}` + "\n" +
		"```\n" +
		"```payload:1\n" +
		payload +
		"```payload:1:end\n"

	tc, _ := parseTextToolCall(text)
	if tc == nil {
		t.Fatal("parser returned nil on a payload that is mostly fences")
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("args did not decode: %v", err)
	}
	// The captured value is the payload minus the single trailing newline before
	// the terminator.
	if got, _ := args["content"].(string); got != strings.TrimSuffix(payload, "\n") {
		t.Errorf("payload truncated at a bare fence.\n got: %q\nwant: %q",
			got, strings.TrimSuffix(payload, "\n"))
	}
}

// A message with no ducklab envelope is not a tool call and must round-trip as
// plain text — the fix must not turn ordinary prose into a call.
func TestProseWithoutAnEnvelopeIsNotAToolCall(t *testing.T) {
	text := "Just some prose that happens to mention ```payload: in passing."
	tc, remaining := parseTextToolCall(text)
	if tc != nil {
		t.Fatalf("prose parsed as a tool call: %+v", tc)
	}
	if remaining != text {
		t.Errorf("prose was altered: %q", remaining)
	}
}
