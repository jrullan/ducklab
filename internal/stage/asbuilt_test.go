package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func writeDoc(t *testing.T, root string, kind artifact.Kind, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(artifact.Path(root, kind)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(root, kind), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The first spec of an adopted project is a survey too: it must teach the
// as-built marker, or the plan re-plans the product and the spine demands
// tasks for built things.
func TestAdoptedSpecPromptTeachesTheAsBuiltMarker(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements,
		"---\nkind: requirements\norigin: adopted\napproved_by: human\n---\n\n"+
			"## REQ-001 — Engine\n\n**Priority:** must\n\nIt runs.\n")
	prompt, err := BuildPrompt(root, Spec, "", &artifact.Document{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "**As-built:** yes") {
		t.Error("the adopted spec prompt does not teach the marker")
	}
	if !strings.Contains(prompt, "ADOPTED") {
		t.Error("the prompt does not say why the marker matters")
	}
}

// A greenfield spec prompt says nothing about adoption.
func TestGreenfieldSpecPromptStaysClean(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindRequirements,
		"---\nkind: requirements\napproved_by: human\n---\n\n"+
			"## REQ-001 — Engine\n\n**Priority:** must\n\nIt runs.\n")
	prompt, err := BuildPrompt(root, Spec, "", &artifact.Document{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "As-built") {
		t.Error("a greenfield prompt teaches an adoption marker")
	}
}

// The plan prompt tells the model to skip as-built sections.
func TestPlanPromptSkipsAsBuiltSections(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Engine\n\n**Implements:** REQ-001\n**As-built:** yes\n\nRuns.\n\n"+
			"## SPEC-002 — New thing\n\n**Implements:** REQ-002\n\nNot built yet.\n")
	prompt, err := BuildPrompt(root, Plan, "", &artifact.Document{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Plan NO tasks for them") {
		t.Error("the plan prompt does not exempt as-built sections")
	}
}
