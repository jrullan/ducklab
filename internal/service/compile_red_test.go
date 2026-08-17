package service

import "testing"

// The classifier must read go's STRUCTURE, not its vocabulary: a test about
// compile-red detection legitimately carries "does not compile" in its
// fixtures, and an assertion failure quoting it is still assertion-red.
func TestCompileFailureIsStructuralNotLexical(t *testing.T) {
	assertionRed := `--- FAIL: TestDetectorIgnoresDownloadChatter (0.00s)
    detector_test.go:12: output "go: downloading x" judged as does not compile; want assertion-red, saw build failed marker
FAIL
FAIL	github.com/jrullan/ducklab/internal/verify	0.1s
FAIL`
	if compileFailure(assertionRed) {
		t.Error("assertion output quoting compiler vocabulary judged as compile failure")
	}
	buildRed := `# github.com/jrullan/ducklab/internal/service [github.com/jrullan/ducklab/internal/service.test]
internal/service/x_test.go:5:2: undefined: Nope
FAIL	github.com/jrullan/ducklab/internal/service [build failed]
FAIL`
	if !compileFailure(buildRed) {
		t.Error("a real [build failed] line not recognized")
	}
	tscRed := `src/views/Board.tsx(12,5): error TS2304: Cannot find name 'nope'.`
	if !compileFailure(tscRed) {
		t.Error("a tsc error not recognized")
	}
}
