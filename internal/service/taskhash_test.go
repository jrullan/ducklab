package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
)

func planWithTask(t *testing.T, id, body string) *artifact.Document {
	t.Helper()
	return &artifact.Document{Sections: []artifact.Section{{
		ID: "M-01", Title: "Milestone",
		Children: []artifact.Section{{ID: id, Title: "Task", Body: body}},
	}}}
}

// Accepting a spec-alignment that added ONE **Implements:** line to 64 tasks
// orphaned every accepted run and the whole board reappeared in todo (B-093).
// The hash covers the task's substance: annotating traceability keeps the
// history; rewriting the work does not.
// A plan gate must expose the board-side consequence of changing a task that
// already has accepted evidence. The same revision boundary that makes the
// task todo after promotion must be visible before a person accepts it.
func TestPlanProposalWarnsWhenItWouldOrphanAcceptedTaskHistory(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno", "pato-dos")
	oldBody := "**Implements:** SPEC-001\n\nKeep a durable audit record."
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindPlan: "## M-001 — Core\n\n### T-001 — Audit\n\n" + oldBody + "\n",
	})

	accepted := &runlog.Run{
		ID: "r-accepted", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true,
		TaskBodyHash: taskBodyHash(oldBody), StartedAt: "2026-08-20T00:00:00Z",
	}
	w, err := runlog.NewWriter(dir, accepted)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	architect, err := s.ducklings.Get("pato-uno")
	if err != nil {
		t.Fatal(err)
	}
	architect.Caps.ContextTokens = 32_000 // use the section-wise task-edit path

	fake, ok := s.providers["fake"].(*provider.Fake)
	if !ok {
		t.Fatal("test service did not install the fake provider")
	}
	propose := func(body string) *runlog.Run {
		fake.ScriptFunc = func(req provider.ChatRequest, _ int) *provider.ChatResponse {
			text := "T-001\n"
			for _, message := range req.Messages {
				if strings.Contains(message.Content, "Update ONE section") {
					text = "## T-001 — Audit\n\n" + body + "\n"
					break
				}
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{
				Message: provider.Message{Role: "assistant", Content: text},
				FinishReason: provider.FinishStop,
			}}}
		}
		run, err := s.StageStart(context.Background(), id, StageRequest{Stage: "plan", Mode: "solo", Revise: "align task wording"})
		if err != nil {
			t.Fatal(err)
		}
		s.runsMu.RLock()
		rs := s.runs[run.ID]
		s.runsMu.RUnlock()
		<-rs.done
		detail, err := s.RunGet(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Run.Status != "paused" || detail.Run.PendingKind != "gate" {
			t.Fatalf("proposal was blocked instead of left for acceptance: %s/%s: %s", detail.Run.Status, detail.Run.PendingKind, detail.Run.Failure)
		}
		if err := s.ArtifactDiscard(context.Background(), id, "plan"); err != nil {
			t.Fatal(err)
		}
		return detail.Run
	}

	rewritten := propose("**Implements:** SPEC-001\n\nReplace the audit record with a destructive reset.")
	if !strings.Contains(rewritten.Warning, "1") ||
		!strings.Contains(strings.ToLower(rewritten.Warning), "accepted history") ||
		!strings.Contains(strings.ToLower(rewritten.Warning), "stop counting") {
		t.Errorf("rewrite warning = %q; want count and accepted-history consequence", rewritten.Warning)
	}

	traceabilityOnly := propose("**Implements:** SPEC-002\n\nKeep a durable audit record.")
	if traceabilityOnly.Warning != "" {
		t.Errorf("Implements-only proposal warned as a body rewrite: %q", traceabilityOnly.Warning)
	}
}

func TestTraceabilityEditsKeepTaskHistory(t *testing.T) {
	oldBody := "Fixes B-061.\n\n## Reported\n\nThe gate died on a missing node_modules."
	newBody := "**Implements:** SPEC-060\n\n" + oldBody
	rewritten := "A genuinely different assignment."

	// The accepted run recorded the RAW hash of the body as it stood then —
	// the pre-fix spelling.
	accepted := &runlog.Run{TaskID: "T-071", TaskBodyHash: rawTaskBodyHash(oldBody), StartedAt: "2026-08-10T00:00:00Z"}

	hashes := taskBodyHashes(planWithTask(t, "T-071", newBody))
	if got := runsForCurrentTaskBodies([]*runlog.Run{accepted}, hashes); len(got) != 1 {
		t.Fatal("an Implements-only edit orphaned the task's accepted run")
	}

	hashes = taskBodyHashes(planWithTask(t, "T-071", rewritten))
	if got := runsForCurrentTaskBodies([]*runlog.Run{accepted}, hashes); len(got) != 0 {
		t.Fatal("a substance rewrite kept history that belongs to the old assignment")
	}

	// A run recorded AFTER the fix stores the normalized hash and matches
	// the same task with or without its Implements line.
	fresh := &runlog.Run{TaskID: "T-071", TaskBodyHash: taskBodyHash(newBody), StartedAt: "2026-08-19T00:00:00Z"}
	if got := runsForCurrentTaskBodies([]*runlog.Run{fresh}, taskBodyHashes(planWithTask(t, "T-071", oldBody))); len(got) != 1 {
		t.Fatal("a normalized record did not survive removing the Implements line")
	}
}
