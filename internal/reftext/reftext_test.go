package reftext

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// needPDFToText skips where poppler is absent; the extraction is the feature.
func needPDFToText(t *testing.T) {
	t.Helper()
	if _, err := lookPDFToText(); err != nil {
		t.Skip("pdftotext (poppler-utils) is not installed")
	}
}

// B-519: a PDF reference reaches the model as its words, page-marked, never
// as object syntax.
func TestPDFIsExtractedToPageMarkedText(t *testing.T) {
	needPDFToText(t)
	d, err := Load(filepath.Join("testdata", "guide.pdf"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Readable() || d.Kind != KindPDF || d.Pages != 2 {
		t.Fatalf("doc = kind %q pages %d reason %q", d.Kind, d.Pages, d.Reason)
	}
	for _, bad := range []string{"%PDF", "endobj", "/Type"} {
		if strings.Contains(d.Text, bad) {
			t.Errorf("text carries PDF syntax %q:\n%s", bad, d.Text)
		}
	}
	p1 := strings.Index(d.Text, "--- page 1 ---")
	one := strings.Index(d.Text, "Calculator guidebook page one")
	p2 := strings.Index(d.Text, "--- page 2 ---")
	two := strings.Index(d.Text, "toggle key switches fractions")
	if p1 != 0 || one < p1 || p2 < one || two < p2 {
		t.Errorf("page markers out of order (p1=%d one=%d p2=%d two=%d):\n%s", p1, one, p2, two, d.Text)
	}
}

// The extraction is paid once per document version: the second load is a
// cache hit with the same text.
func TestPDFExtractionIsCachedByContent(t *testing.T) {
	needPDFToText(t)
	cache := t.TempDir()
	first, err := Load(filepath.Join("testdata", "guide.pdf"), cache)
	if err != nil || first.Cached {
		t.Fatalf("first load: cached=%v err=%v", first.Cached, err)
	}
	second, err := Load(filepath.Join("testdata", "guide.pdf"), cache)
	if err != nil || !second.Cached || second.Text != first.Text {
		t.Fatalf("second load: cached=%v same=%v err=%v", second.Cached, second.Text == first.Text, err)
	}
}

// A scanned PDF has no text layer: an empty reference reads as "covered".
func TestScannedPDFIsUnreadable(t *testing.T) {
	needPDFToText(t)
	d, err := Load(filepath.Join("testdata", "scanned.pdf"), "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Readable() {
		t.Fatalf("a PDF with no text was readable: %q", d.Text)
	}
	n := d.Notice()
	for _, want := range []string{"scanned.pdf: unreadable binary reference (PDF, ", "no text could be extracted", "ask the person for a text or PDF version"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice lacks %q: %s", want, n)
		}
	}
}

// Without poppler the PDF is unreadable with the reason — never bytes.
func TestPDFWithoutPDFToTextIsUnreadable(t *testing.T) {
	old := pdftotext
	pdftotext = "ducklab-no-such-pdftotext"
	defer func() { pdftotext = old }()
	d, err := Load(filepath.Join("testdata", "guide.pdf"), "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Readable() || d.Text != "" || !strings.Contains(d.Notice(), "pdftotext (poppler-utils) is not installed") {
		t.Errorf("doc = readable %v notice %q", d.Readable(), d.Notice())
	}
}

// Binary is sniffed from the content, not the extension: a docx renamed to
// .md is still a Word document, and nothing of it reaches a model.
func TestBinaryReferencesAreSniffedAndNeverPassedAsBytes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		typ  string
	}{
		{"notes.md", zipFixture(t, "word/document.xml"), "Word document"},
		{"budget.xlsx", zipFixture(t, "xl/workbook.xml"), "Excel workbook"},
		{"bundle.zip", zipFixture(t, "readme.txt"), "zip archive"},
		{"blob.txt", []byte("abc\x00\x01\x02\x03def"), "binary .txt"},
		{"photo.md", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := Load(p, "")
		if err != nil {
			t.Fatal(err)
		}
		if d.Readable() || d.Text != "" || d.Kind != KindBinary {
			t.Errorf("%s: readable=%v kind=%q", c.name, d.Readable(), d.Kind)
		}
		want := c.name + ": unreadable binary reference (" + c.typ + ", "
		if !strings.HasPrefix(d.Notice(), want) || !strings.HasSuffix(d.Notice(), "ask the person for a text or PDF version") {
			t.Errorf("%s: notice %q, want prefix %q", c.name, d.Notice(), want)
		}
	}
}

// Text stays text, including 8-bit notes a model can still read.
func TestTextPassesThrough(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"a.md":      []byte("# RBAC\nmodule-first permissions — ñ\n"),
		"latin.txt": {'c', 'a', 'f', 0xe9, '\n'},
		"empty.md":  {},
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		d, err := Load(p, "")
		if err != nil || !d.Readable() || d.Kind != KindText || d.Text != string(data) {
			t.Errorf("%s: readable=%v kind=%q err=%v", name, d.Readable(), d.Kind, err)
		}
	}
}

// The real case: TI-36X Pro guidebook. Opt-in, as the file is the person's:
// REFTEXT_REAL_PDF=~/Downloads/TI-36XPro_Guidebook_EN.pdf go test ./internal/reftext
func TestRealGuidebookReadsAsEnglish(t *testing.T) {
	path := os.Getenv("REFTEXT_REAL_PDF")
	if path == "" {
		t.Skip("REFTEXT_REAL_PDF not set")
	}
	needPDFToText(t)
	d, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Readable() {
		t.Fatalf("unreadable: %s", d.Notice())
	}
	t.Logf("%d bytes → %d pages, %d chars", d.Size, d.Pages, len([]rune(d.Text)))
	if os.Getenv("REFTEXT_DUMP") != "" {
		os.WriteFile(os.Getenv("REFTEXT_DUMP"), []byte(d.Text), 0o644)
	}
}

// zipFixture builds a small zip holding one named entry: an Office file is a
// zip whose first directory says what it is.
func zipFixture(t testing.TB, entry string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(entry)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("<w:document><w:body><w:t>secret plan</w:t></w:body></w:document>"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
