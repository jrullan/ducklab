// Package fixtures guards the acceptance-test fixtures against silent decay.
//
// fixture-go-red was committed in its *fixed* state at 8e0a39b, after a run
// was executed directly against the checked-in directory. AC-7 then passed
// with the model doing nothing: the test it was supposed to make green was
// already green. These tests make that failure mode loud.
package fixtures

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copyTree copies a fixture to a temp dir so a test can never mutate the
// checked-in copy.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// fixture-go-red must be RED. If it is green, every acceptance criterion that
// uses it is vacuous.
func TestFixtureGoRedIsActuallyRed(t *testing.T) {
	dir := copyTree(t, "fixture-go-red")

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fixture-go-red PASSES its own tests — it is no longer a red fixture, "+
			"so AC-7 and AC-8 prove nothing. Restore the bug in add.go.\n%s", out)
	}
	if !strings.Contains(string(out), "TestAdd") {
		t.Errorf("expected TestAdd to fail; got:\n%s", out)
	}
}

// The bug must be the one the task description refers to, or the model is
// being asked to fix something that is not there.
func TestFixtureGoRedContainsTheExpectedBug(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("fixture-go-red", "add.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "a - b") {
		t.Errorf("add.go no longer contains the seeded bug 'a - b':\n%s", data)
	}
}

// fixture-nogate must stay free of anything a gate could detect, or AC-13
// stops testing the UNVERIFIED path.
func TestFixtureNogateHasNothingExecutable(t *testing.T) {
	var offenders []string
	err := filepath.Walk("fixture-nogate", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch filepath.Base(path) {
		case "go.mod", "package.json", "Cargo.toml", "pytest.ini", "pyproject.toml", "Makefile":
			offenders = append(offenders, path)
		}
		if filepath.Ext(path) == ".go" || filepath.Ext(path) == ".py" {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("fixture-nogate contains detectable build/test inputs %v; "+
			"the gate would no longer resolve to 'none'", offenders)
	}
}
