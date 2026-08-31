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
// PersonaCritic marks a reviewer turn as a document critic: what it reviews
// is a draft that exists only in the conversation, and its system prompt must
// say so or the model spends its turns hunting for a diff.
const PersonaCritic = "critic"
const PersonaPlanManifest = "plan_manifest"

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

// InventoryScript is the mandatory first pass of an adoption survey.
func InventoryScript() *Script {
	return &Script{
		Name:  "survey-inventory",
		Turns: []Turn{{Role: config.RoleArchitect, Toolbelt: "full", Contract: "json:inventory", MaxTurns: 12}},
		Until: `round == 1`, MaxRounds: 1,
	}
}

// ArtifactScript returns the script a stage should run for a mode.
//
// Unknown modes fall back to council rather than failing: the default is the
// spec's, and a typo should not stop someone drafting.
func ArtifactScript(prefix, mode string, critics []config.DucklingID) *Script {
	if mode == "solo" {
		return SoloArtifactScript(prefix)
	}
	return CouncilScript(prefix, critics)
}

// CouncilScript builds a council: one drafts, the others critique, the first
// revises.
//
// critics pins each critique turn to its own duckling, in line-up order. For a
// long time the council seated exactly two — which made it a council in name
// only, and made the third model a person ticked in Settings silently a
// spectator. The product's whole thesis is decorrelation between cheap models;
// a draft read by N different models with N different blind spots is that
// thesis applied to documents. Empty critics seats one unpinned reviewer, the
// roster's own, which is the original shape.
func CouncilScript(prefix string, critics []config.DucklingID) *Script {
	contract := fmt.Sprintf("markdown_sections:%s", prefix)
	var turns []Turn
	if prefix == "M" {
		turns = append(turns, Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "none",
			Contract: "json:plan_manifest",
			MaxTurns: 2,
			Persona:  PersonaPlanManifest,
		})
	}
	turns = append(turns,
		Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "document",
			Contract: contract,
			MaxTurns: 12,
		},
	)
	if len(critics) == 0 {
		critics = []config.DucklingID{""}
	}
	for _, c := range critics {
		turns = append(turns, Turn{
			Role:     config.RoleReviewer,
			Duckling: c,
			Toolbelt: "document", // read-only, minus the gate and the diff: a draft lives in the conversation
			Contract: "verdict",
			MaxTurns: 6,
			Persona:  PersonaCritic,
			// Each critic reads the DRAFT, not the other critics. A critic
			// shown a fellow critic's findings anchors on them, and N critics
			// become one critique read N times — the decorrelation the extra
			// seats exist for, undone by the transcript (I7). The architect's
			// revision turn still sees every critique.
			Anonymize: true,
			OmitRole:  config.RoleReviewer,
		})
	}
	turns = append(turns,
		Turn{
			// Conditional: the scheduler skips it unless a human is
			// available and the stage asked for one (05 §4.4).
			Role:     config.RoleHuman,
			Contract: "freeform",
			MaxTurns: 1,
		},
		Turn{
			Role:     config.RoleArchitect,
			Toolbelt: "document",
			Contract: contract,
			MaxTurns: 12,
		},
	)
	return &Script{
		Name:  "council",
		Turns: turns,
		// Two rounds at most: an artifact that has not converged after a draft,
		// the critiques and a revision needs a person, not another lap. The
		// round's verdict is the WORST across critics — one request-changes
		// among approvals is a request for changes.
		Until:     `verdict == "approve"`,
		MaxRounds: 2,
		// Round 2 opens on the revision round 1 closed with: re-drafting it
		// first cost every council an architect turn per extra round
		// (benchmark run 6: draft → critique → revision → draft again).
		RevisionOpensNextRound: true,
	}
}
