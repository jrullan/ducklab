package main

import (
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/engineapi"
)

// B-520 review: the stage endpoint accepts a reference as a path string OR
// {path, role}. The generated client must say both, with role restricted to
// the values the server accepts — not narrow every existing caller's
// string[] into objects.
func TestGeneratedClientKeepsBothReferenceShapes(t *testing.T) {
	ts := renderTS(engineapi.BuildOpenAPI("0.3.0"))
	for _, want := range []string{
		`export type ServiceStageRef = string | { path: string; role?: "scope" | "background" };`,
		"refs?: ServiceStageRef[];",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("generated.ts lacks %q", want)
		}
	}
	if strings.Contains(ts, "export interface ServiceStageRef") {
		t.Error("StageRef rendered as an object-only interface")
	}
}

func TestTSTypeRendersUnionsAndEnums(t *testing.T) {
	union := map[string]any{"oneOf": []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "object", "required": []any{"a"}, "properties": map[string]any{
			"a": map[string]any{"type": "integer"},
			"b": map[string]any{"type": "string", "enum": []any{"x", "y"}},
		}},
	}}
	if got, want := tsType(union), `string | { a: number; b?: "x" | "y" }`; got != want {
		t.Errorf("tsType(union) = %s, want %s", got, want)
	}
	if !isUnion(union) || isUnion(map[string]any{"type": "object"}) {
		t.Error("isUnion misclassifies")
	}
}
