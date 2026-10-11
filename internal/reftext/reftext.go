// Package reftext turns a reference file into the text a model may read.
//
// References used to be read with os.ReadFile and handed to the model as
// bytes (B-519): a 684 KB calculator guidebook reached the architect as PDF
// object syntax, "chars 12000 of 684099" counted bytes, and nothing told the
// person or the model that not one word of the manual had been read. Every
// reference path — inline, digest and ref_read — now goes through Load:
//
//   - text is returned as it is;
//   - a PDF is extracted to text, page-aware (each page opens with
//     "--- page N ---", so an offset maps to a page), and the extraction is
//     cached by content hash in the person's cache directory (CacheDir);
//   - anything else binary (docx, xlsx, zip, an unknown blob — sniffed from
//     the content, not the extension) is never passed on: the caller gets a
//     notice to show the model instead and the reason to warn the person.
//
// A PDF with no extractable text (scanned pages) is unreadable in the same
// way: an empty reference reads as "covered" when nothing was.
//
// Extraction uses poppler's pdftotext. No PDF library is in go.mod, and the
// pure-Go ones either drop layout and word spacing on real manuals or are
// large; pdftotext is the reference implementation, ships with every Linux
// desktop that has a PDF viewer, and is bounded here by a timeout. When it is
// missing the PDF is reported unreadable, with the reason — never as bytes.
package reftext

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Kinds of reference.
const (
	KindText   = "text"
	KindPDF    = "pdf"
	KindBinary = "binary"
)

// Doc is one reference as a model may see it.
type Doc struct {
	Path string
	// Kind is text, pdf or binary.
	Kind string
	// Type names the format for a person: "PDF", "Word document", ….
	Type string
	// Size is the file's size in bytes.
	Size int64
	// Pages is the PDF's page count; 0 for other kinds.
	Pages int
	// Text is the readable text: the file itself, or the PDF's extraction
	// with page markers. Empty when the reference is unreadable.
	Text string
	// Reason says why the reference is unreadable; empty when it is not.
	Reason string
	// Cached reports that a PDF extraction came from the cache.
	Cached bool
}

// Readable reports whether the model gets text from this reference.
func (d Doc) Readable() bool { return d.Reason == "" }

// Notice is what the model sees in place of an unreadable reference.
func (d Doc) Notice() string {
	if d.Readable() {
		return ""
	}
	return fmt.Sprintf("%s: unreadable binary reference (%s, %s); %s; ask the person for a text or PDF version",
		filepath.Base(d.Path), d.Type, HumanSize(d.Size), d.Reason)
}

// PDFTimeout bounds one extraction. A guidebook of a hundred pages takes a
// tenth of a second; a minute is room for a big one without letting a
// pathological file stall a stage.
var PDFTimeout = 60 * time.Second

// pdftotext is the extractor's executable; a variable so tests can point it
// at a missing binary.
var pdftotext = "pdftotext"

// cacheVersion changes when the extraction's output format changes, so a
// cache written by an older format is never served.
const cacheVersion = "pdftext-v1"

// Load reads a reference and returns it as model-readable text, or as an
// unreadable Doc with the reason. cacheDir may be "" to disable the
// extraction cache. The error is only for a file that cannot be read at all.
//
// ctx bounds the PDF extraction together with PDFTimeout: an aborted run or
// a canceled ref_read stops a hung pdftotext instead of waiting it out.
func Load(ctx context.Context, path, cacheDir string) (Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	d := Doc{Path: path, Size: int64(len(raw))}
	if isPDF(raw) {
		d.Kind, d.Type = KindPDF, "PDF"
		if extractPDF(ctx, &d, raw, cacheDir) == nil || looksLikePDFBody(raw) {
			return d, nil
		}
		// pdftotext refused a text file that only starts like a PDF: it is a
		// text reference, and losing it is the worse error.
		d = Doc{Path: path, Size: d.Size}
	}
	if text, enc, ok := decodeText(raw); ok {
		d.Kind, d.Type, d.Text = KindText, "text", text
		if enc != "" {
			d.Type = "text (" + enc + ")"
		}
		return d, nil
	}
	d.Kind, d.Type = KindBinary, binaryType(raw, path)
	d.Reason = "its content is not text and cannot be extracted"
	return d, nil
}

