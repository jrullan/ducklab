package service

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// B-457: image references travel as images, copied into the project, with a
// citable id; text references keep their path.
func TestImageReferencesAreSplitCopiedAndNamed(t *testing.T) {
	text, imgs := splitImageRefs([]string{"notes/brief.md", "/tmp/front.PNG", "photo.jpeg", "docs/"})
	if strings.Join(text, ",") != "notes/brief.md,docs/" || strings.Join(imgs, ",") != "/tmp/front.PNG,photo.jpeg" {
		t.Fatalf("split = %v / %v", text, imgs)
	}

	src := filepath.Join(t.TempDir(), "ti36x-front.png")
	writePNG(t, src, 40, 80)
	root := t.TempDir()
	urls, recs, err := loadRefImages(root, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Fatalf("urls = %v", urls)
	}
	r := recs[0]
	if !strings.HasPrefix(r.ID, "REF-IMG-") || len(r.ID) != len("REF-IMG-")+8 || r.Width != 40 || r.Height != 80 ||
		!strings.HasPrefix(r.Stored, ".ducklab/refs/images/") || !strings.HasSuffix(r.Stored, ".png") {
		t.Fatalf("record = %+v", r)
	}
	// Review of #120: the id is durable — the same image gets the same id in
	// another run, and a different image a different one.
	_, again, err := loadRefImages(t.TempDir(), []string{src})
	if err != nil || again[0].ID != r.ID {
		t.Fatalf("same image, different id: %v vs %v (%v)", again[0].ID, r.ID, err)
	}
	other := filepath.Join(t.TempDir(), "back.png")
	writePNG(t, other, 41, 80)
	_, diff, _ := loadRefImages(t.TempDir(), []string{other})
	if diff[0].ID == r.ID {
		t.Fatal("two different images share an id")
	}
	if _, err := os.Stat(filepath.Join(root, r.Stored)); err != nil {
		t.Fatalf("image not copied into the project: %v", err)
	}

	seeing := renderRefImages(recs, true)
	if !strings.Contains(seeing, r.ID+": ti36x-front.png") || !strings.Contains(seeing, "40x80 px") || !strings.Contains(seeing, "shown with this message") {
		t.Fatalf("seeing section = %s", seeing)
	}
	blind := renderRefImages(recs, false)
	if !strings.Contains(blind, "cannot see images") || !strings.Contains(blind, "open question") {
		t.Fatalf("blind section = %s", blind)
	}
}

func TestAMissingOrOversizedReferenceImageFailsLoudly(t *testing.T) {
	if _, _, err := loadRefImages(t.TempDir(), []string{"/nonexistent/x.png"}); err == nil {
		t.Fatal("a missing image was silently dropped")
	}
	big := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(big, make([]byte, refImageBudget+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadRefImages(t.TempDir(), []string{big}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("oversized image err = %v", err)
	}
}

// Second review of #120: [valid, missing] used to copy the valid image and
// then fail before any event named it, leaving an orphan no cleanup found.
func TestAFailedImageListWritesNothing(t *testing.T) {
	valid := filepath.Join(t.TempDir(), "valid.png")
	writePNG(t, valid, 10, 10)
	root := t.TempDir()
	if _, _, err := loadRefImages(root, []string{valid, "/nonexistent/missing.png"}); err == nil {
		t.Fatal("a missing image was accepted")
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".ducklab", "refs", "images")); len(entries) != 0 {
		t.Fatalf("failed image load left %d copied image(s)", len(entries))
	}
}
