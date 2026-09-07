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
}

const unresolvedPlanSeedPrefix = "UNRESOLVED:"

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

// seedPlanManifest creates a structurally valid but explicitly provisional
// checkpoint. It deliberately knows no paths, commands, or task cohesion: the
// architect supplies those through bounded patches. One accepted SPEC section
// is the smallest partition Ducklab can derive without pretending prose is an
// executable ontology.
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
	if len(scoped) > agent.MaxPlanManifestTasks {
		return nil, fmt.Errorf("plan manifest seed: %d in-scope SPEC sections exceed the %d-task boundary; split the product scope before planning", len(scoped), agent.MaxPlanManifestTasks)
	}
	milestone := agent.ManifestMilestone{ID: "M-01", Title: "Specification coverage"}
	for i, spec := range scoped {
		id := fmt.Sprintf("T-%03d", i+1)
		slug := strings.ToLower(spec.ID)
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			title = spec.ID
		}
		milestone.Tasks = append(milestone.Tasks, agent.ManifestTask{
			ID: id, Title: "Resolve " + title, Implements: []string{spec.ID},
			WorkUnit:         unresolvedPlanSeedPrefix + " define one cohesive work unit for " + spec.ID,
			AcceptanceSlices: []string{unresolvedPlanSeedPrefix + " derive observable outcomes for " + spec.ID},
			AcceptanceProbes: []string{"false"},
			Produces:         []string{"capability:unresolved-" + slug},
			Consumes:         []string{}, Verification: "false",
		})
	}
	return &agent.PlanManifest{Milestones: []agent.ManifestMilestone{milestone}}, nil
}

func unresolvedPlanSeedTasks(manifest *agent.PlanManifest) []string {
	if manifest == nil {
		return nil
	}
	var out []string
	for _, milestone := range manifest.Milestones {
		for _, task := range milestone.Tasks {
			unresolved := strings.HasPrefix(task.WorkUnit, unresolvedPlanSeedPrefix) || task.Verification == "false"
			for _, item := range task.AcceptanceSlices {
				unresolved = unresolved || strings.HasPrefix(item, unresolvedPlanSeedPrefix)
			}
			for _, item := range task.AcceptanceProbes {
				unresolved = unresolved || item == "false"
			}
			for _, item := range task.Produces {
				unresolved = unresolved || strings.HasPrefix(item, "capability:unresolved-")
			}
			if unresolved {
				out = append(out, task.ID)
			}
		}
	}
	sort.Strings(out)
	return out
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
