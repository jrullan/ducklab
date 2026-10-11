package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// B-519: ref_read served a guidebook PDF as object syntax ("chars 60000 of
// 684099" counted bytes). It now serves the extracted, page-marked text, and
// the counts are characters of that text.
func TestRefReadServesAPDFAsItsText(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler-utils) is not installed")
	}
	ref, err := filepath.Abs(filepath.Join("..", "reftext", "testdata", "guide.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(ref)
	cache := t.TempDir()
	ectx := &ExecContext{RefPaths: []string{ref}, RefCacheDir: cache}
	res, err := (&RefRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"path":"guide.pdf"}`))
	if err != nil || res.IsError {
		t.Fatalf("read failed: %v %v", err, res)
	}
	for _, want := range []string{"--- page 1 ---", "Calculator guidebook page one", "--- page 2 ---", "toggle key switches fractions"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result lacks %q:\n%s", want, res.Content)
		}
	}
	for _, bad := range []string{"%PDF", "endobj", fmt.Sprintf("of %d)", info.Size())} {
		if strings.Contains(res.Content, bad) {
			t.Errorf("result carries %q — bytes, not text:\n%s", bad, res.Content)
		}
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Errorf("extraction not cached by content hash: %d cache entries", len(entries))
	}
}

// A binary reference that cannot be extracted answers with the notice, never
// its bytes — sniffed from the content, so a docx renamed .md is caught.
func TestRefReadNeverServesBinaryBytes(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "notes.md")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte("<w:t>secret plan</w:t>"))
	zw.Close()
	os.WriteFile(ref, buf.Bytes(), 0o644)
	var opened []string
	ectx := &ExecContext{RefPaths: []string{ref}, OnRefRead: func(p string) { opened = append(opened, p) }}
	res, err := (&RefRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"path":"notes.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Content, "notes.md: unreadable binary reference (Word document, ") ||
		!strings.HasSuffix(res.Content, "ask the person for a text or PDF version") {
		t.Errorf("result = %q", res.Content)
	}
	if strings.Contains(res.Content, "PK") || strings.Contains(res.Content, "secret plan") {
		t.Errorf("binary bytes leaked: %q", res.Content)
	}
	if len(opened) != 1 {
		t.Errorf("the attempt was not marked as opened: %v", opened)
	}
}
