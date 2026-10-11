package reftext

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	d, err := Load(context.Background(), filepath.Join("testdata", "guide.pdf"), "")
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
	first, err := Load(context.Background(), filepath.Join("testdata", "guide.pdf"), cache)
	if err != nil || first.Cached {
		t.Fatalf("first load: cached=%v err=%v", first.Cached, err)
	}
	second, err := Load(context.Background(), filepath.Join("testdata", "guide.pdf"), cache)
	if err != nil || !second.Cached || second.Text != first.Text {
		t.Fatalf("second load: cached=%v same=%v err=%v", second.Cached, second.Text == first.Text, err)
	}
}

// A scanned PDF has no text layer: an empty reference reads as "covered".
func TestScannedPDFIsUnreadable(t *testing.T) {
	needPDFToText(t)
	d, err := Load(context.Background(), filepath.Join("testdata", "scanned.pdf"), "")
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
	d, err := Load(context.Background(), filepath.Join("testdata", "guide.pdf"), "")
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
		d, err := Load(context.Background(), p, "")
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

// Text stays text. An 8-bit note is still readable, but it reaches the model
// transcoded to UTF-8 — review of #173: raw invalid UTF-8 on the prompt path
// is bytes, whatever the sniffer called it.
func TestTextPassesThrough(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		data []byte
		want string
		typ  string
	}{
		"a.md":      {[]byte("# RBAC\nmodule-first permissions — ñ\n"), "# RBAC\nmodule-first permissions — ñ\n", "text"},
		"latin.txt": {[]byte{'c', 'a', 'f', 0xe9, '\n'}, "café\n", "text (Windows-1252)"},
		"cp1252.md": {[]byte("He said \x93press 2nd\x94 \x96 then MODE.\n"), "He said “press 2nd” – then MODE.\n", "text (Windows-1252)"},
		"empty.md":  {[]byte{}, "", "text"},
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, c.data, 0o644)
		d, err := Load(context.Background(), p, "")
		if err != nil || !d.Readable() || d.Kind != KindText || d.Text != c.want || d.Type != c.typ {
			t.Errorf("%s: readable=%v kind=%q type=%q text=%q err=%v", name, d.Readable(), d.Kind, d.Type, d.Text, err)
		}
	}
}

// Review of #173: 1 KB of 0xff passed as "text" — control-free is not prose.
// Invalid UTF-8 that is not a plausible legacy note is binary.
func TestHighByteBlobIsBinary(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"ff.md":    bytes.Repeat([]byte{0xff}, 1024),
		"mixed.md": append([]byte("header line\n"), bytes.Repeat([]byte{0xc3, 0x28, 0xa0, 0xa1}, 200)...),
		"undef.md": []byte("mostly ascii prose here with one \x81 undefined byte\n"),
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		d, err := Load(context.Background(), p, "")
		if err != nil || d.Readable() || d.Kind != KindBinary || d.Text != "" {
			t.Errorf("%s: readable=%v kind=%q text=%q", name, d.Readable(), d.Kind, d.Text)
		}
		if !strings.HasPrefix(d.Notice(), name+": unreadable binary reference (") {
			t.Errorf("%s: notice %q", name, d.Notice())
		}
	}
}

// Review of #173: the extraction ran under context.Background, so aborting
// a run could not stop a hung pdftotext. A blocking extractor must stop as
// soon as the caller cancels, long before PDFTimeout.
func TestCanceledContextStopsTheExtractor(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "pdftotext")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := pdftotext
	pdftotext = fake
	defer func() { pdftotext = old }()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	d, err := Load(ctx, filepath.Join("testdata", "guide.pdf"), "")
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("extraction ran %s after the caller canceled", took)
	}
	if err != nil || d.Readable() || !strings.Contains(d.Reason, "stopped") {
		t.Errorf("canceled extraction: readable=%v reason=%q err=%v", d.Readable(), d.Reason, err)
	}
}

