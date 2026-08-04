package stage

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// Adoption is the front door for a codebase that already exists: the survey
// prompt asks for the requirements the code ALREADY satisfies, grounded in
// the tree, with inferred intent marked — not an interview about an idea.
func TestAdoptIntakeAsksForASurveyOfTheTree(t *testing.T) {
	root := t.TempDir()
	prompt, err := BuildPrompt(root, Intake, "", &artifact.Document{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"requirements it ALREADY satisfies",
		"read it before writing",
		"**Assumption:**",
		"Invent nothing aspirational",
	} {
		if !strings.Contains(prompt, must) {
			t.Errorf("survey prompt lacks %q", must)
		}
	}
	// The interview fallback is for products that are still ideas; a survey
	// asking the human what they are building has not read the room.
	if strings.Contains(prompt, "Ask the human what they are building") {
		t.Error("the survey fell back to the interview")
	}
}

// A brief alongside adoption is orientation, not the task.
func TestAdoptCarriesThePersonsContext(t *testing.T) {
	root := t.TempDir()
	prompt, err := BuildPrompt(root, Intake, "It is a multi-LLM dev harness.", &artifact.Document{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## Context from the person") ||
		!strings.Contains(prompt, "multi-LLM dev harness") {
		t.Error("the person's context was dropped from the survey")
	}
}
