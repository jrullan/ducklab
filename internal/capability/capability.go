// Package capability composes stack-specific harness behaviour without
// teaching the execution core about languages, frameworks, or build systems.
package capability

import (
	"fmt"
	"sort"
)

// Enforcement says whether a contributed check is information or contract.
type Enforcement string

const (
	Diagnostic Enforcement = "diagnostic"
	Required   Enforcement = "required"
)

// Context is the stack-neutral input available to capability providers.
type Context struct {
	ProjectRoot      string
	TaskVerification string
	Policies         map[string]string
}

// Check is an executable contribution from a resolved capability.
type Check struct {
	Capability  string
	Name        string
	Command     string
	Enforcement Enforcement
}

// Provider recognizes one reusable project or stack capability.
type Provider interface {
	ID() string
	Checks(Context) []Check
}

// Registry resolves independently useful providers into one harness profile.
// Providers are deliberately additive: a polyglot project is not forced into
// a single project-type label.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		r.providers[provider.ID()] = provider
	}
	return r
}

// Resolve returns deterministic checks in provider/name order. Enabled is an
// explicit opt-in in addition to auto detection; Disabled always wins.
func (r *Registry) Resolve(ctx Context, auto bool, enabled, disabled []string) ([]Check, error) {
	disabledSet := stringSet(disabled)
	selected := make(map[string]bool)
	if auto {
		for id := range r.providers {
			selected[id] = true
		}
	}
	for _, id := range enabled {
		if _, ok := r.providers[id]; !ok {
			return nil, fmt.Errorf("unknown capability %q", id)
		}
		selected[id] = true
	}
	for id := range disabledSet {
		delete(selected, id)
	}

	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var checks []Check
	for _, id := range ids {
		checks = append(checks, r.providers[id].Checks(ctx)...)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if checks[i].Capability == checks[j].Capability {
			return checks[i].Name < checks[j].Name
		}
		return checks[i].Capability < checks[j].Capability
	})
	return checks, nil
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
