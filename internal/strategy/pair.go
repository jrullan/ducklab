package strategy

import (
	"github.com/jrullan/ducklab/internal/config"
)

// PairScript returns the pair mode script: a driver that edits and a navigator
// that observes (05 §4.2).
//
// The reviewer sees the DIFF, never the implementer's transcript. A reviewer
// that reads the author's rationalisation adopts it, and the second model
// stops being decorrelated — which is the entire reason it is there.
func PairScript() *Script {
	return &Script{
		Name: "pair",
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
			},
		},
		Until:     `gate == "green" and verdict == "approve"`,
		MaxRounds: 3,
	}
}
