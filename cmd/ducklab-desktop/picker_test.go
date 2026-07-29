package main

import (
	"strings"
	"testing"
)

// The frontend calls this binding by name, and Wails keys bindings on the
// package's import path. Writing that string in TypeScript would mean moving
// the package breaks the folder picker at runtime, silently, until someone
// clicks the button.
func TestChooseDirectoryFQNMatchesWailsBindingFormat(t *testing.T) {
	got := ChooseDirectoryFQN()
	if !strings.HasSuffix(got, ".Picker.ChooseDirectory") {
		t.Errorf("FQN = %q, want it to end in .Picker.ChooseDirectory", got)
	}
	if !strings.HasPrefix(got, "github.com/jrullan/ducklab/") {
		t.Errorf("FQN = %q, want the module's import path", got)
	}
}
