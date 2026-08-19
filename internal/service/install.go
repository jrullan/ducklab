package service

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/config"
)

// InstallResult is one run of the project's declared install chain.
type InstallResult struct {
	Command  string  `json:"command"`
	ExitCode int     `json:"exit_code"`
	Output   string  `json:"output"`
	Seconds  float64 `json:"seconds"`
	OK       bool    `json:"ok"`
}

// ProjectInstall runs the project's declared install command, from the
// project root, and returns what happened — so a developer never leaves
// ducklab to make accepted work runnable. Refused, with the fix named, when
// nothing is declared: guessing a build chain is how checkouts got
// hard-coded (B-061).
func (s *Service) ProjectInstall(ctx context.Context, projectID string) (*InstallResult, error) {
	entry, err := s.registry.Get(projectID)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadProject(filepath.Join(entry.Path, ".ducklab", "project.toml"))
	if err != nil {
		return nil, err
	}
	cmdline := strings.TrimSpace(cfg.Install.Command)
	if cmdline == "" {
		return nil, fmt.Errorf("this project declares no install command; next: add [install] command = \"…\" to .ducklab/project.toml")
	}
	timeout := time.Duration(cfg.Install.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, "bash", "-lc", cmdline)
	cmd.Dir = entry.Path
	raw, err := cmd.CombinedOutput()
	exit := cmd.ProcessState.ExitCode()
	out := string(raw)
	res := &InstallResult{
		Command: cmdline, ExitCode: exit, Output: tail(out, 16000),
		Seconds: time.Since(start).Seconds(), OK: err == nil && exit == 0,
	}
	if cctx.Err() == context.DeadlineExceeded {
		res.Output += fmt.Sprintf("\n(killed after %s — raise [install] timeout_s if the chain honestly needs longer)", timeout)
	}
	return res, nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
