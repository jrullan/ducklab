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

// TestFirstScript writes the failing test, and stops when the gate is red.
//
// The condition is inverted on purpose. Every other script drives towards a
// green gate; here red is the goal, because a test that does not fail against
// today's code has asserted nothing. Reusing the solo script made the model
// spend two further rounds trying to make its own new test pass — which it
// cannot, since the write guard allows it only test files, and should not,
// since passing is the failure.
//
// One round. There is no second attempt to loop towards: either a failing test
// was written or it was not, and a person reads it either way.
func TestFirstScript() *Script {
	return &Script{
		Name: "test-first",
		Turns: []Turn{
			{
				Role:     config.RoleImplementer,
				Toolbelt: "full",
				Contract: "edits",
				MaxTurns: 24,
			},
		},
		// One round, so no expression can buy another: the old
		// `gate == "red"` made the round gate look load-bearing, and the
		// service wired a Gate in — the suite ran there AND again as the
		// stage's own "after" measurement, back to back on an unchanged
		// tree. Minutes per test-first, measuring nothing twice. The pair
		// script's gate stays: with two rounds, green-means-retry works.
		Until:     "round == 1",
		MaxRounds: 1,
	}
}

// TestFirstScriptFor picks the test-writing conversation by mode.
//
// pair adds a decorrelated reviewer over the TEST: the failing test is the
// specification of the build to come, and a second model reading it is worth
// paying for exactly when a person will not read it themselves — the chained
// flow commits it unread. Red stays the goal; the reviewer's approval is of
// the test's quality, not of the code it condemns. Two rounds, because a
// critique of a test the implementer cannot revise is a critique wasted.
func TestFirstScriptFor(mode string) *Script {
	if mode != "pair" {
		return TestFirstScript()
	}
	return &Script{
		Name: "test-first-pair",
		Turns: []Turn{
			{
				Role:     config.RoleImplementer,
				Toolbelt: "full",
				Contract: "edits",
				MaxTurns: 24,
			},
			{
				Role:      config.RoleReviewer,
				Toolbelt:  "full", // narrowed to the reviewer's read-only ceiling
				Contract:  "verdict",
				MaxTurns:  8,
				Anonymize: true,
				OmitRole:  config.RoleImplementer,
			},
		},
		Until:     `gate == "red" and verdict == "approve"`,
		MaxRounds: 2,
	}
}

// Script is a conversation script.
type Script struct {
	Name      string
	Turns     []Turn
	Until     string
	MaxRounds int
	// FragmentPrefix marks an artifact update whose architect replies are
	// partial H2 patches. The scheduler materializes those patches before a
	// critic sees them and before returning the proposal; otherwise a revision
	// of REQ-001 makes unchanged REQ-006/009 appear deleted in the next round.
	FragmentPrefix string
	// TurnIndexBase offsets every turn's Index. A sectioned document update
	// runs MANY small conversations in one run; without distinct
	// coordinates their streamed text lands in the same delta key and the
	// lanes concatenate strangers.
	TurnIndexBase int
	// RevisionOpensNextRound skips the first turn of every round after the
	// first: the previous round's closing revision is the draft the next
	// round's critics judge (council).
	RevisionOpensNextRound bool
}

// Turn is a script turn.
type Turn struct {
	Role     config.Role
	Duckling config.DucklingID
	Toolbelt string // "full", "read-only", or a comma-separated list
	Contract string
	MaxTurns int
	// Images are data URLs for a vision duckling — a bug's screenshots on a
	// triage turn. Carried through to the agent turn untouched.
	Images []string
	// Anonymize hides WHO wrote each prior turn. It does not control whether
	// the transcript appears at all — those are different questions, and
	// conflating them meant council's reviewer was asked to review nothing.
	Anonymize bool
	// OmitRole drops a role's turns from what this turn sees. pair omits the
	// implementer so the reviewer cannot adopt the author's rationalisation;
	// council's critics omit each other, so N critics stay N opinions instead
	// of one critique read N times. The draft itself always shows: the
	// architect's draft IS the artifact under review.
	OmitRole config.Role
	// Persona narrows the role's system prompt to the situation. "critic"
	// tells a reviewer it is reading a proposed DOCUMENT that lives only in
	// the conversation — the code-review framing sent one hunting for a diff
	// that by design does not exist, and its tools truthfully told it the
	// wrong story: git_diff empty, artifact_read serving the old document.
	Persona string
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

// ReleaseScript is the one turn a release involves a model in (05 §9.1).
//
// The scribe writes prose and touches nothing: what shipped is collected
// deterministically before this runs, and the document's inventory is rendered
// from that record rather than from anything the model says.
func ReleaseScript() *Script {
	return &Script{
		Name: "release",
		Turns: []Turn{{
			Role:     config.RoleScribe,
			Toolbelt: "full", // narrowed to the scribe's read-only ceiling
			Contract: "freeform",
			// Room to glance at a task or two, not to read the inventory
			// one call at a time — the prompt carries each task's summary
			// so the scribe need not.
			MaxTurns: 8,
		}},
		Until:     "round == 1",
		MaxRounds: 1,
	}
}
