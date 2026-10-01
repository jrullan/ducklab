package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig for .gif references
	_ "image/jpeg" // DecodeConfig for .jpg/.jpeg references
	_ "image/png"  // DecodeConfig for .png references
	"os"
	"path/filepath"
	"strings"
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

// loadRefImages copies each image into .ducklab/refs/<runID>/ and returns the
// data URLs (within the budget) and the record. An unreadable or oversized
// image is an error: a reference the person named and the run silently lost
// is exactly the gap this exists to close.
func loadRefImages(projectRoot, runID string, paths []string) ([]string, []refImage, error) {
	var urls []string
	var recs []refImage
	total := 0
	dir := filepath.Join(projectRoot, ".ducklab", "refs", runID)
	for i, p := range paths {
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
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
		name := fmt.Sprintf("img-%d%s", i+1, strings.ToLower(filepath.Ext(src)))
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return nil, nil, err
		}
		rec := refImage{
			ID:     fmt.Sprintf("REF-IMG-%d", i+1),
			Source: src,
			Stored: filepath.ToSlash(filepath.Join(".ducklab", "refs", runID, name)),
			Bytes:  len(data),
		}
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			rec.Width, rec.Height = cfg.Width, cfg.Height
		}
		recs = append(recs, rec)
		urls = append(urls, "data:"+refImageTypes[strings.ToLower(filepath.Ext(src))]+";base64,"+base64.StdEncoding.EncodeToString(data))
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
			"acceptance depends on appearance must cite the image id (for example \"matches REF-IMG-1\") and state " +
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
