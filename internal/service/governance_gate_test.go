package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/runlog"
)

const desktopSourceOnlyDiff = `diff --git a/frontend/src/components/RunLauncher.tsx b/frontend/src/components/RunLauncher.tsx
index 1111111..2222222 100644
--- a/frontend/src/components/RunLauncher.tsx
+++ b/frontend/src/components/RunLauncher.tsx
@@ -1 +1 @@
-old
+new
`

func TestDesktopStaleDetectsUnrebuiltFrontend(t *testing.T) {
	if !desktopStale(desktopSourceOnlyDiff) {
		t.Fatal("frontend source-only diff did not mark desktop stale")
	}
	withBundle := desktopSourceOnlyDiff + `diff --git a/cmd/ducklab-desktop/frontend/dist/app.js b/cmd/ducklab-desktop/frontend/dist/app.js
index 1111111..2222222 100644
--- a/cmd/ducklab-desktop/frontend/dist/app.js
+++ b/cmd/ducklab-desktop/frontend/dist/app.js
@@ -1 +1 @@
-old
+new
`
	if desktopStale(withBundle) {
		t.Fatal("frontend source diff with rebuilt bundle marked desktop stale")
	}
	if desktopStale(`diff --git a/internal/service/service.go b/internal/service/service.go
index 1111111..2222222 100644
--- a/internal/service/service.go
+++ b/internal/service/service.go
`) {
		t.Fatal("unrelated diff marked desktop stale")
	}
}

const autonomyFlipDiff = `diff --git a/.ducklab/project.toml b/.ducklab/project.toml
index 1111111..2222222 100644
--- a/.ducklab/project.toml
+++ b/.ducklab/project.toml
@@ -1 +1 @@
-autonomy = "guarded"
+autonomy = "yolo"
`

// A governance edit must be spoken before a reviewer decides, rather than
// buried in the diff. The same callout is the data the human-gate surface
// carries; it names the setting and both values so the person need not parse
// TOML or hunt through a large diff.
func TestDesktopStaleReachesReviewerAndHumanGatePayload(t *testing.T) {
	payload := reviewPrompt("T-123", desktopSourceOnlyDiff)
	if !strings.Contains(payload, desktopStaleMessage) {
		t.Errorf("reviewer payload omitted desktop warning %q:\n%s", desktopStaleMessage, payload)
	}
	const recovery = "run make desktop, or cut a release"
	if !strings.Contains(payload, recovery) {
		t.Errorf("reviewer payload omitted stale-bundle recovery %q:\n%s", recovery, payload)
	}
	// PendingData and human_needed use this same plain-words value when the
	// paused-gate branch observes DesktopStale.
	run := &runlog.Run{DesktopStale: true, PendingData: map[string]interface{}{"desktop_stale": desktopStaleMessage}}
	gateData := map[string]interface{}{"desktop_stale": run.PendingData["desktop_stale"]}
	if gateData["desktop_stale"] != desktopStaleMessage {
		t.Fatalf("human_needed gate data = %#v", gateData)
	}
	if warning, _ := gateData["desktop_stale"].(string); !strings.Contains(warning, recovery) {
		t.Errorf("human_needed gate data omitted stale-bundle recovery %q: %#v", recovery, gateData)
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"desktop_stale":true`) {
		t.Fatalf("run payload = %s; want desktop_stale", encoded)
	}
}

func TestAutonomyFlipIsCalledOutInReviewerAndHumanGatePayload(t *testing.T) {
	const callout = "this diff changes project autonomy from guarded to yolo"
	payload := reviewPrompt("T-116", autonomyFlipDiff)
	if !strings.Contains(payload, callout) {
		t.Errorf("reviewer payload omitted governance callout %q:\n%s", callout, payload)
	}

	// The paused run is the human-gate payload. Keep this reflective until the
	// field is introduced: it makes the behavioral requirement red today without
	// coupling the test to an analyzer helper or its placement.
	run := &runlog.Run{}
	field := reflect.ValueOf(run).Elem().FieldByName("GovernanceModified")
	if !field.IsValid() {
		t.Error("run record has no GovernanceModified field for the human gate")
		return
	}
	if field.Kind() != reflect.Bool {
		t.Fatalf("GovernanceModified kind = %s, want bool", field.Kind())
	}
	field.SetBool(true)
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"governance_modified":true`) {
		t.Fatalf("human-gate run payload = %s; want governance_modified", encoded)
	}
}
