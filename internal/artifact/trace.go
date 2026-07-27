package artifact

import (
	"fmt"
	"sort"
	"strings"
)

// The traceability spine (02 §4.1):
//
//	requirement → spec_section → task → run → release
//	bug → task
//
// This check is DETERMINISTIC and model-free. A spine verified by asking a
// model whether the work looks traceable would be exactly the unfalsifiable
// claim the spine exists to replace.

// TraceErrorKind names a break in the spine.
type TraceErrorKind string

const (
	OrphanRequirement TraceErrorKind = "orphan_requirement"
	UnimplementedSpec TraceErrorKind = "unimplemented_spec"
	UnjustifiedTask   TraceErrorKind = "unjustified_task"
	DanglingReference TraceErrorKind = "dangling_reference"
	AcceptedDraftReq  TraceErrorKind = "accepted_task_on_draft_requirement"
)

// TraceError is one break, with enough detail to act on.
type TraceError struct {
	Kind    TraceErrorKind `json:"kind"`
	ID      string         `json:"id"`
	Detail  string         `json:"detail"`
	Missing string         `json:"missing,omitempty"`
}

func (e TraceError) String() string {
	if e.Missing != "" {
		return fmt.Sprintf("%s %s: %s (%s)", e.Kind, e.ID, e.Detail, e.Missing)
	}
	return fmt.Sprintf("%s %s: %s", e.Kind, e.ID, e.Detail)
}

// Spine is the parsed cycle: requirements, spec sections, and plan tasks.
type Spine struct {
	Requirements *Document
	Spec         *Document
	Plan         *Document
}

// LoadSpine reads the three artifacts a check needs.
func LoadSpine(projectRoot string) (*Spine, error) {
	reqs, err := Load(projectRoot, KindRequirements)
	if err != nil {
		return nil, err
	}
	spec, err := Load(projectRoot, KindSpec)
	if err != nil {
		return nil, err
	}
	plan, err := Load(projectRoot, KindPlan)
	if err != nil {
		return nil, err
	}
	return &Spine{Requirements: reqs, Spec: spec, Plan: plan}, nil
}

// Check walks the spine and reports every break.
//
// An empty result means the cycle is linked end to end. The checks are
// deliberately specific: "something is wrong" is not actionable, so each error
// names the id and what it is missing.
func (s *Spine) Check() []TraceError {
	var errs []TraceError

	reqIDs := map[string]Section{}
	for _, r := range s.Requirements.Sections {
		reqIDs[r.ID] = r
	}
	specIDs := map[string]Section{}
	for _, sp := range s.Spec.Sections {
		specIDs[sp.ID] = sp
	}

	// requirement → spec
	coveredReqs := map[string]bool{}
	for _, sp := range s.Spec.Sections {
		if len(sp.Implements) == 0 {
			errs = append(errs, TraceError{
				Kind: UnimplementedSpec, ID: sp.ID,
				Detail: "spec section implements no requirement",
			})
			continue
		}
		for _, req := range sp.Implements {
			if _, ok := reqIDs[req]; !ok {
				errs = append(errs, TraceError{
					Kind: DanglingReference, ID: sp.ID,
					Detail: "implements a requirement that does not exist", Missing: req,
				})
				continue
			}
			coveredReqs[req] = true
		}
	}

	// Only a `must` requirement is a hard gap. A `could` with no spec section
	// is a decision, not an error — flagging it would train people to ignore
	// the check.
	for _, r := range s.Requirements.Sections {
		if coveredReqs[r.ID] {
			continue
		}
		priority := strings.ToLower(r.Field("priority"))
		if priority == "wont" || priority == "could" {
			continue
		}
		errs = append(errs, TraceError{
			Kind: OrphanRequirement, ID: r.ID,
			Detail: "no spec section implements this requirement",
		})
	}

	// spec → task
	coveredSpecs := map[string]bool{}
	for _, m := range s.Plan.Sections {
		for _, task := range m.Children {
			if len(task.Implements) == 0 {
				errs = append(errs, TraceError{
					Kind: UnjustifiedTask, ID: task.ID,
					Detail: "task implements no spec section",
				})
				continue
			}
			for _, sp := range task.Implements {
				if _, ok := specIDs[sp]; !ok {
					errs = append(errs, TraceError{
						Kind: DanglingReference, ID: task.ID,
						Detail: "implements a spec section that does not exist", Missing: sp,
					})
					continue
				}
				coveredSpecs[sp] = true
			}
		}
	}

	// A section that records what will NOT be built has nothing for a task to
	// implement, and demanding one turns the check into noise the reader
	// learns to skip — the same reasoning that exempts a `wont` requirement
	// above. It is keyed on the marker, never on the title: guessing that a
	// section headed "Out of Scope" is non-normative would make the spine
	// depend on prose a model happened to write.
	for _, sp := range s.Spec.Sections {
		if coveredSpecs[sp.ID] || len(sp.Implements) == 0 {
			continue
		}
		if nonNormative(sp) {
			continue
		}
		errs = append(errs, TraceError{
			Kind: UnimplementedSpec, ID: sp.ID,
			Detail: "no task implements this spec section",
		})
	}

	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Kind != errs[j].Kind {
			return errs[i].Kind < errs[j].Kind
		}
		return errs[i].ID < errs[j].ID
	})
	return errs
}

// Node is one step when walking the spine from an id.
type Node struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Title string   `json:"title"`
	Up    []string `json:"up,omitempty"`
	Down  []string `json:"down,omitempty"`
}

// Walk returns the chain around an id: what it implements, and what implements
// it. This is what `trace show` and the desktop's rail render.
// nonNormative reports whether a section describes absence rather than
// behaviour, and so needs no task.
func nonNormative(sec Section) bool {
	for _, f := range []string{"priority", "status", "normative"} {
		switch strings.ToLower(strings.TrimSpace(sec.Field(f))) {
		case "wont", "won't", "out_of_scope", "out of scope", "non-normative", "false", "no":
			return true
		}
	}
	return false
}

func (s *Spine) Walk(id string) (*Node, error) {
	if sec := s.Requirements.Section(id); sec != nil {
		n := &Node{ID: id, Kind: "requirement", Title: sec.Title}
		for _, sp := range s.Spec.Sections {
			if contains(sp.Implements, id) {
				n.Down = append(n.Down, sp.ID)
			}
		}
		return n, nil
	}
	if sec := s.Spec.Section(id); sec != nil {
		n := &Node{ID: id, Kind: "spec_section", Title: sec.Title, Up: sec.Implements}
		for _, m := range s.Plan.Sections {
			for _, t := range m.Children {
				if contains(t.Implements, id) {
					n.Down = append(n.Down, t.ID)
				}
			}
		}
		return n, nil
	}
	if sec := s.Plan.Section(id); sec != nil {
		return &Node{ID: id, Kind: planKind(id), Title: sec.Title, Up: sec.Implements}, nil
	}
	return nil, fmt.Errorf("%q is not in requirements, spec or plan", id)
}

func planKind(id string) string {
	if strings.HasPrefix(id, "M-") {
		return "milestone"
	}
	return "task"
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
