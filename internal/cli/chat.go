package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duck"
	"github.com/jrullan/ducklab/internal/prim"
	"github.com/jrullan/ducklab/internal/project"
	"github.com/jrullan/ducklab/internal/run"
	"github.com/jrullan/ducklab/internal/source"
	"github.com/jrullan/ducklab/internal/strategy"
)

func cmdChat(args []string) int {
	_, f := parseRunFlags(args)
	repo := f.repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	repo, _ = filepath.Abs(repo)
	gate := prim.DetectGate(repo)
	if strings.TrimSpace(f.tests) != "" {
		gate = prim.GateFromCmd(f.tests)
	}
	m := newChatModel(repo, gate)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, duck.Bad.Render("chat needs a terminal: ")+err.Error())
		return 1
	}
	return 0
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ─── messages ─────────────────────────────────────────────────────
type stageMsg struct{ stage, src string }
type callMsg struct {
	src    string
	tokens int
	secs   float64
}
type retryMsg struct {
	attempt int
	reason  string
}
type noticeMsg struct{ text string }
type tickMsg struct{}
type doneMsg struct {
	outcome strategy.Outcome
	err     error
}

// ─── model ────────────────────────────────────────────────────────
type chatModel struct {
	vp      viewport.Model
	ti      textinput.Model
	lines   []string
	sub     chan tea.Msg
	running bool
	ready   bool

	repo              string
	gate              prim.Gate
	mode, a, b, judge string
	goal              string
	current           string
	last              *strategy.Outcome
	quip              int

	// live phase telemetry
	phaseStart time.Time
	phaseName  string
	elapsed    float64
}

func newChatModel(repo string, gate prim.Gate) *chatModel {
	ti := textinput.New()
	ti.Placeholder = "describe a task, or /help"
	ti.Prompt = duck.Prompt()
	ti.Focus()
	ti.CharLimit = 0
	m := &chatModel{
		ti: ti, repo: repo, gate: gate,
		mode: "driver", a: "beelink", b: "aitopatom", judge: "aitopatom",
		sub: make(chan tea.Msg, 64),
	}
	m.println(duck.Banner("a multi-model harness for local LLMs"))
	m.println(duck.Quack.Render("  " + duck.Quip(0)))
	m.println("")
	m.println(duck.Dim.Render("  /help for commands · " + m.configLine()))
	return m
}

func (m *chatModel) Init() tea.Cmd { return textinput.Blink }

func (m *chatModel) println(s string) { m.lines = append(m.lines, s) }

func (m *chatModel) configLine() string {
	return fmt.Sprintf("mode=%s · %s · gate=%s", m.mode, rolesDesc(m.mode, m.a, m.b, m.judge), m.gate.Label())
}

