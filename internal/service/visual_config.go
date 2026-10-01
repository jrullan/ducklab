package service

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/config"
)

// Configuring the visual gate without editing TOML (B-460, part 2).
//
// The gate's inputs are things the project already knows: the reference
// images the requirements cite, and the captures the last run produced. A
// form that asked for "REF-IMG-1a2b3c4d" and "scene-01.png" by hand would be
// asking the person for fields the record holds. So the view offers both,
// and an image chosen from disk is imported as a reference first, which
// gives it the same citable id the requirements use.

// ReferenceImageInfo is one image stored under .ducklab/refs/images.
type ReferenceImageInfo struct {
	ID     string `json:"id"`
	Stored string `json:"stored"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// VisualCheckView is the visual gate's configuration and what it can be
// built from.
type VisualCheckView struct {
	// Configured is true when the project names a capture command. Not
	// RenderConfigured: every saved project.toml carries an empty [render]
	// table that reads as declared (B-466).
	Configured bool `json:"configured"`
	// Command is render.command; EffectiveCommand is what runs (it falls back
	// to [run].command), empty when nothing would capture.
	Command          string                 `json:"command"`
	EffectiveCommand string                 `json:"effective_command"`
	Artifacts        string                 `json:"artifacts"`
	Scenes           []string               `json:"scenes,omitempty"`
	Viewport         string                 `json:"viewport,omitempty"`
	Enforcement      string                 `json:"enforcement"`
	Compare          []config.RenderCompare `json:"compare"`
	// RecentCaptures are the capture names of the latest run that produced
	// any, from RecentRunID: the names a comparison can hold.
	RecentCaptures []string `json:"recent_captures,omitempty"`
	RecentRunID    string   `json:"recent_run_id,omitempty"`
	// References are the project's stored reference images.
	References []ReferenceImageInfo `json:"references"`
}

// VisualCheckSetRequest replaces the visual gate's configuration.
type VisualCheckSetRequest struct {
	Command     string                 `json:"command"`
	Artifacts   string                 `json:"artifacts"`
	Scenes      []string               `json:"scenes,omitempty"`
	Viewport    string                 `json:"viewport,omitempty"`
	Enforcement string                 `json:"enforcement"`
	Compare     []config.RenderCompare `json:"compare"`
	// Actor is recorded in config-audit.jsonl; empty means a person.
	Actor string `json:"actor,omitempty"`
}

// ReferenceImportRequest copies an image into the project's references.
type ReferenceImportRequest struct {
	Path string `json:"path"`
}

// defaultRenderArtifacts is where a capture command is told to write.
const defaultRenderArtifacts = ".ducklab-render-captures/*.png"

func (s *Service) projectRootAndConfig(id string) (string, *config.Project, error) {
	entry, err := s.registry.Get(id)
	if err != nil {
		return "", nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return "", nil, err
	}
	return entry.Path, cfg, nil
}

// ReferenceImages lists the project's stored reference images, newest
// first.
func (s *Service) ReferenceImages(ctx context.Context, id string) ([]ReferenceImageInfo, error) {
	root, _, err := s.projectRootAndConfig(id)
	if err != nil {
		return nil, err
	}
	return listReferenceImages(root), nil
}

func listReferenceImages(root string) []ReferenceImageInfo {
	dir := filepath.Join(root, ".ducklab", "refs", "images")
	entries, _ := os.ReadDir(dir)
	type dated struct {
		info ReferenceImageInfo
		mod  time.Time
	}
	var all []dated
	for _, e := range entries {
		name := e.Name()
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if e.IsDir() || !isRefImage(name) || len(stem) < 8 {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info := ReferenceImageInfo{
			ID:     "REF-IMG-" + stem[:8],
			Stored: filepath.ToSlash(filepath.Join(".ducklab", "refs", "images", name)),
			Bytes:  fi.Size(),
		}
		if f, err := os.Open(filepath.Join(dir, name)); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				info.Width, info.Height = cfg.Width, cfg.Height
			}
			_ = f.Close()
		}
		all = append(all, dated{info, fi.ModTime()})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	out := make([]ReferenceImageInfo, 0, len(all))
	for _, d := range all {
		out = append(out, d.info)
	}
	return out
}

// ReferenceImage returns one stored reference image's bytes and media type.
func (s *Service) ReferenceImage(ctx context.Context, id, ref string) ([]byte, string, error) {
	root, _, err := s.projectRootAndConfig(id)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(ref, "REF-IMG-") {
		return nil, "", fmt.Errorf("%q is not a reference image id", ref)
	}
	path, err := resolveRenderReference(root, ref)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, refImageTypes[strings.ToLower(filepath.Ext(path))], nil
}

// ReferenceImport copies an image into the project's references and returns
// it with its citable id (the same id an intake reference would get).
func (s *Service) ReferenceImport(ctx context.Context, id string, req ReferenceImportRequest) (*ReferenceImageInfo, error) {
	root, _, err := s.projectRootAndConfig(id)
	if err != nil {
		return nil, err
	}
	if !isRefImage(req.Path) {
		return nil, fmt.Errorf("%q is not a PNG, JPEG, WebP or GIF image", req.Path)
	}
	_, recs, err := loadRefImages(root, []string{req.Path})
	if err != nil {
		return nil, err
	}
	for _, info := range listReferenceImages(root) {
		if info.ID == recs[0].ID {
			return &info, nil
		}
	}
	return nil, fmt.Errorf("imported %s but cannot find it", recs[0].ID)
}

// VisualCheck is the visual gate's configuration and its building blocks.
func (s *Service) VisualCheck(ctx context.Context, id string) (*VisualCheckView, error) {
	root, cfg, err := s.projectRootAndConfig(id)
	if err != nil {
		return nil, err
	}
	r := cfg.Render
	view := &VisualCheckView{
		Configured: strings.TrimSpace(r.Command) != "", Command: r.Command, Artifacts: r.Artifacts,
		Scenes: r.Scenes, Viewport: r.Viewport, Enforcement: r.Enforcement,
		Compare: r.Compare, References: listReferenceImages(root),
	}
	if view.Enforcement == "" {
		view.Enforcement = "diagnostic"
	}
	if view.Compare == nil {
		view.Compare = []config.RenderCompare{}
	}
	if cfg.RenderConfigured {
		view.EffectiveCommand = r.Command
		if view.EffectiveCommand == "" {
			view.EffectiveCommand = cfg.Run.Command
		}
	}
	if runs, err := s.RunList(ctx, RunFilter{ProjectID: id}); err == nil {
		var latest string
		for _, run := range runs {
			if len(run.Captures) > 0 && run.StartedAt > latest {
				latest = run.StartedAt
				view.RecentCaptures, view.RecentRunID = run.Captures, run.ID
			}
		}
	}
	return view, nil
}

// VisualCheckSet replaces [render]'s capture and comparison settings. A
// comparison with nothing to capture it is refused: it could only fail.
func (s *Service) VisualCheckSet(ctx context.Context, id string, req VisualCheckSetRequest) (*VisualCheckView, error) {
	root, cfg, err := s.projectRootAndConfig(id)
	if err != nil {
		return nil, err
	}
	updated := *cfg
	updated.Render.Command = strings.TrimSpace(req.Command)
	updated.Render.Artifacts = strings.TrimSpace(req.Artifacts)
	if updated.Render.Artifacts == "" {
		updated.Render.Artifacts = defaultRenderArtifacts
	}
	if req.Scenes != nil {
		updated.Render.Scenes = req.Scenes
	}
	if req.Viewport != "" {
		updated.Render.Viewport = strings.TrimSpace(req.Viewport)
	}
	updated.Render.Enforcement = strings.TrimSpace(req.Enforcement)
	if updated.Render.Enforcement == "diagnostic" {
		updated.Render.Enforcement = "" // the default stays implicit in the file
	}
	updated.Render.Compare = nil
	for _, c := range req.Compare {
		c.Capture, c.Reference = strings.TrimSpace(c.Capture), strings.TrimSpace(c.Reference)
		updated.Render.Compare = append(updated.Render.Compare, c)
	}
	updated.RenderConfigured = true
	// Explicit, not the [run].command fallback: a run command usually starts
	// a server or a window and never writes a PNG, so comparisons fed by it
	// could only fail by timeout.
	if len(updated.Render.Compare) > 0 && updated.Render.Command == "" {
		return nil, fmt.Errorf("a visual check needs a capture command: the command that writes PNGs to $DUCKLAB_RENDER_OUTPUT")
	}
	for i, c := range updated.Render.Compare {
		if _, err := resolveRenderReference(root, c.Reference); err != nil {
			return nil, fmt.Errorf("comparison %d: %w", i+1, err)
		}
	}
	path := filepath.Join(root, ".ducklab", "project.toml")
	if err := updated.Validate(path); err != nil {
		return nil, err
	}
	if err := config.SaveProject(path, &updated); err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "human"
	}
	if receipt, err := os.OpenFile(filepath.Join(root, ".ducklab", "config-audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_ = json.NewEncoder(receipt).Encode(map[string]interface{}{
			"actor": actor, "source": "visual_check", "keys": []string{"render"}, "ts": time.Now().UTC().Format(time.RFC3339),
		})
		_ = receipt.Close()
	}
	s.projMu.Lock()
	delete(s.projects, id)
	s.projMu.Unlock()
	return s.VisualCheck(ctx, id)
}
