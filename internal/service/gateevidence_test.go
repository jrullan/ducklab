package service

import (
	"context"
	"image"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/capability"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/verify"
)

// gateEvidenceBuild runs a solo build whose gate command is verifyCmd and,
// when mismatch is set, a required visual check whose capture differs from
// its reference. It returns the run and its final gate.
func gateEvidenceBuild(t *testing.T, verifyCmd string, mismatch bool) (*runlog.Run, *runlog.Event) {
	t.Helper()
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
	tomlPath := filepath.Join(dir, ".ducklab", "project.toml")
	cfg, err := config.LoadProject(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Verify = config.Verify{Mode: "tests", Tests: verifyCmd}
	if mismatch {
		shot := filepath.Join(t.TempDir(), "shot.png")
		paintPNG(t, shot, 50, 50, white, black, image.Rect(0, 0, 25, 50))
		paintPNG(t, filepath.Join(dir, "ref.png"), 50, 50, black, white, image.Rect(0, 0, 25, 50))
		cfg.Render = config.RenderContract{
			Command:     `mkdir -p "$DUCKLAB_RENDER_OUTPUT" && cp '` + shot + `' "$DUCKLAB_RENDER_OUTPUT/calculator.png"`,
			Artifacts:   ".ducklab-render-captures/*.png",
			Enforcement: "required",
			Compare:     []config.RenderCompare{{Capture: "calculator.png", Reference: "ref.png"}},
		}
	}
	if err := config.SaveProject(tomlPath, cfg); err != nil {
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
			message.Content = "The page is written."
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
	events, err := runlog.ReadEvents(s.RunDir(r.ID))
	if err != nil {
		t.Fatal(err)
	}
	var gate *runlog.Event
	for _, e := range events {
		if e.Type == "gate" {
			gate = e
		}
	}
	if gate == nil {
		t.Fatal("no final gate event")
	}
	return detail.Run, gate
}

func redChecks(e *runlog.Event) []string {
	var out []string
	items, _ := e.Data["red_by"].([]interface{})
	for _, it := range items {
		m, _ := it.(map[string]interface{})
		out = append(out, stringValueAny(m["check"])+": "+stringValueAny(m["summary"]))
	}
	return out
}

// B-510, as r-20261005-110414-sumi ran: npm test printed "# pass 174 # fail
// 0" and the required visual check found 31.8% > 20%. The gate event said
// exit_code 1, as if the command had failed. It now keeps the command's 0,
// names the visual check, and the run is FAILED exactly as before.
func TestB510AVisualRedGateKeepsThePassingCommandsExitCode(t *testing.T) {
	run, gate := gateEvidenceBuild(t, `printf '# pass 174\n# fail 0\n'`, true)
	if run.Verdict != "FAILED" {
		t.Fatalf("verdict = %s, want FAILED: the visual check still fails the run", run.Verdict)
	}
	if gate.Data["gate"] != "red" || gate.Data["exit_code"] != 0 || intValue(gate.Data["effective_exit_code"]) != 1 || gate.Data["command_gate"] != "tests" {
		t.Fatalf("gate event = %v", gate.Data)
	}
	causes := redChecks(gate)
	if len(causes) != 1 || !strings.HasPrefix(causes[0], "visual: visual check failed: calculator.png differs from ref.png in 100.0% of pixels") {
		t.Fatalf("red_by = %q, want the visual check alone", causes)
	}
	for _, want := range []string{"gate red: tests passed (exit 0)", "visual check failed: calculator.png"} {
		if !strings.Contains(run.Failure, want) {
			t.Errorf("failure %q lacks %q", run.Failure, want)
		}
	}
	if strings.Contains(run.Failure, "gate failed (exit 1)") {
		t.Errorf("failure still blames the command: %q", run.Failure)
	}
}

// A failing command alone: its real exit code, named as the only cause,
// and the failure line it always had.
func TestB510ARedCommandIsItsOwnCause(t *testing.T) {
	run, gate := gateEvidenceBuild(t, `echo '# fail 1'; exit 3`, false)
	if run.Verdict != "FAILED" {
		t.Fatalf("verdict = %s", run.Verdict)
	}
	if gate.Data["exit_code"] != 3 || intValue(gate.Data["effective_exit_code"]) != 3 || gate.Data["gate"] != "tests" {
		t.Fatalf("gate event = %v", gate.Data)
	}
	if causes := redChecks(gate); len(causes) != 1 || causes[0] != `command: echo '# fail 1'; exit 3 exited 3` {
		t.Fatalf("red_by = %q", causes)
	}
	if !strings.HasPrefix(run.Failure, "gate failed (exit 3):") {
		t.Errorf("failure = %q", run.Failure)
	}
}

// A green gate names nothing and keeps exit_code and effective_exit_code
// equal.
func TestB510AGreenGateHasNoCauses(t *testing.T) {
	run, gate := gateEvidenceBuild(t, `true`, false)
	if run.Verdict == "FAILED" {
		t.Fatalf("verdict = %s (%s)", run.Verdict, run.Failure)
	}
	if gate.Data["exit_code"] != 0 || intValue(gate.Data["effective_exit_code"]) != 0 || gate.Data["red_by"] != nil {
		t.Fatalf("gate event = %v", gate.Data)
	}
}

// The matrix: every check that can turn the final gate red, alone, over a
// passing command. Each one is named in red_by with its summary; the event's
// effective exit code and `gate` stay red, so no consumer reading either
// takes the command's 0 for a pass; and the failure line names it.
func TestB510EachRedSourceIsNamedAndStaysRed(t *testing.T) {
	passing := &verify.Result{Gate: verify.GateTests, Command: "npm test --silent", ExitCode: 0}
	failing := &verify.Result{Gate: verify.GateTests, Command: "npm test --silent", ExitCode: 1}
	required := []capability.GateFinding{{Capability: "web", Kind: "untested_produces", Detail: "index.html has no test", Files: []string{"index.html"}, Enforcement: capability.Required}}
	advisory := []capability.GateFinding{{Capability: "web", Kind: "untested_produces", Detail: "advisory only", Enforcement: capability.Diagnostic}}
	cases := []struct {
		name                                                    string
		command                                                 *verify.Result
		task, taskLog, probe, probeLog, smoke, smokeWhy, visual string
		visualWhy                                               string
		coverage                                                []capability.GateFinding
		want                                                    string
		summary                                                 string
	}{
		{name: "command", command: failing, want: "command", summary: "npm test --silent exited 1"},
		{name: "task", command: passing, task: "red", taskLog: "task verification:\ngate: custom\ncmd: node --test t.mjs\nexit: 1\nnot ok 1",
			want: "task_verification", summary: "task verification: node --test t.mjs exited 1"},
		{name: "task produces", command: passing, task: "red", taskLog: "task artifact contract:\ngate: red\nmissing declared Produces files: a.js",
			want: "task_verification", summary: "task artifact contract: missing declared Produces files: a.js"},
		{name: "probes", command: passing, probe: "red", probeLog: "acceptance probe 2:\ngate: custom\ncmd: curl -f localhost\nexit: 7\n",
			want: "acceptance_probes", summary: "acceptance probe 2: curl -f localhost exited 7"},
		{name: "app smoke", command: passing, smoke: "red", smokeWhy: "the window closed after 2s",
			want: "app_smoke", summary: "product smoke failed: the window closed after 2s"},
		{name: "visual", command: passing, visual: "red", visualWhy: "visual check failed: calculator.png differs from REF-IMG-6c63e390 in 35.1% of pixels (allowed 30.0%)",
			want: "visual", summary: "visual check failed: calculator.png differs from REF-IMG-6c63e390 in 35.1% of pixels (allowed 30.0%)"},
		{name: "capability coverage", command: passing, coverage: required,
			want: "capability_coverage", summary: "capability coverage [web/untested_produces]: index.html has no test (index.html)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			redBy := finalGateRedBy(c.command, c.task, c.taskLog, c.probe, c.probeLog, c.smoke, c.smokeWhy, c.visual, c.visualWhy, append(c.coverage, advisory...))
			if len(redBy) != 1 || redBy[0].Check != c.want || redBy[0].Summary != c.summary {
				t.Fatalf("red_by = %+v, want %s: %q", redBy, c.want, c.summary)
			}
			event := finalGateEvent("red", 1, c.command, "out", redBy)
			if event["exit_code"] != c.command.ExitCode || event["effective_exit_code"] != 1 || event["gate"] != "red" {
				t.Fatalf("event = %v", event)
			}
			failure := finalGateFailure(c.command, redBy)
			if c.want == "command" {
				if failure != "gate failed (exit 1)" {
					t.Errorf("failure = %q", failure)
				}
				return
			}
			if failure != "gate red: tests passed (exit 0); "+c.summary {
				t.Errorf("failure = %q", failure)
			}
		})
	}
	// Green: nothing named, even with advisory coverage findings and a
	// diagnostic visual miss (visual == "").
	if redBy := finalGateRedBy(passing, "green", "", "green", "", "green", "", "", "", advisory); len(redBy) != 0 {
		t.Errorf("green gate named %+v", redBy)
	}
	// A run with no gate command but a red task check: the command's absence
	// is not a cause, and the failure line does not invent a passing one.
	none := &verify.Result{Gate: verify.GateNone}
	redBy := finalGateRedBy(none, "red", "task verification:\ncmd: x\nexit: 1", "none", "", "none", "", "", "", nil)
	if len(redBy) != 1 || redBy[0].Check != "task_verification" {
		t.Fatalf("red_by = %+v", redBy)
	}
	if got := finalGateFailure(none, redBy); got != "gate red; task verification: x exited 1" {
		t.Errorf("failure = %q", got)
	}
	// Several causes keep their order: the one the run met first leads.
	both := finalGateRedBy(failing, "red", "task verification:\ncmd: t\nexit: 2", "none", "", "none", "", "red", "visual check failed: x", nil)
	if len(both) != 3 || both[0].Check != "task_verification" || both[1].Check != "command" || both[2].Check != "visual" {
		t.Errorf("red_by = %+v", both)
	}
	if got := finalGateFailure(failing, both); got != "gate red: tests failed (exit 1); task verification: t exited 2; visual check failed: x" {
		t.Errorf("failure = %q", got)
	}
}

// The verdict side of the matrix: settleFinalGate is what executeRun judges
// by. Every red source alone keeps the gate red with a non-zero effective
// exit — exactly as before B-510 — while the command's own code is untouched
// and the source is named. Coverage is observed only over an otherwise green
// gate (B-400).
func TestB510SettledGateKeepsTheVerdictForEveryRedSource(t *testing.T) {
	passing := &verify.Result{Gate: verify.GateTests, Command: "npm test", ExitCode: 0}
	required := []capability.GateFinding{{Capability: "web", Kind: "untested_produces", Detail: "no test", Enforcement: capability.Required}}
	diagnostic := []capability.GateFinding{{Capability: "web", Kind: "untested_produces", Detail: "no test", Enforcement: capability.Diagnostic}}
	cases := []struct {
		name     string
		command  *verify.Result
		checks   finalGateChecks
		coverage []capability.GateFinding
		red      bool
		exit     int
		named    string
	}{
		{"green", passing, finalGateChecks{Task: "green", Probe: "green", Smoke: "green", Visual: "green"}, nil, false, 0, ""},
		{"diagnostic coverage", passing, finalGateChecks{}, diagnostic, false, 0, ""},
		{"command", &verify.Result{Gate: verify.GateTests, Command: "npm test", ExitCode: 4}, finalGateChecks{}, nil, false, 4, "command"},
		{"task", passing, finalGateChecks{Task: "red", TaskLog: "task verification:\ncmd: t\nexit: 1"}, required, true, 1, "task_verification"},
		{"probes", passing, finalGateChecks{Probe: "red", ProbeLog: "acceptance probe 1:\ncmd: p\nexit: 1"}, required, true, 1, "acceptance_probes"},
		{"app smoke", passing, finalGateChecks{Smoke: "red", SmokeReason: "exited"}, required, true, 1, "app_smoke"},
		{"visual", passing, finalGateChecks{Visual: "red", VisualSummary: "visual check failed: x"}, required, true, 1, "visual"},
		{"capability coverage", passing, finalGateChecks{}, required, true, 1, "capability_coverage"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			observed := false
			got := settleFinalGate(c.command, c.checks, "out", func() []capability.GateFinding {
				observed = true
				return c.coverage
			})
			if (got.Gate == "red") != c.red || got.Exit != c.exit {
				t.Fatalf("gate %q exit %d, want red=%v exit %d", got.Gate, got.Exit, c.red, c.exit)
			}
			if c.command.ExitCode != 0 && got.Gate != "tests" {
				t.Errorf("a red command alone keeps its gate word: %q", got.Gate)
			}
			// Coverage is looked at only when nothing else is red.
			if wantObserved := c.command.ExitCode == 0 && c.checks.Task != "red" && c.checks.Probe != "red" && c.checks.Smoke != "red" && c.checks.Visual != "red"; observed != wantObserved {
				t.Errorf("coverage observed = %v, want %v", observed, wantObserved)
			}
			if c.named == "" {
				if len(got.RedBy) != 0 {
					t.Errorf("green gate named %+v", got.RedBy)
				}
				return
			}
			if len(got.RedBy) != 1 || got.RedBy[0].Check != c.named {
				t.Fatalf("red_by = %+v, want %s", got.RedBy, c.named)
			}
			event := finalGateEvent(got.Gate, got.Exit, c.command, got.Output, got.RedBy)
			if event["exit_code"] != c.command.ExitCode || event["effective_exit_code"] != got.Exit {
				t.Errorf("event = %v", event)
			}
		})
	}
	// Without capabilities nothing is observed and nothing changes.
	if got := settleFinalGate(passing, finalGateChecks{}, "out", nil); got.Gate != "tests" || got.Exit != 0 || got.Output != "out" {
		t.Errorf("settled = %+v", got)
	}
}

func TestB510GateLogSummaryIsBounded(t *testing.T) {
	long := "task verification:\ncmd: " + strings.Repeat("é", 400) + "\nexit: 1"
	got := gateLogSummary(long, "fallback")
	if len(got) > 240 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "task verification: é") {
		t.Errorf("summary = %q (%d bytes)", got, len(got))
	}
	if gateLogSummary("", "fallback") != "fallback" {
		t.Error("empty log has no fallback")
	}
	// Earlier green evidence is not the red check's summary.
	got = gateLogSummary("blocking capability diagnostic [web/lint, required]:\ngate: red\nlint found 2 errors\n\nprior successful evidence:\ntask verification:\ncmd: ok\nexit: 0", "")
	if got != "capability diagnostic [web/lint, required]: lint found 2 errors" {
		t.Errorf("summary = %q", got)
	}
}
