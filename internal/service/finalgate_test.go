package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// B-554: with a smoke or render after it, the final gate reuses the round
// gate's green only while nothing has executed since that gate; without one
// it always may (B-545).
func TestTheFinalGateRunsFreshOnlyWhenSomethingRanAfterTheRoundGate(t *testing.T) {
	ctx := context.Background()
	plain := &config.Project{}
	smoke := &config.Project{Run: config.RunApp{Smoke: "true"}}
	render := &config.Project{RenderConfigured: true, Render: config.RenderContract{Command: "true"}}

	sealed := tools.NewGateMemo()
	sealed.SealRoundGate(true)
	touched := tools.NewGateMemo()
	touched.SealRoundGate(true)
	touched.Touch()
	red := tools.NewGateMemo()
	red.SealRoundGate(false)

	for _, c := range []struct {
		name  string
		cfg   *config.Project
		memo  *tools.GateMemo
		fresh bool
	}{
		{"no smoke or render", plain, touched, false},
		{"smoke, untouched green", smoke, sealed, false},
		{"render, untouched green", render, sealed, false},
		{"smoke, something ran since", smoke, touched, true},
		{"render, something ran since", render, touched, true},
		{"smoke, round gate not green", smoke, red, true},
		{"smoke, no memo", smoke, nil, true},
	} {
		if got := tools.IsFreshGate(finalGateContext(ctx, c.cfg, c.memo)); got != c.fresh {
			t.Errorf("%s: fresh = %v, want %v", c.name, got, c.fresh)
		}
	}
}

// End to end: a solo build with a product smoke runs the project gate once.
// The round gate decides; the final gate reuses it on the untouched tree and
// says so, and the smoke still runs.
func TestASmokeAfterAnUntouchedGreenRoundGateDoesNotRerunTheGate(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "seed", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	counter := filepath.Join(t.TempDir(), "gate-runs")
	smoked := filepath.Join(t.TempDir(), "smoked")
	if _, err := s.ProjectUpdate(context.Background(), id, map[string]string{
		"run.smoke":        "touch '" + smoked + "'",
		"run.smoke_expect": "exit",
		"verify.mode":      "custom",
		"verify.custom":    "pwd -P >> '" + counter + "'; true",
	}); err != nil {
		t.Fatal(err)
	}
	native := true
	for did, duck := range s.cfg.Ducklings {
		duck.Caps.NativeTools = &native
		s.cfg.Ducklings[did] = duck
	}
	fake := s.providers["fake"].(*provider.Fake)
	fake.ScriptFunc = func(req provider.ChatRequest, call int) *provider.ChatResponse {
		var message provider.Message
		finish := provider.FinishStop
		if call == 1 {
			tc := provider.ToolCall{ID: "call-1", Type: "function"}
			tc.Function.Name, tc.Function.Arguments = "fs_write", `{"path":"index.html","content":"<p>calc</p>\n"}`
			message.ToolCalls = []provider.ToolCall{tc}
			finish = provider.FinishToolCalls
		} else {
			message.Content = "Done."
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: finish}}}
	}
	r, err := s.RunStart(context.Background(), id, RunRequest{TaskID: "T-001", Mode: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.waitForRun(context.Background(), r.ID); err != nil && !strings.Contains(err.Error(), "waiting for a human") {
		t.Fatal(err)
	}
	detail, err := s.RunGet(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rounds int
	var final string
	for _, e := range detail.Events {
		switch e.Type {
		case "round_gate":
			rounds++
		case "gate":
			final, _ = e.Data["output"].(string)
		}
	}
	if rounds != 1 || detail.Run.Verdict != "PASSED" {
		t.Fatalf("round gates = %d, verdict = %s (%s)", rounds, detail.Run.Verdict, detail.Run.Failure)
	}
	if !strings.Contains(final, "[reused: this exact tree") {
		t.Errorf("the final gate did not reuse the round gate's green:\n%s", final)
	}
	if _, err := os.Stat(smoked); err != nil {
		t.Error("the product smoke did not run after the reused gate")
	}
	// Only the round gate ran the command in the run's tree; a paused
	// worktree run's pre-reproduction runs it again in a clean checkout
	// elsewhere, which is not this gate.
	tree := dir
	if detail.Run.WorktreePath != "" {
		tree = detail.Run.WorktreePath
	}
	real, err := filepath.EvalSymlinks(tree)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(counter)
	if n := strings.Count(string(b), real+"\n"); n != 1 {
		t.Errorf("the gate command ran %d times in the run's tree, want 1 (the round gate):\n%s", n, b)
	}
}
