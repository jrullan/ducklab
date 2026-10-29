package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/registry"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
)

// Pre-reproduction: the accept's clean-checkout reproduction, started while a
// green build waits at its gate (B-545).
//
// Accept reproduces the gate from a clean checkout of the commit it is about
// to land — on T-020 another full minute, after the person or the
// coordinator had already decided. That minute need not follow the decision:
// the commit an isolated run will land is known when it pauses (its
// worktree, staged as accept stages it, on its HEAD). So it is built as a
// dangling commit and reproduced in the background. Accept reuses that
// result only for the very tree it commits, under the same gate
// configuration, green, and recent; anything else — main moved and the
// accept rebases, the tree changed, the config changed, the reproduction
// failed or is old — and accept reproduces as before. Nothing lands that did
// not reproduce: what is reused IS a clean-checkout reproduction of that
// exact tree.
//
// It is cancelled when the run leaves its gate any other way (resume,
// reject, abort): a clean-checkout gate running beside a resumed run's own
// gate could fight it for ports and files.

// preReproductionMaxAge bounds how stale a reused reproduction may be: the
// clean checkout links installed dependencies, which can change under it.
const preReproductionMaxAge = 30 * time.Minute

type preReproduction struct {
	project string
	tree    string
	cfgKey  string
	done    chan struct{}
	cancel  context.CancelFunc
	// Set before done closes.
	repro *runlog.GateReproduction
	err   error
	at    time.Time
}

// preReproductions holds each run's in-flight or finished pre-reproduction.
type preReproductions struct {
	mu   sync.Mutex
	runs map[string]*preReproduction
}

func (p *preReproductions) put(runID string, pre *preReproduction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runs == nil {
		p.runs = map[string]*preReproduction{}
	}
	if old := p.runs[runID]; old != nil {
		old.cancel()
	}
	p.runs[runID] = pre
}

// take removes and returns the run's pre-reproduction.
func (p *preReproductions) take(runID string) *preReproduction {
	p.mu.Lock()
	defer p.mu.Unlock()
	pre := p.runs[runID]
	delete(p.runs, runID)
	return pre
}

// busy reports whether a pre-reproduction of the project is still running.
func (p *preReproductions) busy(projectID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pre := range p.runs {
		if pre.project != projectID {
			continue
		}
		select {
		case <-pre.done:
		default:
			return true
		}
	}
	return false
}

// interruptPreReproductions cancels every pre-reproduction and waits for
// them to unwind, so engine shutdown leaves no gate process, temporary
// checkout or registered worktree behind (review of B-545: they ran on a
// context nothing cancelled).
func (s *Service) interruptPreReproductions(ctx context.Context) {
	s.preRepro.mu.Lock()
	all := s.preRepro.runs
	s.preRepro.runs = nil
	s.preRepro.mu.Unlock()
	for _, pre := range all {
		pre.cancel()
	}
	for _, pre := range all {
		select {
		case <-pre.done:
		case <-ctx.Done():
			return
		}
	}
}

// otherRunActive reports whether another run of the project is running or
// queued: its own gate could fight a background clean-checkout gate for the
// same ports and files (review of B-545).
func (s *Service) otherRunActive(projectID, runID string) bool {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	for id, rs := range s.runs {
		if id == runID || rs.run == nil || rs.run.ProjectID != projectID {
			continue
		}
		if st := rs.snapshotRun().Status; st == "running" || st == "queued" {
			return true
		}
	}
	return false
}

// dropPreReproduction cancels a run's pre-reproduction: the run is leaving
// its gate some way other than accept.
func (s *Service) dropPreReproduction(runID string) {
	if pre := s.preRepro.take(runID); pre != nil {
		pre.cancel()
	}
}

// gateConfigKey is the gate configuration a reproduction ran under.
func gateConfigKey(projectRoot string) string {
	cfg, err := config.LoadProject(filepath.Join(projectRoot, ".ducklab", "project.toml"))
	if err != nil {
		return ""
	}
	key, _ := json.Marshal(cfg.Verify)
	return string(key)
}

