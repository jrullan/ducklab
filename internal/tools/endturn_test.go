package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Refusal alone did not change a small seat's behaviour: 29 identical
// failing calls in a row, 23 minutes. At the sixth the reply's tool use is
// closed and the seat is told to answer.
func TestRepeatedIdenticalFailuresCloseToolUse(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&ArtifactRead{})
	ectx := &ExecContext{ProjectRoot: t.TempDir()}
	ectx.BeginTurn()
	bad := json.RawMessage(`{"kind":"nope"}`)
	var last *Result
	for i := 0; i < RepeatFailEndTurn; i++ {
		last, _ = reg.Execute(context.Background(), ectx, "artifact_read", bad)
	}
	if !last.EndTurn || !strings.Contains(last.Content, "CLOSED") {
		t.Fatalf("the %dth identical failure did not close tool use: %+v", RepeatFailEndTurn, last)
	}
	if !ectx.ToolsClosed {
		t.Fatal("the context did not record that tools are closed")
	}
	// Any further call, even a good one, is refused with the same order.
	more, _ := reg.Execute(context.Background(), ectx, "artifact_read", json.RawMessage(`{"kind":"plan"}`))
	if !more.IsError || !more.EndTurn {
		t.Fatalf("a call after closing was served: %+v", more)
	}
	// A new turn reopens.
	ectx.BeginTurn()
	if ectx.ToolsClosed {
		t.Fatal("BeginTurn did not reopen tool use")
	}
}
