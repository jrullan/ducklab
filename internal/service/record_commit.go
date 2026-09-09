package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// commitRunRecord gives the harness's own record of an acceptance a commit
// owner (B-291, scope A): right after the landing commit and its receipt, the
// run's receipt — and the bug-ladder lines this very run produced, when the
// audit file carries nothing else — land in a commit of their own, worded,
// marked with a Ducklab-Record trailer so the release inventory never counts
// it as delivered work. Everything else the harness dirties (other bug moves,
// plan edits, Settings) is the release cut's sweep, never mixed in here.
//
// It commits on the registered checkout only when that checkout is on the
// default branch with a clean index: the landing already advanced the branch
// and synced the checkout, so a plain commit there is the line of record. Any
// other situation defers to the sweep and says so on the run stream; a record
// that cannot be committed must never fail the accept that produced it.
func (s *Service) commitRunRecord(root string, rs *runState) {
	git := vcs.New(root)
	if !git.HasGit() {
		return
	}
	note := func(kind, detail string, extra map[string]interface{}) {
		data := map[string]interface{}{"detail": detail}
		for k, v := range extra {
			data[k] = v
		}
		if rs.writer != nil {
			rs.writer.AppendEvent(kind, data)
		}
	}
	onDefault, err := git.OnDefaultBranch()
	if err != nil || !onDefault {
		note("record_deferred", "the registered checkout is not on the default branch; the run's receipt stays uncommitted until the release sweep", nil)
		return
	}
	if staged, err := git.StagedPaths(); err != nil || len(staged) > 0 {
		note("record_deferred", "the registered checkout has staged changes of its own; the run's receipt stays uncommitted until the release sweep", nil)
		return
	}
	receipt := filepath.ToSlash(filepath.Join(".ducklab", "runs", rs.run.ID, "receipt.json"))
	if _, err := os.Stat(filepath.Join(root, receipt)); err != nil {
		return
	}
	// A project ships its proofs only if its .gitignore says so (ducklab's own
	// negates receipt.json under .ducklab/runs). Anything else has nothing to
	// record here, and the .gitignore contract is not this code's to change.
	if git.IsIgnored(receipt) {
		note("record_skipped", "this project's .gitignore does not track run receipts; nothing to record", nil)
		return
	}
	paths := []string{receipt}
	fixed := fixedBugOf(rs)
	audit := filepath.ToSlash(filepath.Join(".ducklab", "bugs", "audit.jsonl"))
	if lines, err := git.AddedLines(audit); err == nil && len(lines) > 0 && !git.IsIgnored(audit) {
		own := fixed != ""
		for _, line := range lines {
			if !strings.Contains(line, `"bug":"`+fixed+`"`) {
				own = false
				break
			}
		}
		if own {
			paths = append(paths, audit)
		} else {
			note("record_partial", "the bug audit trail carries moves this run did not make; they stay uncommitted until the release sweep", map[string]interface{}{"file": audit})
		}
	}
	if err := git.Add(paths...); err != nil {
		note("warning", fmt.Sprintf("the run's record could not be staged: %v", err), nil)
		return
	}
	staged, err := git.StagedPaths()
	if err != nil || len(staged) == 0 {
		return
	}
	subject := "ducklab: record " + rs.run.ID
	if rs.run.TaskID != "" {
		subject += " (" + rs.run.TaskID + ")"
	}
	body := "Acceptance receipt of " + rs.run.ID
	if len(paths) > 1 {
		body += "; bug ladder move of " + fixed
	}
	sha, err := git.CommitWithTrailer(subject+"\n\n"+body+".", map[string]string{"Ducklab-Record": rs.run.ID})
	if err != nil {
		note("warning", fmt.Sprintf("the run's record could not be committed: %v", err), nil)
		return
	}
	note("record_committed", "the run's receipt landed in its own commit", map[string]interface{}{"sha": sha, "paths": staged})
}

// fixedBugOf reads the bug this run's accept moved to fixed, from the run's
// own record.
func fixedBugOf(rs *runState) string {
	if rs.runDir == "" {
		return ""
	}
	events, err := runlog.ReadEvents(rs.runDir)
	if err != nil {
		return ""
	}
	fixed := ""
	for _, e := range events {
		if e.Type == "bug_fixed" {
			if id, ok := e.Data["bug"].(string); ok {
				fixed = id
			}
		}
	}
	return fixed
}
