package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

// ref_read is a bridge to the run's reference documents and nothing else:
// references live outside the project root on purpose, and an unlisted path
// must be refused even when the file exists.
func TestRefReadOnlyServesTheRunsReferences(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "rbac.md")
	os.WriteFile(ref, []byte("module-first permissions"), 0o644)
	secret := filepath.Join(dir, "secret.md")
	os.WriteFile(secret, []byte("not a reference"), 0o644)

	var opened []string
	ectx := &ExecContext{RefPaths: []string{ref}, OnRefRead: func(p string) { opened = append(opened, p) }}
	tool := &RefRead{}

	res, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"`+ref+`"}`))
	if err != nil || res.IsError {
		t.Fatalf("read of a listed ref failed: %v %v", err, res)
	}
	if !strings.Contains(res.Content, "module-first permissions") {
		t.Errorf("content missing: %q", res.Content)
	}
	if len(opened) != 1 || opened[0] != ref {
		t.Errorf("OnRefRead not called with the canonical path: %v", opened)
	}

	res, _ = tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"`+secret+`"}`))
	if !res.IsError || !strings.Contains(res.Content, "not one of this run's references") {
		t.Errorf("an unlisted path was served: %v", res)
	}

	// A small model often repeats just the file name from the digest heading.
	res, _ = tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"rbac.md"}`))
	if res.IsError {
		t.Errorf("basename match refused: %v", res)
	}
}

// A document larger than the seat's result budget returns in portions, and
// the portion itself says how to continue — a blind truncation would cut the
// very sentence that says so.
func TestRefReadPortionsAndContinues(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "big.md")
	os.WriteFile(ref, []byte(strings.Repeat("x", 30_000)), 0o644)
	ectx := &ExecContext{RefPaths: []string{ref}, SeatContextTokens: 16_000}
	tool := &RefRead{}

	res, err := tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"`+ref+`"}`))
	if err != nil || res.IsError {
		t.Fatal(err, res)
	}
	if !strings.Contains(res.Content, "[continues — call ref_read again with offset=") {
		t.Fatalf("no continuation marker:\n…%s", res.Content[len(res.Content)-120:])
	}
	marker := res.Content[strings.LastIndex(res.Content, "offset=")+len("offset="):]
	marker = strings.TrimSuffix(marker, "]")
	res2, _ := tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"`+ref+`","offset":`+marker+`}`))
	if res2.IsError {
		t.Fatalf("continuation refused: %v", res2)
	}
	if !strings.Contains(res2.Content, "of 30000)") {
		t.Errorf("continuation lost its bounds header: %q", res2.Content[:100])
	}

	res3, _ := tool.Execute(context.Background(), ectx, json.RawMessage(`{"path":"`+ref+`","offset":99999}`))
	if !res3.IsError {
		t.Error("an offset past the end should be an error result")
	}
}

// The architect and reviewer ceilings carry the tool; the implementer's does
// not — reference documents brief the design seats, code briefs the coder.
func TestRefReadInDesignSeatCeilings(t *testing.T) {
	r := NewRegistry()
	for _, role := range []config.Role{config.RoleArchitect, config.RoleReviewer} {
		found := false
		for _, name := range r.Available(role) {
			if name == "ref_read" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s ceiling lacks ref_read", role)
		}
	}
}
