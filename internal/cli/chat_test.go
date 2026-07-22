package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// tail returns the last appended scrollback line.
func tail(m *chatModel) string {
	if len(m.lines) == 0 {
		return ""
	}
	return m.lines[len(m.lines)-1]
}

func TestChatBannerAndConfig(t *testing.T) {
	m := newChatModel("/tmp/x", "go test ./...")
	joined := strings.Join(m.lines, "\n")
	if !strings.Contains(joined, "ducklab") {
		t.Error("banner missing wordmark")
	}
	if !strings.Contains(m.configLine(), "mode=driver") {
		t.Errorf("default config wrong: %s", m.configLine())
	}
}

func TestChatCommands(t *testing.T) {
	m := newChatModel("/tmp/x", "")

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

	// /judge, /repo, /tests
	m.handle("/judge aitopatom")
	m.handle("/tests pytest -q")
	if m.judge != "aitopatom" || m.tests != "pytest -q" {
		t.Errorf("judge=%q tests=%q", m.judge, m.tests)
	}

	// unknown command is flagged
	before := len(m.lines)
	m.handle("/frobnicate")
	if len(m.lines) == before || !strings.Contains(tail(m), "unknown command") {
		t.Errorf("unknown command not reported: %q", tail(m))
	}

	// /run with no goal after clearing → guarded, not a crash
	m.goal = ""
	m.handle("/run")
	if !strings.Contains(tail(m), "no goal") {
		t.Errorf("empty-goal run not guarded: %q", tail(m))
	}
}

func TestChatQuit(t *testing.T) {
	m := newChatModel("/tmp/x", "")
	_, cmd := m.handle("/exit")
	if cmd == nil {
		t.Fatal("/exit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("/exit did not produce tea.Quit")
	}
}

func TestChatRenders(t *testing.T) {
	m := newChatModel("/tmp/x", "")
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
