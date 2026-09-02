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

// Detection records why a provider considers itself applicable. Evidence is
// relative to the project root so the same profile is reproducible in a clean
// checkout.
type Detection struct {
	Capability string
	Evidence   []string
}

// GateCandidate is one executable project-gate option. Lower Priority wins,
// preserving the established preference order without a language switch in
// the verifier. Supplemental test candidates compose with a selected root
// test gate instead of competing with it.
type GateCandidate struct {
	Capability   string
	Kind         string
	Command      string
	Scope        string
	Priority     int
	Supplemental bool
	Unavailable  error
}

// Check is an executable contribution from a resolved capability.
type Check struct {
	Capability  string
	Name        string
	Command     string
	Enforcement Enforcement
}

// ReviewRule is bounded stack knowledge supplied to coding seats after the
// provider has been detected. The core transports these rules without knowing
// what X11, GLib, a framework, or a language means.
type ReviewRule struct {
	Capability string
	ID         string
	Guidance   string
}

// GateObservation is the stack-neutral evidence available after verification.
// Providers interpret their own build tool's output; the core only records
// the structured findings they contribute.
type GateObservation struct {
	ProjectRoot string
	Diff        string
	Output      string
}

type GateFinding struct {
	Capability string
	Kind       string
	Detail     string
	Files      []string
}

// Contributions are everything one provider knows how to add. The shape can
// grow with setup or context facts without changing the core/provider boundary.
type Contributions struct {
	Detection   Detection
	Gates       []GateCandidate
	ReviewRules []ReviewRule
}

// Profile is the deterministic composition of all matching providers.
type Profile struct {
	Detections  []Detection
	Gate        *GateCandidate
	ReviewRules []ReviewRule
}

// Provider names one reusable project or stack capability. Optional detector
// and checker interfaces keep project discovery out of per-task diagnostics.
type Provider interface {
	ID() string
}

type Detector interface {
	Provider
	Detect(Context) Contributions
}

type Checker interface {
	Provider
	Checks(Context) []Check
}

type GateObserver interface {
	Provider
	ObserveGate(GateObservation) []GateFinding
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

// ResolveProject detects and composes the project's stack and gate candidates.
// It is intentionally separate from ResolveChecks: detection may probe a test
// collector or toolchain and must not recur on every verify_run call.
func (r *Registry) ResolveProject(ctx Context, auto bool, enabled, disabled []string) (Profile, error) {
	ids, err := r.selected(auto, enabled, disabled)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	var gates []GateCandidate
	for _, id := range ids {
		detector, ok := r.providers[id].(Detector)
		if !ok {
			continue
		}
		contribution := detector.Detect(ctx)
		if len(contribution.Detection.Evidence) > 0 {
			profile.Detections = append(profile.Detections, contribution.Detection)
			profile.ReviewRules = append(profile.ReviewRules, contribution.ReviewRules...)
		}
		gates = append(gates, contribution.Gates...)
	}
	resolveGate(&profile, gates)
	sort.SliceStable(profile.ReviewRules, func(i, j int) bool {
		if profile.ReviewRules[i].Capability == profile.ReviewRules[j].Capability {
			return profile.ReviewRules[i].ID < profile.ReviewRules[j].ID
		}
		return profile.ReviewRules[i].Capability < profile.ReviewRules[j].Capability
	})
	if profile.Gate != nil && profile.Gate.Unavailable != nil {
		return profile, profile.Gate.Unavailable
	}
	return profile, nil
}

// ResolveChecks returns deterministic per-task diagnostics without running
// project detection probes.
func (r *Registry) ResolveChecks(ctx Context, auto bool, enabled, disabled []string) ([]Check, error) {
	ids, err := r.selected(auto, enabled, disabled)
	if err != nil {
		return nil, err
	}
	var checks []Check
	for _, id := range ids {
		checker, ok := r.providers[id].(Checker)
		if !ok {
			continue
		}
		checks = append(checks, checker.Checks(ctx)...)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if checks[i].Capability == checks[j].Capability {
			return checks[i].Name < checks[j].Name
		}
		return checks[i].Capability < checks[j].Capability
	})
	return checks, nil
}

// ObserveGate asks only the capabilities fixed in the run profile to
// interpret their own verifier output. It performs no stack detection.
func (r *Registry) ObserveGate(observation GateObservation, capabilityIDs []string) []GateFinding {
	var findings []GateFinding
	for _, id := range capabilityIDs {
		observer, ok := r.providers[id].(GateObserver)
		if !ok {
			continue
		}
		findings = append(findings, observer.ObserveGate(observation)...)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Capability == findings[j].Capability {
			return findings[i].Kind < findings[j].Kind
		}
		return findings[i].Capability < findings[j].Capability
	})
	return findings
}

func (r *Registry) selected(auto bool, enabled, disabled []string) ([]string, error) {
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
	return ids, nil
}

func resolveGate(profile *Profile, candidates []GateCandidate) {
	var primary []GateCandidate
	var supplemental []GateCandidate
	for _, candidate := range candidates {
		if candidate.Supplemental {
			supplemental = append(supplemental, candidate)
		} else {
			primary = append(primary, candidate)
		}
	}
	sort.SliceStable(primary, func(i, j int) bool {
		if primary[i].Priority == primary[j].Priority {
			return primary[i].Capability < primary[j].Capability
		}
		return primary[i].Priority < primary[j].Priority
	})
	if len(primary) == 0 {
		return
	}
	selected := primary[0]
	if selected.Kind == "tests" && selected.Unavailable == nil {
		sort.SliceStable(supplemental, func(i, j int) bool {
			if supplemental[i].Priority == supplemental[j].Priority {
				return supplemental[i].Capability < supplemental[j].Capability
			}
			return supplemental[i].Priority < supplemental[j].Priority
		})
		for _, extra := range supplemental {
			if extra.Unavailable == nil && extra.Command != "" {
				selected.Command += " && " + extra.Command
			}
		}
	}
	profile.Gate = &selected
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