// rolesDesc names the models actually used by a mode, so it's obvious which
// sources are active (e.g. plan uses A+B, not the judge).
func rolesDesc(mode, a, b, judge string) string {
	switch mode {
	case "solo":
		return "solver=" + a
	case "driver":
		return "driver=" + a + " observer=" + b
	case "plan":
		return "planner=" + a + " reviewer=" + b
	case "tournament":
		return "A=" + a + " B=" + b + " judge=" + judge
	default:
		return "a=" + a + " b=" + b + " judge=" + judge
	}
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h := msg.Height - 3
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = msg.Width, h
		}
		m.ti.Width = msg.Width - 8
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.running {
				return m, nil
			}
			line := strings.TrimSpace(m.ti.Value())
			m.ti.Reset()
			return m.handle(line)
		}

	case stageMsg:
		m.phaseStart = time.Now()
		m.phaseName = msg.stage
		m.elapsed = 0
		if msg.src != "" {
			m.phaseName += " — " + msg.src
			m.println(duck.Dim.Render("    · " + msg.stage + " — " + msg.src))
		} else {
			m.println(duck.Dim.Render("    · " + msg.stage))
		}
		m.refresh()
		return m, m.waitFor()

	case callMsg:
		m.println(duck.Dim.Render(fmt.Sprintf("      ↳ %s · %d tok · %.1fs",
			msg.src, msg.tokens, msg.secs)))
		m.refresh()
		return m, m.waitFor()

	case retryMsg:
		m.println(duck.Warns.Render(fmt.Sprintf("      ↻ %s — retrying (%d/%d)",
			msg.reason, msg.attempt+1, 3)))
		m.refresh()
		return m, m.waitFor()

	case noticeMsg:
		m.println(duck.Hunk.Render("  ◌ " + msg.text))
		m.refresh()
		return m, m.waitFor()

	case tickMsg:
		if !m.running {
			return m, nil
		}
		if !m.phaseStart.IsZero() {
			m.elapsed = time.Since(m.phaseStart).Seconds()
		}
		return m, m.tick()

	case doneMsg:
		m.running = false
		m.phaseName = ""
		if msg.err != nil {
			m.println(duck.Bad.Render("  ✗ " + msg.err.Error()))
			if strings.Contains(msg.err.Error(), "uncommitted changes") {
				m.println(duck.Dim.Render("  ▸ /commit to keep those changes, then /run again"))
			}
		} else {
			m.last = &msg.outcome
			// Remember a failed approach so a re-run of this goal avoids it.
			if msg.outcome.State == "ESCALATED" && m.current != "" {
				if r, err := run.Open(filepath.Join(m.repo, "runs", m.current)); err == nil {
					recordFailure(m.repo, r, msg.outcome)
				}
			}
			m.renderOutcome(msg.outcome)
		}
		m.refresh()
		return m, nil
	}

	var cmds []tea.Cmd
	var c tea.Cmd
	m.ti, c = m.ti.Update(msg)
	cmds = append(cmds, c)
	m.vp, c = m.vp.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m *chatModel) refresh() {
	if m.ready {
		m.vp.SetContent(strings.Join(m.lines, "\n"))
		m.vp.GotoBottom()
	}
}

// tick drives the live per-phase elapsed clock while a run is in flight.
func (m *chatModel) tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *chatModel) View() string {
	if !m.ready {
		return "starting ducklab…"
	}
	status := duck.Dim.Render(strings.Repeat("─", max(m.vp.Width, 1)))
	if m.running && m.phaseName != "" {
		status = duck.Hunk.Render(fmt.Sprintf("⏱ %2.0fs", m.elapsed)) + "  " + duck.Dim.Render(m.phaseName)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), status, m.ti.View())
}

// ─── command handling ─────────────────────────────────────────────
func (m *chatModel) handle(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m, nil
	}
	m.println(duck.Prompt() + line)
	if !strings.HasPrefix(line, "/") {
		m.goal = line
		m.println(duck.Quack.Render("  goal set. /run when ready."))
		m.refresh()
		return m, nil
	}
	fields := strings.Fields(line)
	cmd, rest := fields[0], fields[1:]
	switch cmd {
	case "/help":
		m.help()
	case "/config":
		m.println(duck.Dim.Render("  " + m.configLine()))
		m.println(duck.Dim.Render("  repo=" + m.repo + " goal=" + orNone(m.goal)))
		if p, ok := project.Load(m.repo); ok && p.Description != "" {
			m.println(duck.Dim.Render("  project=" + p.Description +
				fmt.Sprintf(" (%d past goals)", len(p.Goals))))
		}
	case "/project":
		if len(rest) > 0 {
			desc := strings.Join(rest, " ")
			_ = project.SetDescription(m.repo, desc)
			m.println(duck.OK.Render("  project → " + desc))
		} else if p, ok := project.Load(m.repo); ok && (p.Description != "" || len(p.Goals) > 0) {
			m.println(duck.Key.Render("  project ") + orNone(p.Description))
			if len(p.Goals) > 0 {
				m.println(duck.Dim.Render("  goals: " + strings.Join(p.Goals, " · ")))
			}
		} else {
			m.println(duck.Dim.Render("  no project note yet — /project \"...\" or it's inferred on first run"))
		}
	case "/mode":
		if len(rest) == 1 {
			if _, ok := strategy.Get(rest[0]); ok {
				m.mode = rest[0]
				m.println(duck.OK.Render("  mode → " + m.mode))
			} else {
				m.println(duck.Bad.Render("  unknown mode. have: " + strings.Join(strategy.Names(), ", ")))
			}
		}
	case "/models":
		if len(rest) == 4 && rest[0] == "A" && rest[2] == "B" {
			m.a, m.b = rest[1], rest[3]
			m.println(duck.OK.Render(fmt.Sprintf("  A=%s B=%s", m.a, m.b)))
		} else {
			m.listSources()
		}
	case "/judge":
		if len(rest) == 1 {
			m.judge = rest[0]
			m.println(duck.OK.Render("  judge → " + m.judge))
		}
	case "/repo":
		if len(rest) == 1 {
			m.repo, _ = filepath.Abs(rest[0])
			m.gate = prim.DetectGate(m.repo)
			m.println(duck.OK.Render("  repo → " + m.repo))
			m.println(duck.Dim.Render("  gate → " + m.gate.Label()))
		}
	case "/verify", "/tests":
		if len(rest) == 1 && rest[0] == "auto" {
			m.gate = prim.DetectGate(m.repo)
		} else if len(rest) == 1 && rest[0] == "none" {
			m.gate = prim.Gate{Kind: "none"}
		} else if len(rest) >= 1 {
			m.gate = prim.GateFromCmd(strings.Join(rest, " "))
		}
		m.println(duck.OK.Render("  gate → " + m.gate.Label()))
	case "/goal":
		if len(rest) >= 1 {
			m.goal = strings.Join(rest, " ")
		}
		m.println(duck.Dim.Render("  goal: " + orNone(m.goal)))
	case "/run":
		return m.startRun()
	case "/show":
		m.show()
	case "/diff":
		m.diff()
	case "/commit":
		m.commit(rest)
	case "/accept":
		m.accept()
	case "/reject":
		m.reject()
	case "/exit", "/quit":
		return m, tea.Quit
	default:
		m.println(duck.Bad.Render("  unknown command: " + cmd))
	}
	m.refresh()
	return m, nil
}

