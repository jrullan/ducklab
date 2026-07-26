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

// ResolveToolbelt resolves the turn's toolbelt against its ROLE's ceiling.
//
// The role decides what its holder may ever touch (04 §2.5); the turn may only
// narrow that. Previously this returned registry.List() for "full", so the
// script — not the role — chose the toolbelt, and a Turn{Role: reviewer,
// Toolbelt: "full"} would have handed a reviewer fs_write and shell.
func (t *Turn) ResolveToolbelt(registry *tools.Registry) ([]string, error) {
	return registry.NarrowToolbelt(t.Role, t.Toolbelt)
}

// Validate checks a script before it runs. A script that widens a role's
// toolbelt, names an unknown role, or leaves a loop unbounded is rejected here
// rather than midway through a run, when half the work is already done and a
// bad toolbelt has already been handed to a model (04 §2.5, I3).
func (s *Script) Validate(registry *tools.Registry) error {
	if s.Name == "" {
		return fmt.Errorf("script has no name")
	}
	if len(s.Turns) == 0 {
		return fmt.Errorf("script %q has no turns", s.Name)
	}
	if s.MaxRounds <= 0 {
		return fmt.Errorf("script %q: MaxRounds must be > 0 (I3: nothing unbounded)", s.Name)
	}
	for i, turn := range s.Turns {
		if !validRole(turn.Role) {
			return fmt.Errorf("script %q turn %d: unknown role %q", s.Name, i, turn.Role)
		}
		if turn.MaxTurns <= 0 {
			return fmt.Errorf("script %q turn %d (%s): MaxTurns must be > 0 (I3)", s.Name, i, turn.Role)
		}
		if turn.Role == config.RoleHuman {
			continue // a human turn runs no agent loop and needs no toolbelt
		}
		if _, err := turn.ResolveToolbelt(registry); err != nil {
			return fmt.Errorf("script %q turn %d: %w", s.Name, i, err)
		}
	}
	return nil
}

func validRole(r config.Role) bool {
	for _, valid := range config.ValidRoles() {
		if r == valid {
			return true
		}
	}
	return false
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
	Text    string
	Rounds  int
	Outcome *agent.Outcome
	Error   error
}

// ExecuteScript executes a conversation script.
func ExecuteScript(ctx context.Context, script *Script, params *ExecuteParams) (*ExecuteResult, error) {
	result := &ExecuteResult{}
	if err := script.Validate(params.AgentLoop.Registry); err != nil {
		result.Error = err
		return result, err
	}
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