// CacheDir is where PDF extractions and reference digests are cached: the
// person's cache directory, never the project. Under <project>/.ducklab the
// cache was untracked plaintext of the person's private references, one
// `git add -A` from being committed and a dirty tree for every clean-tree
// guard (review of #173). Entries are content-hash keyed, so one cache
// serves every project. "" when the platform has no cache directory, which
// disables caching rather than falling back to the worktree.
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "ducklab", "refs")
}

// isPDF sniffs the header. A header at the start (after a BOM or whitespace)
// is a PDF. Readers also accept junk before it within the first kilobyte, but
// a header that late counts only when the content is not text: a Markdown
// note that mentions "%PDF-" near its top is a note about PDFs, and routing
// it to pdftotext lost a readable reference (review of #173).
func isPDF(raw []byte) bool {
	if bytes.HasPrefix(trimLead(raw), pdfHeader) {
		return true
	}
	head := raw
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, pdfHeader) && !isText(raw)
}

var pdfHeader = []byte("%PDF-")

// trimLead drops a UTF-8 BOM and leading whitespace.
func trimLead(raw []byte) []byte {
	return bytes.TrimLeft(bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF")), " \t\r\n\f")
}

// looksLikePDFBody reports PDF object syntax: a text file without it that
// pdftotext refused is text that merely starts like a PDF.
func looksLikePDFBody(raw []byte) bool {
	return bytes.Contains(raw, []byte("endobj")) || bytes.Contains(raw, []byte("xref"))
}

func isText(raw []byte) bool {
	_, _, ok := decodeText(raw)
	return ok
}

// decodeText returns raw as valid UTF-8 text, naming the legacy encoding it
// was transcoded from ("" for UTF-8), or false for a binary.
//
// Invalid UTF-8 is binary unless it reads as a Windows-1252 (Latin-1
// superset) note: mostly ASCII, every high byte a defined character. The
// first cut accepted any control-free bytes, so 1 KB of 0xff reached the
// prompt as "text" (review of #173); a legacy note is transcoded, never
// passed through raw.
func decodeText(raw []byte) (string, string, bool) {
	if len(raw) == 0 {
		return "", "", true
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", "", false
	}
	// "application/pdf" is DetectContentType reading the first five bytes;
	// whether the rest is text is decided below, like any other file — prose
	// that opens with "%PDF-" is still prose (review of #173).
	if ct := http.DetectContentType(raw); !strings.HasPrefix(ct, "text/") && ct != "application/pdf" {
		return "", "", false
	}
	// DetectContentType reads 512 bytes; a binary tail must not pass.
	control, high := 0, 0
	for _, c := range raw {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f' && c != 0x1b {
			control++
		}
		if c >= 0x80 {
			high++
		}
	}
	if control*100 > len(raw) {
		return "", "", false
	}
	if utf8.Valid(raw) {
		return string(raw), "", true
	}
	// Legacy 8-bit prose is mostly ASCII: accented letters and typographic
	// quotes are a small share of it. A blob of high bytes is not prose.
	if control > 0 || high*4 > len(raw) {
		return "", "", false
	}
	var b strings.Builder
	b.Grow(len(raw) + high)
	for _, c := range raw {
		switch {
		case c < 0x80:
			b.WriteByte(c)
		case c < 0xA0:
			r := cp1252[c-0x80]
			if r == 0 {
				return "", "", false
			}
			b.WriteRune(r)
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String(), "Windows-1252", true
}

// cp1252 maps Windows-1252's 0x80–0x9F; 0 marks the five undefined bytes.
var cp1252 = [32]rune{
	'€', 0, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0,
	0, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ',
}

// binaryType names a binary for the person, from its content.
func binaryType(raw []byte, path string) string {
	if bytes.HasPrefix(raw, []byte("PK\x03\x04")) {
		if zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw))); err == nil {
			for _, f := range zr.File {
				switch {
				case strings.HasPrefix(f.Name, "word/"):
					return "Word document"
				case strings.HasPrefix(f.Name, "xl/"):
					return "Excel workbook"
				case strings.HasPrefix(f.Name, "ppt/"):
					return "PowerPoint presentation"
				case f.Name == "mimetype":
					return "OpenDocument file"
				}
			}
		}
		return "zip archive"
	}
	if bytes.HasPrefix(raw, []byte{0xD0, 0xCF, 0x11, 0xE0}) {
		return "legacy Office document"
	}
	ct := http.DetectContentType(raw)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if ct == "application/octet-stream" {
		if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."); ext != "" {
			return "binary ." + ext
		}
		return "binary"
	}
	return ct
}

