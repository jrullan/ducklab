package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCapabilityIsDiagnosticByDefault(t *testing.T) {
	checks, err := DefaultRegistry().ResolveChecks(Context{
		TaskVerification: "cc -fsyntax-only src/backend/capture.c",
	}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Enforcement != Diagnostic {
		t.Fatalf("checks = %+v", checks)
	}
	if checks[0].Command != "cc -fsyntax-only src/backend/capture.c -Wall -Wextra -Werror" {
		t.Fatalf("command = %q", checks[0].Command)
	}
}

func TestNativePolicyCanRequireOrDisableWarnings(t *testing.T) {
	ctx := Context{TaskVerification: "clang -fsyntax-only app.c", Policies: map[string]string{"c-native.warnings": "required"}}
	checks, _ := DefaultRegistry().ResolveChecks(ctx, true, nil, nil)
	if len(checks) != 1 || checks[0].Enforcement != Required {
		t.Fatalf("required checks = %+v", checks)
	}
	ctx.Policies["c-native.warnings"] = "off"
	checks, _ = DefaultRegistry().ResolveChecks(ctx, true, nil, nil)
	if len(checks) != 0 {
		t.Fatalf("disabled checks = %+v", checks)
	}
}

func TestCapabilitiesComposeWithoutProjectTypeLabels(t *testing.T) {
	// A local adapter is enough to prove the registry composes providers; the
	// core does not need a switch for every language or stack.
	var _ Provider = testProvider{}
	r := NewRegistry(Native{}, testProvider{})
	checks, err := r.ResolveChecks(Context{TaskVerification: "cc -fsyntax-only app.c"}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || checks[0].Capability != "c-native" || checks[1].Capability != "test-stack" {
		t.Fatalf("composed checks = %+v", checks)
	}
}

func TestNativeDesktopReviewRulesComposeFromVerificationEvidence(t *testing.T) {
	profile, err := DefaultRegistry().ResolveProject(Context{
		TaskVerification: "cc -fsyntax-only $(pkg-config --cflags gio-2.0 x11 xfixes) src/backend/x11_capture.c",
	}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantCapabilities := map[string]bool{"c-native": false, "glib-async": false, "x11-image": false}
	for _, detected := range profile.Detections {
		if _, ok := wantCapabilities[detected.Capability]; ok {
			wantCapabilities[detected.Capability] = true
		}
	}
	for capability, found := range wantCapabilities {
		if !found {
			t.Errorf("capability %q was not detected: %+v", capability, profile.Detections)
		}
	}
	if len(profile.ReviewRules) != 7 {
		t.Fatalf("review rules = %+v", profile.ReviewRules)
	}
	joined := ""
	for _, rule := range profile.ReviewRules {
		joined += rule.Guidance + "\n"
	}
	for _, want := range []string{"not powers of two", "trailing zeroes", "width*4", "zero does not mean", "g_object_ref(task)", "g_thread_unref", "nested owned allocations"} {
		if !strings.Contains(joined, want) {
			t.Errorf("review guidance lacks %q:\n%s", want, joined)
		}
	}
}

func TestReviewRulesAreAbsentWithoutMatchingStackEvidence(t *testing.T) {
	profile, err := DefaultRegistry().ResolveProject(Context{TaskVerification: "cc -fsyntax-only plain.c"}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.ReviewRules) != 0 {
		t.Fatalf("unrelated native task received rules: %+v", profile.ReviewRules)
	}
}

type testProvider struct{}

func (testProvider) ID() string { return "test-stack" }
func (testProvider) Checks(Context) []Check {
	return []Check{{Capability: "test-stack", Name: "fixture", Command: "true", Enforcement: Diagnostic}}
}

func TestCompoundCommandIsNotRewritten(t *testing.T) {
	checks, _ := DefaultRegistry().ResolveChecks(Context{TaskVerification: "cc -fsyntax-only a.c && ./check"}, true, nil, nil)
	if len(checks) != 0 {
		t.Fatalf("compound command contributed checks: %+v", checks)
	}
}

func TestProjectCapabilitiesComposeGoAndFrontendFromEvidence(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/fixture\n\ngo 1.24\n")
	writeFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	writeFixture(t, root, "frontend/package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFixture(t, root, "frontend/tsconfig.json", `{}`)

	profile, err := DefaultRegistry().ResolveProject(Context{ProjectRoot: root}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Gate == nil || profile.Gate.Kind != "tests" {
		t.Fatalf("gate = %+v", profile.Gate)
	}
	for _, want := range []string{"go test ./...", "cd frontend", "tsc --noEmit", "vitest run"} {
		if !strings.Contains(profile.Gate.Command, want) {
			t.Errorf("composed command %q lacks %q", profile.Gate.Command, want)
		}
	}
	gotEvidence := make(map[string]bool)
	for _, detection := range profile.Detections {
		for _, evidence := range detection.Evidence {
			gotEvidence[evidence] = true
		}
	}
	for _, want := range []string{"go.mod", "frontend/package.json", "frontend/tsconfig.json"} {
		if !gotEvidence[want] {
			t.Errorf("resolved profile lacks evidence %q: %+v", want, profile.Detections)
		}
	}
}

func TestProjectGatePriorityIsDataNotRegistryOrder(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Cargo.toml", "[package]\nname='fixture'\nversion='0.1.0'\n")
	writeFixture(t, root, "tsconfig.json", `{}`)
	// Reverse provider registration to prove selection follows candidate
	// priority rather than a hardcoded if/else or registry order.
	profile, err := NewRegistry(TypeScript{}, Rust{}).ResolveProject(Context{ProjectRoot: root}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Gate == nil || profile.Gate.Capability != "rust" || profile.Gate.Command != "cargo test" {
		t.Fatalf("resolved gate = %+v", profile.Gate)
	}
}

func TestDisabledCapabilityDoesNotContribute(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Cargo.toml", "[package]\nname='fixture'\nversion='0.1.0'\n")
	profile, err := DefaultRegistry().ResolveProject(Context{ProjectRoot: root}, true, nil, []string{"rust"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Gate != nil {
		t.Fatalf("disabled Rust contributed gate %+v", profile.Gate)
	}
}

func TestStandaloneGolangCILintMarkerPreservesFallbackGate(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".golangci.yml", "run:\n  timeout: 5m\n")
	profile, err := DefaultRegistry().ResolveProject(Context{ProjectRoot: root}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Gate == nil || profile.Gate.Kind != "lint" || profile.Gate.Command != "golangci-lint run" {
		t.Fatalf("lint fallback = %+v", profile.Gate)
	}
}

func TestBuiltInStackGateCandidates(t *testing.T) {
	tests := []struct {
		name, marker, body, capability, kind, command string
	}{
		{"node tests", "package.json", `{"scripts":{"test":"vitest run"}}`, "node", "tests", "npm test --silent"},
		{"rust tests", "Cargo.toml", "[package]\nname='fixture'\nversion='0.1.0'\n", "rust", "tests", "cargo test"},
		{"typescript build", "tsconfig.json", `{}`, "typescript", "build", "npx tsc --noEmit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, test.marker, test.body)
			profile, err := DefaultRegistry().ResolveProject(Context{ProjectRoot: root}, true, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Gate == nil || profile.Gate.Capability != test.capability || profile.Gate.Kind != test.kind || profile.Gate.Command != test.command {
				t.Fatalf("resolved gate = %+v", profile.Gate)
			}
		})
	}
}

func TestPythonSourceFallsBackToAvailableInterpreterCompile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "app.py", "print('ok')\n")
	profile, err := DefaultRegistry().ResolveProject(Context{ProjectRoot: root}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Gate == nil || profile.Gate.Capability != "python" || profile.Gate.Kind != "build" || !strings.Contains(profile.Gate.Command, "-m compileall -q .") {
		t.Fatalf("Python fallback = %+v", profile.Gate)
	}
}

func TestMesonWarnsWhenNinjaIgnoresNewSources(t *testing.T) {
	diff := "diff --git a/src/backend/capture.c b/src/backend/capture.c\n" +
		"new file mode 100644\n--- /dev/null\n+++ b/src/backend/capture.c\n"
	findings := DefaultRegistry().ObserveGate(GateObservation{
		Diff: diff, Output: "ninja: no work to do.\n",
	}, []string{"meson"})
	if len(findings) != 1 || findings[0].Kind != "build-integration" || len(findings[0].Files) != 1 || findings[0].Files[0] != "src/backend/capture.c" {
		t.Fatalf("findings = %+v", findings)
	}
	if got := DefaultRegistry().ObserveGate(GateObservation{Diff: diff, Output: "[1/1] Compiling C object"}, []string{"meson"}); len(got) != 0 {
		t.Fatalf("a compiling gate was flagged: %+v", got)
	}
	if got := DefaultRegistry().ObserveGate(GateObservation{Diff: diff, Output: "ninja: no work to do."}, []string{"go"}); len(got) != 0 {
		t.Fatalf("an unselected Meson adapter contributed: %+v", got)
	}
}
