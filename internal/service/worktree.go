package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
	"github.com/jrullan/ducklab/internal/xplat"
)

// createRunWorktree creates the clean, isolated checkout used by build and test runs.
func (s *Service) createRunWorktree(run *runlog.Run, projectRoot string) error {
	state, err := xplat.StateDir()
	if err != nil {
		return fmt.Errorf("worktree state directory: %w", err)
	}
	path := filepath.Join(state, "worktrees", run.ProjectID, run.ID)
	base, err := vcs.New(projectRoot).DefaultBranchHead()
	if err != nil {
		return fmt.Errorf("read default branch HEAD: %w", err)
	}
	suffix := run.ID
	if i := strings.LastIndex(suffix, "-"); i >= 0 {
		suffix = suffix[i+1:]
	}
	branch := "ducklab/" + run.TaskID + "-" + suffix
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	git := vcs.New(projectRoot)
	if err := git.WorktreeAddAt(path, branch, base); err != nil {
		return fmt.Errorf("create run worktree: %w", err)
	}
	cfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		_ = git.WorktreeRemove(path)
		return err
	}
	run.LinkedDeps = linkInstalledDeps(projectRoot, path, cfg.Verify.LinkDeps)
	if cfg.Verify.Setup != "" {
		setup := config.Verify{Mode: string(verify.GateCustom), Custom: cfg.Verify.Setup, TimeoutS: cfg.Verify.TimeoutS}
		result, err := verify.Run(context.Background(), path, setup, verify.Identity{RunID: run.ID, ProjectID: run.ProjectID})
		if err != nil || !verify.IsGreen(result) {
			_ = git.WorktreeRemove(path)
			if err != nil {
				return err
			}
			return fmt.Errorf("prepare run worktree failed: %s", result.Output)
		}
	}
	run.WorktreePath, run.Branch, run.BaseSHA = path, branch, base
	return nil
}

func runRoot(run *runlog.Run, fallback string) string {
	if run != nil && run.WorktreePath != "" {
		return run.WorktreePath
	}
	return fallback
}

func (s *Service) cleanupRunWorktree(rs *runState, projectRoot string) {
	if rs.run.WorktreePath == "" {
		return
	}
	if _, err := os.Lstat(rs.run.WorktreePath); os.IsNotExist(err) {
		// A crash can remove the directory before the terminal decision. The
		// branch is still ours to retire even though git has nothing to remove.
		if rs.run.Branch != "" {
			_ = vcs.New(projectRoot).DeleteBranch(rs.run.Branch)
		}
		return
	}
	git := vcs.New(projectRoot)
	if err := git.WorktreeRemove(rs.run.WorktreePath); err != nil {
		rs.run.WorktreeCleanupFailure = rs.run.WorktreePath
		rs.writer.AppendEvent("warning", map[string]interface{}{"detail": "could not remove run worktree " + rs.run.WorktreePath + ": " + err.Error()})
		rs.writer.WriteState()
		return
	}
	if rs.run.Branch != "" {
		if err := git.DeleteBranch(rs.run.Branch); err != nil {
			rs.writer.AppendEvent("warning", map[string]interface{}{"detail": "could not delete run branch " + rs.run.Branch + ": " + err.Error()})
			_ = rs.writer.WriteState()
		}
	}
}
