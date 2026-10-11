package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reference roles (B-520).
//
// A reference used to be background by fiat: intake's guidance said "never
// derive requirements from it", a rule born when a bug-inbox snapshot became
// fifteen requirements. On the TI-36X the person attached the calculator's
// guidebook and asked to "fill in the gap between the implemented
// functionality and the intended functionality" — and the architect obeyed
// the system prompt over the person, documented the as-built calculator and
// declared the manual's missing features "correctly out of scope".
//
// The person decides, deterministically and visibly: every reference carries
// a role, `scope` (it describes the intended product) or `background`
// (context; the default). The role lives INSIDE the reference so whatever
// carries a run's references forward — a revision, a retry — carries the
// person's choice with it.

const (
	RefRoleBackground = "background"
	RefRoleScope      = "scope"
)

// StageRef is one reference a stage run is launched with.
//
// On the wire it is either a plain path string (background — every request
// written before roles existed) or an object {"path": "...", "role":
// "scope"|"background"}. It is written back in the same compact shape: a
// background reference stays a string, so records without scope references
// are byte-identical to the ones before roles.
type StageRef struct {
	// Path is a file, a directory of .md/.txt/.json, or an image.
	Path string `json:"path"`
	// Role is "scope" — the reference describes the intended product and the
	// brief asks to build toward it — or "background" (context only). Empty
	// means background.
	Role string `json:"role,omitempty"`
}

// IsScope reports whether the person marked this reference as scope.
func (r StageRef) IsScope() bool { return r.Role == RefRoleScope }

// normalizeRefRole maps a role to its canonical stored form: "" for
// background, "scope" for scope. Anything else is refused rather than
// guessed — a misspelled "scope" silently read as background is the bug this
// role exists to prevent.
func normalizeRefRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", RefRoleBackground:
		return "", nil
	case RefRoleScope:
		return RefRoleScope, nil
	}
	return "", fmt.Errorf("reference role %q: use %q or %q", role, RefRoleScope, RefRoleBackground)
}