func (m *chatModel) help() {
	for _, l := range []string{
		duck.Title.Render("  commands"),
		"  " + duck.Key.Render("/mode") + " solo|driver|tournament|plan   collaboration recipe",
		"  " + duck.Key.Render("/models") + " [A <s> B <s>]           list or assign contestants",
		"  " + duck.Key.Render("/judge") + " <src>                    assign judge/observer",
		"  " + duck.Key.Render("/repo") + " <path>   " + duck.Key.Render("/verify") + " <cmd|auto|none>   repo & verification gate",
		"  " + duck.Key.Render("/goal") + " <text>  (or just type)     set the task",
		"  " + duck.Key.Render("/project") + " [\"desc\"]                 project memory (inferred if unset)",
		"  " + duck.Key.Render("/run") + "                             launch the run",
		"  " + duck.Key.Render("/show") + "  " + duck.Key.Render("/diff") + "   inspect result (summary · full patch)",
		"  " + duck.Key.Render("/accept") + "  " + duck.Key.Render("/reject") + "  " + duck.Key.Render("/commit") + " [msg]      merge · discard · keep manual edits",
		"  " + duck.Key.Render("/config") + "   " + duck.Key.Render("/exit"),
	} {
		m.println(l)
	}
}

func (m *chatModel) listSources() {
	srcs, err := config.Load()
	if err != nil {
		m.println(duck.Bad.Render("  " + err.Error()))
		return
	}
	ctx := context.Background()
	for name, s := range srcs {
		model, err := s.ResolveModel(ctx)
		if err != nil {
			m.println("  " + duck.Key.Render(pad(name, 12)) + duck.Bad.Render("unreachable"))
		} else {
			m.println("  " + duck.Key.Render(pad(name, 12)) + duck.OK.Render(model))
		}
	}
}

