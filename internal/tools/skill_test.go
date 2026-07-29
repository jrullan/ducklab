package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/skill"
)

func skillFixture(t *testing.T) (root string, ectx *ExecContext) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(skill.ProjectDir(root), "greet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
name: greet
description: Print a greeting for someone. Use when a task asks for a greeting.
version: 1
args:
  - name: who
    type: string
    required: true
  - name: loudly
    type: bool
    required: false
entry: run.sh
timeout_s: 30
---

Prints a greeting.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"argv: $*\"\necho \"env: $DUCKLAB_ARG_WHO\"\necho \"dir: $DUCKLAB_SKILL_DIR\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	docDir := filepath.Join(skill.ProjectDir(root), "house-style")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: house-style\ndescription: How this codebase writes comments. Use when writing any new file.\nversion: 1\n---\n\nComments say why, not what.\n"
	if err := os.WriteFile(filepath.Join(docDir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	return root, &ExecContext{ProjectRoot: root, ShellPolicy: config.ShellPolicy{Mode: "free", TimeoutS: 30}}
}

func run(t *testing.T, tool Tool, ectx *ExecContext, args interface{}) *Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), ectx, raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSkillListSaysWhichAreRunnable(t *testing.T) {
	_, ectx := skillFixture(t)
	out := run(t, &SkillList{}, ectx, map[string]interface{}{}).Content
	if !strings.Contains(out, "greet (run with skill_run)") {
		t.Errorf("a runnable skill was not marked as such: %q", out)
	}
	if !strings.Contains(out, "house-style (read with skill_read)") {
		t.Errorf("a documentation skill was not marked as such: %q", out)
	}
	if !strings.Contains(out, "Use when a task asks for a greeting") {
		t.Errorf("the description a duckling chooses by was dropped: %q", out)
	}
}

// A model that gets nothing back spends a turn guessing spellings.
func TestSkillListSaysSoWhenThereAreNone(t *testing.T) {
	out := run(t, &SkillList{}, &ExecContext{ProjectRoot: t.TempDir()}, map[string]interface{}{}).Content
	if !strings.Contains(out, "No skills are available") {
		t.Errorf("out = %q", out)
	}
}

func TestSkillReadReturnsTheBodyAndTheArguments(t *testing.T) {
	_, ectx := skillFixture(t)
	out := run(t, &SkillRead{}, ectx, map[string]string{"name": "greet"}).Content
	for _, want := range []string{"Prints a greeting.", "who (string, required)", "loudly (bool, optional)"} {
		if !strings.Contains(out, want) {
			t.Errorf("skill_read output missing %q: %q", want, out)
		}
	}
}

func TestSkillReadSaysWhenThereIsNothingToRun(t *testing.T) {
	_, ectx := skillFixture(t)
	out := run(t, &SkillRead{}, ectx, map[string]string{"name": "house-style"}).Content
	if !strings.Contains(out, "documentation") {
		t.Errorf("out = %q", out)
	}
}

// Arguments arrive twice, as the spec requires (05 §7): positionally and in the
// environment, so a script reads whichever suits it.
func TestSkillRunPassesArgumentsBothWays(t *testing.T) {
	_, ectx := skillFixture(t)
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose"},
	})
	if res.IsError {
		t.Fatalf("skill_run failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "argv: --who=Jose") {
		t.Errorf("positional argument missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, "env: Jose") {
		t.Errorf("DUCKLAB_ARG_WHO missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, "dir: ") {
		t.Errorf("DUCKLAB_SKILL_DIR missing: %q", res.Content)
	}
}

func TestSkillRunRefusesWithoutARequiredArgument(t *testing.T) {
	_, ectx := skillFixture(t)
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{"name": "greet"})
	if !res.IsError || !strings.Contains(res.Content, "who") {
		t.Errorf("a missing required argument was accepted: %+v", res)
	}
}

// A model that passed `person` where the skill wants `who` otherwise watches
// the skill run and do nothing.
func TestSkillRunNamesAnArgumentItDoesNotTake(t *testing.T) {
	_, ectx := skillFixture(t)
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose", "person": "Jose"},
	})
	if !res.IsError || !strings.Contains(res.Content, "person") {
		t.Errorf("an undeclared argument was silently dropped: %+v", res)
	}
}

