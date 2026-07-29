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
// SoloArtifactScript is one architect, drafting alone.
//
// A deviation from 05 §4.4, which names council as the artifact mode. It is
// offered because the choice is real: council's value is a second model
// critiquing the draft, and that is worth its cost on a first draft of
// requirements and often not worth it on a small revision. Council stays the
// default; this is the cheaper answer for someone who knows they want it.
//
// No reviewer turn, so no verdict to wait on: one round, one draft.
func SoloArtifactScript(prefix string) *Script {
	return &Script{
		Name: "solo",
		Turns: []Turn{
			{
				Role:     config.RoleArchitect,
				Toolbelt: "full",
				Contract: fmt.Sprintf("markdown_sections:%s", prefix),
				MaxTurns: 12,
			},
		},
		// MaxRounds is 1, so the loop stops after the first round whatever this
		// says. It has to compile, and it must not wait on a verdict: there is
		// no reviewer to produce one.
		Until:     `round == 1`,
		MaxRounds: 1,
	}
}

// ArtifactScript returns the script a stage should run for a mode.
//
// Unknown modes fall back to council rather than failing: the default is the
// spec's, and a typo should not stop someone drafting.
func ArtifactScript(prefix, mode string) *Script {
	if mode == "solo" {
		return SoloArtifactScript(prefix)
	}
	return CouncilScript(prefix)
}

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
