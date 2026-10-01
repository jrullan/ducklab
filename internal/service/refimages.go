package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig for .gif references
	_ "image/jpeg" // DecodeConfig for .jpg/.jpeg references
	_ "image/png"  // DecodeConfig for .png references
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/vcs"
)

// Image references for a document stage (B-457).
//
// A request like "a pixel perfect replica of the TI-36X Pro" has no source of
// truth in prose: casing proportions, LCD segments, key legends and spacing
// cannot be reconstructed from a product name. Intake accepted only .md/.txt
// references, and an image path named as a reference was read as text.
//
// An image named in Refs is now: copied into the project (so the requirement
// that cites it stays traceable after the original file moves), shown to a
// seeing architect as an image, and always named in the prompt with a stable
// id the document can cite, REF-IMG-n. An architect without vision is told
// the images exist and it cannot see them, rather than nothing.

var refImageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif",
}

// refImageBudget bounds the raw bytes of one stage run's images. The vision
// gate in StageStart caps the data URLs at 8 MB, and base64 is a third
// larger than the file, so 6 MB raw is what actually reaches the architect.
const refImageBudget = 6 << 20

func isRefImage(path string) bool {
	_, ok := refImageTypes[strings.ToLower(filepath.Ext(strings.TrimSpace(path)))]
	return ok
}

// splitImageRefs separates image references from text references.
func splitImageRefs(refs []string) (text, images []string) {
	for _, r := range refs {
		if isRefImage(r) {
			images = append(images, r)
		} else {
			text = append(text, r)
		}
	}
	return text, images
}

// refImage is one image reference, for the record and the prompt.
type refImage struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	// Stored is the project-relative copy the documents cite.
	Stored string `json:"stored"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// loadRefImages copies each image into .ducklab/refs/images/ under a name
// derived from its content and returns the data URLs (within the budget) and
// the record. An unreadable or oversized image is an error: a reference the
// person named and the run silently lost is exactly the gap this exists to
// close.
//
// The id is the content's hash, not its position (review of #120): with
// REF-IMG-1 reused by every run, an extended document could hold old and new
// sections citing the same id for different files. Content addressing makes
// the id durable across runs, and the same image always gets the same one.
func loadRefImages(projectRoot string, paths []string) ([]string, []refImage, error) {
	// Two phases (second review of #120): every image is read and checked
	// before any is written. Copying as it went, a list like [valid, missing]
	// left the valid copy behind, and since the run failed before the
	// reference_images event, no cleanup could find it.
	type loaded struct {
		src  string
		data []byte
	}
	var all []loaded
	total := 0
	for _, p := range paths {
		src := strings.TrimSpace(p)
		if strings.HasPrefix(src, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				src = filepath.Join(home, src[2:])
			}
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, nil, fmt.Errorf("reference image %q: %w", p, err)
		}
		if total += len(data); total > refImageBudget {
			return nil, nil, fmt.Errorf("reference images exceed the %d MB budget at %q; send fewer or smaller images", refImageBudget>>20, p)
		}
		all = append(all, loaded{src: src, data: data})
	}

	dir := filepath.Join(projectRoot, ".ducklab", "refs", "images")
	var urls []string
	var recs []refImage
	var created []string
	undo := func() {
		for _, f := range created {
			_ = os.Remove(f)
		}
	}
	for _, im := range all {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			undo()
			return nil, nil, err
		}
		sum := sha256.Sum256(im.data)
		digest := hex.EncodeToString(sum[:])
		name := digest[:12] + strings.ToLower(filepath.Ext(im.src))
		target := filepath.Join(dir, name)
		if _, err := os.Stat(target); err != nil {
			if err := os.WriteFile(target, im.data, 0o644); err != nil {
				// Only this call's own copies: a file that already existed
				// may belong to an accepted document.
				undo()
				return nil, nil, err
			}
			created = append(created, target)
		}
		rec := refImage{
			ID:     "REF-IMG-" + digest[:8],
			Source: im.src,
			Stored: filepath.ToSlash(filepath.Join(".ducklab", "refs", "images", name)),
			Bytes:  len(im.data),
		}
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(im.data)); err == nil {
			rec.Width, rec.Height = cfg.Width, cfg.Height
		}
		recs = append(recs, rec)
		urls = append(urls, "data:"+refImageTypes[strings.ToLower(filepath.Ext(im.src))]+";base64,"+base64.StdEncoding.EncodeToString(im.data))
	}
	return urls, recs, nil
}

// renderRefImages is the prompt section that names the images. It is written
// whether or not the architect can see them: the ids must be citable either
// way, and a blind architect must know the visual authority exists.
func renderRefImages(recs []refImage, canSee bool) string {
	if len(recs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Reference images\n\n")
	if canSee {
		b.WriteString("The person attached these images; they are shown with this message. They are the visual " +
			"authority for appearance: layout, proportions, colours, typography, labels. A requirement whose " +
			"acceptance depends on appearance must cite the image id exactly as listed below and state " +
			"the viewport or scale at which it is judged.\n\n")
	} else {
		b.WriteString("The person attached these images, but this seat cannot see images. Do not invent visual " +
			"detail. Cite the image id as the visual authority in any requirement about appearance, and record " +
			"each visual detail you need as an open question for the person.\n\n")
	}
	for _, r := range recs {
		size := ""
		if r.Width > 0 && r.Height > 0 {
			size = fmt.Sprintf(", %dx%d px", r.Width, r.Height)
		}
		fmt.Fprintf(&b, "- %s: %s (stored at %s%s)\n", r.ID, filepath.Base(r.Source), r.Stored, size)
	}
	return b.String()
}

// removeUnacceptedRefImages deletes the reference images a run wrote when the
// run ends without acceptance (review of #120: a rejected intake left them in
// the checkout). Stage runs keep no tree snapshot, so the generic restore does
// not reach them. A file git already tracks belongs to an earlier accepted
// document (ids are content-addressed, so two runs can name the same file)
// and is kept.
func removeUnacceptedRefImages(projectRoot string, written []string) {
	tracked := map[string]bool{}
	for _, f := range vcs.New(projectRoot).LsFiles() {
		tracked[filepath.ToSlash(f)] = true
	}
	for _, p := range written {
		p = filepath.ToSlash(p)
		if !strings.HasPrefix(p, ".ducklab/refs/images/") || tracked[p] {
			continue
		}
		_ = os.Remove(filepath.Join(projectRoot, filepath.FromSlash(p)))
	}
}
