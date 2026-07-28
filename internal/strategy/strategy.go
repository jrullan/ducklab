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
	Role     config.Role
	Duckling config.DucklingID
	Toolbelt string // "full", "read-only", or a comma-separated list
	Contract string
	MaxTurns int
	// Anonymize hides WHO wrote each prior turn. It does not control whether
	// the transcript appears at all — those are different questions, and
	// conflating them meant council's reviewer was asked to review nothing.
	Anonymize bool
	// OmitRole drops a role's turns from what this turn sees. pair omits the
	// implementer so the reviewer cannot adopt the author's rationalisation;
	// council omits nothing, because the architect's draft IS the artifact
	// under review.
	OmitRole config.Role
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

// ReviewScript returns the review stage's conversation (05 §1).
//
// Solo is one reviewer reading the diff. Council adds a second reviewer and a
// human turn, for work where one opinion is not enough.
//
// The reviewer sees the diff and nothing else. It is reading committed work,
// so there is no implementer transcript to be swayed by — and there will not
// be one later either, because a review that adopted the author's reasoning
// would stop being a second reading (I7).
func ReviewScript(council bool) *Script {
	turns := []Turn{{
		Role:     config.RoleReviewer,
		Toolbelt: "full", // narrowed to the reviewer's read-only ceiling
		Contract: "verdict",
		MaxTurns: 8,
	}}
	if council {
		turns = append(turns,
			Turn{
				// Conditional: the scheduler skips it unless a human is
				// available (05 §4.4).
				Role:     config.RoleHuman,
				Contract: "freeform",
				MaxTurns: 1,
			},
			Turn{
				Role:     config.RoleReviewer,
				Toolbelt: "full",
				Contract: "verdict",
				MaxTurns: 8,
			},
		)
	}
	return &Script{
		Name:  "review",
		Turns: turns,
		// One pass. A review is a reading, not a negotiation: re-reading until
		// the verdict changes is how a review becomes a rubber stamp.
		//
		// Said with the identifiers the expression language already has. Its
		// set is closed on purpose (05 §3.3), and adding a `true` literal to
		// spell a one-round script would be widening a language to avoid
		// learning it.
		Until:     "round == 1",
		MaxRounds: 1,
	}
}
