// Package strategy implements the duck modes as conversation scripts.
// v0.1 implements only solo; the rest come in later phases.
package strategy

import (
	"context"
	"fmt"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/tools"
)

// SoloScript returns the solo mode script.
func SoloScript() *Script {
	return &Script{
		Name: "solo",
		Turns: []Turn{
			{
				Role:     config.RoleImplementer,
				Toolbelt: "full",
				Contract: "edits",
				MaxTurns: 24,
			},
		},
		Until:     `gate == "green"`,
		MaxRounds: 3,
	}
}

// Script is a conversation script.
type Script struct {
	Name      string
	Turns     []Turn
	Until     string
	MaxRounds int
}

// Turn is a script turn.
type Turn struct {
	Role      config.Role
	Duckling  config.DucklingID
	Toolbelt  string // "full", "read-only", or a comma-separated list
	Contract  string
	MaxTurns  int
	Anonymize bool
}

// ResolveToolbelt resolves the toolbelt string to a list of tool names.
func (t *Turn) ResolveToolbelt(registry *tools.Registry) ([]string, error) {
	switch t.Toolbelt {
	case "full":
		return registry.List(), nil
	case "read-only":
		return []string{"fs_read", "fs_search", "git_diff", "verify_run", "artifact_read"}, nil
	default:
		// Comma-separated list
		var result []string
		for _, name := range splitComma(t.Toolbelt) {
			if _, err := registry.Get(name); err != nil {
				return nil, fmt.Errorf("unknown tool %q in toolbelt", name)
			}
			result = append(result, name)
		}
		return result, nil
	}
}

func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// ExecuteSolo executes the solo mode.
func ExecuteSolo(ctx context.Context, params *ExecuteParams) (*ExecuteResult, error) {
	script := SoloScript()
	return ExecuteScript(ctx, script, params)
}

// ExecuteParams are the parameters for executing a script.
type ExecuteParams struct {
	ProjectRoot string
	TaskID      string
	Prompt      string
	AgentLoop   *agent.Loop
	ExecContext *tools.ExecContext
	Rounds      int
}

// ExecuteResult is the result of executing a script.
type ExecuteResult struct {
	Text     string
	Rounds   int
	Outcome  *agent.Outcome
	Error    error
}

// ExecuteScript executes a conversation script.
func ExecuteScript(ctx context.Context, script *Script, params *ExecuteParams) (*ExecuteResult, error) {
	result := &ExecuteResult{}
	maxRounds := params.Rounds
	if maxRounds <= 0 {
		maxRounds = script.MaxRounds
	}
	if maxRounds <= 0 {
		maxRounds = 3
	}

	for round := 1; round <= maxRounds; round++ {
		result.Rounds = round
		for _, turn := range script.Turns {
			// Resolve toolbelt
			toolbelt, err := turn.ResolveToolbelt(params.AgentLoop.Registry)
			if err != nil {
				result.Error = err
				return result, err
			}

			// Build agent turn
			agentTurn := &agent.Turn{
				Role:      turn.Role,
				Duckling:  turn.Duckling,
				Prompt:    params.Prompt,
				Toolbelt:  toolbelt,
				Contract:  turn.Contract,
				MaxTurns:  turn.MaxTurns,
				Anonymize: turn.Anonymize,
			}

			// Run the turn
			outcome, err := agent.RunTurn(ctx, params.AgentLoop, agentTurn, params.ExecContext)
			if err != nil {
				result.Error = err
				return result, err
			}
			result.Outcome = outcome
			result.Text = outcome.Text
		}

		// Check Until condition
		// For solo, this is: gate == "green"
		// The actual gate check is done by the orchestrator after the script
		// For now, we break after one round if no error
		break
	}

	return result, nil
}
