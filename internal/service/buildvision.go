package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/strategy"
)

// Reference images and visual feedback in build, test-first and review turns
// (B-504).
//
// TI-36X Pro T-008, r-20261005-004552-4tpn: the slice demanded "matching
// REF-IMG-6c63e390's layout: enclosure shape, solar panel above LCD,
// branding, d-pad, and key grid in physical positions". The implementer luna
// declares vision; the stored photo sat in the worktree; and no build turn
// carried an image — images reached only document-stage architects (B-457)
// and the consultant chat. luna reasoned "I can't directly view the image",
// built the composition from prose, the reviewer approved it blind, and the
// visual gate's 44.5% mismatch — capture and diff on disk — reached neither
// seat. The build PASSED with the d-pad on the wrong side.
//
// Here every implementer, reviewer and judge turn of a task that cites a
// REF-IMG (in its own text, or in the SPEC and REQ sections it implements)
// is shown the cited images when its seat can see, and is told it cannot
// when it cannot — never silently dropped. In solo and pair builds with a
// visual check configured, a seeing seat also gets the latest capture and its
// diff: rendered just before a seeing reviewer judges, and at a round gate
// after the implementer changed the tree, so the next round's implementer
// sees what its work looks like. Every such turn records what it saw
// (turn_images).
//
// The advisor is deliberately left out: it answers the implementer's
// distress about the conversation, not the appearance of the product, and a
// consult that carried every image would double the visual context per round
// for the role with the smallest budget.

// The bounds of one turn. Six images and 6 MB are the consultant chat's own
// bounds (maxChatImages, maxChatImageTotal); references take at most four
// slots so the capture and its diff always fit beside them.
const (
	maxTurnImages     = maxChatImages
	maxTurnRefImages  = 4
	maxTurnImageTotal = refImageBudget
	// maxTurnImageBytes and maxTurnImageSide bound one image. A larger one is
	// downscaled to fit and re-encoded; one still too large is skipped with a
	// note, never sent whole.
	maxTurnImageBytes = 2 << 20
	maxTurnImageSide  = 1568
)

var refImageIDPattern = regexp.MustCompile(`REF-IMG-[0-9a-fA-F]{8}`)

