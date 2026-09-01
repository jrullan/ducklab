package stage

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/strategy"
)

// The engine as working memory: a triage pass names the sections, then each
// is its own fresh conversation — the request, that section, one answer.
// Coherence over twenty thousand tokens becomes coherence over eight
// hundred, N independent times. This is how a 32k local seat updates a spec
// it could never hold.
func TestSectionedUpdateVisitsOneSectionPerCall(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Login\n\nOriginal login.\n\n"+
			"## SPEC-002 — Profile\n\nOriginal profile.\n\n"+
			"## SPEC-003 — Dashboard\n\nOriginal dashboard.\n")
	base, _ := artifact.Load(root, artifact.KindSpec)

	var prompts []string
	var scriptNames []string
	res, err := runSectioned(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-s", Mode: "council",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			prompts = append(prompts, prompt)
			scriptNames = append(scriptNames, script.Name)
			switch len(prompts) {
			case 1: // triage: touch SPEC-002, add one
				return "SPEC-002\nNEW: Exercise search\n", nil
			case 2: // the SPEC-002 pass
				if !strings.Contains(prompt, "Original profile.") {
					t.Error("the section pass did not carry the section's full text")
				}
				if strings.Contains(prompt, "Original login.") {
					t.Error("another section's body leaked into a section pass")
				}
				if !strings.Contains(prompt, "Apply ONLY the clauses whose subject belongs to this section") ||
					!strings.Contains(prompt, "Do not copy, summarize, or mention clauses about another capability") {
					t.Error("section pass lacks a semantic scope boundary")
				}
				return "## SPEC-002 — Profile, editable\n\nPer-field modals.\n", nil
			default: // the new-section pass
				return "## SPEC-900 — Exercise search\n\nSearch the catalog.\n", nil
			}
		},
	}, base, "make the profile editable and add search")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 3 {
		t.Fatalf("calls = %d, want triage + one per section", len(prompts))
	}
	if strings.Join(scriptNames, ",") != "solo,council,council" {
		t.Fatalf("sectioned modes = %v, want solo triage then reviewed section passes", scriptNames)
	}
	got := res.Proposed
	if len(got.Sections) != 4 {
		t.Fatalf("sections = %d, want 3 kept/changed + 1 added", len(got.Sections))
	}
	if got.Sections[0].Body != "Original login.” " && got.Sections[0].Body != "Original login." {
		t.Errorf("untouched section changed: %q", got.Sections[0].Body)
	}
	if !strings.Contains(got.Sections[1].Body, "Per-field modals") {
		t.Errorf("the visited section did not change: %q", got.Sections[1].Body)
	}
	if got.Sections[3].ID != "SPEC-004" || got.Sections[3].Title != "Exercise search" {
		t.Errorf("the addition landed wrong: %s — %s", got.Sections[3].ID, got.Sections[3].Title)
	}
}

// UNCHANGED is a real answer: the section survives byte for byte, and an
// unusable pass loses only its own section — never the document.
func TestSectionedRespectsUnchangedAndSurvivesBadPasses(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindSpec,
		"## SPEC-001 — Login\n\nOriginal login.\n\n## SPEC-002 — Profile\n\nOriginal profile.\n")
	base, _ := artifact.Load(root, artifact.KindSpec)
	call := 0
	res, err := runSectioned(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-u",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			call++
			switch call {
			case 1:
				return "SPEC-001\nSPEC-002\n", nil
			case 2:
				return "UNCHANGED", nil
			default:
				return "sorry, I got confused and produced no section", nil
			}
		},
	}, base, "tighten wording")
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposed.Sections[0].Body != "Original login." || res.Proposed.Sections[1].Body != "Original profile." {
		t.Errorf("UNCHANGED or a bad pass altered the document: %+v", res.Proposed.Sections)
	}
}

// A triage that names more sections than an update should touch is a
// redesign wearing an update's clothes — refused with the numbers.
func TestSectionedRefusesARedesign(t *testing.T) {
	root := t.TempDir()
	var doc, list strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&doc, "## SPEC-%03d — Section %d\n\nBody %d.\n\n", i, i, i)
	}
	for i := 1; i <= 14; i++ {
		fmt.Fprintf(&list, "SPEC-%03d\n", i)
	}
	writeDoc(t, root, artifact.KindSpec, doc.String())
	base, _ := artifact.Load(root, artifact.KindSpec)
	_, err := runSectioned(context.Background(), Params{
		ProjectRoot: root, Stage: Spec, RunID: "r-c",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			return list.String(), nil
		},
	}, base, "rewrite everything")
	if err == nil || !strings.Contains(err.Error(), "redesign") {
		t.Fatalf("a 14-section update was not refused: %v", err)
	}
}

// Sectioned, for the plan: the triage names TASKS (T-) as well as
// milestones, each visited in its own fresh conversation.
func TestSectionedPlanVisitsTasks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, artifact.KindPlan,
		"## M-001 — Core\n\nFoundation.\n\n### T-001 — Schema\n\nOriginal schema.\n\n### T-002 — Boundary\n\nOriginal boundary.\n")
	base, _ := artifact.Load(root, artifact.KindPlan)
	call := 0
	res, err := runSectioned(context.Background(), Params{
		ProjectRoot: root, Stage: Plan, RunID: "r-sp",
		Execute: func(ctx context.Context, script *strategy.Script, prompt string) (string, error) {
			call++
			if call == 1 {
				return "T-002\n", nil
			}
			if !strings.Contains(prompt, "Original boundary.") {
				t.Error("the task pass did not carry the task's body")
			}
			return "## T-002 — Boundary, hardened\n\nNew boundary body.\n", nil
		},
	}, base, "harden the boundary")
	if err != nil {
		t.Fatal(err)
	}
	got := res.Proposed
	if got.Sections[0].Children[1].Body != "New boundary body." {
		t.Errorf("the visited task did not change: %+v", got.Sections[0].Children[1])
	}
	if got.Sections[0].Children[0].Body != "Original schema." {
		t.Error("an unvisited task changed")
	}
}
