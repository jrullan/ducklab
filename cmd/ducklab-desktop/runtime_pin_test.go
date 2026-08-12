package main

import (
	"strings"
	"testing"
)

// Every native binding — restart engine, reconnect, the directory picker, OS
// notifications — reaches Go through window.wails.Call.ByName. Wails v3
// serves that runtime at /wails/runtime.js but does not inject it: the page
// must ask for it, and for months ours didn't. The picker and notifications
// degraded silently behind their fallbacks; engine supervision was the first
// binding to say out loud, in the desktop itself, that it was "only available
// in the desktop app". The pin reads the shipped bundle, not the source —
// what regressed was the built artifact.
func TestTheShippedPageLoadsTheWailsRuntime(t *testing.T) {
	html, err := assets.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("no embedded index.html — was the frontend built? %v", err)
	}
	if !strings.Contains(string(html), "/wails/runtime.js") {
		t.Error("index.html never loads /wails/runtime.js, so window.wails is absent and every native binding is dead")
	}
}
