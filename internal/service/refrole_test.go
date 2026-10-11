package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// B-520: old requests (plain path strings) parse as background; the new
// object shape carries the role; background is written back as a string so
// records without scope references keep their old bytes.
func TestStageRefParsesOldAndNewShapes(t *testing.T) {
	var req StageRequest
	raw := `{"stage":"intake","refs":["/w/old.md",{"path":"/w/guide.md","role":"scope"},{"path":"/w/ctx.md","role":"background"},{"path":"/w/up.md","role":"SCOPE"}]}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	want := []StageRef{{Path: "/w/old.md"}, {Path: "/w/guide.md", Role: RefRoleScope}, {Path: "/w/ctx.md"}, {Path: "/w/up.md", Role: RefRoleScope}}
	if len(req.Refs) != len(want) {
		t.Fatalf("refs = %+v", req.Refs)
	}
	for i := range want {
		if req.Refs[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, req.Refs[i], want[i])
		}
	}
	out, err := json.Marshal(StageRequest{Refs: req.Refs[:3]})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"refs":["/w/old.md",{"path":"/w/guide.md","role":"scope"},"/w/ctx.md"]`) {
		t.Errorf("marshalled refs = %s", out)
	}
	if err := json.Unmarshal([]byte(`{"refs":[{"path":"/w/x.md","role":"scoped"}]}`), &req); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Errorf("a misspelled role must be refused, not read as background: %v", err)
	}
	// ProjectStart shares the type.
	var ps ProjectStartRequest
	if err := json.Unmarshal([]byte(`{"name":"p","refs":["/w/a.md",{"path":"/w/b.md","role":"scope"}]}`), &ps); err != nil {
		t.Fatal(err)
	}
	if len(ps.Refs) != 2 || ps.Refs[0].IsScope() || !ps.Refs[1].IsScope() {
		t.Errorf("project_start refs = %+v", ps.Refs)
	}
}

func TestNormalizeStageRefsKeepsRolesAndScopeWins(t *testing.T) {
	root := t.TempDir()
	got := normalizeStageRefs(root, "spec", []StageRef{
		{Path: "docs/guide.md"}, {Path: "notes.md"}, {Path: "docs/guide.md", Role: RefRoleScope},
	})
	if len(got) != 2 || got[0].Path != filepath.Join(root, "docs", "guide.md") || !got[0].IsScope() || got[1].IsScope() {
		t.Fatalf("normalized = %+v", got)
	}
}

func TestRefGuidancePerRole(t *testing.T) {
	bgIntake := refGuidance("intake", false)
	if !strings.Contains(bgIntake, "Provided by the person as BACKGROUND, not as scope.") ||
		!strings.Contains(bgIntake, "never derive requirements from it") ||
		strings.Contains(bgIntake, "SCOPE references") {
		t.Errorf("default intake guidance changed:\n%s", bgIntake)
	}
	scopeIntake := refGuidance("intake", true)
	for _, want := range []string{
		"SCOPE references describe the INTENDED product",
		"derive requirements for the behavior it describes that the brief asks for",
		"lift or narrow the exclusion",
		"Never declare behavior a scope reference describes out of scope merely because the code does not implement it yet",
		"the code stays the truth only for as-built claims",
		"BACKGROUND references are context, not scope.",
		"never derive requirements from it", // the MiEmpresa lesson survives for background
		"Reviewers: for each SCOPE reference, check that every gap the brief asks to close was addressed",
	} {
		if !strings.Contains(scopeIntake, want) {
			t.Errorf("scope intake guidance lacks %q", want)
		}
	}
	for _, stageName := range []string{"spec", "plan"} {
		bg := refGuidance(stageName, false)
		if bg != specBackgroundGuidance || strings.Contains(bg, "SCOPE") {
			t.Errorf("%s default guidance changed:\n%s", stageName, bg)
		}
		sc := refGuidance(stageName, true)
		for _, want := range []string{
			"SCOPE references describe the INTENDED product",
			"Within the approved requirements",
			"Never drop, defer or mark as excluded",
			"name it in your reply as a gap for an intake amendment",
			"BACKGROUND references are design context.",
			"never add sections",
			"Reviewers: for each SCOPE reference",
		} {
			if !strings.Contains(sc, want) {
				t.Errorf("%s scope guidance lacks %q", stageName, want)
			}
		}
		if strings.Contains(sc, "never derive requirements") {
			t.Errorf("%s scope guidance carries intake's hardline", stageName)
		}
	}
	if !strings.Contains(refGuidance("plan", true), "write the tasks") || !strings.Contains(refGuidance("spec", true), "write the specification sections") {
		t.Error("spec/plan scope guidance is not stage-shaped")
	}
}

