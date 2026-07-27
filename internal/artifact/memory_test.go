package artifact

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// AC-42: project.md after 200 accepted tasks is <= 8192 bytes and says how
// many were folded.
func TestMemoryStaysUnderTheCapAfterManyTasks(t *testing.T) {
	m := &Memory{
		Description: "A timesheet and invoicing product for small firms in Puerto Rico.",
		Conventions: "Spanish UI strings live in i18n/es.json. Never edit generated code.",
	}
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 200; i++ {
		m.RecordAccepted(
			fmt.Sprintf("T-%03d", i),
			"Implement a reasonably descriptive piece of work here",
			fmt.Sprintf("r-2026%04d-120000-ab%02d", i, i%100),
			when.AddDate(0, 0, i),
		)
	}

	rendered := RenderMemory(m)
	if len(rendered) > MaxMemoryBytes {
		t.Errorf("project.md is %d bytes, cap is %d", len(rendered), MaxMemoryBytes)
	}
	if m.Folded == 0 {
		t.Error("200 tasks fit without folding; the cap is not being applied")
	}
	if !strings.Contains(rendered, "- … and ") {
		t.Errorf("no folding line:\n%s", rendered[:200])
	}
	// The description and conventions must survive: they are the part a
	// follow-up task actually needs.
	if !strings.Contains(rendered, "Puerto Rico") || !strings.Contains(rendered, "i18n/es.json") {
		t.Error("folding discarded the description or conventions")
	}
}

// Recent work is what a follow-up needs; the oldest entries go first.
func TestFoldingDropsOldestFirst(t *testing.T) {
	m := &Memory{Description: strings.Repeat("x", 7000)}
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 40; i++ {
		m.RecordAccepted(fmt.Sprintf("T-%03d", i), "work", "r-x", when)
	}
	rendered := RenderMemory(m)
	if strings.Contains(rendered, "T-001") {
		t.Error("the oldest entry survived while newer ones were folded")
	}
	if !strings.Contains(rendered, "T-040") {
		t.Error("the newest entry was folded")
	}
}

// The count preserves that earlier work existed rather than pretending the
// project started recently.
func TestFoldedCountAccumulatesAcrossReloads(t *testing.T) {
	m := &Memory{Description: strings.Repeat("y", 7500)}
	when := time.Now()
	for i := 0; i < 30; i++ {
		m.RecordAccepted("T-001", "work", "r", when)
	}
	first := m.Folded
	if first == 0 {
		t.Fatal("nothing folded")
	}

	reloaded := ParseMemory(RenderMemory(m))
	if reloaded.Folded != first {
		t.Errorf("folded count lost on reload: %d then %d", first, reloaded.Folded)
	}

	for i := 0; i < 10; i++ {
		reloaded.RecordAccepted("T-002", "more", "r", when)
	}
	if reloaded.Folded <= first {
		t.Error("the count did not accumulate")
	}
}

func TestMemoryRoundTrip(t *testing.T) {
	m := &Memory{Description: "Build a thing.", Conventions: "Be terse."}
	m.RecordAccepted("T-001", "First task", "r-1", time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))

	back := ParseMemory(RenderMemory(m))
	if back.Description != m.Description || back.Conventions != m.Conventions {
		t.Errorf("round trip lost prose: %+v", back)
	}
	if len(back.Accepted) != 1 || !strings.Contains(back.Accepted[0], "T-001") {
		t.Errorf("round trip lost the log: %v", back.Accepted)
	}
}

// An empty scaffold in every prompt is noise a model reads past on every call.
func TestEmptyMemoryInjectsNothing(t *testing.T) {
	if got := (&Memory{}).PromptContext(); got != "" {
		t.Errorf("empty memory injected %q", got)
	}
}

func TestPromptContextIncludesWhatMatters(t *testing.T) {
	m := &Memory{Description: "A billing product.", Conventions: "No new deps."}
	m.RecordAccepted("T-007", "Add invoices", "r-7", time.Now())
	got := m.PromptContext()
	for _, want := range []string{"A billing product.", "No new deps.", "T-007"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt context missing %q:\n%s", want, got)
		}
	}
}

func TestPromptContextReportsOmittedCount(t *testing.T) {
	m := &Memory{Description: strings.Repeat("z", 7500)}
	for i := 0; i < 30; i++ {
		m.RecordAccepted("T-001", "work", "r", time.Now())
	}
	if !strings.Contains(m.PromptContext(), "earlier tasks omitted") {
		t.Error("the prompt does not say work was omitted")
	}
}

// A model re-run on the same task repeats the same dead end; naming what was
// tried is the cheapest correction available.
func TestRenderFailedAttempts(t *testing.T) {
	got := RenderFailedAttempts([]FailedAttempt{
		{RunID: "r-1", Mode: "pair", Summary: "changed the handler signature", Gate: "2 tests red (TestLogin: nil pointer at auth.go:88)"},
	})
	for _, want := range []string{"already tried and failed", "r-1", "pair", "handler signature", "auth.go:88"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestNoFailedAttemptsRendersNothing(t *testing.T) {
	if got := RenderFailedAttempts(nil); got != "" {
		t.Errorf("rendered %q for no attempts", got)
	}
}
