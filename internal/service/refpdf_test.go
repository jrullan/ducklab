package service

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
)

// B-519 fixtures: a real two-page PDF (testdata of internal/reftext) and a
// docx built here — an Office file is a zip whose first directory names it.

func pdfFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler-utils) is not installed")
	}
	data, err := os.ReadFile(filepath.Join("..", "reftext", "testdata", "guide.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "guide.pdf")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func docxFixture(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte("<w:document><w:t>secret plan</w:t></w:document>"))
	zw.Close()
	p := filepath.Join(t.TempDir(), "notes.docx")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// pdfSyntax is what reached the architect before B-519.
func assertNoPDFSyntax(t *testing.T, where, s string) {
	t.Helper()
	for _, bad := range []string{"%PDF", "endobj", "/Type"} {
		if strings.Contains(s, bad) {
			t.Errorf("%s carries PDF syntax %q", where, bad)
		}
	}
}

// Inline: the prompt carries the PDF's words, page-marked, and the record
// counts characters of that text — not the file's bytes.
func TestInlineReferenceCarriesThePDFsText(t *testing.T) {
	pdf := pdfFixture(t)
	info, _ := os.Stat(pdf)
	rendered, loaded, _, err := loadReferences([]string{pdf}, config.References{}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- page 1 ---", "Calculator guidebook page one", "--- page 2 ---", "toggle key switches fractions"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("inline section lacks %q:\n%s", want, rendered)
		}
	}
	assertNoPDFSyntax(t, "inline section", rendered)
	if len(loaded) != 1 || loaded[0].Type != "pdf" || loaded[0].Pages != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded[0].Chars == 0 || int64(loaded[0].Chars) >= info.Size() {
		t.Errorf("chars = %d for a %d-byte file: must count extracted text", loaded[0].Chars, info.Size())
	}
}

// A directory of references picks up its PDFs too, now that they read.
func TestReferenceDirectoryIncludesPDFs(t *testing.T) {
	pdf := pdfFixture(t)
	files, err := collectRefFiles([]string{filepath.Dir(pdf)})
	if err != nil || len(files) != 1 || files[0] != pdf {
		t.Errorf("files = %v err = %v", files, err)
	}
}

// Inline: a binary that cannot be extracted is a notice, never bytes.
func TestInlineBinaryReferenceIsANotice(t *testing.T) {
	docx := docxFixture(t)
	rendered, loaded, _, err := loadReferences([]string{docx}, config.References{}, "intake")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "notes.docx: unreadable binary reference (Word document, ") ||
		!strings.Contains(rendered, "ask the person for a text or PDF version") {
		t.Errorf("inline section lacks the notice:\n%s", rendered)
	}
	if strings.Contains(rendered, "secret plan") || strings.Contains(rendered, "PK") {
		t.Errorf("binary bytes reached the prompt:\n%s", rendered)
	}
	if len(loaded) != 1 || loaded[0].Unreadable == "" || loaded[0].Chars != 0 {
		t.Errorf("loaded = %+v", loaded)
	}
}

// Digest: the librarian summarizes the PDF's text, and is never asked to
// summarize a binary — the digest of object syntax is a confident nothing.
func TestDigestReadsPDFTextAndSkipsBinaries(t *testing.T) {
	pdf, docx := pdfFixture(t), docxFixture(t)
	var mu sync.Mutex
	var prompts []string
	summarize := func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		return "digest of the guidebook", nil
	}
	cache := t.TempDir()
	digests, err := digestReferences(context.Background(), []string{pdf, docx}, 10_000, "m1", cache, summarize, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Calculator guidebook page one") {
		t.Fatalf("summarized %d prompts: %q", len(prompts), prompts)
	}
	assertNoPDFSyntax(t, "digest prompt", prompts[0])
	if digests[1].Unreadable == "" || !strings.Contains(digests[1].Unreadable, "Word document") {
		t.Errorf("binary digest = %+v", digests[1])
	}
	section := renderRefDigests(digests, 100, "intake")
	if !strings.Contains(section, "notes.docx: unreadable binary reference") {
		t.Errorf("digest section lacks the notice:\n%s", section)
	}
	// The extraction is cached next to the digest: PDF text + digest.
	entries, _ := os.ReadDir(cache)
	var extraction bool
	for _, e := range entries {
		extraction = extraction || strings.Contains(e.Name(), "pdftext")
	}
	if !extraction {
		t.Errorf("no extraction in the refs cache: %v", entries)
	}
}

// End to end through an intake: the architect's prompt carries the PDF's
// words and the docx's notice; the run carries a warning (Now, run page, MCP
// run_get) and the event stream a warning event (conversation lane).
func TestStageWithUnreadableReferenceWarnsAndPromptsWithText(t *testing.T) {
	pdf, docx := pdfFixture(t), docxFixture(t)
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	projectID, _ := projectWithDocs(t, s, map[artifact.Kind]string{})
	fake := s.providers["fake"].(*provider.Fake)
	var mu sync.Mutex
	sawText, sawNotice, sawBytes := false, false, false
	fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range req.Messages {
			sawText = sawText || strings.Contains(m.Content, "Calculator guidebook page one")
			sawNotice = sawNotice || strings.Contains(m.Content, "notes.docx: unreadable binary reference (Word document")
			sawBytes = sawBytes || strings.Contains(m.Content, "endobj") || strings.Contains(m.Content, "secret plan")
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{
			Message:      provider.Message{Role: "assistant", Content: "## REQ-001 — Mode key\n\n**Priority:** must\n\nPressing 2nd then MODE quits.\n"},
			FinishReason: provider.FinishStop,
		}}}
	}
	run, err := s.StageStart(context.Background(), projectID, StageRequest{
		Stage: "intake", Mode: "solo", From: "A calculator.", Refs: []string{pdf, docx},
		Ducklings: []string{"pato-uno"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runsMu.RLock()
	rs := s.runs[run.ID]
	s.runsMu.RUnlock()
	<-rs.done
	detail, err := s.RunGet(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Run.Status == "failed" {
		t.Fatalf("run failed: %s", detail.Run.Failure)
	}
	mu.Lock()
	if !sawText || !sawNotice || sawBytes {
		t.Errorf("prompt: text=%v notice=%v bytes=%v", sawText, sawNotice, sawBytes)
	}
	mu.Unlock()
	if !strings.Contains(detail.Run.Warning, "notes.docx: unreadable binary reference") {
		t.Errorf("run warning = %q", detail.Run.Warning)
	}
	var warned bool
	for _, e := range eventsOfType(detail.Events, "warning") {
		if e.Data["kind"] == "unreadable_reference" && strings.Contains(e.Data["detail"].(string), "notes.docx") {
			warned = true
		}
	}
	if !warned {
		t.Error("no unreadable_reference warning event")
	}
	loaded := eventsOfType(detail.Events, "references_loaded")
	if len(loaded) != 1 || loaded[0].Data["unreadable"] == nil {
		t.Fatalf("references_loaded = %+v", loaded)
	}
}
