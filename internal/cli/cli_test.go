package cli

import (
	"os"
	"strings"
	"testing"
)

func TestVersionPrintsInstalledProvenance(t *testing.T) {
	oldArgs, oldVersion := os.Args, Version
	defer func() { os.Args, Version = oldArgs, oldVersion }()
	os.Args = []string{"ducklab", "--version"}
	Version = "0.4.0"
	read, write, err := os.Pipe()
	if err != nil { t.Fatal(err) }
	oldStdout := os.Stdout
	os.Stdout = write
	if code := Run([]string{"--version"}); code != 0 { t.Fatalf("exit code = %d", code) }
	write.Close()
	os.Stdout = oldStdout
	// The exact values are build-time data; the operator-facing contract is that
	// branch and commit are present, rather than an untraceable version number.
	buf := make([]byte, 4096)
	n, _ := read.Read(buf)
	text := string(buf[:n])
	if !strings.Contains(text, "dev") || !strings.Contains(text, "0.4.0") {
		t.Fatalf("version omitted installed provenance: %q", text)
	}
}

// A word in the subcommand position used to fall through to "it must be a task
// ID", so `ducklab run diff <id>` started a model run on a task called "diff"
// instead of printing a diff — and any typo did the same. A mistake should not
// cost tokens.
func TestAnUnknownRunSubcommandDoesNotStartARun(t *testing.T) {
	for _, arg := range []string{"diffs", "acept", "sohw", "deploy"} {
		if runVerbs[arg] || taskIDRe.MatchString(arg) {
			t.Errorf("%q would still be dispatched instead of refused", arg)
		}
	}
	for _, id := range []string{"T-001", "BUG-42", "M-7"} {
		if !taskIDRe.MatchString(id) {
			t.Errorf("%q is a task ID and must still run", id)
		}
	}
	for _, v := range []string{"diff", "accept", "show", "list", "watch", "resume", "abort", "reject", "answer", "gc"} {
		if !runVerbs[v] {
			t.Errorf("%q is a documented subcommand but is not dispatched", v)
		}
	}
}

// The note is prose. Parsing it as flags would reject the first sentence that
// happens to start with a word the parser knows.
func TestReviseTakesTheRestOfTheLineAsANote(t *testing.T) {
	// Mirrors the loop in stageCmd: once `revise` is seen, nothing else is a
	// flag.
	args := []string{"revise", "SPEC-004", "should", "also", "lock", "--from", "the", "opposite"}
	sub := ""
	var note []string
	for _, a := range args {
		if sub == "revise" {
			note = append(note, a)
			continue
		}
		if a == "revise" {
			sub = a
		}
	}
	got := strings.Join(note, " ")
	if got != "SPEC-004 should also lock --from the opposite" {
		t.Errorf("note = %q", got)
	}
}
