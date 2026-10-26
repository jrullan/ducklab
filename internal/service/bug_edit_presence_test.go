package service

import (
	"context"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func ptrTo[T any](v T) *T { return &v }

// Review of #179: the edit guessed presence from non-empty strings, so
// {"body":""} answered success and kept the old text, and a title-only edit
// erased the body. Every field now changes exactly when it is sent.
func TestABugEditChangesExactlyTheFieldsSent(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{
		Title: "drag broke", Body: "steps to reproduce", Severity: "low",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Title: ptrTo("vertex drag broke")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "vertex drag broke" || got.Body != "steps to reproduce" || got.Severity != "low" {
		t.Errorf("title-only edit = %q / %q / %q, want only the title changed", got.Title, got.Body, got.Severity)
	}
	got, err = s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Body: ptrTo("better steps")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "vertex drag broke" || got.Body != "better steps" {
		t.Errorf("body-only edit = %q / %q, want only the body changed", got.Title, got.Body)
	}
	got, err = s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Body: ptrTo("")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "" || got.Title != "vertex drag broke" {
		t.Errorf(`{"body":""} = %q / %q, want the body cleared and the title kept`, got.Title, got.Body)
	}
	// The desktop form's shape — all three, title trimmed blank — keeps its
	// old meaning: a blank title or severity is ignored, never stored.
	got, err = s.BugEdit(context.Background(), id, "B-001", BugEditRequest{Title: ptrTo(""), Body: ptrTo("again"), Severity: ptrTo("")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "vertex drag broke" || got.Body != "again" || got.Severity != "low" {
		t.Errorf("desktop-shaped edit = %q / %q / %q", got.Title, got.Body, got.Severity)
	}
}
