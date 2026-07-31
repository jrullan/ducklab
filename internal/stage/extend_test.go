package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func projectWithRequirements(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	docs := filepath.Join(root, ".ducklab", "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `---
kind: requirements
version: 2
approved_by: human
---

## REQ-001 — Draggable triangle

**Priority:** must

The vertices can be dragged and every measurement updates live.
`
	if err := os.WriteFile(filepath.Join(docs, "requirements.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// Growing a project is the normal case after the first week, and the prompt
// fought it: a re-run showed existing sections as an id list — "keep these ids
// for these items" — and a model cannot return unchanged what it was never
// given. Since the proposal replaces the document wholesale, "add a feature"
// proposed a document missing every body the model had not seen.
func TestExtendingADocumentHandsTheModelTheWholeDocument(t *testing.T) {
	root := projectWithRequirements(t)
	current, err := artifact.Load(root, artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}

	prompt, err := BuildPrompt(root, Intake, "Add undo: ctrl-z reverts the last drag.", current, "")
	if err != nil {
		t.Fatal(err)
	}

	// The body it must preserve, not just the id.
	if !strings.Contains(prompt, "every measurement updates live") {
		t.Errorf("the existing section's body is not in the prompt:\n%s", prompt)
	}
	// The framing: an addition to an existing product, not a blank page.
	if !strings.Contains(prompt, "Extend the project's requirements") {
		t.Errorf("the task line still claims a blank page:\n%s", prompt)
	}
	for _, want := range []string{"exactly as it is", "Removing a section is a decision a person makes"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the extension rules are missing %q", want)
		}
	}
	if !strings.Contains(prompt, "Add undo") {
		t.Errorf("the brief is not in the prompt")
	}
	if !strings.Contains(prompt, "REQ-002") {
		t.Errorf("the next free id is not stated:\n%s", prompt)
	}
}

// A first run is still a first run: no ghost document, no extension rules.
func TestAFreshProjectStillGetsABlankPage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := &artifact.Document{Front: artifact.Frontmatter{Kind: artifact.KindRequirements}}
	prompt, err := BuildPrompt(root, Intake, "A triangle calculator.", empty, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Write the project's requirements") {
		t.Errorf("a fresh project is not framed as one:\n%s", prompt)
	}
	if strings.Contains(prompt, "already approved") {
		t.Errorf("a fresh project got the extension rules:\n%s", prompt)
	}
}