func TestLoadReferencesLabelsEachReferenceRole(t *testing.T) {
	dir := t.TempDir()
	guideDir := filepath.Join(dir, "guide")
	os.MkdirAll(guideDir, 0o755)
	os.WriteFile(filepath.Join(guideDir, "matrix.md"), []byte("Matrix mode: 3x3 determinants."), 0o644)
	ctx := filepath.Join(dir, "notes.md")
	os.WriteFile(ctx, []byte("Open bugs list."), 0o644)

	rendered, loaded, _, err := loadReferences([]StageRef{{Path: guideDir, Role: RefRoleScope}, {Path: ctx}}, config.References{}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "### Reference: "+filepath.Join(guideDir, "matrix.md")+" — role: SCOPE") ||
		!strings.Contains(rendered, "### Reference: "+ctx+" — role: background") ||
		!strings.Contains(rendered, "SCOPE references describe the INTENDED product") {
		t.Errorf("rendered references do not carry roles:\n%s", rendered)
	}
	roles := map[string]string{}
	for _, f := range loaded {
		roles[filepath.Base(f.Path)] = f.Role
	}
	if roles["matrix.md"] != RefRoleScope || roles["notes.md"] != "" {
		t.Errorf("recorded roles = %v", roles)
	}

	plain, _, _, err := loadReferences(backgroundRefs(guideDir, ctx), config.References{}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "role:") || strings.Contains(plain, "SCOPE references") {
		t.Errorf("an all-background run changed its prompt:\n%s", plain)
	}
}

func TestDigestReferencesLabelScope(t *testing.T) {
	roleOf := refRoleOf([]StageRef{{Path: "/w/guide.md", Role: RefRoleScope}, {Path: "/w/notes.md"}})
	got := renderRefDigests([]refDigest{{Path: "/w/guide.md", Chars: 10, digest: "g"}, {Path: "/w/notes.md", Chars: 10, digest: "n"}}, 20, "spec", roleOf)
	if !strings.Contains(got, "### Digest: /w/guide.md — role: SCOPE") || !strings.Contains(got, "### Digest: /w/notes.md — role: background") ||
		!strings.Contains(got, "SCOPE references describe the INTENDED product") {
		t.Errorf("digest section does not carry roles:\n%s", got)
	}
}