func TestSkillRunRefusesDocumentation(t *testing.T) {
	_, ectx := skillFixture(t)
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{"name": "house-style"})
	if !res.IsError || !strings.Contains(res.Content, "skill_read") {
		t.Errorf("res = %+v", res)
	}
}

// Skill arguments come from a model. A value with a semicolon in it must not
// become a second command.
func TestSkillRunQuotesArgumentValues(t *testing.T) {
	root, ectx := skillFixture(t)
	marker := filepath.Join(root, "pwned")
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "x'; touch " + marker + "; echo '"},
	})
	if res.IsError {
		t.Fatalf("skill_run failed: %s", res.Content)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument value escaped its quotes and ran a command")
	}
}

// A skill is not a privilege escalation: the policy sees its command exactly as
// it sees one a model wrote (05 §7), and the denylist applies to both.
func TestSkillRunIsSubjectToTheDenylist(t *testing.T) {
	_, ectx := skillFixture(t)
	ectx.ShellPolicy = config.ShellPolicy{Mode: "guarded", Deny: []string{"greet"}, TimeoutS: 30}
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose"},
	})
	if !res.IsError || !strings.Contains(res.Content, "denylist") {
		t.Errorf("the denylist did not apply to a skill: %+v", res)
	}
}

// In guarded mode a model's own command must match an allowlist prefix. A
// skill's command cannot: it is an absolute path to a script, and no sane
// prefix list contains one. Applying the allowlist literally would make every
// skill unusable in the default mode.
//
// What actually gates a skill is different and stronger: the script is a file
// on disk that a human read and accepted before it could be called at all (05
// §7.1). The model chooses which accepted script to run and supplies argument
// values, which are quoted. It never composes the command.
func TestGuardedModeStillRunsAnAcceptedSkill(t *testing.T) {
	_, ectx := skillFixture(t)
	ectx.ShellPolicy = config.ShellPolicy{Mode: "guarded", AllowPrefixes: []string{"go "}, TimeoutS: 30}
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose"},
	})
	if res.IsError {
		t.Fatalf("a skill the human accepted was refused in guarded mode: %s", res.Content)
	}
}

// Off means off. A human who turned the shell off did not mean "except through
// a skill".
func TestSkillRunHonoursShellOff(t *testing.T) {
	_, ectx := skillFixture(t)
	ectx.ShellPolicy = config.ShellPolicy{Mode: "off"}
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose"},
	})
	if !res.IsError {
		t.Error("a skill ran with the shell turned off")
	}
}

// A skill whose entry moved fails confusingly. Validating before running says
// what is actually wrong.
func TestSkillRunRefusesAnInvalidSkill(t *testing.T) {
	root, ectx := skillFixture(t)
	if err := os.Remove(filepath.Join(skill.ProjectDir(root), "greet", "run.sh")); err != nil {
		t.Fatal(err)
	}
	res := run(t, &SkillRun{}, ectx, map[string]interface{}{
		"name": "greet",
		"args": map[string]interface{}{"who": "Jose"},
	})
	if !res.IsError || !strings.Contains(res.Content, "does not exist") {
		t.Errorf("res = %+v", res)
	}
}

func TestSkillToolsNameWhatIsAvailableWhenTheNameIsWrong(t *testing.T) {
	_, ectx := skillFixture(t)
	res := run(t, &SkillRead{}, ectx, map[string]string{"name": "greeting"})
	if !res.IsError || !strings.Contains(res.Content, "greet") {
		t.Errorf("res = %+v", res)
	}
}
