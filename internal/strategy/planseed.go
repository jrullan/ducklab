package strategy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrullan/ducklab/internal/agent"
)

// PlanSeedSpec is the part of an accepted specification the engine can own
// without semantic inference: stable identity, human-approved title and scope.
type PlanSeedSpec struct {
	ID       string
	Title    string
	Priority string
	AsBuilt  bool
	// Digest identifies the complete accepted SPEC section without exposing its
	// prose as model-owned planning state. Manifest-critic cache keys use it so
	// a changed obligation invalidates every task that implements that section.
	Digest string
}

func planSeedInScope(spec PlanSeedSpec) bool {
	if spec.AsBuilt {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(spec.Priority)) {
	case "wont", "could":
		return false
	default:
		return true
	}
}

// seedPlanManifest creates an empty topology beside the engine-owned coverage
// ledger. It deliberately creates no task: one accepted SPEC section is a
// coverage slot, not evidence that implementation has the same partition.
// The architect adds or groups tasks through bounded patches while the engine
// keeps the required SPEC identities separately in ExecuteScript.
func seedPlanManifest(specs []PlanSeedSpec) (*agent.PlanManifest, error) {
	var scoped []PlanSeedSpec
	for _, spec := range specs {
		if planSeedInScope(spec) {
			scoped = append(scoped, spec)
		}
	}
	if len(scoped) == 0 {
		return nil, fmt.Errorf("plan manifest seed: accepted specification has no in-scope sections")
	}
	milestone := agent.ManifestMilestone{
		ID: "M-01", Title: "Implementation", Tasks: []agent.ManifestTask{},
	}
	return &agent.PlanManifest{Milestones: []agent.ManifestMilestone{milestone}}, nil
}

func planCoverageSlotPrompt(specs []PlanSeedSpec, missing []string) string {
	wanted := map[string]bool{}
	for _, id := range missing {
		wanted[id] = true
	}
	var slots []string
	for _, spec := range specs {
		if !planSeedInScope(spec) || !wanted[spec.ID] {
			continue
		}
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			title = spec.ID
		}
		slots = append(slots, fmt.Sprintf("- %s — %s", spec.ID, title))
	}
	return strings.Join(slots, "\n")
}

func missingPlanSeedCoverage(manifest *agent.PlanManifest, required []string) []string {
	covered := map[string]bool{}
	if manifest != nil {
		for _, milestone := range manifest.Milestones {
			for _, task := range milestone.Tasks {
				for _, specID := range task.Implements {
					covered[specID] = true
				}
			}
		}
	}
	var missing []string
	for _, specID := range required {
		if !covered[specID] {
			missing = append(missing, specID)
		}
	}
	sort.Strings(missing)
	return missing
}
