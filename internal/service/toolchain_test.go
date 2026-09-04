package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

// The plan declares its toolchain per milestone; the task's own milestone is
// consulted first, and a missing binary is named for the person.
func TestDeclaredToolchainComesFromTheTasksMilestone(t *testing.T) {
	plan, err := artifact.Parse("## M-01 — Build\n\n**Toolchain:** meson, ninja, pkg-config\n\n### T-001 — Scaffold\n\n**Implements:** SPEC-001\n\nBody.\n\n## M-02 — Tests\n\n**Toolchain:** pytest\n\n### T-002 — Tests\n\n**Implements:** SPEC-002\n\nBody.\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(declaredToolchain(plan, "T-001"), ","); got != "meson,ninja,pkg-config" {
		t.Fatalf("T-001 toolchain = %q", got)
	}
	if got := strings.Join(declaredToolchain(plan, "T-002"), ","); got != "pytest" {
		t.Fatalf("T-002 toolchain = %q", got)
	}
	// A tool that is surely absent is reported; one that is surely present is not.
	missing := missingTools([]string{"sh", "definitely-not-a-binary-xyz (apt: nothing)"})
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "definitely-not-a-binary-xyz") {
		t.Fatalf("missing = %v", missing)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	q := toolchainQuestion("T-001", missing)
	if !strings.Contains(q.Question, "definitely-not-a-binary-xyz") || len(q.Options) != 2 {
		t.Fatalf("question = %+v", q)
	}
}

func TestCapabilityStructureRejectsGTK3ContractsInGTK4PlanTask(t *testing.T) {
	plan := &artifact.Document{Sections: []artifact.Section{{ID: "M-01", Children: []artifact.Section{{
		ID: "T-006", Body: "**Verification:** `cc -c ui.c $(pkg-config --cflags gtk4)`\nUse gtk_window_set_keep_above and GtkEventControllerButton.",
	}}}}}
	joined := strings.Join(capabilityStructureFindings(plan), "\n")
	if !strings.Contains(joined, "T-006") || !strings.Contains(joined, "removed-window-api") || !strings.Contains(joined, "invented-controller") {
		t.Fatalf("GTK4 plan findings = %s", joined)
	}
}

func TestToolchainCapabilitiesIncludePkgConfigModules(t *testing.T) {
	declared := []string{
		"cmd:sh",
		"cmd:definitely-not-a-command-xyz",
		"pkg-config:definitely-not-a-module-xyz>=99.0",
	}
	missing := missingTools(declared)
	got := strings.Join(missing, ",")
	if !strings.Contains(got, "cmd:definitely-not-a-command-xyz") {
		t.Fatalf("missing capabilities = %q; absent command was not reported", got)
	}
	if !strings.Contains(got, "pkg-config:definitely-not-a-module-xyz>=99.0") {
		t.Fatalf("missing capabilities = %q; absent pkg-config module was not reported", got)
	}
	if strings.Contains(got, "cmd:sh") {
		t.Fatalf("missing capabilities = %q; available command was reported missing", got)
	}
}

func TestClosestCapabilityCorrectsPackageNamesToPkgConfigModules(t *testing.T) {
	modules := []string{"gtk4", "x11", "gdk-pixbuf-2.0", "Qt6Xml"}
	for input, want := range map[string]string{"libgtk-4": "gtk4", "libx11": "x11"} {
		if got := closestCapability(input, modules); got != want {
			t.Errorf("closestCapability(%q) = %q, want %q", input, got, want)
		}
	}
	if got := closestCapability("libtoml", modules); got != "" {
		t.Errorf("closestCapability(libtoml) = %q, want no semantically unsupported suggestion", got)
	}
}

