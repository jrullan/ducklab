package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jrullan/ducklab/internal/agent"
	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/duckling"
	"github.com/jrullan/ducklab/internal/provider"
	"github.com/jrullan/ducklab/internal/runlog"
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
			s.recordAdviceFailure(rs, q, advisor, adviceError(err))
			// No advice is a degraded question card, not a failure:
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

		// Under yolo the draft IS the answer: the run asked, an advisor
		// reasoned from the same documents, and nobody is watching the
		// inbox. Submitted through the same RunAnswer a person would use,
		// with the decider on the record — a failed submit degrades back to
		// an ordinary question card.
		if rs.run.Autonomy == "yolo" {
			w.AppendEvent("advice_taken", map[string]interface{}{
				"question_id": q.ID, "advisor": advisor,
			})
			if err := s.RunAnswer(context.Background(), rs.run.ID, q.ID, answer); err != nil {
				w.AppendEvent("warning", map[string]interface{}{
					"detail": "advisor auto-answer failed: " + err.Error(),
				})
			}
		}
	}()
}

// advise picks the advisor, assembles the context, and asks once — a one-shot
// chat, no tools: the advisor reasons from the same documents the asker had.
func (s *Service) advise(ctx context.Context, rs *runState, q *tools.PendingQuestion) (string, string, error) {
	return s.adviseWith(ctx, rs, advisorSystemPrompt, "## The question the human was asked", q)
}

// The rubber duck's framing for a mid-turn consult: no human is in the loop,
// the run is not paused, and the asker is the one who will act on the reply.
const rubberDuckSystemPrompt = `You are the advisor duckling in ducklab — the rubber duck. An implementer
working on a task is stuck and has asked you mid-turn: the run is NOT paused,
no human is involved, and your reply goes straight back to the implementer,
which will act on it in its very next tool call.

Answer as a senior colleague would: concretely, briefly, about the NEXT move.
Name the tool to use instead, the file to read first, the assumption to drop.
If it describes fighting a tool, prefer the tool that re-types the least
(fs_write_lines over fs_patch, fs_read of the exact range first). Cite the
project's own documents when they decide the matter.

Reply with ONLY the advice, 2-8 sentences, imperative voice. No preamble.`

// adviseInline answers an implementer's ask_advisor call. Same seat, same
// documents, same cost accounting as a paused-question consult; different
// framing, because the reader is the model, not the person.
func (s *Service) adviseInline(ctx context.Context, rs *runState, question string) (string, error) {
	answer, advisor, err := s.adviseWith(ctx, rs, rubberDuckSystemPrompt,
		"## The implementer's question (asked mid-turn; the run is not paused)",
		&tools.PendingQuestion{ID: tools.QuestionID(question), Question: question})
	if err != nil {
		rs.writer.AppendEvent("advice_failed", map[string]interface{}{
			"question_id": tools.QuestionID(question), "advisor": advisor, "kind": "inline", "cause": adviceError(err),
		})
		return "", err
	}
	rs.writer.AppendEvent("advice", map[string]interface{}{
		"question_id": tools.QuestionID(question), "advisor": advisor, "kind": "inline", "question": firstN(question, 2000), "answer": firstN(answer, 4000),
	})
	return answer, nil
}