func TestReferenceRolesForReview(t *testing.T) {
	roleOf := refRoleOf([]StageRef{{Path: "/w/guide.md", Role: RefRoleScope}})
	evidence := map[string]string{"/w/guide.md": "Matrix mode computes determinants.", "/w/notes.md": "BUG-7 crash on resize"}
	got := renderReferenceRolesForReview([]string{"/w/guide.md", "/w/notes.md"}, roleOf, "intake", evidence, 16_000)
	for _, want := range []string{"- /w/guide.md: scope", "- /w/notes.md: background", "keeps or adds an exclusion", "Lifting or narrowing an exclusion with traceability",
		"## Scope reference content — bounded", "### /w/guide.md", "Matrix mode computes determinants."} {
		if !strings.Contains(got, want) {
			t.Errorf("review roles lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BUG-7") {
		t.Error("a background reference's content reached the reviewer; it can never ground the finding")
	}
	bg := renderReferenceRolesForReview([]string{"/w/notes.md"}, refRoleOf(nil), "intake", evidence, 16_000)
	if !strings.Contains(bg, "Every reference is background") || strings.Contains(bg, "SCOPE") || strings.Contains(bg, "BUG-7") {
		t.Errorf("background-only review roles:\n%s", bg)
	}
	if renderReferenceRolesForReview(nil, roleOf, "intake", evidence, 16_000) != "" {
		t.Error("no references must render nothing")
	}
	// The budget is honoured and the cut is named, never silent.
	long := map[string]string{"/w/guide.md": strings.Repeat("x", 5000) + "TAIL"}
	cut := renderReferenceRolesForReview([]string{"/w/guide.md"}, roleOf, "intake", long, 2_000)
	if strings.Contains(cut, "TAIL") || !strings.Contains(cut, "cut at the review budget") {
		t.Errorf("budget not enforced or cut not named:\n%s", cut[len(cut)-300:])
	}
}

func TestReviewEvidenceBudgetFitsSmallSeats(t *testing.T) {
	for _, tc := range []struct{ ctx, want int }{{0, 16_000}, {200_000, 16_000}, {16_384, 8_192}, {2_000, 2_000}} {
		if got := reviewEvidenceBudget(tc.ctx); got != tc.want {
			t.Errorf("reviewEvidenceBudget(%d) = %d, want %d", tc.ctx, got, tc.want)
		}
	}
}

// The TI-36X shape end to end: an intake amendment over requirements that
// exclude matrix mode, with the guidebook marked scope. The architect, the
// council critic and the post-composition reviewer must each be told the
// reference is the intended product.
func TestScopeReferenceReachesArchitectCriticAndCompositionReviewer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  string
		scope bool
	}{{"scope", RefRoleScope, true}, {"background", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno", "pato-dos")
			id, _ := projectWithDocs(t, s, map[artifact.Kind]string{
				artifact.KindRequirements: "## REQ-001 — Arithmetic\n\n**Priority:** must\n\nThe calculator adds and subtracts.\n\n" +
					"## REQ-002 — Unsupported modes\n\n**Priority:** must\n\nMatrix mode is not supported.\n",
			})
			guide := filepath.Join(t.TempDir(), "guidebook.md")
			// Statistics appears ONLY in the guidebook: the candidate below omits
			// it, and the composition reviewer must be shown the text it omits.
			os.WriteFile(guide, []byte("# Guidebook\n\n## Matrix\n\nThe calculator computes 3x3 determinants.\n\n## Statistics\n\nThe calculator computes one-variable statistics: mean and standard deviation."), 0o644)

			var mu sync.Mutex
			var architect, critic, composition string
			fake := s.providers["fake"].(*provider.Fake)
			fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
				var all strings.Builder
				for _, m := range req.Messages {
					all.WriteString(m.Content)
					all.WriteString("\n")
				}
				prompt := all.String()
				text := "REQ-002\n" // the sectioned triage pass
				section := "## REQ-002 — Matrix mode\n\n**Priority:** must\n\nThe calculator computes 3x3 determinants (Source: guidebook.md § Matrix).\n"
				mu.Lock()
				switch {
				case strings.Contains(prompt, "Final candidate — exact"):
					composition = prompt
					text = `{"verdict":"approve","findings":[]}`
				case req.Model == "m-pato-dos":
					critic = prompt
					text = `{"verdict":"approve","findings":[]}`
				default:
					if architect == "" && strings.Contains(prompt, "## Reference documents") {
						architect = prompt
					}
					if strings.Contains(prompt, "Update ONE section") {
						text = section
					}
				}
				mu.Unlock()
				return &provider.ChatResponse{Choices: []provider.Choice{{
					Message: provider.Message{Role: "assistant", Content: text}, FinishReason: provider.FinishStop,
				}}}
			}
			run, err := s.StageStart(context.Background(), id, StageRequest{
				Stage: "intake", Mode: "council", Ducklings: []string{"pato-uno", "pato-dos"},
				From: "Use this guide to fill in the gap between the implemented and the intended functionality.",
				Refs: []StageRef{{Path: guide, Role: tc.role}},
			})
			if err != nil {
				t.Fatal(err)
			}
			cleanupStartedRun(t, s, run.ID)
			s.runsMu.RLock()
			rs := s.runs[run.ID]
			s.runsMu.RUnlock()
			<-rs.done

			mu.Lock()
			defer mu.Unlock()
			scopeText := "SCOPE references describe the INTENDED product"
			for name, prompt := range map[string]string{"architect": architect, "critic": critic} {
				if prompt == "" {
					t.Fatalf("%s was never called", name)
				}
				if got := strings.Contains(prompt, scopeText) && strings.Contains(prompt, "— role: SCOPE"); got != tc.scope {
					t.Errorf("%s prompt carries scope guidance = %v, want %v", name, got, tc.scope)
				}
			}
			if !strings.Contains(critic, "Reviewers: for each SCOPE reference") && tc.scope {
				t.Error("critic was not told to check the scope reference's gaps")
			}
			if !tc.scope && !strings.Contains(architect, "Provided by the person as BACKGROUND, not as scope.") {
				t.Error("default background guidance lost")
			}
			if composition == "" {
				t.Fatal("post-composition reviewer was never called")
			}
			if !strings.Contains(composition, "## Reference roles — set by the person") {
				t.Errorf("composition reviewer not told the reference roles:\n%s", composition)
			}
			wantRole := guide + ": background"
			if tc.scope {
				wantRole = guide + ": scope"
			}
			if !strings.Contains(composition, wantRole) {
				t.Errorf("composition reviewer roles lack %q", wantRole)
			}
			// Codex on #174: knowing the role is not enough; the reviewer must
			// see the behavior the candidate omits, in its own evidence block
			// (not merely echoed inside the delta).
			evidence := ""
			if i := strings.Index(composition, "## Scope reference content — bounded"); i >= 0 {
				evidence = composition[i:]
			}
			if got := strings.Contains(evidence, "one-variable statistics: mean and standard deviation"); got != tc.scope {
				t.Errorf("composition reviewer evidence carries the omitted behavior = %v, want %v", got, tc.scope)
			}
			if got := strings.Contains(composition, "Lifting or narrowing an exclusion with traceability"); got != tc.scope {
				t.Errorf("composition scope instruction = %v, want %v", got, tc.scope)
			}
			// The role is persisted inside the request, so a revision that
			// inherits the refs inherits the role.
			sreq, ok := loadStageRequest(rs.runDir)
			if !ok || len(sreq.Refs) != 1 || sreq.Refs[0].IsScope() != tc.scope {
				t.Errorf("persisted refs = %+v", sreq.Refs)
			}
		})
	}
}
