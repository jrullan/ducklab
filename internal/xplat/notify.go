package xplat

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// Urgency of a notification. Only two levels, because a scale nobody can
// distinguish is a scale nobody reads.
type Urgency string

const (
	UrgencyNormal   Urgency = "normal"
	UrgencyCritical Urgency = "critical"
)

// Notification is an OS-level message.
type Notification struct {
	Title   string
	Body    string
	Urgency Urgency
}

// Notify sends an OS notification.
//
// Best-effort by design: a machine with no notification daemon is a normal
// machine, and a run must never fail because a toast could not be shown. The
// error is returned for logging, never for control flow.
func Notify(ctx context.Context, n Notification) error {
	if n.Title == "" {
		return fmt.Errorf("notification needs a title")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch runtime.GOOS {
	case "linux":
		args := []string{n.Title}
		if n.Body != "" {
			args = append(args, n.Body)
		}
		if n.Urgency == UrgencyCritical {
			args = append([]string{"-u", "critical"}, args...)
		}
		return runIfPresent(ctx, "notify-send", args...)

	case "darwin":
		script := fmt.Sprintf(
			`display notification %q with title %q`, n.Body, n.Title)
		return runIfPresent(ctx, "osascript", "-e", script)

	case "windows":
		// PowerShell toast without extra dependencies.
		ps := fmt.Sprintf(
			`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null;`+
				`$t=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent(2);`+
				`$t.GetElementsByTagName('text')[0].AppendChild($t.CreateTextNode(%q)) > $null;`+
				`$t.GetElementsByTagName('text')[1].AppendChild($t.CreateTextNode(%q)) > $null;`+
				`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('ducklab').Show(`+
				`[Windows.UI.Notifications.ToastNotification]::new($t))`,
			n.Title, n.Body)
		return runIfPresent(ctx, "powershell", "-NoProfile", "-Command", ps)
	}
	return fmt.Errorf("notifications unsupported on %s", runtime.GOOS)
}

// NotificationsAvailable reports whether an OS notifier exists, so a UI can
// hide a setting that would do nothing.
func NotificationsAvailable() bool {
	switch runtime.GOOS {
	case "linux":
		return hasBinary("notify-send")
	case "darwin":
		return hasBinary("osascript")
	case "windows":
		return hasBinary("powershell")
	}
	return false
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runIfPresent(ctx context.Context, name string, args ...string) error {
	if !hasBinary(name) {
		return fmt.Errorf("%s not found", name)
	}
	return exec.CommandContext(ctx, name, args...).Run()
}

// RunFinished builds the notification for a completed run. Only two outcomes
// are urgent enough to interrupt: a failure, and something waiting on a person.
func RunFinished(taskID, verdict string) Notification {
	switch verdict {
	case "PASSED":
		return Notification{Title: "ducklab: run passed", Body: taskID, Urgency: UrgencyNormal}
	case "UNVERIFIED":
		return Notification{
			Title:   "ducklab: run finished unverified",
			Body:    taskID + " — nothing executable ran",
			Urgency: UrgencyNormal,
		}
	default:
		return Notification{
			Title:   "ducklab: run failed",
			Body:    fmt.Sprintf("%s — %s", taskID, verdict),
			Urgency: UrgencyCritical,
		}
	}
}

// HumanNeeded builds the notification for a run waiting on a person. This one
// is critical: nothing progresses until someone answers.
func HumanNeeded(taskID, kind string) Notification {
	return Notification{
		Title:   "ducklab needs you",
		Body:    fmt.Sprintf("%s is waiting (%s)", taskID, kind),
		Urgency: UrgencyCritical,
	}
}
