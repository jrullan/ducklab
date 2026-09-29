package service

import (
	"bytes"
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
type renderOutcome struct {
	Captures []string
	Note     string
}

const defaultProductSmokeTimeoutS = 3

// productSmokeConfig keeps the compatibility fallback in one place. A
// project may give the gate a headless/test-safe command without changing the
// command a person launches.
func productSmokeConfig(run config.RunApp) (command, source, expectation string, timeoutS int) {
	command = strings.TrimSpace(run.Smoke)
	source = "run.smoke"
	if command == "" {
		command = strings.TrimSpace(run.Command)
		source = "run.command"
	}
	timeoutS = run.SmokeTimeoutS
	if timeoutS <= 0 {
		timeoutS = defaultProductSmokeTimeoutS
	}
	expectation = strings.TrimSpace(run.SmokeExpect)
	if expectation == "" {
		switch {
		case strings.TrimSpace(run.URL) != "", strings.TrimSpace(run.Health) != "", source == "run.command":
			expectation = "live"
		default:
			expectation = "exit"
		}
	}
	return command, source, expectation, timeoutS
}

// smokeRunCommand closes the gap between "compiled" and "runs". A command
// that exits successfully is a valid CLI smoke; a long-lived GUI/server that
// survives the observation window is stopped and counted as live. Because the
// command is the human-approved run.smoke (or its documented run.command
// fallback), so this adds no inferred executable authority to the gate.
func smokeRunCommand(ctx context.Context, root, command, source, expectation string, timeoutS int, runID, projectID string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil
	}
	if strings.TrimSpace(source) == "" {
		source = "run.smoke"
	}
	if timeoutS <= 0 {
		timeoutS = defaultProductSmokeTimeoutS
	}
	if expectation == "" {
		expectation = "exit"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parentCtx := ctx
	window := time.Duration(timeoutS) * time.Second
	env := append(os.Environ(), "DUCKLAB_RUN_ID="+runID, "DUCKLAB_PROJECT_ID="+projectID)
	if expectation == "live" {
		runCtx, hardStop := context.WithCancel(parentCtx)
		defer hardStop()
		cmd := xplat.ShellContext(runCtx, root, env, command)
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("%s expected live: %w", source, err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		timer := time.NewTimer(window)
		defer timer.Stop()
		select {
		case err := <-done:
			detail := fmt.Sprintf("%s expected live for %s but exited early", source, window)
			if err == nil {
				detail += " with status 0"
			} else {
				detail += ": " + err.Error()
			}
			if text := renderNoteOutput(output.Bytes()); text != "" {
				detail += "; output: " + text
			}
			return "", fmt.Errorf("%s", detail)
		case <-parentCtx.Done():
			hardStop()
			<-done
			return "", parentCtx.Err()
		case <-timer.C:
			_ = xplat.TerminateProcessGroup(cmd)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				hardStop()
				<-done
			}
			note := fmt.Sprintf("%s met expectation live: stayed alive for %s; stopped gracefully after the product-smoke observation window", source, window)
			if text := renderNoteOutput(output.Bytes()); text != "" {
				note += "; output: " + text
			}
			return note, nil
		}
	}
	smokeCtx, cancel := context.WithTimeout(parentCtx, window)
	defer cancel()
	out, commandErr := xplat.ShellContext(smokeCtx, root, env, command).CombinedOutput()
	if commandErr == nil {
		if expectation == "live" {
			detail := fmt.Sprintf("%s expected live for %s but exited early with status 0", source, window)
			if output := renderNoteOutput(out); output != "" {
				detail += "; output: " + output
			}
			return "", fmt.Errorf("%s", detail)
		}
		return source + " met expectation exit: exited successfully", nil
	}
	if smokeCtx.Err() == context.DeadlineExceeded && parentCtx.Err() == nil {
		if expectation == "exit" {
			return "", fmt.Errorf("%s expected exit within %s but stayed alive", source, window)
		}
		note := fmt.Sprintf("%s met expectation live: stayed alive for %s; stopped after the product-smoke observation window", source, window)
		if output := renderNoteOutput(out); output != "" {
			note += "; output: " + output
		}
		return note, nil
	}
	return "", fmt.Errorf("%s expected %s: %s: %w", source, expectation, strings.TrimSpace(string(out)), commandErr)
}

func captureRender(ctx context.Context, root string, contract config.RenderContract, writer *runlog.Writer, runID, projectID string) (renderOutcome, error) {
	if strings.TrimSpace(contract.Command) == "" {
		return renderOutcome{}, nil
	}
	timeout := contract.TimeoutS
	if timeout <= 0 {
		timeout = 120
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parentCtx := ctx
	renderCtx, cancel := context.WithTimeout(parentCtx, time.Duration(timeout)*time.Second)
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
	out, commandErr := xplat.ShellContext(renderCtx, root, env, contract.Command).CombinedOutput()
	if strings.TrimSpace(contract.Artifacts) == "" {
		if commandErr != nil {
			// Without an artifact contract this is an app-liveness smoke. A GUI
			// that is still serving at the end of the observation window answered
			// the question successfully; ShellContext killing it is cleanup, not
			// an application failure. A parent cancellation remains a failure.
			if renderCtx.Err() == context.DeadlineExceeded && parentCtx.Err() == nil {
				note := fmt.Sprintf("render smoke stayed alive for %ds; stopped after the observation window", timeout)
				if output := renderNoteOutput(out); output != "" {
					note += "; output: " + output
				}
				return renderOutcome{Note: note}, nil
			}
			return renderOutcome{}, fmt.Errorf("render command: %s: %w", strings.TrimSpace(string(out)), commandErr)
		}
		return renderOutcome{}, nil
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
		return renderOutcome{}, fmt.Errorf("render artifacts glob: %w", err)
	}
	if len(matches) == 0 {
		if commandErr != nil {
			return renderOutcome{}, fmt.Errorf("render command: %s: %w", strings.TrimSpace(string(out)), commandErr)
		}
		return renderOutcome{}, fmt.Errorf("render artifacts matched no files: %s", contract.Artifacts)
	}
	var captures []string
	defer func() {
		for _, path := range matches {
			_ = os.Remove(path)
		}
	}()
	for _, path := range matches {
		if strings.ToLower(filepath.Ext(path)) != ".png" {
			return renderOutcome{}, fmt.Errorf("render artifact is not a PNG: %s", filepath.Base(path))
		}
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return renderOutcome{}, readErr
		}
		// The contract is PNG images, not arbitrary files renamed with .png.
		// Check the PNG signature before attaching evidence so clients can safely
		// render every capture as an image.
		if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
			return renderOutcome{}, fmt.Errorf("render artifact is not a valid PNG: %s", filepath.Base(path))
		}
		name := filepath.Base(path)
		if err := writer.WriteCapture(name, data); err != nil {
			return renderOutcome{}, err
		}
		captures = append(captures, name)
	}
	if commandErr != nil {
		// A dirty renderer is still successful when it produced usable evidence.
		// Preserve the captures and let the caller record the exit as a caveat.
		return renderOutcome{Captures: captures}, fmt.Errorf("render command exited unsuccessfully: %s: %w", strings.TrimSpace(string(out)), commandErr)
	}
	return renderOutcome{Captures: captures}, nil
}

func renderNoteOutput(out []byte) string {
	const maxRunes = 2000
	runes := []rune(strings.TrimSpace(string(out)))
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return string(runes)
}
