package prim

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectGateGo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	g := DetectGate(dir)
	if g.Kind != "tests" || g.Cmd != "go test ./..." {
		t.Errorf("go repo gate = %+v", g)
	}
}

func TestDetectGatePythonBuildFallback(t *testing.T) {
	// .py files but no test files and no venv → build tier (compileall)
	dir := t.TempDir()
	write(t, dir, "app.py", "x = 1\n")
	g := DetectGate(dir)
	if g.Kind != "build" {
		t.Errorf("python-no-tests gate = %+v (want build)", g)
	}
}

func TestDetectGateNone(t *testing.T) {
	// docs-only repo → no automated gate
	dir := t.TempDir()
	write(t, dir, "README.md", "# hi\n")
	g := DetectGate(dir)
	if g.Active() {
		t.Errorf("docs repo should have no gate, got %+v", g)
	}
	if g.Label() == "" || g.Kind != "none" {
		t.Errorf("none gate mislabeled: %+v", g)
	}
}

func TestGateFromCmd(t *testing.T) {
	if g := GateFromCmd("  "); g.Active() {
		t.Error("blank command should be inactive")
	}
	g := GateFromCmd("pytest -q")
	if g.Kind != "custom" || !g.Active() {
		t.Errorf("explicit gate = %+v", g)
	}
}
