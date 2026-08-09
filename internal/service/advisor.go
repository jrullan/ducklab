package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/tools"
)

// The advisor: a second model drafts the answer a paused question waits for.
//
// ask_human assumed the human KNOWS the answer. Many questions — "which
// entrypoint contract should the acceptance test use?" — are technical
// decisions where the person becomes an unwilling researcher, and a fleet of
// models sat idle while one model's question stalled the run on exactly the
// kind of judgement the fleet exists for. The run still pauses and the human
// still decides (I2); the advisor turns the decision from research into
// reading a founded recommendation and clicking once.

// advisorSystemPrompt is decisive on purpose: a recommendation hedged into
// a survey re-creates the research burden it exists to remove.
const advisorSystemPrompt = `You are the advisor duckling in ducklab. Another model paused its run to ask
the human a question. Draft the answer the human should give.

Be decisive and concrete: pick ONE recommendation. Cite the project's own
spec sections and conventions when they decide the matter — the project's
established contracts beat your preferences. If the question offers options,
choose one.

Reply with ONLY the recommended answer text, 2-8 sentences, written as the
reply itself (it will be sent back to the asking model verbatim if the human
accepts it). No preamble, no "I recommend".`

// adviseQuestion runs the advisor asynchronously: the pause must not wait on
// a model call. The recommendation lands on the record as an `advice` event
// and on the pending data, where the question card renders it.
func (s *Service) adviseQuestion(rs *runState, q *tools.PendingQuestion) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		answer, advisor, err := s.advise(ctx, rs, q)
		if err != nil || strings.TrimSpace(answer) == "" {
			// No advice is a degraded question card, not a failure: the
			// person can still answer, exactly as before advisors existed.
			return
		}
		// The run may have moved on while the advisor thought; a
		// recommendation for a question already answered is noise.
		if rs.run.Status != "paused" || rs.run.PendingKind != "question" {
			return
		}
		w, werr := s.ensureWriter(rs)
		if werr != nil {
			return
		}
		rs.wmu.Lock()
		if rs.run.PendingData == nil {
			rs.run.PendingData = map[string]interface{}{}
		}
		rs.run.PendingData["advice"] = answer
		rs.run.PendingData["advisor"] = advisor
		rs.wmu.Unlock()
		w.AppendEvent("advice", map[string]interface{}{
			"question_id": q.ID, "advisor": advisor, "answer": answer,
		})
		_ = w.WriteState()
	}()
}

// advise picks the advisor, assembles the context, and asks once — a one-shot
// chat, no tools: the advisor reasons from the same documents the asker had.
func (s *Service) advise(ctx context.Context, rs *runState, q *tools.PendingQuestion) (string, string, error) {
	advisorID := s.pickAdvisor(rs)
	if advisorID == "" {
		return "", "", fmt.Errorf("no advisor available")
	}
	d, err := s.ducklings.Get(advisorID)
	if err != nil {
		return "", "", err
	}
	p, err := s.ducklings.Provider(advisorID)
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	if rs.run.TaskID != "" {
		taskPrompt := s.buildTaskPrompt(ctx, rs.run.ProjectID, rs.projectPath, rs.run.TaskID)
		if len(taskPrompt) > 12000 {
			taskPrompt = taskPrompt[:12000] + "\n…(truncated)"
		}
		b.WriteString("## The work the asking model was doing\n\n" + taskPrompt + "\n\n")
	}
	b.WriteString("## The question the human was asked\n\n" + q.Question + "\n")
	if len(q.Options) > 0 {
		b.WriteString("\nOffered options:\n")
		for _, o := range q.Options {
			b.WriteString("- " + o + "\n")
		}
	}

	maxTok := 1200
	resp, err := p.Chat(ctx, provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{
			{Role: "system", Content: advisorSystemPrompt},
			{Role: "user", Content: b.String()},
		},
		MaxTokens: &maxTok,
	})
	if err != nil {
		return "", string(advisorID), err
	}

	// The consultation is real spend: on the tracker and in llm.jsonl like
	// every other call this run caused.
	calc := provider.CostCalculator{
		InputPerMTok: d.Cost.InputPerMTok, OutputPerMTok: d.Cost.OutputPerMTok,
	}
	cost := calc.Cost(resp.Usage)
	if rs.tracker != nil {
		rs.tracker.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cost)
	}
	if w := s.llmWriter(rs, rs.tracker); w != nil {
		w.AppendLLM(&agent.LLMCallRecord{
			Duckling: string(advisorID), Provider: string(d.Provider), Model: d.Model,
			Role:    "advisor",
			Request: map[string]interface{}{"question": q.Question},
			Response: map[string]interface{}{
				"content": firstN(answerText(resp), 2000),
			},
			CostUSD:      cost,
			FinishReason: resp.FinishReason,
		})
	}
	return strings.TrimSpace(answerText(resp)), string(advisorID), nil
}

// pickAdvisor prefers the run's recorded architect — decorrelated from the
// implementer that asked — then any duckling at all.
func (s *Service) pickAdvisor(rs *runState) config.DucklingID {
	if id := rs.run.Roster["architect"]; id != "" {
		return config.DucklingID(id)
	}
	for id := range s.cfg.Ducklings {
		return id
	}
	return ""
}

func answerText(resp provider.ChatResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
