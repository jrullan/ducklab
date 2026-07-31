package main

import (
	"fmt"
	"reflect"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
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
}

// NotifyFQN is the name the frontend calls Notify by.
func NotifyFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.Notify"
}

// SetBadgeFQN is the name the frontend calls SetBadge by.
func SetBadgeFQN() string {
	return reflect.TypeOf(Shell{}).PkgPath() + ".Shell.SetBadge"
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
