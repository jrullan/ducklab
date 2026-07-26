package xplat

import (
	"context"
	"strings"
	"testing"
)

func TestNotifyRequiresATitle(t *testing.T) {
	if err := Notify(context.Background(), Notification{Body: "x"}); err == nil {
		t.Error("a notification with no title was accepted")
	}
}

// A machine with no notification daemon is a normal machine; the error is for
// logging, and a run must never fail because a toast could not be shown.
func TestNotifyFailureIsReportedNotFatal(t *testing.T) {
	err := Notify(context.Background(), Notification{Title: "t", Body: "b"})
	_ = err // may succeed or fail depending on the host; must not panic
}

func TestNotificationsAvailableDoesNotPanic(t *testing.T) {
	_ = NotificationsAvailable()
}

func TestRunFinishedDistinguishesOutcomes(t *testing.T) {
	passed := RunFinished("T-001", "PASSED")
	if passed.Urgency != UrgencyNormal || !strings.Contains(passed.Title, "passed") {
		t.Errorf("passed = %+v", passed)
	}

	// UNVERIFIED must not read as success: nothing was executed.
	unv := RunFinished("T-001", "UNVERIFIED")
	if strings.Contains(unv.Title, "passed") {
		t.Errorf("unverified notification reads as a pass: %+v", unv)
	}
	if !strings.Contains(unv.Body, "nothing executable") {
		t.Errorf("unverified notification does not say why: %+v", unv)
	}

	failed := RunFinished("T-001", "FAILED")
	if failed.Urgency != UrgencyCritical {
		t.Errorf("a failure should be critical: %+v", failed)
	}
}

// Nothing progresses until someone answers, so this one interrupts.
func TestHumanNeededIsCritical(t *testing.T) {
	n := HumanNeeded("T-001", "gate")
	if n.Urgency != UrgencyCritical {
		t.Errorf("urgency = %q, want critical", n.Urgency)
	}
	if !strings.Contains(n.Body, "T-001") || !strings.Contains(n.Body, "gate") {
		t.Errorf("body does not say what is waiting: %+v", n)
	}
}