func (s *Service) adviseWith(ctx context.Context, rs *runState, systemPrompt, header string, q *tools.PendingQuestion) (string, string, error) {
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

	if w, werr := s.ensureWriter(rs); werr == nil {
		w.AppendEvent("advice_started", map[string]interface{}{"advisor": advisorID, "question_id": q.ID})
	}

	var b strings.Builder
	if rs.run.TaskID != "" {
		taskPrompt := s.buildTaskPrompt(ctx, rs.run.ProjectID, rs.projectPath, rs.run.TaskID)
		if len(taskPrompt) > 12000 {
			taskPrompt = taskPrompt[:12000] + "\n…(truncated)"
		}
		b.WriteString("## The work the asking model was doing\n\n" + taskPrompt + "\n\n")
	}
	// Advisors must receive the project documents even when the question was
	// asked outside a task turn. Without this, the system prompt's promise to
	// cite the project's spec is not actionable.
	for _, kind := range []artifact.Kind{artifact.KindRequirements, artifact.KindSpec, artifact.KindPlan} {
		doc, loadErr := artifact.Load(rs.projectPath, kind)
		if loadErr == nil && strings.TrimSpace(doc.Raw) != "" {
			raw := doc.Raw
			if len(raw) > 16000 {
				raw = raw[:16000] + "\n…(truncated)"
			}
			b.WriteString("## Project " + string(kind) + " document\n\n" + raw + "\n\n")
		}
	}
	b.WriteString(header + "\n\n" + q.Question + "\n")
	if len(q.Options) > 0 {
		b.WriteString("\nOffered options:\n")
		for _, o := range q.Options {
			b.WriteString("- " + o + "\n")
		}
	}

	// 2000, not 1200: a terse reasoning seat can spend a few hundred tokens
	// before the answer even with suppression applied, and an advisor cut
	// off mid-answer fails its contract as surely as an empty one.
	maxTok := 2000
	resp, err := oneShotChat(ctx, p, d, systemPrompt, b.String(), maxTok)
	if err != nil {
		s.logFailedOneShot(rs, advisorID, d, "advisor", q.Question, err)
		return "", string(advisorID), err
	}

	answer := truncateAdvisorAnswer(stripAdvisorThinking(answerText(resp)))
	if violation := advisorViolation(answer); violation != "" {
		repairPrompt := b.String() + "\n\nYour previous answer was:\n" + answer +
			"\n\nContract violation: " + violation +
			". Reply with only the corrected answer text."
		repair, repairErr := oneShotChat(ctx, p, d, systemPrompt, repairPrompt, maxTok)
		if repairErr != nil {
			s.logFailedAdvisorAnswer(rs, advisorID, d, "advisor", q.Question, answer, repairErr)
			return "", string(advisorID), repairErr
		}
		answer = truncateAdvisorAnswer(stripAdvisorThinking(answerText(repair)))
		if violation = advisorPostRepairViolation(answer); violation != "" {
			err := fmt.Errorf("advisor contract violation after repair: %s", violation)
			s.logFailedAdvisorAnswer(rs, advisorID, d, "advisor", q.Question, answer, err)
			return "", string(advisorID), err
		}
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
				"content": firstN(answer, 2000),
			},
			Usage: map[string]interface{}{
				"prompt_tokens":     resp.Usage.PromptTokens,
				"completion_tokens": resp.Usage.CompletionTokens,
			},
			CostUSD:      cost,
			FinishReason: resp.FinishReason,
		})
	}
	return strings.TrimSpace(answer), string(advisorID), nil
}

// oneShotChat is the single way a service-side one-shot call reaches a
// provider. adviseWith used to build a raw ChatRequest — no sampling
// params, no thinking suppression — so a seat configured with
// disable_thinking reasoned straight into the 1200-token cap and the
// visible answer arrived empty; the repair repeated the identical
// conditions and the card said "empty answer" (B-123). The loop already
// knew how to make this call correctly; one-shots now borrow exactly that.
func oneShotChat(ctx context.Context, p provider.Provider, d *duckling.Duckling, system, user string, maxTok int) (provider.ChatResponse, error) {
	req := provider.ChatRequest{
		Model: d.Model,
		Messages: []provider.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: &maxTok,
	}
	if d.Params.Temperature != nil {
		req.Temperature = d.Params.Temperature
	}
	if d.Params.TopP != nil {
		req.TopP = d.Params.TopP
	}
	if d.Params.DisableThinking {
		agent.ApplyThinkingSuppression(&req, provider.Capabilities{
			NativeTools: d.Caps.NativeTools, JSONMode: d.Caps.JSONMode,
			ContextTokens: d.Caps.ContextTokens, Vision: d.Caps.Vision,
		})
	}
	return p.Chat(ctx, req)
}

// wireAdvisor arms ask_advisor on an ExecContext, guarded so the tool can
// tell the model plainly when there is truly nobody to ask. Shared by the
// build and test-first paths: it was wired on build only, and a test run's
// implementer was told "no advisor is seated" while the seat chip showed
// one sitting right there (B-115).
func (s *Service) wireAdvisor(rs *runState, ectx *tools.ExecContext) {
	if s.pickAdvisor(rs) == "" {
		return
	}
	ectx.OnAskAdvisor = func(ctx context.Context, question string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return s.adviseInline(cctx, rs, question)
	}
}

