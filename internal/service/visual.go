package service

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // reference images may be .gif
	_ "image/jpeg" // reference images may be .jpg/.jpeg
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // reference images may be .webp (G3 accepts them)

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// The visual gate (B-460).
//
// A pixel-perfect replica had no gate that could see: [render] captured
// scenes, a person had to eyeball them, and a capture that looked nothing
// like the reference still ended PASSED. The gate here is deliberately
// stack-neutral: it compares a PNG the project's own capture command
// produced against a reference image, so a web page, a desktop window and a
// device screen are judged the same way. How to capture each kind of product
// is stack knowledge and lives outside the core.
//
// The measure is the fraction of pixels whose perceptual colour distance
// (YIQ, as pixelmatch computes it) exceeds a threshold; the run passes when
// that fraction is within the tolerance. A reference of another size is
// scaled to the capture's size and the result says so: an honest comparison
// of a phone-sized photo with a desktop capture is still a comparison, and
// the person sees exactly what was compared.

// visualCaptureNames are the stored names of the reference (at the capture's
// size) and of the difference image for comparison n (1-based). The number is
// part of the name (review of #123): two comparisons of the same capture
// against different references wrote the same files, and the first result
// then pointed at the second one's evidence.
func visualCaptureNames(n int, capture string) (ref, diff string) {
	base := strings.TrimSuffix(capture, filepath.Ext(capture))
	return fmt.Sprintf("visual-%02d-ref-%s.png", n, base), fmt.Sprintf("visual-%02d-diff-%s.png", n, base)
}

// resolveRenderReference finds the file a compare entry names: a reference
// image id from the requirements (REF-IMG-<8 hex>, stored under
// .ducklab/refs/images/<12 hex>.<ext>) or a project-relative path.
func resolveRenderReference(root, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if id, ok := strings.CutPrefix(ref, "REF-IMG-"); ok {
		if len(id) < 8 || strings.Trim(strings.ToLower(id), "0123456789abcdef") != "" {
			return "", fmt.Errorf("%s is not a reference image id (REF-IMG- and 8 hex digits)", ref)
		}
		matches, _ := filepath.Glob(filepath.Join(root, ".ducklab", "refs", "images", strings.ToLower(id)+"*"))
		var files []string
		for _, m := range matches {
			if isRefImage(m) {
				files = append(files, m)
			}
		}
		switch len(files) {
		case 0:
			return "", fmt.Errorf("%s is not stored in this project (.ducklab/refs/images); attach it as a reference to the requirements first", ref)
		case 1:
			return files[0], nil
		}
		return "", fmt.Errorf("%s matches %d stored images; name the file path instead", ref, len(files))
	}
	if filepath.IsAbs(ref) || !cleanRelativePathService(ref) {
		return "", fmt.Errorf("reference %q must be REF-IMG-… or a path inside the project", ref)
	}
	return filepath.Join(root, filepath.FromSlash(ref)), nil
}

// cleanRelativePathService mirrors config's check for a path inside the tree.
func cleanRelativePathService(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/")
}

func decodeImageFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s is not a readable PNG, JPEG, GIF or WebP image: %w", filepath.Base(path), err)
	}
	return img, nil
}

// maxYIQDelta is the largest squared YIQ distance between two colours
// (black and white), as in pixelmatch.
const maxYIQDelta = 35215.0

// yiqDelta is the perceptual squared distance between two 8-bit colours,
// each blended over white first so transparency compares as it is seen.
func yiqDelta(a, b color.Color) float64 {
	r1, g1, b1 := blendWhite(a)
	r2, g2, b2 := blendWhite(b)
	y := rgb2y(r1, g1, b1) - rgb2y(r2, g2, b2)
	i := rgb2i(r1, g1, b1) - rgb2i(r2, g2, b2)
	q := rgb2q(r1, g1, b1) - rgb2q(r2, g2, b2)
	return 0.5053*y*y + 0.299*i*i + 0.1957*q*q
}

func blendWhite(c color.Color) (float64, float64, float64) {
	r, g, b, a := c.RGBA() // premultiplied, 0..65535
	white := 65535 - float64(a)
	return (float64(r) + white) / 257, (float64(g) + white) / 257, (float64(b) + white) / 257
}

func rgb2y(r, g, b float64) float64 { return r*0.29889531 + g*0.58662247 + b*0.11448223 }
func rgb2i(r, g, b float64) float64 { return r*0.59597799 - g*0.27417610 - b*0.32180189 }
func rgb2q(r, g, b float64) float64 { return r*0.21147017 - g*0.52261711 + b*0.31114694 }

// visualDiff compares capture with reference (already at the capture's
// size). It returns the fraction of differing pixels and an image showing
// the capture faded with each differing pixel in red.
func visualDiff(capture, reference image.Image, threshold float64) (float64, *image.RGBA) {
	b := capture.Bounds()
	rb := reference.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	limit := threshold * threshold * maxYIQDelta
	differ := 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := capture.At(b.Min.X+x, b.Min.Y+y)
			if yiqDelta(c, reference.At(rb.Min.X+x, rb.Min.Y+y)) > limit {
				differ++
				out.Set(x, y, color.RGBA{R: 230, G: 0, B: 0, A: 255})
				continue
			}
			r, g, bl := blendWhite(c)
			// Faded grey: the layout stays legible behind the red.
			v := uint8(255 - (255-rgb2y(r, g, bl))*0.25)
			out.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	total := b.Dx() * b.Dy()
	if total == 0 {
		return 1, out
	}
	return float64(differ) / float64(total), out
}