// startPreReproduction starts the accept's reproduction for a build whose
// gate passed and that just paused for a decision in an isolated worktree.
// Best-effort: when anything is off, it does nothing and accept reproduces
// as before. At most one runs per project, and none while another run of
// the project is live: a background gate must not turn someone else's gate
// red.
func (s *Service) startPreReproduction(rs *runState, entry *registry.ProjectEntry) {
	run := rs.snapshotRun()
	if run.Stage != "build" || run.WorktreePath == "" || s.shuttingDown.Load() {
		return
	}
	if s.preRepro.busy(run.ProjectID) || s.otherRunActive(run.ProjectID, run.ID) {
		return
	}
	work := vcs.New(run.WorktreePath)
	defaultGit := vcs.New(entry.Path)
	defaultSHA, err := defaultGit.DefaultBranchHead()
	if err != nil {
		return
	}
	// Accept would rebase onto a moved main: the tree it lands is not this one.
	if ok, _ := work.IsAncestor(defaultSHA, "HEAD"); !ok {
		return
	}
	excluded := append([]string(nil), run.LinkedDeps...)
	excluded = append(excluded, landingExclusions(work.Root, entry.Path)...)
	sha, tree, err := work.CandidateCommit("ducklab-pre-reproduction", excluded...)
	if err != nil {
		return
	}
	cfgKey := gateConfigKey(entry.Path)
	if cfgKey == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	pre := &preReproduction{project: run.ProjectID, tree: tree, cfgKey: cfgKey, done: make(chan struct{}), cancel: cancel}
	s.preRepro.put(run.ID, pre)
	go func() {
		defer close(pre.done)
		pre.repro, pre.err = verifyAcceptedCommit(ctx, defaultGit, entry.Path, sha, run.Stage,
			verify.Identity{RunID: run.ID, ProjectID: run.ProjectID})
		pre.at = time.Now()
		// The config the reproduction read is the one at its start; if it
		// changed while it ran, the result proves neither (review of B-545).
		if pre.err == nil && gateConfigKey(entry.Path) != cfgKey {
			pre.err = errors.New("the gate configuration changed while the reproduction ran")
		}
		if ctx.Err() != nil {
			return
		}
		// Only a run still waiting at its gate gets the note: one already
		// decided has closed its log (review of B-545).
		if st := rs.snapshotRun(); st.Status != "paused" || st.PendingKind != "gate" {
			return
		}
		if w, werr := s.ensureWriter(rs); werr == nil {
			data := map[string]interface{}{"tree": tree, "green": pre.err == nil && pre.repro != nil && pre.repro.Green}
			if pre.repro != nil {
				data["duration_s"] = pre.repro.Duration
			}
			_ = w.AppendEvent("pre_reproduction", data)
		}
	}()
}

// preReproduced returns the run's pre-reproduction when it reproduced
// exactly the tree of sha, green, under the current gate configuration, and
// recently — waiting for it if it is still running. ok is false otherwise;
// the caller then reproduces as before.
func (s *Service) preReproduced(ctx context.Context, runID string, git *vcs.Git, projectRoot, sha string) (*runlog.GateReproduction, bool) {
	pre := s.preRepro.take(runID)
	if pre == nil {
		return nil, false
	}
	tree, err := git.TreeOf(sha)
	if err != nil || tree != pre.tree {
		pre.cancel()
		return nil, false
	}
	select {
	case <-pre.done:
	case <-ctx.Done():
		pre.cancel()
		return nil, false
	}
	if pre.err != nil || pre.repro == nil || !pre.repro.Green || time.Since(pre.at) > preReproductionMaxAge {
		return nil, false
	}
	if gateConfigKey(projectRoot) != pre.cfgKey {
		return nil, false
	}
	reused := *pre.repro
	reused.Output = "[reproduced from a clean checkout of this exact tree at " + pre.at.Local().Format("15:04:05") +
		" while the gate waited; not re-run]\n" + reused.Output
	return &reused, true
}
