package capability

import "testing"

func TestNativeCapabilityIsDiagnosticByDefault(t *testing.T) {
	checks, err := DefaultRegistry().Resolve(Context{
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
	checks, _ := DefaultRegistry().Resolve(ctx, true, nil, nil)
	if len(checks) != 1 || checks[0].Enforcement != Required {
		t.Fatalf("required checks = %+v", checks)
	}
	ctx.Policies["c-native.warnings"] = "off"
	checks, _ = DefaultRegistry().Resolve(ctx, true, nil, nil)
	if len(checks) != 0 {
		t.Fatalf("disabled checks = %+v", checks)
	}
}

func TestCapabilitiesComposeWithoutProjectTypeLabels(t *testing.T) {
	// A local adapter is enough to prove the registry composes providers; the
	// core does not need a switch for every language or stack.
	var _ Provider = testProvider{}
	r := NewRegistry(Native{}, testProvider{})
	checks, err := r.Resolve(Context{TaskVerification: "cc -fsyntax-only app.c"}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || checks[0].Capability != "c-native" || checks[1].Capability != "test-stack" {
		t.Fatalf("composed checks = %+v", checks)
	}
}

type testProvider struct{}

func (testProvider) ID() string { return "test-stack" }
func (testProvider) Checks(Context) []Check {
	return []Check{{Capability: "test-stack", Name: "fixture", Command: "true", Enforcement: Diagnostic}}
}

func TestCompoundCommandIsNotRewritten(t *testing.T) {
	checks, _ := DefaultRegistry().Resolve(Context{TaskVerification: "cc -fsyntax-only a.c && ./check"}, true, nil, nil)
	if len(checks) != 0 {
		t.Fatalf("compound command contributed checks: %+v", checks)
	}
}
