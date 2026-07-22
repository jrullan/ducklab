package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duck"
	"github.com/jrullan/ducklab/internal/prim"
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
	return fmt.Sprintf("mode=%s a=%s b=%s judge=%s gate=%s",
		m.mode, m.a, m.b, m.judge, m.gate.Label())
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
		if msg.src != "" {
			m.println(duck.Dim.Render("    · " + msg.stage + " — " + msg.src))
		} else {
			m.println(duck.Dim.Render("    · " + msg.stage))
		}
		m.refresh()
		return m, m.waitFor()

	case doneMsg:
		m.running = false
		if msg.err != nil {
			m.println(duck.Bad.Render("  ✗ " + msg.err.Error()))
		} else {
			m.last = &msg.outcome
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

func (m *chatModel) View() string {
	if !m.ready {
		return "starting ducklab…"
	}
	bar := duck.Dim.Render(strings.Repeat("─", max(m.vp.Width, 1)))
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), bar, m.ti.View())
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
		"  " + duck.Key.Render("/mode") + " tournament|driver|solo   collaboration recipe",
		"  " + duck.Key.Render("/models") + " [A <s> B <s>]           list or assign contestants",
		"  " + duck.Key.Render("/judge") + " <src>                    assign judge/observer",
		"  " + duck.Key.Render("/repo") + " <path>   " + duck.Key.Render("/verify") + " <cmd|auto|none>   repo & verification gate",
		"  " + duck.Key.Render("/goal") + " <text>  (or just type)     set the task",
		"  " + duck.Key.Render("/run") + "                             launch the run",
		"  " + duck.Key.Render("/show") + "  " + duck.Key.Render("/diff") + "   inspect result (summary · full patch)",
		"  " + duck.Key.Render("/accept") + "   " + duck.Key.Render("/reject") + "                     merge or discard",
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

	env := strategy.Env{
		Ctx: context.Background(), TaskID: taskID, Requirement: m.goal,
		Repo: m.repo, Gate: m.gate,
		Contestants: []source.Client{a, b}, Judge: j, Run: r,
		OnStage: func(stage, src string) { m.sub <- stageMsg{stage, src} },
	}
	m.running = true
	m.quip++
	m.println(duck.Quack.Render(fmt.Sprintf("  quack — %s on [%s] · %s", m.mode, taskID, duck.Quip(m.quip))))
	m.refresh()
	go func() {
		out, err := strat.Run(env)
		m.sub <- doneMsg{out, err}
	}()
	return m, m.waitFor()
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
