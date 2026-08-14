package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jrullan/ducklab/internal/artifact"
)

// ArtifactRead lets a duckling read the cycle's documents.
//
// Read-only by construction: there is no artifact_write. A model changes an
// artifact by producing a proposal through a stage, which a human then accepts
// — never by writing the file directly (05 §1.1).
type ArtifactRead struct{}

func (t *ArtifactRead) Name() string   { return "artifact_read" }
func (t *ArtifactRead) Mutating() bool { return false }

func (t *ArtifactRead) Description() string {
	return "Read a lifecycle document: requirements, spec, plan or project. " +
		"Use it when you need to know what was already decided. Pass an id to read one section."
}

func (t *ArtifactRead) Schema() interface{} {
	return NewSchema().
		AddString("kind", "requirements | spec | plan | project", true).
		AddString("id", "Optional section id, e.g. REQ-001", false)
}

type artifactReadArgs struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (t *ArtifactRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a artifactReadArgs
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	// Ask strictly, read generously: a small model put the KIND in the id
	// field — {"id":"plan"} — and the old error named the valid values
	// without naming the field, so the model read "available: plan",
	// thought "that is what I said", and repeated the identical call six
	// times. When the intent is unambiguous, honor it.
	if a.Kind == "" && artifact.ValidKind(a.ID) {
		a.Kind, a.ID = a.ID, ""
	}
	if !artifact.ValidKind(a.Kind) {
		return ErrorResult("the %q FIELD must be one of requirements | spec | plan | project "+
			"— e.g. {%q:%q}. You sent kind=%q id=%q", "kind", "kind", "plan", a.Kind, a.ID), nil
	}

	doc, err := artifact.Load(ectx.ProjectRoot, artifact.Kind(a.Kind))
	if err != nil {
		return ErrorResult("read %s: %v", a.Kind, err), nil
	}
	if strings.TrimSpace(doc.Raw) == "" {
		// An absent document is a fact worth stating plainly: a model told
		// "not found" may invent one instead of saying it is missing.
		return SuccessResult("%s does not exist yet for this project.", a.Kind), nil
	}

	if a.ID == "" {
		return SuccessResult("%s", doc.Raw), nil
	}
	sec := doc.Section(a.ID)
	if sec == nil {
		return ErrorResult("%s has no section %q (has: %s)",
			a.Kind, a.ID, strings.Join(doc.IDs(), ", ")), nil
	}
	return SuccessResult("## %s — %s\n\n%s", sec.ID, sec.Title, sec.Body), nil
}

// TaskRead lets a duckling read one task from the plan.
type TaskRead struct{}

func (t *TaskRead) Name() string   { return "task_read" }
func (t *TaskRead) Mutating() bool { return false }

func (t *TaskRead) Description() string {
	return "Read a task from the plan by id, with what it implements and what it depends on."
}

func (t *TaskRead) Schema() interface{} {
	return NewSchema().AddString("id", "Task id, e.g. T-001", true)
}

func (t *TaskRead) Execute(ctx context.Context, ectx *ExecContext, args json.RawMessage) (*Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := ParseArgs(args, &a); err != nil {
		return ErrorResult("invalid args: %v", err), nil
	}
	plan, err := artifact.Load(ectx.ProjectRoot, artifact.KindPlan)
	if err != nil {
		return ErrorResult("read plan: %v", err), nil
	}
	sec := plan.Section(a.ID)
	if sec == nil {
		return ErrorResult("no task %q in the plan (has: %s)", a.ID, strings.Join(plan.IDs(), ", ")), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n", sec.ID, sec.Title)
	if len(sec.Implements) > 0 {
		fmt.Fprintf(&b, "Implements: %s\n", strings.Join(sec.Implements, ", "))
	}
	if dep := sec.Field("depends on"); dep != "" {
		fmt.Fprintf(&b, "Depends on: %s\n", dep)
	}
	if sec.Body != "" {
		b.WriteString("\n" + sec.Body + "\n")
	}
	return SuccessResult("%s", b.String()), nil
}