// UnmarshalJSON accepts a path string (background) or {"path","role"}.
func (r *StageRef) UnmarshalJSON(data []byte) error {
	var path string
	if err := json.Unmarshal(data, &path); err == nil {
		*r = StageRef{Path: path}
		return nil
	}
	var obj struct {
		Path string `json:"path"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("a reference is a path string or {\"path\": ..., \"role\": \"scope\"|\"background\"}: %w", err)
	}
	role, err := normalizeRefRole(obj.Role)
	if err != nil {
		return err
	}
	*r = StageRef{Path: obj.Path, Role: role}
	return nil
}

// MarshalJSON writes background references as plain strings.
func (r StageRef) MarshalJSON() ([]byte, error) {
	if !r.IsScope() {
		return json.Marshal(r.Path)
	}
	return json.Marshal(struct {
		Path string `json:"path"`
		Role string `json:"role"`
	}{r.Path, RefRoleScope})
}

// backgroundRefs wraps plain paths as background references.
func backgroundRefs(paths ...string) []StageRef {
	out := make([]StageRef, 0, len(paths))
	for _, p := range paths {
		out = append(out, StageRef{Path: p})
	}
	return out
}

// refPaths is the path list of a reference set, roles dropped.
func refPaths(refs []StageRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Path)
	}
	return out
}

// refRoleOf maps a loaded file back to the role of the reference that named
// it: the file itself, or a directory containing it. A file reachable through
// both a scope and a background reference is scope — the explicit choice
// wins over the default.
func refRoleOf(refs []StageRef) func(file string) string {
	type root struct {
		path  string
		scope bool
	}
	var roots []root
	for _, r := range refs {
		p := strings.TrimSpace(r.Path)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		roots = append(roots, root{filepath.Clean(p), r.IsScope()})
	}
	return func(file string) string {
		file = filepath.Clean(file)
		for _, rt := range roots {
			if !rt.scope {
				continue
			}
			if file == rt.path || strings.HasPrefix(file, rt.path+string(filepath.Separator)) {
				return RefRoleScope
			}
		}
		return RefRoleBackground
	}
}

// refRoleLabel is the heading suffix that tells every reader — architect and
// critics read the same prompt — which role a reference plays. Empty when no
// reference is scope: an all-background run keeps the prompt it always had.
func refRoleLabel(role string, anyScope bool) string {
	if !anyScope {
		return ""
	}
	if role == RefRoleScope {
		return " — role: SCOPE (describes the intended product)"
	}
	return " — role: background"
}

// refGuidance is stage- and role-shaped. Intake's hardline ("never derive
// requirements") exists because a bug-inbox snapshot once became fifteen
// requirements — but read in a SPEC it told the architect to leave the
// wiki's architecture, RBAC and audit detail out of the document, the
// opposite of why the person attached it; and read over a guidebook the
// person marked as the target, it told intake to keep the guide's features
// out (B-520). Without a scope reference the text is exactly the one runs
// have always had.
func refGuidance(stageName string, anyScope bool) string {
	docStage := stageName == "spec" || stageName == "plan"
	if !anyScope {
		if docStage {
			return specBackgroundGuidance
		}
		return "Provided by the person as BACKGROUND, not as scope. " + intakeBackgroundRule
	}
	var b strings.Builder
	b.WriteString("The person gave each reference a ROLE, shown in its heading.\n\n")
	if docStage {
		unit := "specification sections"
		if stageName == "plan" {
			unit = "tasks"
		}
		fmt.Fprintf(&b, "SCOPE references describe the INTENDED product. Within the approved requirements "+
			"they may add %[1]s: where an approved requirement covers behavior a scope reference "+
			"details, write the %[1]s that deliver that detail and cite the reference section "+
			"they come from. Never drop, defer or mark as excluded behavior a scope reference "+
			"describes under an approved requirement because the code does not implement it yet — "+
			"what is unimplemented is the work. The approved requirements still bound scope: "+
			"behavior no approved requirement covers is not added here; name it in your reply as a "+
			"gap for an intake amendment. Where a scope reference and the code disagree, the code is "+
			"the truth only for as-built claims.\n\n", unit)
		b.WriteString("BACKGROUND references are design context. " + specBackgroundGuidance + "\n")
		b.WriteString("Reviewers: for each SCOPE reference, check that the behavior it details under " +
			"an approved requirement is delivered by the candidate. Raise a finding for any such " +
			"behavior the candidate omits, defers or declares out of scope without the brief or the " +
			"approved requirements saying so. A background reference is never grounds for that " +
			"finding.\n")
		return b.String()
	}
	b.WriteString("SCOPE references describe the INTENDED product — the target the brief asks you to " +
		"reach, not background. For each: derive requirements for the behavior it describes that " +
		"the brief asks for, and cite the reference section each one comes from (\"Source: <file> " +
		"§ <section>\"). Where an existing requirement excludes or omits that behavior (an " +
		"\"unsupported modes\" or out-of-scope requirement), lift or narrow the exclusion and cite " +
		"the reference section that justifies the change. Never declare behavior a scope reference " +
		"describes out of scope merely because the code does not implement it yet: what is " +
		"unimplemented is the gap the brief asks you to close. A requirement for intended behavior " +
		"states what the system MUST do, traced to the reference; the code stays the truth only " +
		"for as-built claims — a statement of what the system does today must match the code. " +
		"Behavior a scope reference describes that the brief plainly does not ask for stays out, " +
		"named explicitly with the reference section it leaves out.\n\n")
	b.WriteString("BACKGROUND references are context, not scope. " + intakeBackgroundRule + "\n")
	b.WriteString("Reviewers: for each SCOPE reference, check that every gap the brief asks to close " +
		"was addressed — a requirement traced to the reference, or an exclusion lifted or narrowed " +
		"with traceability. Raise a finding for any such behavior the candidate leaves out, defers, " +
		"or declares out of scope without the brief saying so. A background reference is never " +
		"grounds for that finding.\n")
	return b.String()
}

const specBackgroundGuidance = "Provided by the person as design context. USE them: ground your sections in the " +
	"detail they carry — architecture decisions, domain rules, exact identifiers, " +
	"workflows — rather than restating the requirements more vaguely. Your document is " +
	"judged against this detail: a section vaguer than the reference it draws on is " +
	"unfinished. Two limits: the " +
	"approved requirements define scope, so a reference's plans, wish lists or open " +
	"problems never add sections; and where a reference and the code disagree, the code " +
	"is the truth for as-built claims — note the disagreement instead of copying the claim.\n"

const intakeBackgroundRule = "Where a reference and the code " +
	"disagree, the code is the truth for as-built claims — note the disagreement instead of " +
	"copying the claim. A reference that lists open problems, pending feedback, plans or " +
	"wishes describes WORK, not the product: never derive requirements from it — the first " +
	"survey of MiEmpresa turned a bug-inbox snapshot into fifteen requirements about its " +
	"fourteen bugs. Requirements state what the system IS.\n"

// renderReferenceRolesForReview is the block the post-composition reviewer
// reads. That reviewer sees the base, the delta and the candidate — not the
// reference section — so without this it could not know a reference was the
// target, and would approve a candidate that silently excluded it.
func renderReferenceRolesForReview(files []string, roleOf func(string) string, stageName string) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Reference roles — set by the person\n\n")
	anyScope := false
	for _, f := range files {
		role := roleOf(f)
		if role == RefRoleScope {
			anyScope = true
		}
		fmt.Fprintf(&b, "- %s: %s\n", f, role)
	}
	b.WriteString("\n")
	if !anyScope {
		b.WriteString("Every reference is background: context that never adds scope.\n")
		return b.String()
	}
	if stageName == "spec" || stageName == "plan" {
		b.WriteString("A SCOPE reference describes the intended product. Raise a finding when the " +
			"candidate omits, defers or declares out of scope behavior a scope reference details " +
			"under an approved requirement, without the request or the approved requirements " +
			"saying so. Background references are never grounds for that finding.\n")
		return b.String()
	}
	b.WriteString("A SCOPE reference describes the intended product. Raise a finding when the " +
		"candidate keeps or adds an exclusion of behavior a scope reference describes and the " +
		"request asks to close, or declares such behavior out of scope merely because it is not " +
		"implemented. Lifting or narrowing an exclusion with traceability to the reference is the " +
		"requested change, not a contradiction. Background references are never grounds for that " +
		"finding.\n")
	return b.String()
}

// splitImageStageRefs separates image references (which travel as images,
// whatever their role) from text references, which keep their roles.
func splitImageStageRefs(refs []StageRef) (text []StageRef, images []string) {
	for _, r := range refs {
		if isRefImage(r.Path) {
			images = append(images, r.Path)
		} else {
			text = append(text, r)
		}
	}
	return text, images
}
