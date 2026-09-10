package tools

import (
	"strings"
	"testing"
)

// B-363: when the gate's output says a toolchain is missing, the log tells the
// seat that the gate runs with HOME isolated and which variables reach a
// toolchain rooted in HOME — otherwise it tries to fix the host from the run.
func TestGateHintNamesIsolatedHomeWhenAToolchainIsMissing(t *testing.T) {
	hint := gateEnvironmentHint("error: rustup could not choose a version of cargo to run, because one wasn't specified explicitly, and no default is configured.\n")
	for _, want := range []string{"HOME is isolated", "RUSTUP_HOME", "CARGO_HOME", "do not try to install or configure"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint omitted %q: %s", want, hint)
		}
	}
	if gateEnvironmentHint("test result: FAILED. 3 passed; 1 failed\n") != "" {
		t.Fatal("an ordinary red gate must not carry the environment hint")
	}
}
