package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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

// PR 42 made the task lane an engine-owned boundary. Keep its wording in one
// place so the implementer notice and both advisor paths cannot grant an
// exception that the round gate and Accept will refuse (B-382).
const taskLaneAuthority = "The accepted task lane is a hard boundary. Never recommend writing outside it. If the task cannot be completed within its lane, recommend an in-lane design or tell the implementer to stop and request a plan amendment."

// advisorSystemPrompt is decisive on purpose: a recommendation hedged into
// a survey re-creates the research burden it exists to remove.
const advisorSystemPrompt = `You are the advisor duckling in ducklab. Another model paused its run to ask
the human a question. Draft the answer the human should give.

Be decisive and concrete: pick ONE recommendation. Cite the project's own
spec sections and conventions when they decide the matter — the project's
established contracts beat your preferences. If the question offers options,
choose one.

` + taskLaneAuthority + `

Reply with ONLY the recommended answer text, 2-8 sentences, written as the
reply itself (it will be sent back to the asking model verbatim if the human
accepts it). No preamble, no "I recommend".`

// adviseQuestion runs the advisor asynchronously: the pause must not wait on
// a model call. The recommendation lands on the record as an `advice` event
// and on the pending data, where the question card renders it.
func (s *Service) adviseQuestion(rs *runState, q *tools.PendingQuestion) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	call, err := s.prepareAdvice(ctx, rs, advisorSystemPrompt, "## The question the human was asked", q)
	if err != nil {
		cancel()
		s.recordAdviceFailure(rs, q, "", adviceError(err))
		return
	}
	go func() {
		defer cancel()
		answer, advisor, err := s.executeAdvice(ctx, rs, q, call)
		if err != nil || strings.TrimSpace(answer) == "" {
			s.recordAdviceFailure(rs, q, advisor, adviceError(err))
			// No advice is a degraded question card, not a failure:
			// person can still answer, exactly as before advisors existed.
			return
		}
		w, werr := s.ensureWriter(rs)
		if werr != nil {
			return
		}
		// The person can answer while the advisor is still assembling context or
		// waiting on its model. Check and publish under the same lock used by run
		// snapshots and resume: otherwise the old question's advisor races the
		// resumed run and can file advice on the next attempt (B-411).
		rs.wmu.Lock()
		questionID, _ := rs.run.PendingData["question_id"].(string)
		_, answered := rs.givenAnswers[q.ID]
		if answered || rs.run.Status != "paused" || rs.run.PendingKind != "question" || (questionID != "" && questionID != q.ID) {
			rs.wmu.Unlock()
			return
		}
		if rs.run.PendingData == nil {
			rs.run.PendingData = map[string]interface{}{}
		}
		rs.run.PendingData["advice"] = answer
		rs.run.PendingData["advisor"] = advisor
		autonomy := rs.run.Autonomy
		runID := rs.run.ID
		w.AppendEvent("advice", map[string]interface{}{
			"question_id": q.ID, "advisor": advisor, "answer": answer,
		})
		_ = w.WriteState()
		rs.wmu.Unlock()

		// Under yolo the draft IS the answer: the run asked, an advisor
		// reasoned from the same documents, and nobody is watching the
		// inbox. Submitted through the same RunAnswer a person would use,
		// with the decider on the record — a failed submit degrades back to
		// an ordinary question card.
		if autonomy == "yolo" {
			w.AppendEvent("advice_taken", map[string]interface{}{
				"question_id": q.ID, "advisor": advisor,
			})
			if err := s.runAnswer(context.Background(), runID, q.ID, answer, "advisor:"+advisor+" (yolo)"); err != nil {
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

You advise within the active task. Never tell the implementer to edit the
accepted plan/task metadata, weaken or bypass verification, append "|| true",
skip a gate, or install host packages. ` + taskLaneAuthority + ` Diagnose
the exact error first; a missing header can be a wrong include path rather than
a missing package. If the environment truly cannot satisfy the unchanged gate,
say to report that blocker — do not manufacture green.

The active harness/stack invariants in your prompt are authoritative environment
facts. Never contradict them from memory. If they answer the question, repeat
their exact API or lifecycle rule; do not invent a substitute.

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
	if violation := inlineAdviceViolation(answer); violation != "" {
		rs.writer.AppendEvent("advice_rejected", map[string]interface{}{
			"question_id": tools.QuestionID(question), "advisor": advisor, "kind": "inline",
			"reason": violation, "answer": firstN(answer, 4000),
		})
		return "Discard the advisor's suggestion: it tried to " + violation + ". Keep the accepted task and its verification unchanged. Read the exact failing output, correct the implementation, command usage, API or include path within the task lane, and rerun verify_run. If the unchanged gate truly cannot run in this environment, report that blocker plainly instead of manufacturing green.", nil
	}
	rs.writer.AppendEvent("advice", map[string]interface{}{
		"question_id": tools.QuestionID(question), "advisor": advisor, "kind": "inline", "question": firstN(question, 2000), "answer": firstN(answer, 4000),
	})
	return answer, nil
}

func inlineAdviceViolation(answer string) string {
	lower := strings.ToLower(answer)
	if strings.Contains(lower, "|| true") || strings.Contains(lower, "--no-verify") ||
		strings.Contains(lower, "skip the gate") || strings.Contains(lower, "bypass the gate") {
		return "weaken or bypass the verification gate"
	}
	metadataTarget := strings.Contains(lower, "plan.md") || strings.Contains(lower, ".ducklab/docs") ||
		strings.Contains(lower, "task metadata") || strings.Contains(lower, "verification command")
	metadataMutation := strings.Contains(lower, "fs_write") || strings.Contains(lower, "replace") ||
		strings.Contains(lower, "edit ") || strings.Contains(lower, "change ") || strings.Contains(lower, "modify ")
	if metadataTarget && metadataMutation {
		return "modify accepted plan or task metadata from a build"
	}
	return ""
}

func (s *Service) adviseWith(ctx context.Context, rs *runState, systemPrompt, header string, q *tools.PendingQuestion) (string, string, error) {
	call, err := s.prepareAdvice(ctx, rs, systemPrompt, header, q)
	if err != nil {
		return "", "", err
	}
	return s.executeAdvice(ctx, rs, q, call)
}

type preparedAdvice struct {
	advisor  config.DucklingID
	duckling *duckling.Duckling
	caps     *duckling.Capabilities
	provider provider.Provider
	system   string
	user     string
	floor    int
}

// prepareAdvice resolves the seat and assembles the bounded project context.
// A paused run calls this before its worker returns: only the provider wait is
// asynchronous, so the old advisor is no longer walking the live run list when
// a human answer starts the next attempt (B-411).
func (s *Service) prepareAdvice(ctx context.Context, rs *runState, systemPrompt, header string, q *tools.PendingQuestion) (*preparedAdvice, error) {
	advisorID := s.pickAdvisor(rs)
	if advisorID == "" {
		return nil, fmt.Errorf("no advisor available")
	}
	d, err := s.ducklings.Get(advisorID)
	if err != nil {
		return nil, err
	}
	p, err := s.ducklings.Provider(advisorID)
	if err != nil {
		return nil, err
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
	// The asking implementer received this resolved stack capsule in its system
	// prompt. The advisor used to receive only task/docs and confidently
	// contradicted it, sending the implementer into a compiler-driven detour.
	// Put the same bounded facts immediately before the question.
	if rs.execCtx != nil && strings.TrimSpace(rs.execCtx.HarnessContext) != "" {
		b.WriteString("## Active harness/stack invariants — authoritative\n\n" + strings.TrimSpace(rs.execCtx.HarnessContext) +
			"\n\nResolve any conflict between memory, task prose and these detected environment facts in favor of these invariants.\n\n")
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
	caps := s.effectiveCaps(ctx, advisorID, false)
	return &preparedAdvice{advisor: advisorID, duckling: d, caps: caps, provider: p, system: systemPrompt, user: b.String(), floor: 2000}, nil
}

func (s *Service) executeAdvice(ctx context.Context, rs *runState, q *tools.PendingQuestion, call *preparedAdvice) (string, string, error) {
	resp, err := s.oneShot(ctx, call.provider, call.duckling, call.caps, call.system, call.user, call.floor)
	if err != nil {
		s.logFailedOneShot(rs, call.advisor, call.duckling, "advisor", q.Question, err, bestResponse(resp))
		return "", string(call.advisor), err
	}

	// The raw text is the evidence. A rejected answer used to be logged
	// AFTER its thinking was stripped — for a seat whose whole reply was an
	// unclosed <think>, the record showed "empty answer" and nothing else,
	// and the failure could not be diagnosed (Neocapture intake, 2026-08-29).
	raw := answerText(resp)
	answer := truncateAdvisorAnswer(stripAdvisorThinking(raw))
	if violation := advisorViolation(answer); violation != "" {
		// The quote is bounded: a runaway answer is the usual violation, and
		// quoted whole it would take the room the repair needs to answer.
		repairPrompt := call.user + "\n\nYour previous answer was:\n" + firstN(answer, maxQuotedAdvisorAnswer) +
			"\n\nContract violation: " + violation +
			". Reply with only the corrected answer text."
		repair, repairErr := s.oneShot(ctx, call.provider, call.duckling, call.caps, call.system, repairPrompt, call.floor)
		if repairErr != nil {
			// The response that triggered the repair is the evidence when the
			// repair itself returns nothing (review of #134).
			s.logFailedAdvisorAnswer(rs, call.advisor, call.duckling, "advisor", q.Question, raw, repairErr, bestResponse(repair, resp))
			return "", string(call.advisor), repairErr
		}
		raw = answerText(repair)
		answer = truncateAdvisorAnswer(stripAdvisorThinking(raw))
		if violation = advisorPostRepairViolation(answer); violation != "" {
			err := fmt.Errorf("advisor contract violation after repair: %s", violation)
			s.logFailedAdvisorAnswer(rs, call.advisor, call.duckling, "advisor", q.Question, raw, err, bestResponse(repair, resp))
			return "", string(call.advisor), err
		}
	}

	// The consultation is real spend: on the tracker and in llm.jsonl like
	// every other call this run caused.
	calc := provider.CostCalculator{
		InputPerMTok: call.duckling.Cost.InputPerMTok, OutputPerMTok: call.duckling.Cost.OutputPerMTok,
	}
	cost := calc.Cost(resp.Usage)
	rs.wmu.Lock()
	tracker := rs.tracker
	rs.wmu.Unlock()
	if tracker != nil {
		tracker.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, cost)
	}
	if w := s.llmWriter(rs, tracker); w != nil {
		w.AppendLLM(&agent.LLMCallRecord{
			Duckling: string(call.advisor), Provider: string(call.duckling.Provider), Model: call.duckling.Model,
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
	return strings.TrimSpace(answer), string(call.advisor), nil
}

// maxQuotedAdvisorAnswer bounds the rejected answer a repair quotes back.
const maxQuotedAdvisorAnswer = 4000

// oneShot sizes the cap from this call's own prompt and sends it. Every
// service one-shot goes through here: a cap sized once and reused for a
// longer prompt (the advisor's repair appends the rejected answer) overran
// the context window it had been clamped to (review of #134).
func (s *Service) oneShot(ctx context.Context, p provider.Provider, d *duckling.Duckling, caps *duckling.Capabilities, system, user string, floor int) (provider.ChatResponse, error) {
	return oneShotChat(ctx, p, d, caps, system, user, s.oneShotCap(d, caps, floor, provider.EstimateTokens(system+user)))
}

// oneShotChat sends a service-side one-shot with a cap already sized for its
// prompt; callers use oneShot. It is the single way such a call reaches a
// provider. adviseWith used to build a raw ChatRequest — no sampling
// params, no thinking suppression — so a seat configured with
// disable_thinking reasoned straight into the 1200-token cap and the
// visible answer arrived empty; the repair repeated the identical
// conditions and the card said "empty answer" (B-123). The loop already
// knew how to make this call correctly; one-shots now borrow exactly that.
func oneShotChat(ctx context.Context, p provider.Provider, d *duckling.Duckling, caps *duckling.Capabilities, system, user string, maxTok int) (provider.ChatResponse, error) {
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
		// The duckling's effective caps, with the probed ThinkingControl —
		// the same request the agent loop builds. Without it, suppression
		// fell through to the local-server parameter OpenRouter ignores.
		if caps == nil {
			caps = &d.Caps
		}
		agent.ApplyThinkingSuppression(&req, duckling.ProviderCaps(caps))
	}
	return p.Chat(ctx, req)
}

// oneShotCap sizes a one-shot's output. A seat that thinks and is not
// suppressed spends its reasoning inside the same cap as its answer: at
// 2000 tokens a local Qwen3.6 seat ran out mid-<think>, the strip removed
// the unclosed block, and the visible answer was empty — twice, since the
// repair repeated the same cap (Neocapture intake, 2026-08-29). Such a seat
// gets the room its configuration already grants it.
func (s *Service) oneShotCap(d *duckling.Duckling, caps *duckling.Capabilities, floor, promptTokens int) int {
	want := floor
	if d != nil && !s.thinkingSuppressed(d, caps) {
		// Reasoning shares the cap with the answer and cannot be assumed off:
		// a mandatory-reasoning endpoint (qwen3.8-max on Alibaba) spent the
		// whole 2000-token floor thinking and answered nothing (B-479). Grant
		// the seat's configured output — its declared ceiling, which the agent
		// loop sends unchanged — or unsuppressedFloor when none is configured.
		if d.Params.MaxTokens != nil {
			want = max(floor, *d.Params.MaxTokens)
		} else {
			want = max(floor, unsuppressedFloor)
		}
	}
	// Prompt and output share the context window. An endpoint rejects a
	// request whose max_tokens does not fit beside the prompt (review of
	// #134: a 16K floor on an 8K seat failed every advisor and digest call,
	// and digestion runs precisely for small-context seats).
	contextTokens := 0
	if caps != nil {
		contextTokens = caps.ContextTokens
	}
	if contextTokens > 0 {
		room := contextTokens - promptTokens - promptTokens/10 - oneShotContextMargin
		if want > room {
			want = max(room, minOneShotOutput)
		}
	}
	return want
}

// unsuppressedFloor is the cap a one-shot gets when its seat may be
// reasoning and configures no output limit: room to think and still answer,
// when the context window has it.
const unsuppressedFloor = 16384

// oneShotContextMargin covers the chat template and the error of the
// characters/4 prompt estimate (plus 10% of the estimate itself).
const oneShotContextMargin = 256

// minOneShotOutput is what a one-shot asks for when the prompt leaves less:
// the call may fail for its size, but it fails with the endpoint's reason.
const minOneShotOutput = 256

// thinkingSuppressed reports whether a one-shot to this duckling actually
// runs without reasoning: suppression is requested, and the endpoint is known
// to honour the control Ducklab sends. An OpenRouter endpoint is trusted only
// when its probe accepted reasoning.enabled=false; a local template server
// takes chat_template_kwargs; a mandatory endpoint never suppresses.
func (s *Service) thinkingSuppressed(d *duckling.Duckling, caps *duckling.Capabilities) bool {
	if !d.Params.DisableThinking {
		return false
	}
	control := ""
	if caps != nil {
		control = caps.ThinkingControl
	}
	switch control {
	case "disabled":
		return true
	case "mandatory":
		return false
	}
	// Unknown control: only a server Ducklab can reasonably assume is a local
	// template server (llama.cpp, vLLM on this machine or the LAN) honours
	// chat_template_kwargs. Any other endpoint — a remote OpenAI-compatible
	// host, Anthropic — is unverified, and assuming suppression there recreates
	// the empty answer this cap exists to prevent (review of #134).
	s.cfgMu.RLock()
	prov, ok := s.cfg.Providers[d.Provider]
	s.cfgMu.RUnlock()
	return ok && localTemplateServer(prov)
}

// localTemplateServer reports whether a provider is a self-hosted
// OpenAI-compatible server on a loopback or private address.
func localTemplateServer(p config.Provider) bool {
	if config.IsOpenRouter(p) || (p.Kind != "" && p.Kind != config.ProviderKindOpenAI) {
		return false
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
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
func (s *Service) logFailedOneShot(rs *runState, seat config.DucklingID, d *duckling.Duckling, role, request string, callErr error, resp *provider.ChatResponse) {
	s.logFailedAdvisorAnswer(rs, seat, d, role, request, "", callErr, resp)
}

// bestResponse is the first response that carries anything: a provider can
// return a partial response beside an error, and a failed repair leaves the
// original response as the only evidence.
func bestResponse(candidates ...provider.ChatResponse) *provider.ChatResponse {
	for i := range candidates {
		r := candidates[i]
		if len(r.Choices) > 0 || r.Usage.CompletionTokens > 0 || r.FinishReason != "" {
			return &r
		}
	}
	return nil
}

// logFailedAdvisorAnswer records rejected provider output as well as its cause,
// so an operator can audit a contract discard.
//
// resp, when there was one, adds what the record needs to explain an empty
// answer: why generation stopped, what it spent and how much went to
// reasoning. "empty answer" alone hid a mandatory-reasoning seat spending its
// whole cap thinking (B-479).
func (s *Service) logFailedAdvisorAnswer(rs *runState, seat config.DucklingID, d *duckling.Duckling, role, request, answer string, callErr error, resp *provider.ChatResponse) {
	if w := s.llmWriter(rs, rs.tracker); w != nil {
		response := map[string]interface{}{"error": callErr.Error()}
		if answer != "" {
			response["content"] = firstN(answer, 2000)
		}
		var usage map[string]interface{}
		finish := ""
		if resp != nil {
			finish = string(resp.FinishReason)
			if finish == "" && len(resp.Choices) > 0 {
				// Providers report it per choice as often as per response.
				finish = string(resp.Choices[0].FinishReason)
			}
			usage = map[string]interface{}{
				"prompt_tokens": resp.Usage.PromptTokens, "completion_tokens": resp.Usage.CompletionTokens,
				"reasoning_tokens": resp.Usage.ReasoningTokens,
			}
			if len(resp.Choices) > 0 {
				response["reasoning_chars"] = len(resp.Choices[0].Message.Reasoning)
			}
		}
		w.AppendLLM(&agent.LLMCallRecord{
			Duckling: string(seat), Provider: string(d.Provider), Model: d.Model, Usage: usage, FinishReason: finish,
			Role: role, Request: map[string]interface{}{"question": firstN(request, 400)}, Response: response,
		})
	}
}

// pickAdvisor uses the run's dedicated advisor seat. Older runs may not have
// recorded one, so fall back to the resolved roster's advisor seat rather than
// silently borrowing the architect.
func (s *Service) pickAdvisor(rs *runState) config.DucklingID {
	if rs == nil {
		return ""
	}
	return s.pickAdvisorForRun(rs.snapshotRun())
}

func (s *Service) pickAdvisorForRun(run *runlog.Run) config.DucklingID {
	if run == nil {
		return ""
	}
	if id := run.Roster[string(config.RoleAdvisor)]; id != "" {
		return config.DucklingID(id)
	}
	if id := run.Roster[string(config.RoleArchitect)]; id != "" {
		// Compatibility for runs created before the advisor seat existed.
		return config.DucklingID(id)
	}
	if proj, err := s.projectConfig(run.ProjectID); err == nil {
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
	if rs == nil || rs.run == nil {
		return nil
	}
	run := rs.snapshotRun()
	if !redoNoteEligible(run) {
		return nil
	}
	parts := make([]string, 0, 4)
	if run.TaskID != "" {
		if task := s.buildTaskPrompt(ctx, run.ProjectID, rs.projectPath, run.TaskID); strings.TrimSpace(task) != "" {
			parts = append(parts, "Task: "+firstN(strings.TrimSpace(task), 2400))
		}
	}
	if strings.TrimSpace(run.Failure) != "" {
		parts = append(parts, "Failure: "+firstN(strings.TrimSpace(run.Failure), 1600))
	}
	if gate, err := s.RunVerify(ctx, run.ID, 20); err == nil && strings.TrimSpace(gate) != "" {
		parts = append(parts, "Gate tail:\n"+firstN(strings.TrimSpace(gate), 4000))
	}
	if diff, err := s.RunDiff(ctx, run.ID); err == nil && strings.TrimSpace(diff) != "" {
		parts = append(parts, "Diff summary:\n"+firstN(strings.TrimSpace(diff), 4000))
	}
	if len(parts) == 0 {
		return nil
	}
	advisor := s.pickAdvisorForRun(run)
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
	questionID, _ := rs.run.PendingData["question_id"].(string)
	_, answered := rs.givenAnswers[q.ID]
	if answered || rs.run.Status != "paused" || rs.run.PendingKind != "question" || (questionID != "" && questionID != q.ID) {
		rs.wmu.Unlock()
		return
	}
	if rs.run.PendingData == nil {
		rs.run.PendingData = map[string]interface{}{}
	}
	rs.run.PendingData["advice_failed"] = cause
	w.AppendEvent("advice_failed", map[string]interface{}{
		"question_id": q.ID,
		"advisor":     advisor,
		"error":       cause,
	})
	_ = w.WriteState()
	rs.wmu.Unlock()
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
