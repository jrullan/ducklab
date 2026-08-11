package main

import (
	"fmt"
	"os/exec"
	"reflect"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/desktop"
	"github.com/jrullan/ducklab/internal/engineclt"
)

// Shell is the desktop's attention surface: OS notifications and the window
// title badge.
//
// It exists because the engine has no screen and the webview has no reliable
// reach outside its own window. A run that pauses for a human while the person
// is in their editor — which is the normal case, runs take minutes and one
// person cannot watch a spinner for a living — needs a way to call them back.
// Until this, attention routing was the user's job: the transcript of the
// first real project is screenshot after screenshot of them polling views to
// find out whether anything had happened.
type Shell struct {
	win      application.Window
	notifier *notifications.NotificationService
	// enginePath and conn feed RestartEngine: the binary to start and the
	// engine to stop. conn is updated on every successful restart so a second
	// restart talks to the current engine, not the first one.
	enginePath string
	conn       *engineclt.Client
}

// NotifyFQN is the name the frontend calls Notify by.
func NotifyFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.Notify"
}

// SetBadgeFQN is the name the frontend calls SetBadge by.
func SetBadgeFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.SetBadge"
}

// RestartEngineFQN is the name the frontend calls RestartEngine by.
func RestartEngineFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.RestartEngine"
}

// RestartEngine swaps the running engine for the installed binary and returns
// the new connection details, which the frontend uses to rebuild its client
// and event stream in place — no relaunch, no terminal.
//
// Refused while runs are running or queued (see desktop.Restart). This is the
// dev loop's missing half: make dev-install updates the binaries, this
// button makes the update the engine that answers.
func (s *Shell) RestartEngine() (map[string]string, error) {
	var ctl desktop.EngineControl = s.conn
	info, err := desktop.Restart(ctl, s.enginePath, 15*time.Second)
	if err != nil {
		return nil, err
	}
	s.conn = engineclt.New(info)
	return map[string]string{
		"baseUrl": fmt.Sprintf("http://127.0.0.1:%d", info.Port),
		"token":   info.Token,
		"version": info.Version,
	}, nil
}

// ReconnectEngineFQN is the name the frontend calls ReconnectEngine by.
func ReconnectEngineFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.ReconnectEngine"
}

// ReconnectEngine re-reads the running engine's connection details without
// touching the process.
//
// The case it exists for: the engine was restarted OUTSIDE the app — a
// terminal, an upgrade — so this window's token died with the old process,
// and every view wore a Load Error while a perfectly healthy engine sat one
// file-read away. Reconnect is the remedy that owns nothing: no shutdown, no
// spawn, no environment questions — just adopt the engine that is already
// there.
func (s *Shell) ReconnectEngine() (map[string]string, error) {
	info, err := daemon.WaitReady(3 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("no healthy engine to reconnect to (%v) — start one, or use Restart engine", err)
	}
	s.conn = engineclt.New(info)
	return map[string]string{
		"baseUrl": fmt.Sprintf("http://127.0.0.1:%d", info.Port),
		"token":   info.Token,
		"version": info.Version,
	}, nil
}

// OpenURLFQN is the name the frontend calls OpenURL by.
func OpenURLFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.OpenURL"
}

// OpenURL opens a web URL in the person's browser. The webview swallows
// target=_blank anchors, so "open http://localhost:8000" was a link that did
// nothing — the one link whose whole job is leaving the app.
func (s *Shell) OpenURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing to open non-http url %q", url)
	}
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// Notify shows one OS notification. The frontend decides WHEN — it is the
// side that sees state transitions — and this side only delivers.
//
// Failures are returned but the caller is expected to swallow them: a missing
// notification daemon must degrade to the title badge, never to an error a
// person has to dismiss.
func (s *Shell) Notify(id, title, body string) error {
	if s.notifier == nil {
		return fmt.Errorf("no notifier")
	}
	return s.notifier.SendNotification(notifications.NotificationOptions{
		ID: id, Title: title, Body: body,
	})
}

// SetBadge puts the waiting count in the window title, so it survives the app
// sitting in another workspace: "ducklab ● 2" in a task switcher is the whole
// point.
func (s *Shell) SetBadge(count int) error {
	if s.win == nil {
		return fmt.Errorf("no window yet")
	}
	if count <= 0 {
		s.win.SetTitle("ducklab")
	} else {
		s.win.SetTitle(fmt.Sprintf("ducklab ● %d", count))
	}
	return nil
}
