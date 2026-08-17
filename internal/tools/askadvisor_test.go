package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// ask_advisor is the implementer's door to the rubber duck: it answers inline
// and never pauses the run.
func TestAskAdvisorReturnsTheAdviceInline(t *testing.T) {
	var asked string
	ectx := &ExecContext{OnAskAdvisor: func(_ context.Context, q string) (string, error) {
		asked = q
		return "Read lines 300-360 with fs_read, then replace them with fs_write_lines.", nil
	}}
	res, err := (&AskAdvisor{}).Execute(context.Background(), ectx, json.RawMessage(`{"question":"fs_patch keeps failing on tools.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "fs_write_lines") {
		t.Errorf("advice did not come back inline: %+v", res)
	}
	if asked != "fs_patch keeps failing on tools.go" {
		t.Errorf("advisor got %q", asked)
	}
	if ectx.Pending != nil {
		t.Error("ask_advisor must never pause the run")
	}
}

// No advisor seated: the tool says so and points at the self-help path — a
// small seat must not be left guessing why nobody answered.
func TestAskAdvisorWithoutASeatTeachesTheNextMove(t *testing.T) {
	res, err := (&AskAdvisor{}).Execute(context.Background(), &ExecContext{}, json.RawMessage(`{"question":"stuck"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "no advisor is seated") || !strings.Contains(res.Content, "fs_write_lines") {
		t.Errorf("missing-seat reply does not teach: %+v", res)
	}
}

// A failing advisor degrades to "proceed" — the consult is optional, the turn is not.
func TestAskAdvisorFailureDoesNotKillTheTurn(t *testing.T) {
	ectx := &ExecContext{OnAskAdvisor: func(context.Context, string) (string, error) {
		return "", errors.New("provider unavailable")
	}}
	res, err := (&AskAdvisor{}).Execute(context.Background(), ectx, json.RawMessage(`{"question":"stuck"}`))
	if err != nil {
		t.Fatal("a failed consult must be a tool result, not a turn error")
	}
	if !res.IsError || !strings.Contains(res.Content, "best judgement") {
		t.Errorf("failure reply does not release the model: %+v", res)
	}
}

// ask_advisor sits in the implementer's ceiling and is not mutating.
func TestAskAdvisorIsInTheImplementerBelt(t *testing.T) {
	if !RoleAllows(config.RoleImplementer, "ask_advisor") {
		t.Error("implementer belt lacks ask_advisor")
	}
	if (&AskAdvisor{}).Mutating() {
		t.Error("ask_advisor must not count as a write")
	}
}
