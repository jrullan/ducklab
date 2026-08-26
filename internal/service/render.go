package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/xplat"
)

// captureRender executes the project's render command and copies its PNG
// artifacts into immutable run evidence. Render output never remains in the
// run checkout, and rendering is deliberately not part of the verification verdict.
func captureRender(ctx context.Context, root string, contract config.RenderContract, writer *runlog.Writer, runID, projectID string) ([]string, error) {
	if strings.TrimSpace(contract.Command) == "" {
		return nil, nil
	}
	timeout := contract.TimeoutS
	if timeout <= 0 {
		timeout = 120
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	baseEnv := os.Environ()
	envValue := func(name string) string {
		for _, item := range baseEnv {
			if strings.HasPrefix(item, name+"=") {
				return strings.TrimPrefix(item, name+"=")
			}
		}
		return ""
	}
	engineURL, token := envValue("DUCKLAB_ENGINE"), envValue("DUCKLAB_TOKEN")
	// The engine owns the connection details; render commands should not need
	// callers to duplicate them in their environment. Environment overrides
	// remain useful for tests and explicitly configured render workers.
	if info, err := daemon.ReadEngineJSON(); err == nil {
		if engineURL == "" && info.Port > 0 {
			engineURL = fmt.Sprintf("http://127.0.0.1:%d", info.Port)
		}
		if token == "" {
			token = info.Token
		}
	}
	url := strings.NewReplacer("{engine}", engineURL, "{token}", token).Replace(contract.URL)
	ready := contract.Ready
	if ready == "" {
		ready = envValue("DUCKLAB_RUN_HEALTH")
	}
	viewport := contract.Viewport
	if viewport == "" {
		viewport = "1440x900"
	}
	outputDir := filepath.Join(writer.RunDir(), "render-output")
	_ = os.RemoveAll(outputDir)
	defer os.RemoveAll(outputDir)
	// Clean up the historical in-checkout location too. A custom renderer may
	// still use that literal path instead of DUCKLAB_RENDER_OUTPUT.
	defer os.RemoveAll(filepath.Join(root, ".ducklab-render-captures"))
	env := append(baseEnv, "DUCKLAB_RUN_ID="+runID, "DUCKLAB_PROJECT_ID="+projectID,
		"DUCKLAB_RENDER_URL="+url, "DUCKLAB_RENDER_TOKEN="+token, "DUCKLAB_RENDER_READY="+ready,
		"DUCKLAB_RENDER_SCENES="+strings.Join(contract.Scenes, "\n"), "DUCKLAB_RENDER_VIEWPORT="+viewport,
		"DUCKLAB_RENDER_OUTPUT="+outputDir,
		"DUCKLAB_RENDER_TIMEOUT_S="+fmt.Sprintf("%d", timeout))
	out, commandErr := xplat.ShellContext(ctx, root, env, contract.Command).CombinedOutput()
	if strings.TrimSpace(contract.Artifacts) == "" {
		if commandErr != nil {
			return nil, fmt.Errorf("render command: %s: %w", strings.TrimSpace(string(out)), commandErr)
		}
		return nil, nil
	}
	artifactPattern := contract.Artifacts
	cleanPattern := filepath.Clean(artifactPattern)
	if cleanPattern == ".ducklab-render-captures" || strings.HasPrefix(cleanPattern, ".ducklab-render-captures"+string(filepath.Separator)) {
		artifactPattern = filepath.Join(outputDir, strings.TrimPrefix(cleanPattern, ".ducklab-render-captures"))
	}
	matches, err := filepath.Glob(filepath.Join(root, artifactPattern))
	if cleanPattern == ".ducklab-render-captures" || strings.HasPrefix(cleanPattern, ".ducklab-render-captures"+string(filepath.Separator)) {
		matches, err = filepath.Glob(artifactPattern)
	}
	if err != nil {
		return nil, fmt.Errorf("render artifacts glob: %w", err)
	}
	if len(matches) == 0 {
		if commandErr != nil {
			return nil, fmt.Errorf("render command: %s: %w", strings.TrimSpace(string(out)), commandErr)
		}
		return nil, fmt.Errorf("render artifacts matched no files: %s", contract.Artifacts)
	}
	var captures []string
	defer func() {
		for _, path := range matches {
			_ = os.Remove(path)
		}
	}()
	for _, path := range matches {
		if strings.ToLower(filepath.Ext(path)) != ".png" {
			return nil, fmt.Errorf("render artifact is not a PNG: %s", filepath.Base(path))
		}
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		// The contract is PNG images, not arbitrary files renamed with .png.
		// Check the PNG signature before attaching evidence so clients can safely
		// render every capture as an image.
		if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
			return nil, fmt.Errorf("render artifact is not a valid PNG: %s", filepath.Base(path))
		}
		name := filepath.Base(path)
		if err := writer.WriteCapture(name, data); err != nil {
			return nil, err
		}
		captures = append(captures, name)
	}
	if commandErr != nil {
		// A dirty renderer is still successful when it produced usable evidence.
		// Preserve the captures and let the caller record the exit as a caveat.
		return captures, fmt.Errorf("render command exited unsuccessfully: %s: %w", strings.TrimSpace(string(out)), commandErr)
	}
	return captures, nil
}