// Review of #173: the cache lived in <project>/.ducklab/refs-cache, one
// `git add -A` from committing private reference text. It lives in the
// person's cache directory now.
func TestCacheDirIsOutsideAnyProject(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", base)
	if got := CacheDir(); got != filepath.Join(base, "ducklab", "refs") {
		t.Errorf("CacheDir() = %q, want under %q", got, base)
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
	d, err := Load(context.Background(), path, "")
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

// Review of #173: "%PDF-" anywhere in the first kilobyte made a note about
// the PDF format a PDF — pdftotext refused it and a text reference was lost.
func TestTextMentioningThePDFHeaderStaysText(t *testing.T) {
	note := []byte("# Export notes\n\nA valid file starts with the bytes `%PDF-1.7`; check the header.\n")
	if isPDF(note) {
		t.Error("a Markdown note mentioning %PDF- was sniffed as a PDF")
	}
	p := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(p, note, 0o644)
	d, err := Load(context.Background(), p, "")
	if err != nil || !d.Readable() || d.Kind != KindText || d.Text != string(note) {
		t.Errorf("doc = kind %q readable %v reason %q", d.Kind, d.Readable(), d.Reason)
	}
}

// A text file that does start with the header is routed to pdftotext; when
// extraction fails on it, it is still text — with or without poppler.
func TestTextStartingWithThePDFHeaderFallsBackToText(t *testing.T) {
	body := []byte("%PDF- is the magic number of the format.\nThis file is plain prose.\n")
	p := filepath.Join(t.TempDir(), "magic.txt")
	os.WriteFile(p, body, 0o644)
	for _, bin := range []string{pdftotext, "ducklab-no-such-pdftotext"} {
		old := pdftotext
		pdftotext = bin
		d, err := Load(context.Background(), p, "")
		pdftotext = old
		if err != nil || !d.Readable() || d.Kind != KindText || d.Text != string(body) {
			t.Errorf("pdftotext=%s: kind %q readable %v reason %q", bin, d.Kind, d.Readable(), d.Reason)
		}
	}
}

// A real PDF with a BOM, whitespace or junk ahead of the header still
// extracts: readers accept it, so the sniffer must.
func TestPDFWithLeadingJunkStillExtracts(t *testing.T) {
	needPDFToText(t)
	pdf, err := os.ReadFile(filepath.Join("testdata", "guide.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, lead := range map[string]string{"bom.pdf": "\xEF\xBB\xBF", "ws.pdf": "\n\n  ", "junk.pdf": "garbage from a mail gateway\n"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, append([]byte(lead), pdf...), 0o644)
		d, err := Load(context.Background(), p, "")
		if err != nil || d.Kind != KindPDF || !d.Readable() || !strings.Contains(d.Text, "Calculator guidebook page one") {
			t.Errorf("%s: kind %q readable %v reason %q", name, d.Kind, d.Readable(), d.Reason)
		}
	}
}

// Review of #173: a cache entry read mid-write was marked Cached and served
// truncated. An entry without the trailer — cut short however it happened —
// is never trusted; the PDF is extracted again and the entry rewritten whole.
func TestTruncatedCacheEntryIsNotTrusted(t *testing.T) {
	needPDFToText(t)
	src := filepath.Join("testdata", "guide.pdf")
	raw, _ := os.ReadFile(src)
	cache := t.TempDir()
	entry := cachePathFor(cache, raw)
	os.WriteFile(entry, []byte("--- page 1 ---\nCalcul"), 0o644)
	d, err := Load(context.Background(), src, cache)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cached || !strings.Contains(d.Text, "toggle key switches fractions") {
		t.Fatalf("truncated entry served: cached=%v text=%q", d.Cached, d.Text)
	}
	again, _ := Load(context.Background(), src, cache)
	if !again.Cached || again.Text != d.Text {
		t.Errorf("the rewritten entry was not served whole: cached=%v", again.Cached)
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(entry) {
		t.Errorf("cache holds %v, want only the renamed entry (no temp files)", entries)
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
