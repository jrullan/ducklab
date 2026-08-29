package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntentJournalImportsEveryHistoricalBrief(t *testing.T) {
	root := t.TempDir()
	for _, run := range []struct {
		id, at, brief string
		accepted      bool
	}{
		{"r-first", "2026-01-01T00:00:00Z", "Build the original calculator", true},
		{"r-later", "2026-02-01T00:00:00Z", "Add zoom and panning", false},
	} {
		dir := filepath.Join(root, ".ducklab", "runs", run.id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := `{"id":"` + run.id + `","stage":"intake","status":"done","started_at":"` + run.at + `","accepted":`
		if run.accepted {
			state += "true}"
		} else {
			state += "false}"
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(run.brief), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := EnsureIntent(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("intent entries = %d, want 2", len(doc.Sections))
	}
	if doc.Sections[0].ID != "INT-001" || !strings.Contains(doc.Sections[0].Body, "Build the original calculator") {
		t.Fatalf("first intent = %+v", doc.Sections[0])
	}
	if doc.Sections[1].Field("outcome") != "not accepted" {
		t.Fatalf("second outcome = %q", doc.Sections[1].Field("outcome"))
	}
	// Migration is idempotent: opening Documents twice cannot duplicate history.
	again, err := EnsureIntent(root)
	if err != nil || len(again.Sections) != 2 {
		t.Fatalf("second import = %d, %v", len(again.Sections), err)
	}
}

func TestAcceptedRequirementsLinkOnlyChangedSectionsToTheirIntent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(DocsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	current, _ := Parse("## REQ-001 — Existing\n\nKeep this.\n", KindRequirements)
	current.Front.Kind, current.Front.ApprovedBy = KindRequirements, "human"
	if err := os.WriteFile(Path(root, KindRequirements), []byte(Render(current)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendIntent(root, "r-new", "2026-01-02T00:00:00Z", "Add an export button"); err != nil {
		t.Fatal(err)
	}
	proposal, _ := Parse("## REQ-001 — Existing\n\nKeep this.\n\n## REQ-002 — Export\n\nDownload the result.\n", KindRequirements)
	if err := WriteProposal(root, KindRequirements, proposal, "r-new", nil); err != nil {
		t.Fatal(err)
	}
	intentID, linked, err := LinkRequirementsProposal(root, "r-new")
	if err != nil {
		t.Fatal(err)
	}
	if intentID != "INT-001" || strings.Join(linked, ",") != "REQ-002" {
		t.Fatalf("link = %s %v", intentID, linked)
	}
	got, _ := LoadProposed(root, KindRequirements)
	if got.Section("REQ-001").Field("originates from") != "" {
		t.Fatal("unchanged requirement acquired a false origin")
	}
	if got.Section("REQ-002").Field("originates from") != "INT-001" {
		t.Fatalf("origin = %q", got.Section("REQ-002").Field("originates from"))
	}
	if err := ResolveIntent(root, "r-new", "accepted", linked); err != nil {
		t.Fatal(err)
	}
	journal, _ := Load(root, KindIntent)
	if journal.Section("INT-001").Field("requirements") != "REQ-002" {
		t.Fatalf("requirements = %q", journal.Section("INT-001").Field("requirements"))
	}
}

func TestIntentWalkIsBidirectional(t *testing.T) {
	intent, _ := Parse("## INT-001 — Export\n\n**Run:** r-1\n", KindIntent)
	reqs, _ := Parse("## REQ-001 — Export\n\n**Originates from:** INT-001\n", KindRequirements)
	spine := &Spine{Intent: intent, Requirements: reqs, Spec: &Document{}, Plan: &Document{}}
	up, err := spine.Walk("REQ-001")
	if err != nil || strings.Join(up.Up, ",") != "INT-001" {
		t.Fatalf("requirement walk = %+v, %v", up, err)
	}
	down, err := spine.Walk("INT-001")
	if err != nil || strings.Join(down.Down, ",") != "REQ-001" {
		t.Fatalf("intent walk = %+v, %v", down, err)
	}
}
