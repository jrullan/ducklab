package artifact

import (
	"fmt"
	"regexp"
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
	UnrealizedIntent  TraceErrorKind = "unrealized_intent"
	// The horizontal edge: task → task. A cycle is fatal — every task in it
	// waits forever. A forward dependency is a plan whose order contradicts its
	// own graph: runnable, but not in the order it is written.
	DependencyCycle   TraceErrorKind = "dependency_cycle"
	ForwardDependency TraceErrorKind = "forward_dependency"
	LaneCollision     TraceErrorKind = "lane_collision"
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
	Intent       *Document
	Requirements *Document
	Spec         *Document
	Plan         *Document
}

// LoadSpine reads the three artifacts a check needs.
func LoadSpine(projectRoot string) (*Spine, error) {
	intent, err := EnsureIntent(projectRoot)
	if err != nil {
		return nil, err
	}
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
	return &Spine{Intent: intent, Requirements: reqs, Spec: spec, Plan: plan}, nil
}

// LoadSpinePending reads the spine with any pending proposal standing in for
// the artifact it would replace, and reports which stages were substituted.
//
// LoadSpine reads only approved artifacts, so a check run while a proposal sat
// at its gate described the document the human had already accepted — an
// autopsy rather than a gate. The one moment the check is worth anything is
// before Accept, and that was the one moment it could not see.
//
// The caller must be told what it is looking at: findings about a plan you are
// about to accept and findings about the plan you accepted last week demand
// different actions, and a rail that cannot tell them apart is worse than one
// that says nothing.
func LoadSpinePending(projectRoot string) (*Spine, []Kind, error) {
	intent, err := EnsureIntent(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	spine := &Spine{Intent: intent}
	var proposed []Kind
	for _, it := range []struct {
		kind Kind
		into **Document
	}{
		{KindRequirements, &spine.Requirements},
		{KindSpec, &spine.Spec},
		{KindPlan, &spine.Plan},
	} {
		doc, err := LoadProposed(projectRoot, it.kind)
		if err != nil {
			return nil, nil, err
		}
		// An empty proposal is not a substitute for an approved document: a
		// stage that produced nothing would otherwise blank the spine and
		// report every requirement orphaned.
		if doc != nil && len(doc.Sections) > 0 {
			*it.into = doc
			proposed = append(proposed, it.kind)
			continue
		}
		if doc, err = Load(projectRoot, it.kind); err != nil {
			return nil, nil, err
		}
		*it.into = doc
	}
	return spine, proposed, nil
}

// Check walks the spine and reports every break.
//
// An empty result means the cycle is linked end to end. The checks are
// deliberately specific: "something is wrong" is not actionable, so each error
// names the id and what it is missing.
func (s *Spine) Check() []TraceError {
	var errs []TraceError
	if s.Intent != nil {
		for _, intent := range s.Intent.Sections {
			if strings.EqualFold(intent.Field("outcome"), "accepted") &&
				len(idsInField(intent.Field("requirements"))) == 0 &&
				!strings.Contains(intent.Body, "Imported from the historical run record") {
				errs = append(errs, TraceError{Kind: UnrealizedIntent, ID: intent.ID, Detail: "accepted intention changed no requirement"})
			}
		}
	}

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
				// A promoted bug task is justified by the report it fixes,
				// not by a spec section — that is its whole nature: the spec
				// described the intent, the bug describes the miss. Flagging
				// every "Fixes B-007" as unjustified taught people to ignore
				// the spine, which is the one thing a check must never teach.
				if fixesBug(task.Body) {
					continue
				}
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
		// A section the existing tree already satisfies needs no task: an
		// adopted project's spec describes code that was built before ducklab
		// arrived, and demanding tasks to build it again would invent work.
		// Keyed on the marker the spec stage teaches and a person approved.
		if asBuilt(sp) {
			continue
		}
		errs = append(errs, TraceError{
			Kind: UnimplementedSpec, ID: sp.ID,
			Detail: "no task implements this spec section",
		})
	}

	errs = append(errs, checkDependencies(s.Plan)...)
	errs = append(errs, checkLaneCollisions(s.Plan)...)

	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Kind != errs[j].Kind {
			return errs[i].Kind < errs[j].Kind
		}
		return errs[i].ID < errs[j].ID
	})
	return errs
}

// checkLaneCollisions validates plan section lanes. A path and any of its
// descendants are overlapping claims; this deliberately handles both files and
// directory globs without consulting the filesystem.
func checkLaneCollisions(plan *Document) []TraceError {
	// A milestone's lane is inherited by its tasks. Keep the milestone index so
	// that this inheritance is not mistaken for two independent claims.
	type claim struct {
		section     Section
		milestone   int
		isMilestone bool
	}
	var claims []claim
	for mi, m := range plan.Sections {
		claims = append(claims, claim{section: m, milestone: mi, isMilestone: true})
		for i := range m.Children {
			child := m.Children[i]
			if len(child.Owns) == 0 {
				child.Owns = append([]string(nil), m.Owns...)
			}
			claims = append(claims, claim{section: child, milestone: mi})
		}
	}
	var errs []TraceError
	for i := 0; i < len(claims); i++ {
		for j := i + 1; j < len(claims); j++ {
			// A milestone's lane is inherited by its children, not a competing
			// claim. Sibling task lanes, however, must be disjoint even under
			// the same milestone.
			if claims[i].milestone == claims[j].milestone &&
				(claims[i].isMilestone || claims[j].isMilestone) {
				continue
			}
			for _, a := range claims[i].section.Owns {
				for _, b := range claims[j].section.Owns {
					if laneOverlap(a, b) {
						errs = append(errs, TraceError{Kind: LaneCollision, ID: claims[i].section.ID,
							Detail: fmt.Sprintf("lane %q overlaps %q claimed by %s", a, b, claims[j].section.ID)})
					}
				}
			}
		}
	}
	return errs
}

