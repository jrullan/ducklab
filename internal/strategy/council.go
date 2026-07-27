package strategy

import (
	"fmt"

	"github.com/jrullan/ducklab/internal/config"
)

// CouncilScript returns the artifact mode used by intake, spec and plan
// (05 §4.4).
//
// No code is written here. The architect drafts, a reviewer critiques the
// draft, and the architect revises. The human turn between them is where the
// rubber-duck premise becomes literal: the user is one of the ducks, not an
// audience for them.
//
// prefix is the section id prefix the artifact expects (REQ, SPEC, M), which
// fixes the architect's output contract.
func CouncilScript(prefix string) *Script {
	contract := fmt.Sprintf("markdown_sections:%s", prefix)
	return &Script{
		Name: "council",
		Turns: []Turn{
			{
				Role:     config.RoleArchitect,
				Toolbelt: "full",
				Contract: contract,
				MaxTurns: 12,
			},
			{
				Role:     config.RoleReviewer,
				Toolbelt: "full", // narrowed to the reviewer's read-only ceiling
				Contract: "verdict",
				MaxTurns: 6,
			},
			{
				// Conditional: the scheduler skips it unless a human is
				// available and the stage asked for one (05 §4.4).
				Role:     config.RoleHuman,
				Contract: "freeform",
				MaxTurns: 1,
			},
			{
				Role:     config.RoleArchitect,
				Toolbelt: "full",
				Contract: contract,
				MaxTurns: 12,
			},
		},
		// Two rounds at most: an artifact that has not converged after a draft,
		// a critique and a revision needs a person, not another lap.
		Until:     `verdict == "approve"`,
		MaxRounds: 2,
	}
}
