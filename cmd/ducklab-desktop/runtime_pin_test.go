package main

import (
	"io/fs"
	"strings"
	"testing"
)

// Every native binding — restart engine, reconnect, the directory picker, OS
// notifications — reaches Go through window.wails.Call.ByName. Wails v3
// serves that runtime at /wails/runtime.js but does not inject it: the page
// must ask for it, and for months ours didn't. Then the first fix asked
// wrongly — a classic script tag, for a file that is an ES module, so its
// `export` line killed the parse and nothing ran. The load now lives in the
// bundle as a dynamic import. The pin walks the shipped artifact — wherever
// the reference landed — because what regresses is the built thing.
func TestTheShippedBundleLoadsTheWailsRuntime(t *testing.T) {
	found := false
	err := fs.WalkDir(assets, "frontend/dist", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return err
		}
		data, rerr := assets.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), "/wails/runtime.js") {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("no embedded dist — was the frontend built? %v", err)
	}
	if !found {
		t.Error("nothing in the shipped bundle loads /wails/runtime.js, so window.wails is absent and every native binding is dead")
	}
}