// scaleTo returns img at w×h (itself when already that size).
func scaleTo(img image.Image, w, h int) image.Image {
	if img.Bounds().Dx() == w && img.Bounds().Dy() == h {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// effectiveRenderContract is the project's [render] with what it inherits
// from [run]: the command and its readiness check. The final gate and the
// between-round visual feedback (B-504) must render the same way.
func effectiveRenderContract(projCfg *config.Project) config.RenderContract {
	render := projCfg.Render
	if projCfg.RenderConfigured {
		if render.Command == "" {
			render.Command = projCfg.Run.Command
		}
		if render.Ready == "" {
			render.Ready = projCfg.Run.Health
		}
	}
	return render
}

// runVisualGate holds each configured capture against its reference and
// stores the reference and the difference next to the captures. A compare
// that cannot run (no such capture, unreadable reference) fails with the
// reason: a gate that silently skips is the gap this closes.
//
// References resolve under root first, then under each of alsoUnder: the
// run passes the registered checkout (where its configuration comes from, and
// where an imported reference lands) and then its own worktree (review of
// #124: a build worktree made from the default branch lacked a reference the
// person had just imported, so every run failed "not stored").
func runVisualGate(root string, contract config.RenderContract, writer *runlog.Writer, captures []string, alsoUnder ...string) *runlog.VisualGate {
	if len(contract.Compare) == 0 {
		return nil
	}
	gate := &runlog.VisualGate{Enforcement: contract.Enforcement, Passed: true}
	if gate.Enforcement == "" {
		gate.Enforcement = "diagnostic"
	}
	have := map[string]bool{}
	for _, c := range captures {
		have[c] = true
	}
	for i, cmp := range contract.Compare {
		res := runlog.VisualCompare{
			Capture: cmp.Capture, Reference: cmp.Reference,
			Tolerance: cmp.EffectiveTolerance(), Threshold: cmp.EffectiveThreshold(),
		}
		fail := func(format string, args ...interface{}) {
			res.Error = fmt.Sprintf(format, args...)
			gate.Results = append(gate.Results, res)
			gate.Passed = false
		}
		if !have[cmp.Capture] {
			if len(captures) == 0 {
				fail("no captures were produced, so %s could not be compared", cmp.Capture)
			} else {
				fail("the capture command produced no %s (it produced %s)", cmp.Capture, strings.Join(captures, ", "))
			}
			continue
		}
		capImg, err := decodeImageFile(filepath.Join(writer.RunDir(), "captures", cmp.Capture))
		if err != nil {
			fail("capture: %v", err)
			continue
		}
		refPath, err := resolveRenderReference(root, cmp.Reference)
		for _, other := range alsoUnder {
			if err == nil || other == "" || other == root {
				break
			}
			if p, otherErr := resolveRenderReference(other, cmp.Reference); otherErr == nil {
				refPath, err = p, nil
			}
		}
		if err != nil {
			fail("%v", err)
			continue
		}
		refImg, err := decodeImageFile(refPath)
		if err != nil {
			fail("reference: %v", err)
			continue
		}
		w, h := capImg.Bounds().Dx(), capImg.Bounds().Dy()
		res.Width, res.Height = w, h
		if rw, rh := refImg.Bounds().Dx(), refImg.Bounds().Dy(); rw != w || rh != h {
			res.ScaledFrom = fmt.Sprintf("%dx%d", rw, rh)
		}
		scaled := scaleTo(refImg, w, h)
		mismatch, diffImg := visualDiff(capImg, scaled, res.Threshold)
		res.Mismatch = mismatch
		res.Passed = mismatch <= res.Tolerance
		refName, diffName := visualCaptureNames(i+1, cmp.Capture)
		if data, err := encodePNG(scaled); err == nil && writer.WriteCapture(refName, data) == nil {
			res.ReferenceCapture = refName
		}
		if data, err := encodePNG(diffImg); err == nil && writer.WriteCapture(diffName, data) == nil {
			res.DiffCapture = diffName
		}
		if !res.Passed {
			gate.Passed = false
		}
		gate.Results = append(gate.Results, res)
	}
	return gate
}

// visualGateSummary is one line for the gate output, the failure and the
// caveat.
func visualGateSummary(g *runlog.VisualGate) string {
	var parts []string
	for _, r := range g.Results {
		switch {
		case r.Error != "":
			parts = append(parts, fmt.Sprintf("%s: %s", r.Capture, r.Error))
		case !r.Passed:
			parts = append(parts, fmt.Sprintf("%s differs from %s in %.1f%% of pixels (allowed %.1f%%)", r.Capture, r.Reference, r.Mismatch*100, r.Tolerance*100))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("visual check passed (%d comparison(s))", len(g.Results))
	}
	return "visual check failed: " + strings.Join(parts, "; ")
}
