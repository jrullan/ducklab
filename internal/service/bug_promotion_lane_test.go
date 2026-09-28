package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/store"
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

// B-442: the proposal already put the regression in tests/, but promote only
// searched its prose for the nouns "test" and "coverage". "Cover ..." was a
// perfectly clear title and, more importantly, Owns was an authoritative lane;
// forcing the person to rewrite English to unlock it made the contract weaker.
func TestBugPromotionRecognizesATestPortionFromItsOwnedLane(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultProject("p", "P")
	if err := config.SaveProject(filepath.Join(root, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/app/composition.c", "tests/test_composition_startup.c"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := &store.Bug{TestStrategy: "test-first"}
	portions := []agent.SplitProposal{
		{Title: "Wire the startup frame into the overlay", Acceptance: []string{"the overlay renders the startup frame"}, Owns: []string{"src/app/composition.c"}},
		{Title: "Cover composed frozen-frame rendering", Acceptance: []string{"the fake portal frame is rendered by the Meson regression target"}, Owns: []string{"tests/test_composition_startup.c"}},
	}
	got, err := preparePromotionPortions(root, rec, portions)
	if err != nil {
		t.Fatalf("promotion rejected an explicit test lane: %v", err)
	}
	if len(got) != 2 || !slices.Contains(got[1].Owns, "tests/test_composition_startup.c") {
		t.Fatalf("promoted portions = %#v", got)
	}
}

func TestBugPromotionUsesTheProjectsTestGlobsForPortionClaims(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultProject("p", "P")
	cfg.Verify.TestGlobs = []string{"checks/**"}
	if err := config.SaveProject(filepath.Join(root, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/widget.go", "checks/widget.case"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := &store.Bug{TestStrategy: "test-first"}
	portions := []agent.SplitProposal{
		{Title: "Correct the widget", Acceptance: []string{"the widget keeps its state"}, Owns: []string{"src/widget.go"}},
		{Title: "Exercise saved state", Acceptance: []string{"the saved state survives reload"}, Owns: []string{"checks/widget.case"}},
	}
	if _, err := preparePromotionPortions(root, rec, portions); err != nil {
		t.Fatalf("promotion ignored verify.test_globs: %v", err)
	}
}

func TestBugPromotionNamesEveryPortionWhenNoTestClaimExists(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultProject("p", "P")
	if err := config.SaveProject(filepath.Join(root, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/alpha.go", "src/beta.go"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := &store.Bug{TestStrategy: "test-first"}
	portions := []agent.SplitProposal{
		{Title: "Correct alpha", Acceptance: []string{"alpha works"}, Owns: []string{"src/alpha.go"}},
		{Title: "Correct beta", Acceptance: []string{"beta works"}, Owns: []string{"src/beta.go"}},
	}
	_, err := preparePromotionPortions(root, rec, portions)
	if err == nil {
		t.Fatal("promotion accepted a split with no test claim")
	}
	for _, want := range []string{
		`portion 1 "Correct alpha" (owns: src/alpha.go)`,
		`portion 2 "Correct beta" (owns: src/beta.go)`,
		`Regression test covers <behavior>`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("promotion error lacks %q:\n%s", want, err)
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

func TestBugPromotionRecordsAndDropsUnresolvableSuspectedFiles(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, root := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, path := range []string{"src/app/clipboard_handler.c", "src/app/lifecycle.c", "src/delivery/lifecycle.c"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BugAdd(context.Background(), id, BugRequest{Title: "clipboard lifecycle fails"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyTriage(context.Background(), id, []map[string]interface{}{{
		"bug": "B-001", "severity": "normal", "reason": "advisory guesses include bad basenames",
		"suspected_files": []string{"config.c", "lifecycle.c", "src/app/clipboard_handler.c"},
		"proposal": []interface{}{map[string]interface{}{
			"title": "Correct clipboard lifecycle", "acceptance": []interface{}{"clipboard lifecycle works"}, "owns": []interface{}{"src/app/clipboard_handler.c"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.BugPromote(context.Background(), id, "B-001", "human")
	if err != nil {
		t.Fatalf("advisory suspected files blocked promotion: %v", err)
	}
	plan, err := artifact.Load(root, artifact.KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	task := plan.Section(out["task"].(string))
	if task == nil {
		t.Fatal("promoted task is absent from plan")
	}
	for _, want := range []string{
		"suspected file config.c ignored: no such repository path",
		"suspected file lifecycle.c ignored: basename is ambiguous",
		"src/app/lifecycle.c",
		"src/delivery/lifecycle.c",
	} {
		if !strings.Contains(task.Body, want) {
			t.Errorf("promotion note lacks %q:\n%s", want, task.Body)
		}
	}
	if slices.Contains(task.Owns, "config.c") || slices.Contains(task.Owns, "lifecycle.c") {
		t.Fatalf("unresolvable advisory paths leaked into enforced lane: %v", task.Owns)
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