func TestAbsentPkgConfigModuleIsDeferredToBuildPreflight(t *testing.T) {
	plan, err := artifact.Parse("## M-01 — Notifications\n\n**Toolchain:** pkg-config:module-that-is-valid-elsewhere\n\n### T-001 — Notify\n\n**Implements:** SPEC-001\n", artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	if findings := capabilityStructureFindings(plan); len(findings) != 0 {
		t.Fatalf("absent module was treated as an invalid name: %v", findings)
	}
	if missing := missingTools([]string{"pkg-config:module-that-is-valid-elsewhere"}); len(missing) != 1 {
		t.Fatalf("build preflight did not retain absent module: %v", missing)
	}
}

func TestActiveSpecCannotImplementAWontRequirement(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	requirements := "## REQ-001 — Required\n\n**Priority:** must\n\nDo it.\n\n## REQ-014 — Excluded\n\n**Priority:** wont\n\nDo not do it.\n"
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "requirements.md"), []byte(requirements), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := artifact.Parse("## SPEC-001 — Active\n\n**Implements:** REQ-001, REQ-014\n\nBuild it.\n", artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	findings := wontRequirementStructureFindings(dir, spec)
	if len(findings) != 1 || !strings.Contains(findings[0], "SPEC-001 actively implements REQ-014") {
		t.Fatalf("findings = %v", findings)
	}
}

func TestWontSpecMayTraceAWontRequirement(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	requirements := "## REQ-014 — Excluded\n\n**Priority:** wont\n\nDo not do it.\n"
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "requirements.md"), []byte(requirements), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := artifact.Parse("## SPEC-014 — Excluded\n\n**Priority:** wont\n\n**Implements:** REQ-014\n\nNot part of the implementation.\n", artifact.KindSpec)
	if err != nil {
		t.Fatal(err)
	}
	if findings := wontRequirementStructureFindings(dir, spec); len(findings) != 0 {
		t.Fatalf("wont exclusion was treated as active work: %v", findings)
	}
}

func TestTaskVerificationCommandIsTheBacktickedCommandOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := "## M-01 — Core\n\n### T-001 — Header\n\n**Implements:** SPEC-001\n**Verification:** `cc -fsyntax-only src/app.h` proves the header compiles.\n**Exercises:** src/app.h\n"
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := taskVerificationCommand(dir, "T-001"); got != "cc -fsyntax-only src/app.h" {
		t.Fatalf("task verification command = %q", got)
	}
	plan = "## M-01 — Core\n\n### T-001 — Header\n\n**Produces:** file:src/app.h, build-target:app, file:include/app.h\n**Consumes:** file:src/base.h, capability:gtk4\n**Verification:** `true`\n"
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(taskArtifactFiles(dir, "T-001", "produces"), ","); got != "src/app.h,include/app.h" {
		t.Fatalf("produced files = %q", got)
	}
	if got := strings.Join(taskArtifactFiles(dir, "T-001", "consumes"), ","); got != "src/base.h" {
		t.Fatalf("consumed files = %q", got)
	}
}

func TestTaskDependencyProducedFilesWalksContractGraph(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — Core\n\n" +
			"### T-001 — Base\n\n**Produces:** file:src/base.c, file:src/base.h\n\n" +
			"### T-002 — Middle\n\n**Depends on:** T-001\n\n**Produces:** file:src/middle.c\n\n" +
			"### T-003 — CLI\n\n**Depends on:** T-002\n\n**Produces:** file:src/cli.c\n",
	})
	got := taskDependencyProducedFiles(dir, "T-003")
	want := []string{"src/base.c", "src/base.h", "src/middle.c"}
	if !slices.Equal(got, want) {
		t.Fatalf("dependency produced files = %v, want %v", got, want)
	}
}

func TestTaskAcceptanceProbesReadsNumberedCommands(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	_, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-01 — CLI\n\n### T-010 — Parse\n\n" +
			"**Acceptance probes:**\n1. `./app --help`\n2. `! ./app --save`\n\n**Produces:** file:src/cli.c\n",
	})
	if got := taskAcceptanceProbes(dir, "T-010"); !slices.Equal(got, []string{"./app --help", "! ./app --save"}) {
		t.Fatalf("acceptance probes = %v", got)
	}
}