func (m *chatModel) startRun() (tea.Model, tea.Cmd) {
	if m.goal == "" {
		m.println(duck.Bad.Render("  no goal. type a task first."))
		m.refresh()
		return m, nil
	}
	strat, ok := strategy.Get(m.mode)
	if !ok {
		m.println(duck.Bad.Render("  unknown mode: " + m.mode))
		m.refresh()
		return m, nil
	}
	// Greenfield: initialize a git repo so ducklab can isolate its work.
	if initialized, err := prim.EnsureRepo(m.repo); err != nil {
		m.println(duck.Bad.Render("  cannot initialize repo: " + err.Error()))
		m.refresh()
		return m, nil
	} else if initialized {
		m.println(duck.Hunk.Render("  ◌ no git repo here — ran git init + initial commit so ducklab can branch"))
		m.gate = prim.DetectGate(m.repo) // re-detect now that the tree exists
	}
	// A gate is optional (no gate → UNVERIFIED). But if one is set, its binary
	// must resolve — a typo shouldn't burn a model call on a phantom red.
	if m.gate.Active() {
		bin := strings.Fields(m.gate.Cmd)[0]
		if ok, _ := prim.Shell("command -v "+bin, m.repo); !ok {
			m.println(duck.Bad.Render("  preflight failed — gate command not found: " + bin))
			m.println(duck.Dim.Render("  check /verify (typo? wrong venv?) or /verify none to run unverified"))
			m.refresh()
			return m, nil
		}
	} else {
		m.println(duck.Hunk.Render("  ◌ no gate — this run will be UNVERIFIED (reviewer + your eyes)"))
	}
	srcs, err := config.Load()
	if err != nil {
		m.println(duck.Bad.Render("  " + err.Error()))
		m.refresh()
		return m, nil
	}
	a, ok1 := srcs[m.a]
	b, ok2 := srcs[m.b]
	j, ok3 := srcs[m.judge]
	if !ok1 || !ok2 || !ok3 {
		m.println(duck.Bad.Render("  unknown source in a/b/judge"))
		m.refresh()
		return m, nil
	}
	taskID := m.dedupID(prim.Slugify(m.goal))
	m.current = taskID
	r, err := run.Open(filepath.Join(m.repo, "runs", taskID))
	if err != nil {
		m.println(duck.Bad.Render("  " + err.Error()))
		m.refresh()
		return m, nil
	}
	_ = r.Set("mode", m.mode)
	_ = r.Set("requirement", m.goal)

	onCall := func(res source.Result) { m.sub <- callMsg{res.Source, res.Tokens(), res.Elapsed.Seconds()} }
	onRetry := func(attempt int, reason string) { m.sub <- retryMsg{attempt, reason} }
	goal := m.goal
	m.running = true
	m.phaseStart = time.Now()
	m.quip++
	m.println(duck.Quack.Render(fmt.Sprintf("  quack — %s on [%s] · %s", m.mode, taskID, duck.Quip(m.quip))))
	m.refresh()
	go func() {
		// Project context: inject the session preamble, inferring the
		// description from history+files the first time if unset.
		ctxOpts := source.Options{Temperature: 0.2, DisableThinking: true, OnDone: onCall}
		m.sub <- stageMsg{"CONTEXT", a.Name()}
		effReq, inferred, priorFails := projectRequirement(a, m.repo, goal, ctxOpts)
		if inferred != "" {
			m.sub <- noticeMsg{"project: " + inferred + "  (/project to edit)"}
		}
		if priorFails > 0 {
			m.sub <- noticeMsg{fmt.Sprintf("avoiding %d prior failed approach(es) for this goal", priorFails)}
		}
		env := strategy.Env{
			Ctx: context.Background(), TaskID: taskID, Requirement: effReq,
			Repo: m.repo, Gate: m.gate,
			Contestants: []source.Client{a, b}, Judge: j, Run: r,
			OnStage: func(stage, src string) { m.sub <- stageMsg{stage, src} },
			OnCall:  onCall,
			OnRetry: onRetry,
		}
		out, err := strat.Run(env)
		m.sub <- doneMsg{out, err}
	}()
	return m, tea.Batch(m.waitFor(), m.tick())
}

func (m *chatModel) waitFor() tea.Cmd {
	return func() tea.Msg { return <-m.sub }
}

func (m *chatModel) dedupID(base string) string {
	if base == "" {
		base = "run"
	}
	if len(base) > 40 {
		base = base[:40]
	}
	id, n := base, 2
	for exists(filepath.Join(m.repo, "runs", id)) {
		id = fmt.Sprintf("%s-%d", base, n)
		n++
	}
	return id
}

