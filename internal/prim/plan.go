package prim

import "strings"

// planRejectionPhrases signal that Model A is standing by its existing plan
// rather than revising it in response to B's observations.
var planRejectionPhrases = []string{
	"i stand by", "keep my plan", "keeping my plan", "no changes needed",
	"no change needed", "the plan is correct", "the plan is adequate",
	"no revision", "i will not change", "i won't change",
	"not necessary to change", "my plan does not need", "my plan doesn't need",
	// Spanish, in case a local model replies in it:
	"mantengo mi plan", "no voy a cambiar", "el plan es correcto",
	"no es necesario cambiar",
}

// IsPlanRejection reports whether a handoff message is Model A declining to
// revise (standing by its prior plan) rather than presenting a new plan. A
// rejection is prose with a stand-pat phrase and no numbered list or code
// fence — a revised plan always carries the plan itself.
func IsPlanRejection(handoff string) bool {
	lower := strings.ToLower(handoff)
	hit := false
	for _, p := range planRejectionPhrases {
		if strings.Contains(lower, p) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	hasList := false
	for _, line := range strings.Split(handoff, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "1.") || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
			hasList = true
			break
		}
	}
	hasFence := strings.Contains(handoff, "```") || strings.Contains(handoff, "<<< SEARCH")
	return !hasList && !hasFence
}

// ExtractPlan pulls the plan out of a conversational handoff: everything from
// the first numbered/bulleted item or a "Plan"/heading line onward. Falls back
// to the whole message when no structure is found.
func ExtractPlan(handoff string) string {
	lines := strings.Split(handoff, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "1.") || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
			return strings.Join(lines[i:], "\n")
		}
		if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "Plan") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return handoff
}
