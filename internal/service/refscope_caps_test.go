package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// Codex on #174: collectRefFiles sorts alphabetically and the caps applied in
// that order, so with max_files 1 `a-background.md` loaded, `z-scope.md`
// dropped, and the prompt still announced a SCOPE reference.
func scopeCapFixture(t *testing.T) (dir, bg, scope string) {
	t.Helper()
	dir = t.TempDir()
	bg = filepath.Join(dir, "a-background.md")
	scope = filepath.Join(dir, "z-scope.md")
	os.WriteFile(bg, []byte("BACKGROUND-NOTES "+strings.Repeat("b", 900)), 0o644)
	os.WriteFile(scope, []byte("SCOPE-GUIDE matrix mode computes determinants."), 0o644)
	return dir, bg, scope
}

func TestScopeReferenceSurvivesTheFileCap(t *testing.T) {
	_, bg, scope := scopeCapFixture(t)
	refs := []StageRef{{Path: bg}, {Path: scope, Role: RefRoleScope}}
	rendered, loaded, dropped, err := loadReferences(refs, config.References{MaxFiles: 1}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Path != scope || loaded[0].Role != RefRoleScope {
		t.Fatalf("loaded = %+v; the scope reference must win the only slot", loaded)
	}
	if len(dropped) != 1 || dropped[0] != bg {
		t.Errorf("dropped = %v, want the background reference", dropped)
	}
	if !strings.Contains(rendered, "SCOPE-GUIDE") || strings.Contains(rendered, "BACKGROUND-NOTES") ||
		!strings.Contains(rendered, "— role: SCOPE") {
		t.Errorf("rendered:\n%s", rendered)
	}
}

func TestScopeReferenceSurvivesTheCharCap(t *testing.T) {
	_, bg, scope := scopeCapFixture(t)
	refs := []StageRef{{Path: bg}, {Path: scope, Role: RefRoleScope}}
	rendered, loaded, _, err := loadReferences(refs, config.References{TotalChars: 200}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]refFile{}
	for _, f := range loaded {
		byPath[f.Path] = f
	}
	if f := byPath[scope]; f.Truncated || f.Role != RefRoleScope || !strings.Contains(f.text, "computes determinants") {
		t.Errorf("scope reference was cut by background text: %+v", f)
	}
	if !byPath[bg].Truncated {
		t.Errorf("the background reference should absorb the cut: %+v", byPath[bg])
	}
	if strings.Contains(rendered, "Scope references not delivered in full") {
		t.Error("a scope reference delivered whole was reported as incomplete")
	}
}

// What still cannot arrive whole is said — and scope is announced only for
// scope content the prompt carries.
func TestScopeShortfallIsNamedNeverSilent(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a-scope.md")
	second := filepath.Join(dir, "b-scope.md")
	os.WriteFile(first, []byte("FIRST guide"), 0o644)
	os.WriteFile(second, []byte("SECOND guide"), 0o644)
	refs := []StageRef{{Path: first, Role: RefRoleScope}, {Path: second, Role: RefRoleScope}}
	rendered, _, _, err := loadReferences(refs, config.References{MaxFiles: 1}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "### Scope references not delivered in full") ||
		!strings.Contains(rendered, second+" — dropped: the reference caps") {
		t.Errorf("dropped scope reference not named:\n%s", rendered)
	}

	long := filepath.Join(dir, "c-scope.md")
	os.WriteFile(long, []byte(strings.Repeat("x", 300)), 0o644)
	rendered, _, _, err = loadReferences([]StageRef{{Path: long, Role: RefRoleScope}}, config.References{PerFileChars: 100}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, long+" — truncated at the reference budget") {
		t.Errorf("truncated scope reference not named:\n%s", rendered)
	}

	// A scope reference the reader cannot read is not scope content.
	binary := filepath.Join(dir, "guide.docx")
	data, _ := os.ReadFile(docxFixture(t))
	os.WriteFile(binary, data, 0o644)
	rendered, _, _, err = loadReferences([]StageRef{{Path: binary, Role: RefRoleScope}}, config.References{}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "SCOPE references describe the INTENDED product") || strings.Contains(rendered, "role: SCOPE") {
		t.Errorf("the prompt claims scope content it does not carry:\n%s", rendered)
	}
	if !strings.Contains(rendered, binary+" — unreadable") {
		t.Errorf("unreadable scope reference not named:\n%s", rendered)
	}
}

// The person sees it: a warning event and the run's warning.
func TestScopeShortfallWarnsTheRun(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, projectRoot := projectWithDocs(t, s, map[artifact.Kind]string{})
	cfg := config.DefaultProject(projectID, "proj")
	cfg.References.MaxFiles = 1
	if err := writeProjectTOML(filepath.Join(projectRoot, ".ducklab", "project.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "a-scope.md")
	second := filepath.Join(dir, "b-scope.md")
	os.WriteFile(first, []byte("FIRST guide"), 0o644)
	os.WriteFile(second, []byte("SECOND guide"), 0o644)

	var mu sync.Mutex
	var prompts strings.Builder
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		mu.Lock()
		for _, m := range req.Messages {
			prompts.WriteString(m.Content)
		}
		mu.Unlock()
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Guide\n\n**Priority:** must\n\nFollow the guide.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "Use the guides.", Ducklings: []string{"pato-uno"},
		Refs: []StageRef{{Path: first, Role: RefRoleScope}, {Path: second, Role: RefRoleScope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupStartedRun(t, s, run.ID)
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.Run.Warning, "scope reference(s) did not reach the stage in full") || !strings.Contains(detail.Run.Warning, second) {
		t.Errorf("run warning = %q", detail.Run.Warning)
	}
	found := false
	for _, e := range eventsOfType(detail.Events, "warning") {
		if e.Data["kind"] == "scope_reference_incomplete" {
			found = true
		}
	}
	if !found {
		t.Error("no scope_reference_incomplete warning event")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(prompts.String(), "Scope references not delivered in full") {
		t.Error("the architect was not told a scope reference is missing")
	}
}

// Digest mode follows the same priority: scope first in the prompt, and an
// unreadable scope reference is not announced as scope.
func TestDigestSectionPutsScopeFirst(t *testing.T) {
	refs := []StageRef{{Path: "/w/a-notes.md"}, {Path: "/w/z-guide.md", Role: RefRoleScope}}
	roleOf := refRoleOf(refs)
	if got := scopeFirst([]string{"/w/a-notes.md", "/w/z-guide.md"}, roleOf); got[0] != "/w/z-guide.md" {
		t.Errorf("scopeFirst = %v", got)
	}
	if got := scopeFirst([]string{"/w/b.md", "/w/a.md"}, refRoleOf(nil)); got[0] != "/w/b.md" {
		t.Errorf("an all-background order changed: %v", got)
	}
	got := renderRefDigests([]refDigest{{Path: "/w/z-guide.md", Unreadable: "[unreadable]"}, {Path: "/w/a-notes.md", Chars: 3, digest: "n"}}, 3, "intake", roleOf)
	if strings.Contains(got, "SCOPE references describe") {
		t.Errorf("digest section claims scope content it does not carry:\n%s", got)
	}
}

// Codex on #174 (P2): the evidence cap sliced bytes, splitting "é" into
// invalid UTF-8. The budget counts characters.
func TestScopeEvidenceCutsOnRunes(t *testing.T) {
	roleOf := refRoleOf([]StageRef{{Path: "/w/guide.md", Role: RefRoleScope}})
	text := strings.Repeat("a", 1999) + "é" + "TAIL"
	got := renderReferenceRolesForReview([]string{"/w/guide.md"}, roleOf, "intake", map[string]string{"/w/guide.md": text}, 2000)
	if !strings.Contains(got, strings.Repeat("a", 1999)+"é\n") {
		t.Error("the multibyte character at the boundary was not kept whole")
	}
	if strings.Contains(got, "TAIL") || !strings.Contains(got, "cut at the review budget") {
		t.Error("budget not enforced in characters")
	}
	for i, r := range got {
		if r == '�' {
			t.Fatalf("invalid UTF-8 at byte %d", i)
		}
	}
}