func (m *chatModel) renderOutcome(o strategy.Outcome) {
	switch o.State {
	case "HUMAN_GATE":
		m.println(duck.OK.Render("  ✓ HUMAN_GATE — ") + o.Message)
	case "UNVERIFIED":
		m.println(duck.Hunk.Render("  ◌ UNVERIFIED — ") + o.Message)
	default:
		m.println(duck.Warns.Render("  ⚠ "+o.State+" — ") + o.Message)
	}
	if o.Resolution != "" {
		line := "    " + duck.Key.Render("resolution ") + o.Resolution
		if o.Winner != "" {
			line += "  " + duck.Key.Render("winner ") + o.Winner
		}
		m.println(line)
	}
	if o.Branch != "" {
		m.println("    " + duck.Key.Render("branch ") + o.Branch)
	}
	m.println(duck.Dim.Render("    /show for detail · /accept to merge · /reject to discard"))
}

func (m *chatModel) show() {
	if m.current == "" {
		m.println(duck.Dim.Render("  no run yet."))
		return
	}
	for _, l := range strings.Split(runReport(m.repo, m.current), "\n") {
		m.println(l)
	}
}

func (m *chatModel) diff() {
	if m.current == "" {
		m.println(duck.Dim.Render("  no run yet."))
		return
	}
	for _, l := range strings.Split(runDiff(m.repo, m.current), "\n") {
		m.println(l)
	}
}

// commit snapshots the user's own working-tree changes onto the current branch
// (excluding ducklab's runs/). The escape hatch for keeping manual edits so the
// next task starts from a clean, committed base.
func (m *chatModel) commit(rest []string) {
	dirty, _ := prim.IsDirty(m.repo)
	if !dirty {
		m.println(duck.Dim.Render("  nothing to commit — working tree is clean"))
		return
	}
	msg := "manual changes"
	if len(rest) > 0 {
		msg = strings.Join(rest, " ")
	}
	prim.Git("add -A", m.repo)
	prim.Git("reset -q -- runs .ducklab", m.repo) // never commit ducklab artifacts
	ok, out := prim.Git(fmt.Sprintf("commit -q -m %q", msg), m.repo)
	if !ok { // no user identity configured — fall back to ducklab's
		ok, out = prim.Git(fmt.Sprintf("%s commit -q -m %q", gitIDConst, msg), m.repo)
	}
	if !ok {
		m.println(duck.Bad.Render("  commit failed: " + firstLine(out)))
		return
	}
	m.println(duck.OK.Render("  ✓ committed working-tree changes to " + prim.CurrentBranch(m.repo)))
}

func (m *chatModel) accept() {
	if m.last == nil || (m.last.State != "HUMAN_GATE" && m.last.State != "UNVERIFIED") {
		m.println(duck.Bad.Render("  nothing to accept (need a run at HUMAN_GATE or UNVERIFIED)."))
		return
	}
	base := prim.CurrentBranch(m.repo)
	branch := m.last.Branch
	if ok, out := prim.Git(fmt.Sprintf("%s merge --no-ff -m %q %s", gitIDConst, "ducklab: merge "+m.current, branch), m.repo); !ok {
		m.println(duck.Bad.Render("  merge failed: " + firstLine(out)))
		return
	}
	m.cleanupBranches()
	// Record the accepted goal, and drop its failed-attempt history (solved).
	if r, err := run.Open(filepath.Join(m.repo, "runs", m.current)); err == nil {
		if g, ok := r.Get("requirement"); ok {
			_ = project.AddGoal(m.repo, asString(g))
			_ = project.ClearAttempts(m.repo, asString(g))
		}
	}
	m.println(duck.OK.Render("  ✓ merged " + branch + " → " + base))
	m.last = nil
}

func (m *chatModel) reject() {
	m.cleanupBranches()
	m.println(duck.Warns.Render("  ✗ discarded run branches for " + m.current))
	m.last = nil
}

func (m *chatModel) cleanupBranches() {
	_, out := prim.Git("branch --list ducklab/"+m.current+"/*", m.repo)
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "* "))
		if l != "" {
			prim.Git("branch -D "+l, m.repo)
		}
	}
}

const gitIDConst = "-c user.email=duck@ducklab.local -c user.name=ducklab"

// helpers
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
