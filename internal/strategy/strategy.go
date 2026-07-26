// Package strategy implements the duck modes as conversation scripts.
// v0.1 implements only solo; the rest come in later phases.
package strategy

import (
	"fmt"

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