// turnImage is one image a turn was shown, as recorded on turn_images.
type turnImage struct {
	ID string `json:"id"`
	// Kind is reference, capture or diff.
	Kind string `json:"kind"`
	// File is where the shown image lives: project-relative for a reference,
	// run-relative (captures/…) for a capture or diff.
	File   string `json:"file"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	// ScaledFrom is the stored size when the image was downscaled to fit
	// maxTurnImageSide/maxTurnImageBytes ("3000x6000").
	ScaledFrom string `json:"scaled_from,omitempty"`
	url        string
}

// visualFeedback is the latest render of the candidate held against its
// references, kept for the next seeing turn.
type visualFeedback struct {
	Round   int
	Phase   string
	Summary string
	Results []runlog.VisualCompare
	images  []turnImage
	notes   []string
}

// taskVision is one run's image context. A nil *taskVision is valid and does
// nothing, so callers wrap unconditionally.
type taskVision struct {
	svc  *Service
	emit func(kind string, data map[string]interface{})
	// roles are the roles whose turns are shown images.
	roles map[config.Role]bool
	cited []string
	refs  []turnImage
	notes []string

	// render captures the candidate and compares it with its references; nil
	// when the run has no visual check or renders nothing between rounds.
	render func(ctx context.Context) (*runlog.VisualGate, []string, error)
	// runDir is where render evidence lives.
	runDir string
	// anySeatSees gates the between-round render: nobody would look at it.
	anySeatSees bool

	mu       sync.Mutex
	feedback *visualFeedback
	// stale says an implementer turn ran after the latest feedback (or none
	// was rendered yet): the tree may no longer look like it.
	stale     bool
	lastRound int
	renders   int
}

// seatCanSee reads the duckling's declared vision capability, the same
// declaration effectiveCaps and the document stages read (B-457).
func (s *Service) seatCanSee(id config.DucklingID) bool {
	dcfg, ok := s.cfg.Ducklings[id]
	return ok && dcfg.Caps.Vision != nil && *dcfg.Caps.Vision
}

// taskCitedRefImages lists, in order and once each, the REF-IMG ids a task
// cites: in its title and body, in the SPEC sections it implements, and in
// the REQ sections those implement (T-008 named it itself; SPEC-002 reached
// it through REQ-002).
func (s *Service) taskCitedRefImages(ctx context.Context, projectID, projectRoot, taskID string) []string {
	task := s.findTask(ctx, projectID, taskID)
	if task == nil {
		return nil
	}
	texts := []string{task.Title, task.Body}
	var reqIDs []string
	spec, _ := artifact.Load(projectRoot, artifact.KindSpec)
	for _, id := range task.Implements {
		if strings.HasPrefix(id, "REQ-") {
			reqIDs = append(reqIDs, id)
			continue
		}
		if spec == nil {
			continue
		}
		if sec := spec.Section(id); sec != nil {
			texts = append(texts, sec.Body)
			reqIDs = append(reqIDs, sec.Implements...)
		}
	}
	if len(reqIDs) > 0 {
		if reqs, err := artifact.Load(projectRoot, artifact.KindRequirements); err == nil {
			for _, id := range reqIDs {
				if sec := reqs.Section(id); sec != nil {
					texts = append(texts, sec.Body)
				}
			}
		}
	}
	seen := map[string]bool{}
	var ids []string
	for _, text := range texts {
		for _, m := range refImageIDPattern.FindAllString(text, -1) {
			id := "REF-IMG-" + strings.ToLower(strings.TrimPrefix(m, "REF-IMG-"))
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// newTaskVision loads the images a task cites and records them. It returns
// nil when the task cites none and no visual feedback is wanted: such a run
// is untouched. roots are where a reference may be stored, in order (the
// registered checkout, then the run's worktree — as runVisualGate resolves).
func (s *Service) newTaskVision(ctx context.Context, projectID, taskID string, roots []string,
	roster map[config.Role]config.DucklingID, roles []config.Role,
	emit func(string, map[string]interface{})) *taskVision {
	docsRoot := ""
	for _, r := range roots {
		if r != "" {
			docsRoot = r
			break
		}
	}
	if docsRoot == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}
	v := &taskVision{svc: s, emit: emit, roles: map[config.Role]bool{}, stale: true}
	for _, r := range roles {
		v.roles[r] = true
	}
	v.cited = s.taskCitedRefImages(ctx, projectID, docsRoot, taskID)
	for _, id := range v.cited {
		if len(v.refs) == maxTurnRefImages {
			v.notes = append(v.notes, fmt.Sprintf("%s was not attached: at most %d reference images per turn", id, maxTurnRefImages))
			continue
		}
		var path string
		var err error
		for _, root := range roots {
			if root == "" {
				continue
			}
			if path, err = resolveRenderReference(root, id); err == nil {
				break
			}
		}
		if err != nil {
			v.notes = append(v.notes, fmt.Sprintf("%s is cited but cannot be shown: %v", id, err))
			continue
		}
		img, err := loadTurnImage(path)
		if err != nil {
			v.notes = append(v.notes, fmt.Sprintf("%s was not attached: %v", id, err))
			continue
		}
		img.ID, img.Kind = id, "reference"
		img.File = filepath.ToSlash(filepath.Join(".ducklab", "refs", "images", filepath.Base(path)))
		v.refs = append(v.refs, img)
	}
	seats := map[string]interface{}{}
	for _, r := range roles {
		if id := roster[r]; id != "" {
			see := s.seatCanSee(id)
			v.anySeatSees = v.anySeatSees || see
			seats[string(r)] = map[string]interface{}{"duckling": string(id), "can_see": see}
		}
	}
	if len(v.cited) > 0 && emit != nil {
		emit("reference_images", map[string]interface{}{
			"phase": "task", "task": taskID, "cited": v.cited, "images": v.refs,
			"notes": v.notes, "seats": seats,
		})
	}
	return v
}

// withFeedback arms the visual feedback for a solo or pair build. It restores
// the latest recorded feedback, so a resumed run's next seat still sees it.
func (v *taskVision) withFeedback(runDir string, render func(ctx context.Context) (*runlog.VisualGate, []string, error)) *taskVision {
	// Only a task that cites a reference is shown renders: a logic task in
	// the same project would pay a render per review for images it has no
	// use for.
	if v == nil || render == nil || len(v.cited) == 0 {
		return v
	}
	v.render, v.runDir = render, runDir
	events, _ := runlog.ReadEvents(runDir)
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Type != "visual_feedback" || e.Data["ok"] != true {
			continue
		}
		fb := &visualFeedback{Round: intValue(e.Data["round"]), Phase: stringValueAny(e.Data["phase"]), Summary: stringValueAny(e.Data["summary"])}
		if raw, ok := e.Data["shown"].([]interface{}); ok {
			for _, item := range raw {
				m, _ := item.(map[string]interface{})
				file := stringValueAny(m["file"])
				if file == "" {
					continue
				}
				img, err := loadTurnImage(filepath.Join(runDir, filepath.FromSlash(file)))
				if err != nil {
					continue
				}
				img.ID, img.Kind, img.File = stringValueAny(m["id"]), stringValueAny(m["kind"]), file
				fb.images = append(fb.images, img)
			}
		}
		if res, ok := e.Data["results"].([]interface{}); ok {
			for _, item := range res {
				m, _ := item.(map[string]interface{})
				mismatch, _ := m["mismatch"].(float64)
				tolerance, _ := m["tolerance"].(float64)
				fb.Results = append(fb.Results, runlog.VisualCompare{
					Capture: stringValueAny(m["capture"]), Reference: stringValueAny(m["reference"]),
					Mismatch: mismatch, Tolerance: tolerance, Error: stringValueAny(m["error"]),
				})
			}
		}
		v.feedback = fb
		v.stale = false
		break
	}
	return v
}

func (v *taskVision) empty() bool {
	return v == nil || (len(v.cited) == 0 && v.render == nil)
}

// wrap shows each eligible turn its images. Every turn of the run goes
// through the runner — script turns, consult and report retries (#146), the
// resumed turn (#155), tournament contestants and the split's subtasks — so
// this is the one place that cannot miss one.
func (v *taskVision) wrap(inner strategy.TurnRunner, roster map[config.Role]config.DucklingID) strategy.TurnRunner {
	if v.empty() {
		return inner
	}
	return func(ctx context.Context, t *strategy.Turn, d config.DucklingID, prompt string, belt []string, tc strategy.TurnContext) (*agent.Outcome, error) {
		if !v.roles[t.Role] {
			return inner(ctx, t, d, prompt, belt, tc)
		}
		seat := d
		if seat == "" {
			seat = roster[t.Role]
		}
		sees := v.svc.seatCanSee(seat)
		v.mu.Lock()
		v.lastRound = tc.Round
		needRender := sees && t.Role == config.RoleReviewer && v.render != nil && v.stale
		v.mu.Unlock()
		if needRender {
			// The reviewer judges the tree as it is now: a capture from before
			// the implementer's latest turn would show it the wrong candidate.
			v.refresh(ctx, "before review", tc.Round)
		}
		images, section, record, notes := v.forTurn(t.Role, sees)
		if section == "" {
			return inner(ctx, t, d, prompt, belt, tc)
		}
		turn := *t
		if sees && len(images) > 0 {
			turn.Images = append(append([]string(nil), t.Images...), images...)
		}
		if v.emit != nil {
			data := map[string]interface{}{
				"round": tc.Round, "turn": tc.Index, "role": string(t.Role), "duckling": string(seat),
				"can_see": sees, "images": record,
			}
			if len(notes) > 0 {
				data["notes"] = notes
			}
			v.emit("turn_images", data)
		}
		out, err := inner(ctx, &turn, d, prompt+section, belt, tc)
		if t.Role == config.RoleImplementer {
			v.mu.Lock()
			v.stale = true
			v.mu.Unlock()
		}
		return out, err
	}
}

// wrapGate renders between rounds: after the round's gate, when an
// implementer changed the tree since the last render and some seat can see,
// so the next round's implementer starts from what its work looks like.
func (v *taskVision) wrapGate(gate strategy.GateRunner) strategy.GateRunner {
	if v == nil || gate == nil || v.render == nil {
		return gate
	}
	return func(ctx context.Context) (string, string, error) {
		word, log, err := gate(ctx)
		if err != nil {
			return word, log, err
		}
		v.mu.Lock()
		need := v.stale && v.anySeatSees
		round := v.lastRound
		v.mu.Unlock()
		if need {
			v.refresh(ctx, "after round gate", round)
		}
		return word, log, err
	}
}

// refresh renders and compares, keeps the result as the latest feedback and
// records it. A render that fails is recorded and leaves the previous
// feedback in place, marked stale: feedback is evidence for the seats, never
// a verdict, and the final gate still renders and judges on its own.
func (v *taskVision) refresh(ctx context.Context, phase string, round int) {
	vg, captures, err := v.render(ctx)
	if err != nil || vg == nil {
		reason := "render produced no comparison"
		if err != nil {
			reason = err.Error()
		}
		if v.emit != nil {
			v.emit("visual_feedback", map[string]interface{}{"round": round, "phase": phase, "ok": false, "reason": reason})
		}
		return
	}
	v.mu.Lock()
	v.renders++
	seq := v.renders
	v.mu.Unlock()
	// The final gate writes the same capture names; feedback evidence is
	// renamed so it survives, and so the run's own captures stay the final's.
	dir := filepath.Join(v.runDir, "captures")
	renamed := map[string]string{}
	for _, c := range captures {
		to := fmt.Sprintf("feedback-%02d-%s", seq, c)
		if os.Rename(filepath.Join(dir, c), filepath.Join(dir, to)) == nil {
			renamed[c] = to
		}
	}
	fb := &visualFeedback{Round: round, Phase: phase, Summary: visualGateSummary(vg)}
	var shown []turnImage
	for _, r := range vg.Results {
		if r.ReferenceCapture != "" {
			// The reference at the capture's size is the gate's evidence, not
			// the seat's: the seat is shown the stored reference itself.
			_ = os.Remove(filepath.Join(dir, r.ReferenceCapture))
			r.ReferenceCapture = ""
		}
		if to, ok := renamed[r.Capture]; ok {
			if img, err := loadTurnImage(filepath.Join(dir, to)); err == nil {
				img.ID, img.Kind, img.File = r.Capture, "capture", "captures/"+to
				shown = append(shown, img)
			} else {
				fb.notes = append(fb.notes, fmt.Sprintf("capture %s was not attached: %v", r.Capture, err))
			}
		}
		if r.DiffCapture != "" {
			to := fmt.Sprintf("feedback-%02d-%s", seq, r.DiffCapture)
			if os.Rename(filepath.Join(dir, r.DiffCapture), filepath.Join(dir, to)) == nil {
				r.DiffCapture = to
				if img, err := loadTurnImage(filepath.Join(dir, to)); err == nil {
					img.ID, img.Kind, img.File = r.Capture+" vs "+r.Reference, "diff", "captures/"+to
					shown = append(shown, img)
				} else {
					fb.notes = append(fb.notes, fmt.Sprintf("diff of %s was not attached: %v", r.Capture, err))
				}
			}
		}
		// r.Capture keeps the name the project's [render.compare] uses: it is
		// what the seat's prompt and the person read; the stored file is on
		// the shown record.
		fb.Results = append(fb.Results, r)
	}
	fb.images = shown
	// The same capture can back several comparisons; show it once.
	seen := map[string]bool{}
	kept := fb.images[:0]
	for _, img := range fb.images {
		if !seen[img.File] {
			seen[img.File] = true
			kept = append(kept, img)
		}
	}
	fb.images = kept
	v.mu.Lock()
	v.feedback = fb
	v.stale = false
	v.mu.Unlock()
	if v.emit != nil {
		v.emit("visual_feedback", map[string]interface{}{
			"round": round, "phase": phase, "ok": true, "passed": vg.Passed,
			"summary": fb.Summary, "results": fb.Results, "shown": fb.images,
		})
	}
}

// forTurn is what one turn is shown: the data URLs (for a seeing seat), the
// prompt section (for every seat), the record and the notes. Within the
// per-turn bounds the references come first — they are the authority — and
// then the capture and its diff.
//
// Feedback exists only where render is armed — solo and pair, whose seats all
// work in the run's own tree — so no tournament judge or contestant is ever
// shown a render of a tree it is not judging.
func (v *taskVision) forTurn(role config.Role, sees bool) ([]string, string, []turnImage, []string) {
	v.mu.Lock()
	fb := v.feedback
	stale := v.stale
	v.mu.Unlock()
	if len(v.cited) == 0 && fb == nil {
		return nil, "", nil, nil
	}
	notes := append([]string(nil), v.notes...)
	var urls []string
	var shown []turnImage
	total := 0
	add := func(img turnImage) {
		switch {
		case len(urls) == maxTurnImages:
			notes = append(notes, fmt.Sprintf("%s (%s) was not attached: at most %d images per turn", img.ID, img.Kind, maxTurnImages))
		case total+img.Bytes > maxTurnImageTotal:
			notes = append(notes, fmt.Sprintf("%s (%s) was not attached: the turn's images would exceed %d MB", img.ID, img.Kind, maxTurnImageTotal>>20))
		default:
			total += img.Bytes
			urls = append(urls, img.url)
			shown = append(shown, img)
		}
	}
	for _, img := range v.refs {
		add(img)
	}
	if fb != nil {
		notes = append(notes, fb.notes...)
		for _, img := range fb.images {
			add(img)
		}
	}
	if !sees {
		urls, shown = nil, nil
	}

	var b strings.Builder
	if len(v.cited) > 0 {
		b.WriteString("\n\n## Reference images\n\n")
		switch {
		case sees && role == config.RoleImplementer:
			b.WriteString("This task cites these reference images, and they are attached to this message. They are the visual " +
				"authority for appearance: layout, proportions, positions, colours, labels. Build what they show, " +
				"and compare your result with them, not with a description of them.\n\n")
		case sees:
			b.WriteString("This task cites these reference images, and they are attached to this message. They are the visual " +
				"authority for appearance: judge every appearance criterion against them, not against the code's own claims.\n\n")
		case role == config.RoleImplementer:
			b.WriteString("This task cites these reference images as its visual authority, but this seat cannot see images. " +
				"Do not invent visual detail or claim the result matches them; in your report, name each appearance " +
				"criterion you could not check against them.\n\n")
		default:
			b.WriteString("This task cites these reference images as its visual authority, but this seat cannot see images. " +
				"You cannot verify appearance against them: say in your verdict which appearance criteria went unverified " +
				"rather than approving them on the code alone.\n\n")
		}
		attached := map[string]bool{}
		for _, img := range shown {
			attached[img.ID] = true
		}
		for _, id := range v.cited {
			line := "- " + id
			for _, img := range v.refs {
				if img.ID == id {
					line += fmt.Sprintf(": stored at %s", img.File)
					if img.Width > 0 {
						line += fmt.Sprintf(", %dx%d px as attached", img.Width, img.Height)
					}
					if img.ScaledFrom != "" {
						line += " (downscaled from " + img.ScaledFrom + ")"
					}
				}
			}
			if sees && !attached[id] {
				line += " — not attached (see the note below)"
			}
			b.WriteString(line + "\n")
		}
	}
	if fb != nil {
		fmt.Fprintf(&b, "\n\n## Visual check of the candidate (rendered %s, round %d)\n\n", fb.Phase, fb.Round)
		if stale {
			b.WriteString("This render predates the implementer's latest turn; the tree may have changed since.\n\n")
		}
		for _, r := range fb.Results {
			switch {
			case r.Error != "":
				fmt.Fprintf(&b, "- %s: %s\n", r.Capture, r.Error)
			default:
				fmt.Fprintf(&b, "- %s against %s: %.1f%% of pixels differ (allowed %.1f%%)\n", r.Capture, r.Reference, r.Mismatch*100, r.Tolerance*100)
			}
		}
		if sees && len(fb.images) > 0 {
			b.WriteString("\nThe capture and its difference image are attached after the references: in the difference image, " +
				"red marks every pixel that differs from the reference, over a faded copy of the capture.\n")
		}
	}
	if len(notes) > 0 {
		b.WriteString("\nNot shown: " + strings.Join(notes, "; ") + "\n")
	}
	if !sees {
		notes = append(notes, "seat cannot see images; none attached")
	}
	return urls, b.String(), shown, notes
}

// loadTurnImage reads an image as a bounded data URL. One larger than
// maxTurnImageSide or maxTurnImageBytes is downscaled and re-encoded (PNG
// stays PNG, everything else becomes JPEG); one still too large is an error.
func loadTurnImage(path string) (turnImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return turnImage{}, err
	}
	mime, ok := refImageTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return turnImage{}, fmt.Errorf("%s is not a PNG, JPEG, GIF or WebP image", filepath.Base(path))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return turnImage{}, fmt.Errorf("%s is not a readable image: %w", filepath.Base(path), err)
	}
	img := turnImage{Width: cfg.Width, Height: cfg.Height}
	if len(data) > maxTurnImageBytes || cfg.Width > maxTurnImageSide || cfg.Height > maxTurnImageSide {
		full, err := decodeImageFile(path)
		if err != nil {
			return turnImage{}, err
		}
		w, h := cfg.Width, cfg.Height
		if w > maxTurnImageSide || h > maxTurnImageSide {
			if w >= h {
				w, h = maxTurnImageSide, max(1, h*maxTurnImageSide/cfg.Width)
			} else {
				w, h = max(1, w*maxTurnImageSide/cfg.Height), maxTurnImageSide
			}
		}
		scaled := scaleTo(full, w, h)
		var buf bytes.Buffer
		if mime == "image/png" {
			data, err = encodePNG(scaled)
		} else {
			mime = "image/jpeg"
			err = jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 85})
			data = buf.Bytes()
		}
		if err != nil {
			return turnImage{}, err
		}
		if len(data) > maxTurnImageBytes {
			return turnImage{}, fmt.Errorf("%s is %d bytes even downscaled to %dx%d; the limit is %d MB", filepath.Base(path), len(data), w, h, maxTurnImageBytes>>20)
		}
		img.ScaledFrom = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
		img.Width, img.Height = w, h
	}
	img.Bytes = len(data)
	img.url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	return img, nil
}
