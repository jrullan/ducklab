package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func TestBugPromotionWidensOnePortionIntoAnExecutableStackLane(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for path, body := range map[string]string{
		"meson.build":                   "subdir('tests')\n",
		"tests/meson.build":             "test('backend', executable('test_backend', 'test_backend.c'))\n",
		"src/backend/portal_capture.c":  "",
		"src/backend/portal_capture.h":  "",
		"src/backend/capture_backend.h": "",
		"src/ui/selection_engine.c":     "",
		"src/ui/selection_engine.h":     "",
		"src/backend/x11_capture.c":     "",
		"src/backend/composition.c":     "",
		"tests/test_backend_dispatch.c": "",
		"tests/test_portal_capture.c":   "",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "backend dispatch ignores active-window bounds"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "dispatch spans both backends",
		"suspected_files": []string{
			"src/backend/composition.c", "src/ui/selection_engine.c", "src/backend/x11_capture.c",
			"src/backend/portal_capture.c", "src/backend/capture_backend.h",
		},
		"deliverables": []string{"Automated regression coverage verifies dispatch and unavailable bounds"},
		"proposal": []interface{}{map[string]interface{}{
			"title":      "Correct active-window backend dispatch",
			"acceptance": []interface{}{"regression tests verify dispatch, cropping, and unavailable bounds"},
			"owns":       []interface{}{"src/backend/portal_capture.c", "src/ui/selection_engine.c"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	task := plan.Section(out["task"].(string))
	if task == nil {
		t.Fatal("promoted task is absent from the plan")
	}
	want := []string{
		"src/backend/portal_capture.c", "src/ui/selection_engine.c",
		"src/backend/composition.c", "src/backend/x11_capture.c", "src/backend/capture_backend.h",
		"src/ui/selection_engine.h", "tests", "meson.build",
	}
	for _, path := range want {
		found := false
		for _, owned := range task.Owns {
			if owned == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("promoted lane omitted %q: %v", path, task.Owns)
		}
	}
	if !strings.Contains(task.Body, "Lane widened at promote:") ||
		!strings.Contains(task.Body, "stack test registration") ||
		!strings.Contains(task.Body, "triage suspected file") {
		t.Errorf("promotion did not explain its deterministic additions:\n%s", task.Body)
	}
}

func TestBugPromotionGivesSplitTestPortionRegistrationAndSiblingHeader(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for path, body := range map[string]string{
		"meson.build":               "subdir('tests')\n",
		"tests/meson.build":         "test('selection', executable('test_selection', 'test_selection.c'))\n",
		"src/core/capture.c":        "",
		"src/ui/selection_engine.c": "",
		"src/ui/selection_engine.h": "",
		"tests/test_selection.c":    "",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "selection and capture fail together"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "two concerns",
		"suspected_files": []string{"src/core/capture.c", "src/ui/selection_engine.c"},
		"proposal": []interface{}{
			map[string]interface{}{"title": "Correct capture", "acceptance": []interface{}{"capture returns pixels"}, "owns": []interface{}{"src/core/capture.c"}},
			map[string]interface{}{"title": "Correct selection", "acceptance": []interface{}{"a regression test proves fake DISPLAY selection behavior"}, "owns": []interface{}{"src/ui/selection_engine.c"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	taskIDs, ok := out["tasks"].([]string)
	if !ok || len(taskIDs) != 2 {
		t.Fatalf("promoted tasks = %#v, want two task ids", out["tasks"])
	}
	plan, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	capture := plan.Section(taskIDs[0])
	selection := plan.Section(taskIDs[1])
	if capture == nil || selection == nil {
		t.Fatalf("promoted tasks absent from plan: %v", taskIDs)
	}
	for _, path := range []string{"src/ui/selection_engine.c", "src/ui/selection_engine.h", "tests", "meson.build"} {
		if !slices.Contains(selection.Owns, path) {
			t.Errorf("test portion omitted %q: %v", path, selection.Owns)
		}
	}
	for _, path := range []string{"tests", "meson.build"} {
		if slices.Contains(capture.Owns, path) {
			t.Errorf("non-test portion unexpectedly owns shared test infrastructure %q: %v", path, capture.Owns)
		}
	}
}

func TestBugPromotionResolvesUniqueBareLanePath(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	path := filepath.Join(root, "src", "app", "clipboard_handler.c")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "clipboard handler fails"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "one concern",
		"suspected_files": []string{"clipboard_handler.c"},
		"proposal": []interface{}{map[string]interface{}{
			"title": "Correct clipboard handling", "acceptance": []interface{}{"clipboard works"}, "owns": []interface{}{"clipboard_handler.c"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	task := plan.Section(out["task"].(string))
	if task == nil || !slices.Contains(task.Owns, "src/app/clipboard_handler.c") {
		t.Fatalf("promoted lane = %#v, want resolved repository-relative path", task)
	}
	if !strings.Contains(task.Body, "resolved bare lane clipboard_handler.c") {
		t.Errorf("promotion did not record the path resolution:\n%s", task.Body)
	}
}

func TestPromotionBareLanePathRequiresExactlyOneMatch(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"src/app/lifecycle.c", "src/delivery/lifecycle.c"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := resolvePromotionLanePath(root, "lifecycle.c")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), "src/app/lifecycle.c") ||
		!strings.Contains(err.Error(), "src/delivery/lifecycle.c") {
		t.Fatalf("ambiguous resolution error = %v", err)
	}
	_, _, err = resolvePromotionLanePath(root, "missing.c")
	if err == nil || !strings.Contains(err.Error(), "matches no repository path") {
		t.Fatalf("missing resolution error = %v", err)
	}
}

func TestBugPromotionRefusesToGuessASuspectedFileBetweenPortions(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, _ := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "two subsystems fail together"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "high", "reason": "two concerns",
		"suspected_files": []string{"shared/contract.h"},
		"proposal": []interface{}{
			map[string]interface{}{"title": "Fix alpha", "acceptance": []interface{}{"alpha works"}, "owns": []interface{}{"alpha/alpha.c"}},
			map[string]interface{}{"title": "Fix beta", "acceptance": []interface{}{"beta works"}, "owns": []interface{}{"beta/beta.c"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BugPromote(context.Background(), id, "B-001", "human"); err == nil || !strings.Contains(err.Error(), "does not assign suspected file shared/contract.h") {
		t.Fatalf("promote error = %v, want an actionable ambiguous-lane refusal", err)
	}
}
