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

// captureRender executes the project's render command in the run checkout and
// copies its PNG artifacts into the immutable run evidence. Rendering is
// deliberately not part of the verification verdict.
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
	env := append(baseEnv, "DUCKLAB_RUN_ID="+runID, "DUCKLAB_PROJECT_ID="+projectID,
		"DUCKLAB_RENDER_URL="+url, "DUCKLAB_RENDER_TOKEN="+token, "DUCKLAB_RENDER_READY="+ready,
		"DUCKLAB_RENDER_SCENES="+strings.Join(contract.Scenes, "\n"), "DUCKLAB_RENDER_VIEWPORT="+viewport,
		"DUCKLAB_RENDER_OUTPUT="+filepath.Join(root, ".ducklab-render-captures"),
		"DUCKLAB_RENDER_TIMEOUT_S="+fmt.Sprintf("%d", timeout))
	out, err := xplat.ShellContext(ctx, root, env, contract.Command).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("render command: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if strings.TrimSpace(contract.Artifacts) == "" {
		return nil, nil
	}
	matches, err := filepath.Glob(filepath.Join(root, contract.Artifacts))
	if err != nil {
		return nil, fmt.Errorf("render artifacts glob: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("render artifacts matched no files: %s", contract.Artifacts)
	}
	var captures []string
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
	return captures, nil
}
