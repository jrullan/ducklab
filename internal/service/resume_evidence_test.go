package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/runlog"
)

// B-509, as r-20261005-110414-sumi ran: three feedback renders (88.5%,
// 40.4%, 38.6%), a pause, a resume, and a fourth render that was written as
// feedback-01 over the first one's capture and diff. Here the resumed run's
// taskVision is a new one reading the record back, as after an engine
// restart, and its render must be feedback-04; every event names files that
// still hold the render it describes.
func TestB509FeedbackNumberingContinuesAcrossResume(t *testing.T) {
	_, v, dir, _ := visionFixture(t, true, true)
	writer, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-b509-resume", ProjectID: "p", Stage: "build"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	emit := func(kind string, data map[string]interface{}) { writer.AppendEvent(kind, data) }
	v.emit = emit
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	shade := 0
	render := func(context.Context) (*runlog.VisualGate, []string, error) {
		// Every render a different picture, so an overwrite is visible.
		shade++
		c := color.RGBA{R: uint8(40 + 50*shade), G: 40, B: 40, A: 255}
		if err := writer.WriteCapture("calculator.png", solidPNG(t, 8, 16, c)); err != nil {
			return nil, nil, err
		}
		return runVisualGate(dir, contract, writer, []string{"calculator.png"}), []string{"calculator.png"}, nil
	}
	tree := 0
	treeOf := func() (string, error) { return fmt.Sprint("tree ", tree), nil }
	v.withFeedback(writer.RunDir(), treeOf, render)
	for i := 0; i < 3; i++ {
		tree++
		v.measure(context.Background(), "before review", i+1)
	}
	digests := feedbackDigests(t, writer.RunDir())

	// The resume: a new process, a new taskVision, the tree moved.
	resumed := (&taskVision{svc: v.svc, roles: v.roles, cited: v.cited, refs: v.refs, emit: emit}).
		withFeedback(writer.RunDir(), treeOf, render)
	tree++
	resumed.measure(context.Background(), "before review", 3)

	events, err := runlog.ReadEvents(writer.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	feedback := eventsOf(events, "visual_feedback")
	if len(feedback) != 4 {
		t.Fatalf("visual_feedback events = %d, want 4", len(feedback))
	}
	owner := map[string]int{}
	for i, e := range feedback {
		if got := intValue(e.Data["feedback"]); got != i+1 {
			t.Errorf("event %d is feedback %d, want %d", i, got, i+1)
		}
		files, _ := e.Data["files"].([]interface{})
		if len(files) != 2 {
			t.Fatalf("event %d names %v, want its capture and its diff", i, files)
		}
		for _, f := range files {
			name := f.(string)
			if prev, dup := owner[name]; dup {
				t.Errorf("%s is named by feedback %d and %d", name, prev+1, i+1)
			}
			owner[name] = i
			if want := fmt.Sprintf("captures/feedback-%02d-", i+1); name[:len(want)] != want {
				t.Errorf("event %d names %s", i, name)
			}
			if _, err := os.Stat(filepath.Join(writer.RunDir(), filepath.FromSlash(name))); err != nil {
				t.Errorf("event %d names a missing file: %v", i, err)
			}
		}
		results, _ := e.Data["results"].([]interface{})
		diff := stringValueAny(results[0].(map[string]interface{})["diff_capture"])
		if want := fmt.Sprintf("feedback-%02d-visual-01-diff-calculator.png", i+1); diff != want {
			t.Errorf("event %d result names diff %q, want %q", i, diff, want)
		}
	}
	// The first three renders' evidence is byte-for-byte what it was.
	after := feedbackDigests(t, writer.RunDir())
	for name, sum := range digests {
		if after[name] != sum {
			t.Errorf("%s was overwritten by the resumed render", name)
		}
	}
	if len(after) != len(digests)+2 {
		t.Errorf("files after resume = %d, want %d", len(after), len(digests)+2)
	}

	// Evidence removed from disk (a person cleaning up captures) does not
	// free its number: the record still names it, so the next resume's
	// render is feedback-05, not a second feedback-01.
	for name := range after {
		if err := os.Remove(filepath.Join(writer.RunDir(), "captures", name)); err != nil {
			t.Fatal(err)
		}
	}
	again := (&taskVision{svc: v.svc, roles: v.roles, cited: v.cited, refs: v.refs, emit: emit}).
		withFeedback(writer.RunDir(), treeOf, render)
	tree++
	again.measure(context.Background(), "before review", 4)
	if _, err := os.Stat(filepath.Join(writer.RunDir(), "captures", "feedback-05-calculator.png")); err != nil {
		t.Errorf("the render after the files were removed is not feedback-05: %v", err)
	}
}

// Files the record never named — a render renamed before the engine died,
// its event never written — still hold their numbers: the next render does
// not overwrite them.
func TestB509FeedbackOnDiskWithoutAnEventKeepsItsNumber(t *testing.T) {
	_, v, dir, _ := visionFixture(t, true, true)
	writer, err := runlog.NewWriter(dir, &runlog.Run{ID: "r-b509-orphan", ProjectID: "p", Stage: "build"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	orphan := solidPNG(t, 8, 16, color.Black)
	if err := writer.WriteCapture("feedback-07-calculator.png", orphan); err != nil {
		t.Fatal(err)
	}
	contract := config.RenderContract{Compare: []config.RenderCompare{{Capture: "calculator.png", Reference: visionRefID}}}
	v.withFeedback(writer.RunDir(), func() (string, error) { return "tree", nil }, func(context.Context) (*runlog.VisualGate, []string, error) {
		if err := writer.WriteCapture("calculator.png", solidPNG(t, 8, 16, color.White)); err != nil {
			return nil, nil, err
		}
		return runVisualGate(dir, contract, writer, []string{"calculator.png"}), []string{"calculator.png"}, nil
	})
	v.measure(context.Background(), "before implementer turn", 1)
	if _, err := os.Stat(filepath.Join(writer.RunDir(), "captures", "feedback-08-calculator.png")); err != nil {
		t.Errorf("the render is not feedback-08: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(writer.RunDir(), "captures", "feedback-07-calculator.png")); sha256.Sum256(data) != sha256.Sum256(orphan) {
		t.Errorf("feedback-07 was overwritten")
	}
}

// The next number skips gaps and counts both the files on disk and what the
// record names: deleted evidence never has its number reused.
func TestB509NextFeedbackSeqIsRobustToGaps(t *testing.T) {
	dir := t.TempDir()
	if got := nextFeedbackSeq(filepath.Join(dir, "missing"), 0); got != 1 {
		t.Errorf("empty run: %d, want 1", got)
	}
	for _, name := range []string{"feedback-01-calculator.png", "feedback-05-visual-01-diff-calculator.png", "calculator.png", "visual-09-diff-calculator.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := nextFeedbackSeq(dir, 0); got != 6 {
		t.Errorf("files 01 and 05: %d, want 6", got)
	}
	if got := nextFeedbackSeq(dir, 7); got != 8 {
		t.Errorf("record at 7: %d, want 8", got)
	}
	events := []*runlog.Event{
		{Type: "visual_feedback", Data: map[string]interface{}{"ok": true,
			"shown": []interface{}{map[string]interface{}{"file": "captures/feedback-11-calculator.png"}}}},
		{Type: "visual_feedback", Data: map[string]interface{}{"ok": true,
			"results": []interface{}{map[string]interface{}{"diff_capture": "feedback-12-visual-01-diff-calculator.png"}}}},
		{Type: "visual_feedback", Data: map[string]interface{}{"ok": false, "feedback": 3}},
		{Type: "visual_compare", Data: map[string]interface{}{"feedback": 99}},
	}
	if got := recordedFeedbackSeq(events); got != 12 {
		t.Errorf("recorded = %d, want 12 (pre-B-509 events name files only)", got)
	}
	events = append(events, &runlog.Event{Type: "visual_feedback", Data: map[string]interface{}{"files": []interface{}{"captures/feedback-14-calculator.png"}}})
	if got := recordedFeedbackSeq(events); got != 14 {
		t.Errorf("recorded = %d, want 14 from files", got)
	}
	// A name already taken is never replaced.
	from := filepath.Join(dir, "calculator.png")
	to := filepath.Join(dir, "feedback-01-calculator.png")
	if err := renameFresh(from, to); err == nil {
		t.Errorf("renameFresh replaced an existing file")
	}
	if data, _ := os.ReadFile(to); string(data) != "x" {
		t.Errorf("existing evidence changed")
	}
}

func feedbackDigests(t *testing.T, runDir string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(runDir, "captures"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][32]byte{}
	for _, e := range entries {
		if feedbackSeqOf(e.Name()) == 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(runDir, "captures", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = sha256.Sum256(data)
	}
	return out
}
