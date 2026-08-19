package tools

import (
	"context"
	"encoding/json"
)

// The team, readable by the models that advise about it.
//
// A consultant asked to suggest a Pair line-up read project.toml by hand,
// found the seats empty (the roster lives in the resolver — global config,
// project overrides, evidence), said so on the record, and reconstructed the
// team from run history. The resolver's answer was one closure away. tools
// is a leaf, so the service injects the summary; when nothing is wired the
// tool says so instead of guessing.

// RosterRead reports the resolved roster and the evidence behind it.
type RosterRead struct{}

func (t *RosterRead) Name() string   { return "roster_read" }
func (t *RosterRead) Mutating() bool { return false }

func (t *RosterRead) Description() string {
	return "Read the resolved team: who is seated per mode (global and this project), each duckling's measured evidence (pass rate, cost, coding index), and the engine's seat suggestions. The source of truth the launchers use — not project.toml alone."
}

func (t *RosterRead) Schema() interface{} {
	return NewSchema()
}

func (t *RosterRead) Execute(ctx context.Context, ectx *ExecContext, _ json.RawMessage) (*Result, error) {
	if ectx.OnRosterRead == nil {
		return ErrorResult("the roster is not readable from this run"), nil
	}
	out, err := ectx.OnRosterRead(ctx)
	if err != nil {
		return ErrorResult("roster: %v", err), nil
	}
	return &Result{Content: out}, nil
}
