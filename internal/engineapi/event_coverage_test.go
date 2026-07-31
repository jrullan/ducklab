package engineapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every event the engine emits, known to the desktop.
//
// The sibling of the route guard, and the one that was missing when it mattered:
// an hour after auditing this exact class I added two event types and neither
// was registered on the client. The discipline that fails is not remembering,
// it is having nothing that remembers for you.
//
// A registered event is not necessarily a rendered one — events.ts is the
// vocabulary, not the UI. But an event missing from the vocabulary cannot be
// rendered at all, and that is the failure this catches.
var emitPattern = regexp.MustCompile(`(?:AppendEvent|emit)\(\s*(?:params|&p\.ExecuteParams|emitFn)?\s*,?\s*"([a-z][a-z_]*)"`)

func TestEveryEmittedEventIsKnownToTheDesktop(t *testing.T) {
	known, err := os.ReadFile("../../frontend/src/api/events.ts")
	if err != nil {
		t.Skipf("no desktop event list to check: %v", err)
	}
	vocabulary := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z][a-z_]*)"`).FindAllStringSubmatch(string(known), -1) {
		vocabulary[m[1]] = true
	}

	emitted := map[string]bool{}
	root := filepath.Join("..", "..", "internal")
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range emitPattern.FindAllStringSubmatch(string(src), -1) {
			emitted[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) < 20 {
		t.Fatalf("only found %d event types; the pattern has stopped matching", len(emitted))
	}

	var unknown []string
	for e := range emitted {
		if !vocabulary[e] {
			unknown = append(unknown, e)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("%d event types the engine emits are not in frontend/src/api/events.ts:\n  %s\n\n"+
			"An event the desktop does not know cannot be rendered, stored, or replayed. "+
			"Add it to the list, even if nothing shows it yet.",
			len(unknown), strings.Join(unknown, "\n  "))
	}
}
