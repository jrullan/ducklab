package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSkill = `---
name: pdf-extract
description: Extract text and tables from a PDF into markdown. Use when a task
  references a PDF the model cannot read directly.
version: 1
args:
  - name: path
    type: string
    required: true
  - name: pages
    type: string
    required: false
entry: run.sh
timeout_s: 120
---

Run it with the path to the PDF.
`

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

func TestParseReadsTheManifestAndTheBody(t *testing.T) {
	sk, err := Parse(goodSkill)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "pdf-extract" || sk.Version != 1 || sk.Entry != "run.sh" || sk.TimeoutS != 120 {
		t.Errorf("manifest = %+v", sk)
	}
	if len(sk.Args) != 2 || sk.Args[0].Name != "path" || !sk.Args[0].Required || sk.Args[1].Required {
		t.Errorf("args = %+v", sk.Args)
	}
	if sk.Body != "Run it with the path to the PDF." {
		t.Errorf("body = %q", sk.Body)
	}
	if !sk.Runnable() {
		t.Error("a skill with an entry is runnable")
	}
}

// The cheap and safe form, and the one the spec says should be the default.
func TestASkillWithNoEntryIsDocumentation(t *testing.T) {
	sk, err := Parse("---\nname: house-style\ndescription: How this codebase writes comments. Use when writing any new file.\nversion: 1\n---\n\nComments say why.\n")
	if err != nil {
		t.Fatal(err)
	}
	if sk.Runnable() {
		t.Error("a skill with no entry must not be runnable")
	}
	if sk.Body != "Comments say why." {
		t.Errorf("body = %q", sk.Body)
	}
}

func TestParseRejectsAFileWithNoFrontmatter(t *testing.T) {
	for _, bad := range []string{"just a document\n", "---\nname: x\nnever closed\n"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted a malformed SKILL.md", bad)
		}
	}
}

// A duckling sees only the description in skill_list. "PDF extraction" tells it
// the skill exists and nothing about when to reach for it (05 §7).
func TestValidateRejectsADescriptionThatIsOnlyALabel(t *testing.T) {
	for _, desc := range []string{"PDF extraction", "", "Database helper"} {
		sk := &Skill{Name: "x", Description: desc, Version: 1}
		if got := Validate(sk); len(got) == 0 {
			t.Errorf("description %q was accepted", desc)
		}
	}
	sk := &Skill{Name: "x", Description: "Extract tables from a PDF. Use when a task references a PDF.", Version: 1, Body: "b"}
	if got := Validate(sk); len(got) != 0 {
		t.Errorf("a description that says when was rejected: %v", got)
	}
}

// A validator that stops at the first problem makes fixing a skill a guessing
// game with one answer per round.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	got := Validate(&Skill{Name: "Bad Name", Description: "x", Version: 0})
	if len(got) < 3 {
		t.Errorf("got %d problems, want the name, the description and the version: %v", len(got), got)
	}
}

func TestValidateChecksArgsAndEntry(t *testing.T) {
	sk := &Skill{
		Name: "x", Version: 1, Body: "b",
		Description: "Does a thing. Use when you need the thing done.",
		Args: []Arg{
			{Name: "a", Type: "string"},
			{Name: "a", Type: "string"},
			{Name: "b", Type: "float"},
			{Name: "", Type: "string"},
		},
		Entry: "../../etc/passwd",
	}
	got := strings.Join(Validate(sk), "; ")
	for _, want := range []string{"declared twice", "unknown type", "has no name", "inside the skill directory"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %q missing %q", got, want)
		}
	}
}

func TestValidateWantsTheEntryToExist(t *testing.T) {
	dir := t.TempDir()
	skillDir := write(t, dir, "thing", goodSkill)
	sk, err := Load(skillDir, ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	// The directory is named "thing" but the manifest says "pdf-extract", and
	// a duckling addresses the skill by directory.
	problems := strings.Join(Validate(sk), "; ")
	if !strings.Contains(problems, "does not match its directory") {
		t.Errorf("a name that drifted from its directory was accepted: %q", problems)
	}
	if !strings.Contains(problems, "entry \"run.sh\" does not exist") {
		t.Errorf("a missing entry was accepted: %q", problems)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sk, _ = Load(skillDir, ScopeProject)
	sk.Name = "thing"
	if got := Validate(sk); len(got) != 0 {
		t.Errorf("a complete skill was rejected: %v", got)
	}
}

// Project shadows global, whole. Half a skill from each is a thing nobody can
// review.
func TestAProjectSkillShadowsAGlobalOne(t *testing.T) {
	global := t.TempDir()
	root := t.TempDir()

	write(t, global, "shared", "---\nname: shared\ndescription: The global one. Use when nothing else applies.\nversion: 1\nentry: g.sh\n---\nglobal body\n")
	write(t, global, "only-global", "---\nname: only-global\ndescription: Only global. Use when you need it.\nversion: 1\n---\nbody\n")
	write(t, ProjectDir(root), "shared", "---\nname: shared\ndescription: The project one. Use when in this project.\nversion: 2\n---\nproject body\n")

	all, problems := List(root, global)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(all) != 2 {
		t.Fatalf("got %d skills, want 2", len(all))
	}
	if all[0].Name != "only-global" || all[1].Name != "shared" {
		t.Errorf("not sorted by name: %v", []string{all[0].Name, all[1].Name})
	}

	shared := all[1]
	if shared.Scope != ScopeProject || shared.Version != 2 || shared.Body != "project body" {
		t.Errorf("global skill was not shadowed: %+v", shared)
	}
	// Shadowed whole: the global entry must not leak into the project's
	// documentation-only skill.
	if shared.Entry != "" {
		t.Errorf("entry %q leaked from the shadowed global skill", shared.Entry)
	}
}

// One unparseable SKILL.md must not hide every other skill on the machine.
func TestABrokenSkillIsReportedNotFatal(t *testing.T) {
	root := t.TempDir()
	write(t, ProjectDir(root), "good", "---\nname: good\ndescription: Fine. Use when you like.\nversion: 1\n---\nbody\n")
	write(t, ProjectDir(root), "broken", "no frontmatter here\n")

	all, problems := List(root, "")
	if len(all) != 1 || all[0].Name != "good" {
		t.Errorf("a broken skill hid the good one: %v", all)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "broken") {
		t.Errorf("problems = %v, want one naming the broken skill", problems)
	}
}

func TestFindNamesWhatIsAvailable(t *testing.T) {
	root := t.TempDir()
	write(t, ProjectDir(root), "alpha", "---\nname: alpha\ndescription: A thing. Use when needed.\nversion: 1\n---\nbody\n")

	if _, err := Find(root, "", "alpha"); err != nil {
		t.Errorf("Find: %v", err)
	}
	_, err := Find(root, "", "beta")
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		// A model that asked for the wrong name needs the right one, not a
		// bare "not found" it will retry differently.
		t.Errorf("err = %v, want it to list what exists", err)
	}
}

func TestNoSkillsDirectoryIsNotAnError(t *testing.T) {
	all, problems := List(t.TempDir(), filepath.Join(t.TempDir(), "nope"))
	if len(all) != 0 || len(problems) != 0 {
		t.Errorf("all=%v problems=%v", all, problems)
	}
}
