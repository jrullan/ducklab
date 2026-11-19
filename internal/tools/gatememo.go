package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/vcs"
	"github.com/jrullan/ducklab/internal/verify"
)

// GateMemo remembers, for one run, which verification commands passed on
// which exact working tree (B-545).
//
// A clean one-round build ran the project gate five times — about a minute
// each on T-020 — and two of them judged a tree nothing had touched: the
// reviewer's verify_run re-ran the implementer's (the reviewer does not
// edit), and the final gate re-ran the round gate one second after it. A
// green result is reused only for the same command on the same tree:
// tracked and untracked files as SnapshotTree sees them. A red result, a
// changed tree, or a gate that changed the tree itself always runs again.
//
// The round gate is the one that decides (I2), so it never reuses: it runs
// under FreshGate and records what it found. What reuses is verify_run and
// the final gate, and what they reuse is the harness's own result, never a
// model's claim.
//
// Ignored files are outside the tree identity: a dependency reinstalled
// after a green verify_run is not seen. The round gate, which always runs,
// and the accept's clean-checkout reproduction are what catch that.
//
// The epoch counts every execution in the run's tree that the memo hears of:
// each real verification and each tool call (Touch). A green round gate seals
// the epoch it ended on; Untouched says nothing has executed since, which is
// what lets the final gate reuse that green even when a smoke or render
// follows (B-554): the build output they read is the one the round gate's own
// run left behind, and nothing has run that could have changed it.
type GateMemo struct {
	mu       sync.Mutex
	entries  map[string]gateMemoEntry
	epoch    uint64
	sealed   bool
	sealedAt uint64
}

// Touch records that something executed in the tree. Nil-safe.
func (m *GateMemo) Touch() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.epoch++
	m.mu.Unlock()
}

// SealRoundGate records how a round gate ended: a green seals the current
// epoch, anything else clears the seal. Nil-safe.
func (m *GateMemo) SealRoundGate(green bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.sealed, m.sealedAt = green, m.epoch
	m.mu.Unlock()
}

// UntouchedSinceGreenRoundGate reports whether the last round gate was green
// and nothing has executed since it ended.
func (m *GateMemo) UntouchedSinceGreenRoundGate() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sealed && m.epoch == m.sealedAt
}

type gateMemoEntry struct {
	res  verify.Result
	at   time.Time
	tree string
}

// NewGateMemo returns an empty memo for one run.
func NewGateMemo() *GateMemo { return &GateMemo{entries: map[string]gateMemoEntry{}} }

type freshGateKey struct{}

// FreshGate marks ctx so verifications run whatever the memo holds, and
// still record what they find: the round gate's context.
func FreshGate(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshGateKey{}, true)
}

type sealedReuseKey struct{}

// ReuseWhileSealed marks ctx so verifications reuse the memo only while
// nothing has executed since the last green round gate, re-checked at each
// verification: the final gate before a smoke or render (B-554). Its first
// step that really runs breaks the seal, and every later step then runs
// too, in the round gate's order, so the build output the smoke or render
// reads is never left behind by a different command (review of B-554).
func ReuseWhileSealed(ctx context.Context) context.Context {
	return context.WithValue(ctx, sealedReuseKey{}, true)
}

func reuseOnlyWhileSealed(ctx context.Context) bool {
	v, _ := ctx.Value(sealedReuseKey{}).(bool)
	return v
}

// IsFreshGate reports whether ctx was marked by FreshGate.
func IsFreshGate(ctx context.Context) bool { return isFreshGate(ctx) }

func isFreshGate(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshGateKey{}).(bool)
	return fresh
}

// treeIdentity names the working tree a verification judges. A variable so
// a test can change the tree without a repository.
var treeIdentity = func(root string) (string, error) { return vcs.New(root).SnapshotTree() }

func gateMemoKey(root string, cfg config.Verify, tree string) string {
	spec, _ := json.Marshal(cfg)
	return root + "\x00" + string(spec) + "\x00" + tree
}

// RunVerify is verify.Run through the run's memo (ectx.GateMemo); without a
// memo it is verify.Run.
func RunVerify(ctx context.Context, ectx *ExecContext, root string, cfg config.Verify, id verify.Identity) (*verify.Result, error) {
	var memo *GateMemo
	if ectx != nil {
		memo = ectx.GateMemo
	}
	if memo == nil {
		return verify.Run(ctx, root, cfg, id)
	}
	before, err := treeIdentity(root)
	if err != nil {
		// No identity, no reuse — and nothing recorded either.
		memo.Touch()
		return verify.Run(ctx, root, cfg, id)
	}
	key := gateMemoKey(root, cfg, before)
	fresh := isFreshGate(ctx) || (reuseOnlyWhileSealed(ctx) && !memo.UntouchedSinceGreenRoundGate())
	if !fresh {
		memo.mu.Lock()
		hit, ok := memo.entries[key]
		memo.mu.Unlock()
		if ok {
			reused := hit.res
			// Nothing ran: the record must not show the earlier run's time
			// as this one's (review of B-545).
			reused.Duration = 0
			reused.Output = fmt.Sprintf("[reused: this exact tree (%s) passed this command at %s in this run; not re-run]\n",
				shortTree(hit.tree), hit.at.Local().Format("15:04:05")) + reused.Output
			return &reused, nil
		}
	}
	memo.Touch()
	res, err := verify.Run(ctx, root, cfg, id)
	// Only the latest run of a key speaks for it. A run that is not green —
	// red, an error, a gate that changed the tree — removes what an earlier
	// green said (review of B-545: an implementer's green on tree T outlived
	// the round gate's red on the same T, and the final gate reused it).
	memo.mu.Lock()
	delete(memo.entries, key)
	memo.mu.Unlock()
	if err != nil || res == nil || !verify.IsGreen(res) {
		return res, err
	}
	// A gate that changed the tree judged a tree that no longer exists.
	if after, aerr := treeIdentity(root); aerr == nil && after == before {
		memo.mu.Lock()
		memo.entries[key] = gateMemoEntry{res: *res, at: time.Now(), tree: before}
		memo.mu.Unlock()
	}
	return res, nil
}

func shortTree(tree string) string {
	if len(tree) > 10 {
		return tree[:10]
	}
	return tree
}
