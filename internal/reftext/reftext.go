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
//     cached by content hash next to the digest cache;
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
func Load(path, cacheDir string) (Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	d := Doc{Path: path, Size: int64(len(raw))}
	switch {
	case isPDF(raw):
		d.Kind, d.Type = KindPDF, "PDF"
		extractPDF(&d, raw, cacheDir)
	case isText(raw):
		d.Kind, d.Type = KindText, "text"
		d.Text = string(raw)
	default:
		d.Kind, d.Type = KindBinary, binaryType(raw, path)
		d.Reason = "its content is not text and cannot be extracted"
	}
	return d, nil
}

// isPDF sniffs the header. The spec allows junk before it; readers accept it
// within the first kilobyte.
func isPDF(raw []byte) bool {
	head := raw
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, []byte("%PDF-"))
}

// isText accepts UTF-8 or 8-bit text without NUL bytes or binary control
// characters. Latin-1 notes are still text to a model; a zip is not.
func isText(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return false
	}
	if !strings.HasPrefix(http.DetectContentType(raw), "text/") {
		return false
	}
	// DetectContentType reads 512 bytes; a binary tail must not pass.
	control := 0
	for _, c := range raw {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f' && c != 0x1b {
			control++
		}
	}
	if control*100 > len(raw) {
		return false
	}
	return utf8.Valid(raw) || control == 0
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

// extractPDF fills d from the PDF's text, through the cache.
func extractPDF(d *Doc, raw []byte, cacheDir string) {
	sum := sha256.Sum256(raw)
	cachePath := ""
	if cacheDir != "" {
		cachePath = filepath.Join(cacheDir, hex.EncodeToString(sum[:16])+"."+cacheVersion+".txt")
		if data, err := os.ReadFile(cachePath); err == nil {
			d.Cached = true
			finishPDF(d, string(data))
			return
		}
	}
	out, err := runPDFToText(d.Path)
	if err != nil {
		d.Reason = err.Error()
		return
	}
	text := pageMarked(out)
	if cachePath != "" && os.MkdirAll(cacheDir, 0o755) == nil {
		_ = os.WriteFile(cachePath, []byte(text), 0o644)
	}
	finishPDF(d, text)
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

func runPDFToText(path string) (string, error) {
	bin, err := lookPDFToText()
	if err != nil {
		return "", errors.New("its text could not be extracted because pdftotext (poppler-utils) is not installed on the engine host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), PDFTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", path, "-")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
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