// logFailedOneShot puts a FAILED one-shot on the record. adviseWith only
// appended to llm.jsonl on success, so the very calls a person needs to see
// — the ones behind "advisor recommendation failed" — were invisible and
// the diagnosis required guessing (B-123).
func (s *Service) logFailedOneShot(rs *runState, seat config.DucklingID, d *duckling.Duckling, role, request string, callErr error) {
	s.logFailedAdvisorAnswer(rs, seat, d, role, request, "", callErr)
}

// logFailedAdvisorAnswer records rejected provider output as well as its cause,
// so an operator can audit a contract discard.
func (s *Service) logFailedAdvisorAnswer(rs *runState, seat config.DucklingID, d *duckling.Duckling, role, request, answer string, callErr error) {
	if w := s.llmWriter(rs, rs.tracker); w != nil {
		response := map[string]interface{}{"error": callErr.Error()}
		if answer != "" {
			response["content"] = firstN(answer, 2000)
		}
		w.AppendLLM(&agent.LLMCallRecord{
			Duckling: string(seat), Provider: string(d.Provider), Model: d.Model,
			Role: role, Request: map[string]interface{}{"question": firstN(request, 400)}, Response: response,
		})
	}
}

// pickAdvisor uses the run's dedicated advisor seat. Older runs may not have
// recorded one, so fall back to the resolved roster's advisor seat rather than
// silently borrowing the architect.
func (s *Service) pickAdvisor(rs *runState) config.DucklingID {
	if id := rs.run.Roster[string(config.RoleAdvisor)]; id != "" {
		return config.DucklingID(id)
	}
	if id := rs.run.Roster[string(config.RoleArchitect)]; id != "" {
		// Compatibility for runs created before the advisor seat existed.
		return config.DucklingID(id)
	}
	if proj, err := s.projectConfig(rs.run.ProjectID); err == nil {
		if id := proj.Roster[config.RoleAdvisor]; id != "" {
			return id
		}
	}
	for id := range s.cfg.Ducklings {
		return id
	}
	return ""
}

// draftRedoNote creates a bounded, editable recommendation from facts already
// recorded for the run. It deliberately never changes run state or starts a
// retry; the note is an advisor recommendation, not a decision.
func (s *Service) draftRedoNote(ctx context.Context, rs *runState) *runlog.RedoNote {
	if rs == nil || rs.run == nil || !redoNoteEligible(rs.run) {
		return nil
	}
	parts := make([]string, 0, 4)
	if rs.run.TaskID != "" {
		if task := s.buildTaskPrompt(ctx, rs.run.ProjectID, rs.projectPath, rs.run.TaskID); strings.TrimSpace(task) != "" {
			parts = append(parts, "Task: "+firstN(strings.TrimSpace(task), 2400))
		}
	}
	if strings.TrimSpace(rs.run.Failure) != "" {
		parts = append(parts, "Failure: "+firstN(strings.TrimSpace(rs.run.Failure), 1600))
	}
	if gate, err := s.RunVerify(ctx, rs.run.ID, 20); err == nil && strings.TrimSpace(gate) != "" {
		parts = append(parts, "Gate tail:\n"+firstN(strings.TrimSpace(gate), 4000))
	}
	if diff, err := s.RunDiff(ctx, rs.run.ID); err == nil && strings.TrimSpace(diff) != "" {
		parts = append(parts, "Diff summary:\n"+firstN(strings.TrimSpace(diff), 4000))
	}
	if len(parts) == 0 {
		return nil
	}
	advisor := s.pickAdvisor(rs)
	note := "Retry the task after addressing the failure.\n\n" + strings.Join(parts, "\n\n")
	return &runlog.RedoNote{Draft: firstN(note, 12000), Advisor: string(advisor), Editable: true}
}

func redoNoteEligible(r *runlog.Run) bool {
	if r == nil {
		return false
	}
	if r.Status == "failed" || r.Verdict == "FAILED" {
		return true
	}
	// A run the advisor stopped pauses with its work in place (the no-error-
	// discards-work rule) and its Failure names the reason and the reshuffle;
	// that IS the redo material.
	if r.Status == "paused" && r.PendingKind == "error" && strings.HasPrefix(r.Failure, "stopped by advisor") {
		return true
	}
	// A green test-first run is actionable: its test is the input to the
	// chained build, even though the test gate itself passed.
	return r.Stage == "test" && r.Status == "paused" && r.PendingKind == "gate" && r.Verdict != "FAILED"
}

func adviceError(err error) string {
	if err == nil {
		return "no advice returned"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "none within deadline"
	}
	return err.Error()
}

