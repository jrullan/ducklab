package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrullan/ducklab/internal/prim"
)

// tail returns the last appended scrollback line.
func tail(m *chatModel) string {
	if len(m.lines) == 0 {
		return ""
	}
	return m.lines[len(m.lines)-1]
}

func TestChatBannerAndConfig(t *testing.T) {
	m := newChatModel("/tmp/x", prim.GateFromCmd("go test ./..."))
	joined := strings.Join(m.lines, "\n")
	if !strings.Contains(joined, "ducklab") {
		t.Error("banner missing wordmark")
	}
	if !strings.Contains(m.configLine(), "mode=driver") {
		t.Errorf("default config wrong: %s", m.configLine())
	}
}

func TestChatCommands(t *testing.T) {
	m := newChatModel("/tmp/x", prim.Gate{Kind: "none"})

	// free text becomes the goal
	m.handle("fix the parser bug")
	if m.goal != "fix the parser bug" {
		t.Errorf("goal = %q", m.goal)
	}

	// /mode switches recipe and confirms
	m.handle("/mode solo")
	if m.mode != "solo" || !strings.Contains(tail(m), "solo") {
		t.Errorf("mode not set: mode=%q last=%q", m.mode, tail(m))
	}

	// unknown mode is rejected, mode unchanged
	m.handle("/mode nope")
	if m.mode != "solo" {
		t.Errorf("bad mode accepted: %q", m.mode)
	}

	// /models A .. B .. assigns contestants
	m.handle("/models A beelink B aitopatom")
	if m.a != "beelink" || m.b != "aitopatom" {
		t.Errorf("contestants = %q/%q", m.a, m.b)
	}

	// /judge and /verify
	m.handle("/judge aitopatom")
	m.handle("/verify pytest -q")
	if m.judge != "aitopatom" || m.gate.Cmd != "pytest -q" || m.gate.Kind != "custom" {
		t.Errorf("judge=%q gate=%+v", m.judge, m.gate)
	}
	// /verify none → explicit unverified
	m.handle("/verify none")
	if m.gate.Active() {
		t.Errorf("/verify none should disable the gate, got %+v", m.gate)
	}

	// unknown command is flagged
	before := len(m.lines)
	m.handle("/frobnicate")
	if len(m.lines) == before || !strings.Contains(tail(m), "unknown command") {
		t.Errorf("unknown command not reported: %q", tail(m))
	}

	// /diff and /show are known commands (referenced in hints) — must not be
	// reported as unknown even before a run exists.
	for _, c := range []string{"/diff", "/show"} {
		m.handle(c)
		if strings.Contains(tail(m), "unknown command") {
			t.Errorf("%s reported as unknown", c)
		}
	}

	// /run with no goal → guarded, not a crash (and no launch)
	m.goal = ""
	m.handle("/run")
	if !strings.Contains(tail(m), "no goal") {
		t.Errorf("empty-goal run not guarded: %q", tail(m))
	}
}

func TestChatGateLabel(t *testing.T) {
	// no gate → config reflects unverified; with a command → shows it
	m := newChatModel("/tmp/x", prim.Gate{Kind: "none"})
	if !strings.Contains(m.configLine(), "unverified") {
		t.Errorf("no-gate config should say unverified: %s", m.configLine())
	}
	m.handle("/verify go build ./...")
	if !strings.Contains(m.configLine(), "go build") {
		t.Errorf("gate not reflected in config: %s", m.configLine())
	}
}

func TestChatQuit(t *testing.T) {
	m := newChatModel("/tmp/x", prim.Gate{Kind: "none"})
	_, cmd := m.handle("/exit")
	if cmd == nil {
		t.Fatal("/exit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("/exit did not produce tea.Quit")
	}
}

func TestChatRenders(t *testing.T) {
	m := newChatModel("/tmp/x", prim.Gate{Kind: "none"})
	// simulate the terminal reporting its size — makes the viewport ready
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.View()
	if !strings.Contains(view, "ducklab") {
		t.Error("rendered view missing banner")
	}
	if !strings.Contains(view, "❯") {
		t.Error("rendered view missing input prompt")
	}
}
