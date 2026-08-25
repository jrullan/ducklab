package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeDoctorProject(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ducklab", "project.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorFindingsAndDeterminism(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[remote \"origin\"]\nurl = https://example.invalid/repo.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDoctorProject(t, root, `schema = 1
id = "doctor"
name = "Doctor"
autonomy = "guarded"

[verify]
mode = "tests"
tests = ""
link_deps = []

[budget]
max_usd = 0

[shell]
allow_prefixes = []

[modes]
build = "pair"
`)

	first, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Doctor output differs across runs:\n%#v\n%#v", first, second)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("Doctor JSON differs across runs:\n%s\n%s", firstJSON, secondJSON)
	}
	want := []Finding{
		{Key: "verify.link_deps", Proposed: "frontend/node_modules", Reason: "frontend is present but its node_modules is not linked into acceptance checkouts"},
		{Key: "github.enabled", Proposed: "true", Reason: "a git remote is configured but no remote or github configuration declares how ducklab should use it"},
		{Key: "verify.tests", Proposed: "npm test", Reason: "the selected verify mode has no command"},
		{Key: "budget.max_usd", Proposed: "5", Reason: "the project budget is zero"},
		{Key: "shell.allow_prefixes", Proposed: "npm ", Reason: "the project toolchain is not allowed by shell policy"},
		{Key: "mode_seats.pair.implementer", Proposed: "", Reason: "a mode in use has no configured required seat"},
		{Key: "mode_seats.pair.reviewer", Proposed: "", Reason: "a mode in use has no configured required seat"},
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("Doctor findings = %#v, want %#v", first, want)
	}
}

func TestDoctorReportsUnconsumedGitHub(t *testing.T) {
	root := t.TempDir()
	writeDoctorProject(t, root, `schema = 1
id = "doctor"
name = "Doctor"
autonomy = "guarded"

[verify]
mode = "tests"
tests = "go test ./..."
link_deps = []

[budget]
max_usd = 5

[github]
enabled = true

[shell]
allow_prefixes = []
`)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{{Key: "github", Proposed: "", Reason: "github configuration is present but no configured command uses GitHub or pull requests"}}
	if !reflect.DeepEqual(findings, want) {
		t.Fatalf("Doctor findings = %#v, want %#v", findings, want)
	}
}

func TestDoctorCleanProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDoctorProject(t, root, `schema = 1
id = "clean"
name = "Clean"
autonomy = "guarded"

[verify]
mode = "tests"
tests = "go test ./..."
link_deps = []

[budget]
max_usd = 5

[shell]
allow_prefixes = ["go "]

[modes]
build = "pair"

[mode_seats.pair]
implementer = ["pato"]
reviewer = ["pata"]
`)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("Doctor clean findings = %#v, want none", findings)
	}
}