// recordAdviceFailure keeps a failed consultation visible on both the question
// card and the event log. Advice is optional, but silently losing it makes a
// degraded card indistinguishable from one whose advisor is still working.
func (s *Service) recordAdviceFailure(rs *runState, q *tools.PendingQuestion, advisor, cause string) {
	w, err := s.ensureWriter(rs)
	if err != nil {
		return
	}
	rs.wmu.Lock()
	if rs.run.PendingData == nil {
		rs.run.PendingData = map[string]interface{}{}
	}
	rs.run.PendingData["advice_failed"] = cause
	rs.wmu.Unlock()
	w.AppendEvent("advice_failed", map[string]interface{}{
		"question_id": q.ID,
		"advisor":     advisor,
		"error":       cause,
	})
	_ = w.WriteState()
}

// stripAdvisorThinking removes provider-specific deliberation wrappers before
// validating or persisting the answer. An unterminated block is not answer
// text, so discard it rather than leaking the model's private reasoning.
func stripAdvisorThinking(text string) string {
	for _, tag := range []string{"think", "thinking", "analysis"} {
		for {
			lower := strings.ToLower(text)
			start := strings.Index(lower, "<"+tag+">")
			if start < 0 {
				break
			}
			end := strings.Index(lower[start:], "</"+tag+">")
			if end < 0 {
				text = text[:start]
				break
			}
			text = text[:start] + text[start+end+len(tag)+3:]
		}
	}
	return strings.TrimSpace(text)
}

func advisorViolation(text string) string {
	text = stripAdvisorThinking(text)
	if text == "" {
		return "empty answer"
	}
	if sentences := advisorSentenceCount(text); sentences > 16 {
		return fmt.Sprintf("expected 2-8 sentences (hard limit 16), got %d", sentences)
	}
	return ""
}

// Terse answers remain useful; only empty answers and runaways are rejected.
func advisorPostRepairViolation(text string) string {
	text = stripAdvisorThinking(text)
	if text == "" {
		return "empty answer"
	}
	if sentences := advisorSentenceCount(text); sentences > 16 {
		return fmt.Sprintf("expected 2-8 sentences (hard limit 16), got %d", sentences)
	}
	return ""
}

// advisorSentenceBoundaries returns sentence-ending byte offsets. Dots in code
// and path-like tokens are not prose boundaries.
func advisorSentenceBoundaries(text string) []int {
	masked := make([]bool, len(text))
	for start := 0; start < len(text); {
		if text[start] == '`' {
			end := strings.IndexByte(text[start+1:], '`')
			if end >= 0 {
				end += start + 2
				for i := start; i < end; i++ {
					masked[i] = true
				}
				start = end
				continue
			}
		}
		if unicode.IsSpace(rune(text[start])) {
			start++
			continue
		}
		end := start
		for end < len(text) && !unicode.IsSpace(rune(text[end])) {
			end++
		}
		tokenEnd := end
		for tokenEnd > start && strings.ContainsRune(".!?", rune(text[tokenEnd-1])) {
			tokenEnd--
		}
		token := text[start:tokenEnd]
		pathLike := strings.Contains(token, "/") || strings.Contains(token, ".")
		if strings.EqualFold(text[start:end], "e.g.") || strings.EqualFold(text[start:end], "i.e.") {
			pathLike = true
			tokenEnd = end
		}
		if pathLike {
			for i := start; i < tokenEnd; i++ {
				masked[i] = true
			}
		}
		start = end
	}

	var boundaries []int
	for i := 0; i < len(text); i++ {
		if masked[i] || !strings.ContainsRune(".!?", rune(text[i])) {
			continue
		}
		j := i + 1
		for j < len(text) && unicode.IsSpace(rune(text[j])) {
			j++
		}
		if j == len(text) || (j > i+1 && unicode.IsUpper(rune(text[j]))) {
			boundaries = append(boundaries, i+1)
		}
	}
	return boundaries
}

func advisorSentenceCount(text string) int { return len(advisorSentenceBoundaries(text)) }

// truncateAdvisorAnswer preserves useful answers that only exceed the requested
// cap. More than twice the cap remains a runaway and is rejected by validation.
func truncateAdvisorAnswer(text string) string {
	boundaries := advisorSentenceBoundaries(text)
	if len(boundaries) <= 8 || len(boundaries) > 16 {
		return text
	}
	return strings.TrimSpace(text[:boundaries[7]])
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