// LaneCollisions exposes the plan-lane half of the trace gate to amendment
// code that must reject an invalid candidate before it reaches human review.
func LaneCollisions(plan *Document) []TraceError {
	return checkLaneCollisions(plan)
}

func laneOverlap(a, b string) bool {
	normalize := func(path string) string {
		path = strings.TrimSpace(path)
		path = strings.TrimSuffix(path, "**")
		path = strings.TrimSuffix(path, "*")
		return strings.TrimRight(path, "/")
	}
	a, b = normalize(a), normalize(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// checkDependencies walks the horizontal edge: task → task.
//
// Check has always walked the vertical spine — requirement to spec to task.
// The **Depends on:** field was parsed, and the board reads it to decide what
// is blocked, but nothing ever looked at the graph it forms. Two of the three
// breaks below are permanent: a task that waits on a cycle or on a task that
// does not exist waits forever, and the board can only show it sitting in
// Blocked without saying the wait will never end.
func checkDependencies(plan *Document) []TraceError {
	var errs []TraceError

	// Document order, which is what "later in the plan" means and what a person
	// working top to bottom will actually follow.
	var order []string
	position := map[string]int{}
	deps := map[string][]string{}
	for _, m := range plan.Sections {
		for _, task := range m.Children {
			position[task.ID] = len(order)
			order = append(order, task.ID)
			deps[task.ID] = splitIDs(task.Field("depends on"))
		}
	}

	for _, id := range order {
		for _, dep := range deps[id] {
			if dep == id {
				errs = append(errs, TraceError{
					Kind: DependencyCycle, ID: id,
					Detail: "task depends on itself, so it can never start",
				})
				continue
			}
			at, exists := position[dep]
			if !exists {
				errs = append(errs, TraceError{
					Kind: DanglingReference, ID: id,
					Detail:  "depends on a task that does not exist, so it can never start",
					Missing: dep,
				})
				continue
			}
			// Not a break, a smell: the plan is runnable but its order lies.
			// Working top to bottom reaches this task before the thing it
			// needs, and a model handed a task whose prerequisite is missing
			// writes the prerequisite too — which is how one task eats another.
			if at > position[id] {
				errs = append(errs, TraceError{
					Kind: ForwardDependency, ID: id,
					Detail:  "depends on a task that comes later in the plan",
					Missing: dep,
				})
			}
		}
	}

	for _, cycle := range findCycles(order, deps) {
		errs = append(errs, TraceError{
			Kind: DependencyCycle, ID: cycle[0],
			Detail:  "dependency cycle: none of these can ever start",
			Missing: strings.Join(cycle, " → "),
		})
	}
	return errs
}

// findCycles returns one entry per cycle, each naming the tasks in it. Reported
// once against its lowest id rather than once per member: a four-task cycle
// listed four times reads as four problems.
func findCycles(order []string, deps map[string][]string) [][]string {
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string
	var found [][]string

	var walk func(id string)
	walk = func(id string) {
		state[id] = onStack
		stack = append(stack, id)
		for _, dep := range deps[id] {
			switch state[dep] {
			case unvisited:
				if _, known := deps[dep]; known {
					walk(dep)
				}
			case onStack:
				// Everything from dep to the top of the stack is the cycle.
				for i, s := range stack {
					if s == dep {
						cycle := append([]string{}, stack[i:]...)
						sort.Strings(cycle)
						found = append(found, cycle)
						break
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
	}

	for _, id := range order {
		if state[id] == unvisited {
			walk(id)
		}
	}
	return dedupeCycles(found)
}

// A cycle is reachable from every task that leads into it, so a plain walk
// finds the same one repeatedly.
func dedupeCycles(cycles [][]string) [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, c := range cycles {
		if len(c) < 2 {
			continue
		}
		key := strings.Join(c, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
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
	if s.Intent != nil {
		if sec := s.Intent.Section(id); sec != nil {
			n := &Node{ID: id, Kind: "intent", Title: sec.Title}
			for _, req := range s.Requirements.Sections {
				if contains(idsInField(req.Field("originates from")), id) {
					n.Down = append(n.Down, req.ID)
				}
			}
			return n, nil
		}
	}
	if sec := s.Requirements.Section(id); sec != nil {
		n := &Node{ID: id, Kind: "requirement", Title: sec.Title, Up: idsInField(sec.Field("originates from"))}
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
	return nil, fmt.Errorf("%q is not in intent, requirements, spec or plan", id)
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

// fixesBug reports whether a task's body names the report that justifies it.
var fixesBugRe = regexp.MustCompile(`\bFixes B-\d+`)

func fixesBug(body string) bool { return fixesBugRe.MatchString(body) }

// asBuilt reports whether a section records behaviour the existing code
// already implements (adopted projects; `**As-built:** yes`).
func asBuilt(s Section) bool {
	v := strings.ToLower(strings.TrimSpace(s.Field("as-built")))
	return v == "yes" || v == "true"
}