// extractPDF fills d from the PDF's text, through the cache. The error is
// the extraction's own failure (pdftotext missing, refused, timed out); a
// PDF with no text is not an error, only unreadable.
func extractPDF(ctx context.Context, d *Doc, raw []byte, cacheDir string) error {
	cachePath := ""
	if cacheDir != "" {
		cachePath = cachePathFor(cacheDir, raw)
		if text, ok := readCache(cachePath); ok {
			d.Cached = true
			finishPDF(d, text)
			return nil
		}
	}
	out, err := runPDFToText(ctx, d.Path)
	if err != nil {
		d.Reason = err.Error()
		return err
	}
	text := pageMarked(out)
	if cachePath != "" {
		writeCache(cachePath, text)
	}
	finishPDF(d, text)
	return nil
}

func cachePathFor(cacheDir string, raw []byte) string {
	sum := sha256.Sum256(raw)
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:16])+"."+cacheVersion+".txt")
}

// cacheTrailer closes every complete cache entry. A plain write let a
// concurrent Load read a half-written entry, mark it Cached and serve a
// truncated document (review of #173). Entries are written to a temp file
// and renamed into place, so a reader sees all or nothing; the trailer is the
// check that does not depend on that — an entry without it (cut by a crash,
// a full disk, or a writer from before the rename) is never trusted, and the
// PDF is extracted again.
const cacheTrailer = "\n--- end of extraction ---\n"

func readCache(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasSuffix(data, []byte(cacheTrailer)) {
		return "", false
	}
	return string(data[:len(data)-len(cacheTrailer)]), true
}

func writeCache(path, text string) {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".extract-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(text + cacheTrailer)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

func finishPDF(d *Doc, text string) {
	d.Pages = strings.Count(text, "--- page ")
	if !hasWords(text) {
		d.Reason = "no text could be extracted from it (scanned pages or images only)"
		return
	}
	d.Text = text
}

// hasWords reports whether the extraction holds anything but page markers
// and whitespace.
func hasWords(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !(strings.HasPrefix(line, "--- page ") && strings.HasSuffix(line, " ---")) {
			return true
		}
	}
	return false
}

func lookPDFToText() (string, error) { return exec.LookPath(pdftotext) }

func runPDFToText(parent context.Context, path string) (string, error) {
	bin, err := lookPDFToText()
	if err != nil {
		return "", errors.New("its text could not be extracted because pdftotext (poppler-utils) is not installed on the engine host")
	}
	// The caller's context, not Background (review of #173): an aborted run
	// or canceled tool call must stop the extractor, not wait out the timeout.
	ctx, cancel := context.WithTimeout(parent, PDFTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", path, "-")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child that inherited the pipes would hold Wait open after the kill.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if perr := parent.Err(); perr != nil {
			return "", fmt.Errorf("text extraction was stopped: %v", perr)
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("text extraction timed out after %s", PDFTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("text extraction failed: %s", msg)
	}
	return stdout.String(), nil
}

// pageMarked turns pdftotext's form-feed page breaks into markers a model can
// cite: "--- page N ---" opens every page.
func pageMarked(out string) string {
	pages := strings.Split(out, "\f")
	// pdftotext ends every page with a form feed, so the last element is
	// what follows the final page: nothing.
	if len(pages) > 1 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	var b strings.Builder
	for i, p := range pages {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "--- page %d ---\n", i+1)
		p = strings.TrimRight(p, " \t\r\n")
		if p != "" {
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// HumanSize prints a byte count for a person.
func HumanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
