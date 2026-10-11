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

// OpenAPISchema documents both accepted wire shapes. Reflecting the struct
// would claim every reference must be an object — a source-breaking change
// for clients that have always sent path strings — and would leave role an
// unrestricted string although anything but scope/background is refused.
func (StageRef) OpenAPISchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string", "description": "a path; the reference is background"},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
					"role": map[string]any{"type": "string", "enum": []any{RefRoleScope, RefRoleBackground}},
				},
				"required":             []any{"path"},
				"additionalProperties": false,
			},
		},
	}
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
// target, and would approve a candidate that silently excluded it. Knowing
// the role is not enough to judge an omission (Codex on #174): the reviewer
// has no ref_read, so the scope references' content — what the stage prompt
// carried, bounded text or digest — rides along within budget. Background
// references send only their names: they can never ground the finding.
func renderReferenceRolesForReview(files []string, roleOf func(string) string, stageName string, evidence map[string]string, budget int) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Reference roles — set by the person\n\n")
	var scope []string
	for _, f := range files {
		role := roleOf(f)
		if role == RefRoleScope {
			scope = append(scope, f)
		}
		fmt.Fprintf(&b, "- %s: %s\n", f, role)
	}
	b.WriteString("\n")
	if len(scope) == 0 {
		b.WriteString("Every reference is background: context that never adds scope.\n")
		return b.String()
	}
	if stageName == "spec" || stageName == "plan" {
		b.WriteString("A SCOPE reference describes the intended product. Raise a finding when the " +
			"candidate omits, defers or declares out of scope behavior a scope reference details " +
			"under an approved requirement, without the request or the approved requirements " +
			"saying so. Background references are never grounds for that finding.\n")
	} else {
		b.WriteString("A SCOPE reference describes the intended product. Raise a finding when the " +
			"candidate keeps or adds an exclusion of behavior a scope reference describes and the " +
			"request asks to close, or declares such behavior out of scope merely because it is not " +
			"implemented. Lifting or narrowing an exclusion with traceability to the reference is the " +
			"requested change, not a contradiction. Background references are never grounds for that " +
			"finding.\n")
	}
	b.WriteString(renderScopeEvidence(scope, evidence, budget))
	return b.String()
}

// reviewEvidenceBudget bounds the scope-reference content shown to the
// composition reviewer: 16k chars, or an eighth of a small seat's context
// (~4 chars per token), never below 2k — the reviewer must still fit the base,
// the delta and the candidate.
func reviewEvidenceBudget(contextTokens int) int {
	const most, least = 16_000, 2_000
	if contextTokens <= 0 {
		return most
	}
	n := contextTokens / 2
	if n > most {
		n = most
	}
	if n < least {
		n = least
	}
	return n
}

// renderScopeEvidence splits the budget evenly across the scope references
// and says plainly where it cut: an elided tail read as "the reference does
// not describe this" is the omission this evidence exists to catch.
func renderScopeEvidence(scope []string, evidence map[string]string, budget int) string {
	var b strings.Builder
	b.WriteString("\n## Scope reference content — bounded\n\n" +
		"What the stage was given for each scope reference (its text, or its digest when the " +
		"corpus was too large). Compare the candidate against it.\n")
	per := budget
	if len(scope) > 0 {
		per = budget / len(scope)
	}
	for _, f := range scope {
		text := strings.TrimSpace(evidence[f])
		fmt.Fprintf(&b, "\n### %s\n\n", f)
		if text == "" {
			b.WriteString("[content unavailable to this review]\n")
			continue
		}
		if len(text) > per {
			text = text[:per]
			b.WriteString(text)
			b.WriteString("\n\n[cut at the review budget — absence of a behavior beyond this point is not evidence the reference omits it]\n")
			continue
		}
		b.WriteString(text + "\n")
	}
	return b.String()
}

// setRefEvidence keeps what the stage prompt carried for each reference.
func (rs *runState) setRefEvidence(loaded []refFile) {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	rs.refEvidence = make(map[string]string, len(loaded))
	for _, f := range loaded {
		rs.refEvidence[f.Path] = f.text
	}
}

func (rs *runState) refEvidenceMap() map[string]string {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	out := make(map[string]string, len(rs.refEvidence))
	for k, v := range rs.refEvidence {
		out[k] = v
	}
	return out
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
