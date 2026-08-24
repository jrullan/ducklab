---
kind: plan
version: 13
updated_at: 2026-08-23T21:49:47Z
run_id: r-20260823-213904-mem2
ducklings: [atom-local, luna, k3, terra, qwen38-max]
based_on: 286522acd735bbab
approved_by: human
---

## M-001 — Reported bugs

### T-001 — Expose budget lifting through MCP and identify the invalid kind field in lift errors

**Implements:** SPEC-021

Fixes B-004.

## Reported

What happened: both spec refresh runs (r-...-7s32, r-...-32gx) hit the default 3M token cap mid-council. run_get offered next: [resume, abort] — but resume continues the ledger from what was spent, so resuming without lifting the cap re-pauses immediately. The only lift door is POST /v1/runs/{id}/budget/lift over HTTP with the bearer token, which a remote MCP operator (Elena) cannot reach. A small operator obeying the offered actions enters resume→pause→resume forever.

Expected: the MCP surface closes the loop it opens — either a budget_lift tool (run_id, kind), or decide accepts a lift alongside resume ("resume with tokens uncapped"). Recorded like the desktop's lift: one-way, per-cap, attributed.

Related detail worth fixing in the same file: the lift endpoint's error for a wrong field names the valid VALUES ("no budget cap named ...") but not the FIELD (kind) — the artifact_read lesson applies (errors teach the field).

## Triage

**Component:** MCP budget controls

MCP offers resume for capped runs without exposing the required lift action, trapping remote operators in an endless pause-resume loop.

**Verification (triage recommends):** test-first — Pause a capped run, lift its budget through MCP, and verify resume proceeds; invalid lift fields should name kind.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-002 — Detect token repetition loops and apply contract-aware generation caps

**Implements:** SPEC-042

Fixes B-007.

## Reported

What happened: during triage run r-20260815-003659-de7d, beelink-local entered a repetition loop while GENERATING its B-005 reply: the stream kept producing tokens for 6+ minutes while events.jsonl and llm.jsonl sat frozen — no tool call ever landed, so the repeat-call brake, gate brake and call caps saw nothing, and the stall watchdog stayed quiet because tokens WERE flowing. The human spotted it in the live stream; an events-based observer (me) verified the wrong layer and initially called it healthy. The duckling's max_tokens=20000 would have cut it eventually — but at local speeds that is ~10 minutes of wasted GPU per occurrence, and the loop landed as a truncated reply, not as a diagnosed loop.

Expected, in small-seat-first order: (a) contract-aware output caps — a json:triage reply is a classification and should cap around 2k tokens, a spec draft legitimately needs 20k; one blanket per-duckling max_tokens cannot serve both; (b) a stream repetition detector (n-gram window over deltas) that cuts the call, records a repetition_loop event, and retries once with the loop named in the prompt; (c) the run view should surface tokens-flowing-but-nothing-landing as its own signal — the human should not be the only detector with eyes on that layer.

## Triage

**Component:** LLM generation streaming

A token-level loop consumes substantial GPU time while bypassing existing brakes and produces a misleading truncated reply.

**Verification (triage recommends):** test-first — Stream repeated n-grams during a JSON triage response and expect a repetition_loop event, cancellation, and one retry with the loop named.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-003 — Always include next_steps in MCP status response, even when empty

**Implements:** SPEC-021

Fixes B-002.

## Reported

What happened: the status tool's description instructs the agent "When the human asks what to do, answer FROM next_steps: it already knows that a triaged bug wants promoting and which task is buildable" — but the response objects carry only name/project/running/waiting_for_decision. No next_steps field arrives, for any project (observed against three projects, one with a pending gate).

Expected: each project in the status response carries next_steps (the same ProjectNext the desktop's guide rail shows), or the tool description stops promising it. An agent operator (Elena) told to answer FROM a field that never arrives either invents guidance or goes silent.

## Triage

**Component:** mcp
**Suspected files:** internal/mcp/tools.go

The status() function conditionally omits next_steps when ProjectNext returns empty, contradicting the tool description that promises it for every project.

**Verification (triage recommends):** test-first — MCP status tool response must include next_steps field for every project, even when empty

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-004 — Add per-project document lifecycle state to MCP status response

**Implements:** SPEC-021

Fixes B-003.

## Reported

What happened: driving the ducklab adopt via MCP, there was no way to learn which lifecycle documents exist and whether they are approved. status doesn't say; the only discovery paths were (a) colliding with stage_start errors ("this project already has requirements") and (b) artifact_get, which returns the ENTIRE document — the ducklab spec is ~15k tokens, fatal context spend for a small local operator (pato-atom, 32k) that only needed "does it exist? approved?".

Expected: a cheap orientation surface — either status gains per-project document state (requirements: approved, spec: approved, plan: none, tasks: N, open bugs: N), or artifact_get grows a summary mode (ids + titles + approved flag, no bodies). The error-message teaching is excellent (it names the right next door) but discovery-by-collision costs a failed call and a confused operator.

## Triage

**Component:** mcp
**Suspected files:** internal/mcp/mcp.go

MCP status lacks document lifecycle state, forcing agents to pay for full documents via artifact_get or collide with stage_start errors

**Verification (triage recommends):** test-first — MCP status tool should return per-project document lifecycle state (requirements/spec/plan status, task count, bug count) without requiring artifact_get

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-005 — Expose reviewer findings and filing action through MCP run tools

**Implements:** SPEC-021

Fixes B-010.

## Reported

What happened: T-002's build (r-20260815-130948-qka6) passed with the reviewer's approve carrying 4 minor findings. The desktop shows them on the run with a one-click "File N findings as bugs" (POST /v1/runs/{id}/file-findings). run_get over MCP returns diff, budget, verdict and next — but no findings at all, and no MCP tool wraps file-findings. The operator deciding accept/reject sees strictly less than the human at the desktop, and the findings→bugs loop the UI offers in one click does not exist for an agent.

Expected: run_get carries the reviewer's findings (severity, file, line, issue, fix — they are already structured), and a file_findings tool (or a decide-adjacent action) lets the operator land them as bugs with attribution, exactly like the desktop button. Small-operator-first: the findings are compact and structured — cheap to carry, decisive to have.

## Triage

**Component:** MCP run operations
**Suspected files:** internal/mcp/tools.go, internal/mcp/mcp_test.go

The MCP run surface omits findings and provides no wrapper for the existing findings-file endpoint, leaving operators unable to make equally informed decisions or file findings.

**Verification (triage recommends):** test-first — run_get for a completed run with structured reviewer findings should return all finding fields, and file_findings should create attributed bugs.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-006 — Surface accepted-unreleased work and stamp installed binaries with branch and commit provenance

**Implements:** SPEC-061

Fixes B-018.

## Reported

What happened (the root cause of a whole night of confusion): T-001's accepted budget_lift lived on ducklab/T-001, never merged; T-002's chain branched from main WITHOUT it; make install ships whichever branch the tree has checked out — so a later install silently REGRESSED the MCP binary, removing a tool an operator was told exists. Three observers (two Claude sessions and Elena) spent hours reconciling what "exists" meant. Nobody had run release; nothing anywhere said accepted-but-unreleased work was piling up on branches.

Expected: the accepted-unreleased pileup is a first-class signal — status/guide surface "N accepted tasks on M branches await a release" with the release step promoted when it grows; and ideally the record warns when installed binary provenance diverges from main (even just a version stamp with branch+sha, printable by ducklab --version and shown in engine health). Accepted must not read as shipped.

## Triage

**Component:** release/install provenance
**Suspected files:** Makefile, internal/cli/cli.go, internal/service/service.go, internal/engineapi/engineapi.go, frontend/src

Accepted work can remain stranded on task branches while install silently ships the checked-out tree, causing operators to believe unreleased functionality is present.

**Verification (triage recommends):** test-first — Create accepted tasks on unmerged branches and verify status/guide counts them, promotes release, and reports installed provenance distinct from main.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-007 — Align MCP status unreleased branch fields with the OpenAPI contract

**Implements:** SPEC-021

Fixes B-021.

## Reported

Inconsistencia introducida por el build de B-018 entre el contrato OpenAPI y la respuesta MCP de status. En docs/openapi.json, ServiceStatus.unreleased_branches está declarado como integer. Sin embargo, internal/mcp/tools.go asigna entry["unreleased_branches"] = branches, donde branches es []string con los nombres de rama (por ejemplo ["ducklab/T-001", "ducklab/T-002"]). El servicio HTTP usa el conteo entero, pero el adaptador MCP expone una lista bajo el mismo campo. Clientes que consuman el contrato pueden fallar o interpretar incorrectamente la respuesta. Esperado: usar un campo separado como unreleased_branch_names para la lista y mantener unreleased_branches como integer, o actualizar explícitamente el contrato si se decide que debe ser una lista; ambos canales deben coincidir y tener tests de contrato.

## Triage

**Component:** MCP status contract
**Suspected files:** internal/mcp/tools.go, docs/openapi.json

The MCP adapter returns []string under a field documented and served by HTTP as an integer, causing contract-incompatible status responses.

**Verification (triage recommends):** test-first — Call MCP status with unreleased branches and assert unreleased_branches is an integer matching the HTTP contract while branch names use a separate field.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-008 — Apply project budget max_tokens and other named caps to runs

**Implements:** SPEC-031

Fixes B-019.

## Reported

What happened: after three budget pauses at the default 3M token cap, project.toml gained [budget] max_tokens = 25000000 — and the very next run (r-20260815-044232-eggr) paused at 3,006,404 >= 3,000,000 anyway. The run's token limit came from the global defaults, ignoring the project's setting; only max_usd from the project block appears to reach runs. A config key that accepts a value and silently doesn't apply it is a promise-vs-delivery break (the B-002 class, in TOML).

Expected: the project [budget] block governs its runs for every cap it names (tokens, usd, wallclock, turns), overriding global defaults — or the loader rejects the keys it will not honor, so the person finds out at write time instead of at the fourth pause.

## Triage

**Component:** budget

The project accepts max_tokens but silently ignores it, causing runs to pause at the global token limit instead of the configured project limit.

**Verification (triage recommends):** test-first — Configure a project budget max_tokens above the global default and verify its run uses the project token cap.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-009 — Only mark bugs fixed after an accepted BUILD run

**Implements:** SPEC-067

Fixes B-009.

## Reported

What happened: B-007 was promoted to T-002 and its TDD chain started. When the test-first run was auto-accepted (accepted by auto:tdd — the red test landing, committed), the engine's task-accepted hook moved B-007 in_progress → fixed at 02:23:58, while the chained BUILD was only just starting. The board showed the bug fixed with zero fix built; anyone reading it during the build window sees a false record.

Expected: the task-accepted → bug-fixed transition should fire only for accepted BUILD runs (the work that answers the report). An accepted test-first is a promise recorded, not a fix delivered — the bug should stay in_progress until the build lands.

## Triage

**Component:** workflow state transitions

The task-accepted hook falsely marks a bug fixed when only its red test has been accepted, before any implementation is built.

**Verification (triage recommends):** test-first — Accept a chained test-first run and verify the bug remains in_progress until the chained BUILD run is accepted.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-010 — Preserve an uncapped wallclock budget for chat after merging project settings

**Implements:** SPEC-031

Fixes B-022.

## Reported

The deliberate zeroing of MaxWallclockS for chat (to avoid capping person thinking time) can now be overridden by a project that sets max_wallclock_s, since projectBudget will merge it in via mergeBudget's >0 check.

Where: internal/service/chat.go:224

Suggested fix: If chat should never have a wallclock cap, zero out MaxWallclockS after the projectBudget call; if this is intentional per the task, add a comment noting the project can opt chat into wallclock.

Found by glm52 reviewing T-008 in run r-20260815-153336-ze43 (verdict: approve).

## Triage

**Component:** chat budget
**Suspected files:** internal/service/chat.go

Chat unexpectedly inherits a project wallclock cap despite explicitly disabling wallclock limits to avoid counting the person's thinking time.

**Verification (triage recommends):** test-first — Configure a positive project max_wallclock_s and verify chat retains MaxWallclockS=0.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-011 — Govern advisor replies through contract validation and record advice failures or timeouts

**Implements:** SPEC-053

Fixes B-016.

## Reported

What happened: T-004's test run asked a question; the advisor (atom-local, 2.5 min) delivered its unfiltered internal monologue as the advice — "We need answer user's request... Need decide... We need be careful..." — the opposite of the prompt's contract (2-8 sentences, decisive, no preamble). The advise() path is a one-shot chat with none of the rails every other turn gets: no contract parsing, no repair pass with the error named, no think-splitter separating deliberation from answer. Separately, when advice never arrives the card degrades silently — nothing on the record says whether the advisor is still thinking or died.

Expected: the advisor's reply passes the same governance as any contracted turn — enforce the shape, repair once with the violation named, strip thinking; and an absent advice stamps its cause (advice_failed with the error, or none within a deadline) so the degraded card can say why.

## Triage

**Component:** advisor
**Suspected files:** internal/service/advisor.go, internal/service/lifecycle.go

The one-shot advisor path stores raw model output and silently drops failed or timed-out advice, allowing ungoverned text and unexplained degraded cards.

**Verification (triage recommends):** test-first — Stub the advisor with deliberation or an error and verify repaired advice is concise while failure metadata records its cause.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-012 — Add a configurable advisor seat and pass relevant project documents to advisor prompts

**Implements:** SPEC-053

Fixes B-017.

## Reported

What happened: pickAdvisor falls back to the run's architect seat — in this project that resolved to atom-local, the slowest and weakest duckling, advising by accident of roster resolution while luna sat idle one seat over. And the advisor's own monologue confessed "We don't have actual spec": REQ-053 promises the advisor "cites the project's own spec when that decides the matter", but the advise() prompt carries only the question and options — none of the documents the asker had.

Expected: an advisor role in the roster (configurable like scribe/triager, defaulting to a fast seat), and the advise prompt carrying the same document context the asking run holds — or at least the spec outline plus the sections the question touches. A promise the prompt makes ("cite the spec") must be a capability the prompt delivers.

## Triage

**Component:** advisor roster and prompt context
**Suspected files:** internal/service/advisor.go, internal/config/config.go, internal/service/defaults.go

pickAdvisor hardcodes the architect fallback and advise only includes task text, question, and options, so the advisor can use an unintended slow seat without the documents its prompt requires.

**Verification (triage recommends):** test-first — Exercise advisor selection and assert its request includes the asking run's relevant spec/document context.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-013 — Emit webhook notifications for operator-relevant run transitions

**Implements:** SPEC-055

Fixes B-020.

## Reported

What happened: across every run this session, the MCP operator learned that a run finished, paused, or asked a question only by polling status/run_get on a guessed cadence. There is no completion or distress signal on the MCP surface: a run that pauses on a question at minute 2 waits silently until the next poll; a run burning GPU in a failure streak announces nothing. The human gets the desktop's live view; the agent gets whatever it thinks to ask for, whenever it thinks to ask. This is the observability asymmetry named in tonight's design conversation — the agent sees problems only after hitting the tree.

Expected: outbound notifications (the SPEC-055 webhook machinery already exists end to end, and Hermes already listens on a webhook) fire on the operator-relevant transitions — run ended (with verdict), paused (with pending_kind), question asked, distress (failure streaks, repetition_loop, budget pause). The operator subscribes; the engine interrupts. Polling stays as fallback, not as the only sense.

## Triage

**Component:** MCP notifications

The MCP surface is poll-only, so operators receive no outbound signal when runs finish, pause, ask questions, or enter distress states.

**Verification (triage recommends):** test-first — Trigger run ended, paused, question, and distress transitions and assert the subscribed webhook receives the expected event payloads.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-014 — Add ducklings, mode, and agent_turns overrides to stage_start and bug_triage

**Implements:** SPEC-021

Fixes B-005.

## Reported

What happened: driving the adopt over MCP, the triager was seated on a slow duckling — the only remedy was abort, hand-edit project.toml, relaunch (three steps and a file a remote operator cannot touch). Later the spec architect underdelivered its 19-section assignment; the levers that exist for exactly this — per-run seat picks, mode (solo/council/sectioned), agent turn caps, all present in StageRequest and the desktop's chips — have no MCP parameters. stage_start takes only project_id/stage/brief/adopt; bug_triage only project_id/bug_id.

Expected: stage_start and bug_triage accept the same per-run overrides the desktop offers (ducklings, mode, agent_turns), so an agent operator can act on a report-card's SUGGESTION without leaving the MCP surface. Recommend, never route — the operator picks, the record says so.

## Triage

**Component:** MCP tools
**Suspected files:** internal/mcp/tools.go, internal/mcp/mcp_test.go

The MCP schemas and handlers omit per-run seat, mode, and turn-cap controls that the underlying stage request already supports.

**Verification (triage recommends):** test-first — Call both MCP tools with ducklings, mode, and agent_turns and verify the options reach the per-run engine request.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-015 — Write .gitattributes with union merge rules at project init

**Implements:** SPEC-008

First tranche of B-006. In `ProjectInit` (internal/service/service.go), beside the existing `vcs.EnsureGitignore` call, write a `.gitattributes` at the project root declaring `merge=union` for the append-only operational files that concurrent task branches both touch: `.ducklab/bugs/audit.jsonl` and any other engine-owned JSONL ledgers (but NOT `project.toml`, the lifecycle docs, or the SQLite db, which stays gitignored). Add an `EnsureGitattributes` helper in `internal/vcs` mirroring `EnsureGitignore` (idempotent, preserves existing content, creates the file when absent). Done when a focused test initializes a project and asserts `.gitattributes` exists with a `merge=union` line for the audit ledger, and a second init leaves the file unmodified (no duplicate lines). Scope is small: file creation and one test only — no merge behavior change, no UI.

### T-016 — Surface a discoverable Reopen action for fixed bugs and accepted tasks

**Implements:** SPEC-017, SPEC-051

Second tranche of B-006 (surface/UI only; no orchestration). Make the already-legal "reopen" move discoverable instead of insider knowledge: `ProjectNext`/`nextSteps` in internal/service/guide.go gains a computed step when a project has bugs in `fixed` awaiting verification or an accepted task whose redo is permitted, each carrying a stable id, outcome-language action, reason, and the object ref a client links to its own button. Expose the matching door on the MCP surface (a `bug_reopen` / reopen affordance consistent with the existing `bug_move` and the redo guard's consent language) so an agent operator sees the same path the desktop shows. Do not change any transition table or run behavior here. Done when a test drives a project into the fixed-bug / accepted-task state and asserts the guide step appears with the correct ref, and the MCP tool list/schema exposes the reopen affordance.

### T-017 — Orchestrate safe test-first redo/relaunch with a required note and an audit entry

**Implements:** SPEC-020, SPEC-048, SPEC-008

Third tranche of B-006 (orchestration; depends on the previous two). Redoing an accepted task currently needs five pieces of insider knowledge; make the redo/relaunch of a test-first chain a single safe, attributable door. When a redo targets an accepted test-first task, the engine must: require a human-supplied `note` saying why (the consent the redo guard already names), refuse a dirty tree and any open run for the task, land the redo as a fresh chained test→build rather than fresh work against committed code, and append a signed audit entry (who, through which door, the note, the prior accepted SHA) to the append-only ledger beside the acceptance it does not erase — reusing the `appendBugAudit`/audit pattern. Reuse the existing `Redo` consent in `RunRequest`/`TestFirstRequest`; do not weaken the accepted-task refusal. Done when a test redoes an accepted test-first task with a note and asserts the chained test relaunches, the note and prior SHA are recorded on the run, and a signed audit line is appended; and a redo without a note is refused with the reason named.

### T-018 — Retire-test cleanup path and a cap on redo commits per task

**Implements:** SPEC-047, SPEC-032

Fourth tranche of B-006 (cleanup and limits), kept separate per the change. Two bounded pieces: (a) a cleanup path that, when a redo supersedes a still-unbuilt committed test, retires that stale test through the existing deterministic `TestRetire` inverse-patch machinery rather than leaving the suite red — refusing with the verdict first when the build already landed, exactly as SPEC-047 requires; (b) a per-task bound on redo commits so an accepted task cannot accumulate unbounded redo/relaunch commits without a person noticing — the cap names itself in the refusal when reached. **Assumption:** the architect confirmed retire-test/cleanup belongs in its own tranche, as the change allows; if it should fold into the orchestration tranche instead, drop (a) here and widen that task. Done when a test redoes a task whose prior test never built and asserts the stale test is retired (revert SHA recorded, queue hold released), and a separate test drives a task to the redo-commit cap and asserts the next redo is refused with the limit named.

### T-019 — Restore the working tree when aborting a paused or failed build

**Implements:** SPEC-064

Fixes B-023.

## Reported

What happened: a build run was aborted while paused, but the working tree was not restored. The retire-test action then rejected because the tree was dirty, even though the abort path was expected to clean up its own changes. Expected: aborting a paused/failed build restores the working tree according to the documented promise "restores working tree on rejection/failure", or explicitly records and exposes any unclean residue so the operator can recover it.

## Triage

**Component:** build run lifecycle
**Suspected files:** internal/service/service.go, internal/vcs/vcs.go

Aborting a paused build leaves model changes behind, violating the cleanup promise and blocking the documented recovery workflow.

**Verification (triage recommends):** test-first — Abort a paused run after it modifies a tracked and untracked file, then assert the tree matches the pre-run snapshot and retire-test can proceed.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-020 — Add actionable Clean or Commit recovery controls to the retire-test error state

**Implements:** SPEC-062

Fixes B-024.

## Reported

What happened: retire-test reported the recovery instruction "commit or clean them" when the working tree was dirty, but the UI offered neither a Clean action nor a Commit action from that error/card. This leaves the operator at a click-distance dead end: the error names the required remedies, but the product exposes no path to execute either one. Expected: provide actionable Clean and/or Commit controls in the relevant UI state, or route the operator directly to the existing recovery action.

## Triage

**Component:** retire-test UI

The reported recovery error is reproducible and leaves operators without an in-product way to perform either required remedy.

**Verification (triage recommends):** test-first — Run retire-test with a dirty working tree and verify the resulting error state exposes or routes to a Clean or Commit action.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-021 — Expose every legal abort and reject action on paused run cards

**Implements:** SPEC-062

Fixes B-025.

## Reported

What happened: a paused run card exposed only Resume, while Abort/Reject was not available on the pause card. To stop the run, the operator had to navigate up to the task level and find Abort elsewhere. Expected: the pause card should expose every legal decision for that paused run, including Abort/Reject where applicable, so the operator can act at the point where the decision is presented.

## Triage

**Component:** paused run decision UI
**Suspected files:** internal/service/next.go, frontend/src/components/DecisionCard.tsx, frontend/src/components/WaitingCard.tsx, frontend/src/views/Board.tsx, frontend/src/views/RunView.tsx

Paused gate cards omit a legal stop/decision action, forcing operators to leave the card and increasing the risk of unintended continuation.

**Verification (triage recommends):** test-first — Render a paused run card with legal abort/reject actions and verify both controls appear and invoke their respective endpoints.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-022 — Include blocked reasons in MCP task_list rows

**Implements:** SPEC-021

Fixes B-026.

## Reported

What happened: task_list over MCP reports a task as blocked but does not include the reason for the blocked state. In this incident, the blocked flag was inherited from an older failed run for a recycled task ID, not a dependency or real execution lock; the engine still offered test_first. Without the reason string, the operator interpreted the state as a genuine dependency/candado and nearly stopped. Expected: every blocked task includes a concise blocked_reason (or equivalent) explaining the actual blocker, its source, and the legal next action.

## Triage

**Component:** MCP task listing
**Suspected files:** internal/mcp/tools.go, internal/mcp/mcp_test.go, internal/service/stages.go

The engine already derives a blocked explanation, but MCP task_list drops it while formatting compact rows, misleading operators about why work is blocked.

**Verification (triage recommends):** test-first — Call task_list with a blocked task and assert its output includes the blocker reason and available next action.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-023 — Prevent historical runs from affecting recycled task IDs after body revisions

**Implements:** SPEC-063

Fixes B-027.

## Reported

What happened: recycling a task ID after rewriting its body allowed historical runs from the previous meaning of that ID to influence the current derived state. The new T-015 tranche was marked with a blocked/last-run-failed state inherited from the older failed Reopen task, even though it was a new .gitattributes-at-init task and the engine offered test_first. Expected: rewriting a task body must either mint a new task ID, or derived state must ignore runs created before the latest task-body revision. Historical runs should remain auditable but must not contaminate the status of a semantically different task.

## Triage

**Component:** task state
**Suspected files:** internal/service/bugs.go, internal/runlog/runlog.go

Reusing a task ID causes stale failed-run state to misclassify a semantically new task and can drive incorrect execution decisions.

**Verification (triage recommends):** test-first — Rewrite a task body under the same ID after a failed historical run and verify the derived state ignores that run.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-024 — Reject compile-red test specifications unless the package builds and the new test fails by assertion

**Implements:** SPEC-066

Fixes B-028.

## Reported

What happened: T-018's test-first run wrote redo_cleanup_cap_test.go referencing an undefined type (RunView). go test failed with a COMPILE error, the gate read red, Until gate==red was satisfied, and auto:tdd committed it as the landed specification. But compile-red is not assertion-red: it breaks the entire service package, every other test in it stops running, and the chained build's job silently changes from "turn one red assertion green" into "make the tree compile again" — luna spent 43 minutes and 55 failed patches there and the run died FAILED.

Expected: the test stage's success criterion distinguishes the two reds — the package must BUILD (go vet or go build on the touched packages green) and the new test must fail on an ASSERTION. A compile error is a malformed specification and should bounce back to the test-writer with the compiler's message, exactly like a contract repair.

## Triage

**Component:** test-first gate
**Suspected files:** internal/service/testfirst.go, internal/verify/verify.go, internal/strategy

The test-first gate treats any nonzero test exit as a valid failing specification, allowing compile errors to land and derail the subsequent build.

**Verification (triage recommends):** test-first — Run test-first with an undefined test type and verify it is rejected as malformed while a compiling assertion failure is accepted as red.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-025 — Add a per-file consecutive fs_patch failure brake with health reporting and fs_write guidance

**Implements:** SPEC-042

Fixes B-029.

## Reported

What happened, three times: T-002's first build — 83 failed fs_patch on internal/agent/agent.go; T-015's rounds — the fs_patch/backtick fight k3 tabulated; T-018's build — 55 failed fs_patch plus 11 red verify_runs across 43 minutes. In each case the implementer varied the search string slightly on every attempt, so the identical-repeat brake (which requires byte-identical calls) never fired, and the run burned its budget fighting the patch tool instead of the problem. The known-good remedy — read the whole function with fs_read and replace it with fs_write — was applied only when a human put it in a redo note.

Expected: a fuzzy streak brake at the tool layer — same tool + same file + failing, N consecutive (say 5) — that REFUSES the next attempt with the remedy named: "fs_patch has failed 5 times on this file; read the full section and rewrite it with fs_write instead of patching". Same family as the repeat brake and the gate brake: deterministic, teaches the exit, costs nothing when healthy. The streak counter belongs in the health surface too, so an operator sees the fight before the budget is gone.

## Triage

**Component:** tool dispatch
**Suspected files:** internal/agent/agent.go

Repeated fs_patch failures can exhaust a run's budget without intervention, and no existing open bug covers this per-file fuzzy failure brake.

**Verification (triage recommends):** test-first — Simulate five failing fs_patch calls against one file, then assert the next call is refused with the read-and-rewrite remedy and streak state is exposed.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-026 — Use git-derived build version and provenance across CLI, engine, and MCP

**Implements:** SPEC-061

Fixes B-033.

## Reported

What happened: preparing the first release cut, the draft proposed v0.1.0 while every running surface announced 0.4.0 — a number that exists only as hand-maintained strings: internal/mcp/mcp.go:128 hardcodes serverInfo version "0.4.0", the engine reports 0.4.0 into engine.json, and internal/cli/cli.go carries Version = "dev". No git tag existed at all. The moment v0.5.0 is cut, the MCP handshake will still tell every client "0.4.0" — an instant lie of the B-002 family (a surface promising what the record contradicts). This also cost real time earlier: the two-day "unknown tool budget_lift" hunt would have ended in seconds if hermes tools could show WHICH ducklab version sat across the pipe.

Expected: one source of truth — the git tag. The Makefile already injects build.Branch and build.Commit via ldflags (B-018's provenance work); add build.Version from `git describe --tags --always`, and have the CLI Version, the engine's reported version, and the MCP serverInfo all read it. Hardcoded version strings are deleted, and `ducklab mcp serve` announces its provenance (version+commit) in serverInfo so an operator can identify a stale process without /proc archaeology.

## Triage

**Component:** build/version provenance
**Suspected files:** internal/build/build.go, Makefile, internal/cli/cli.go, internal/mcp/mcp.go, cmd/ducklab/main.go, cmd/ducklab-engine/main.go

Hardcoded or independently defaulted version values cause released binaries and MCP handshakes to advertise stale or inconsistent provenance.

**Verification (triage recommends):** test-first — Build with a tagged commit and verify CLI output, engine.json/API version, and MCP initialize serverInfo report the same version and commit.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-027 — Map human answer events to running status and clear pending state

**Implements:** SPEC-062

Fixes B-030.

## Reported

What happened: T-018's test paused on a question; the human answered from the desktop; the engine resumed the run (state.json: running, pending none, work progressing) — but Now kept listing it as waiting for answer. The engine emits a "human" event on answer, and the frontend store's applyEvent does not map it: the only status-changing events it hears are run_queued, run_started (queued only), human_needed, error and run_end. The paused→running transition caused by an answer has no event the store acts on, so the stale card survives until a refetch.

Expected: the same cure the triage ending got — either the store maps the human/answer event to status running + pending cleared, or the engine emits an explicit run_resumed on answer and resume alike. Every status transition must have a stream event the store believes; the pattern (two occurrences now: triage run_end, answer resume) suggests auditing all transitions against the store's event map once, rather than patching them one stale card at a time.

## Triage

**Component:** frontend run store
**Suspected files:** frontend/src/store/runs.ts, frontend/src/store/runs.test.ts

The frontend receives the human answer event but does not update the paused run, leaving Now stale until refetch.

**Verification (triage recommends):** test-first — Apply a human event to a paused run and expect status running with pending_kind cleared.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-028 — Cover frontend tests in the gate and diagnose uncovered test-first paths

**Implements:** SPEC-066

Fixes B-032.

## Reported

What happened: T-020 (a frontend task — Clean/Commit recovery controls on the failure card) failed test-first three times. On the last attempt luna wrote the CORRECT specification — a vitest test in frontend/src/views/views.test.tsx asserting the recovery control exists, plus the negative case — and the run still died: the project gate is `go test ./...`, which never runs vitest, so the red test could not turn it red. The engine then reported "the gate is still green, so the new test asserts nothing that is not already true" — a false diagnosis that blames the test, poisons any redo note drafted from it (B-031's advisor would inherit the lie), and makes every frontend task ungateable: no test-first can land red, no build can prove green, for anything in frontend/.

Expected, two layers: (a) the project gate must cover every language the plan's tasks touch — for ducklab that means the frontend suite joins the command; and (b) when a test-first lands green, the engine should check WHERE the new test lives before claiming it asserts nothing: a new test file outside the gate's reach is a gate-coverage error, not a specification error, and the message should say so ("the new test lives in frontend/, which the gate never runs — widen the gate or move the test"). The internal RunRequest.verify override exists but has no MCP or launcher surface; per-task gate override would let UI tasks gate on vitest without taxing every Go run.

## Triage

**Component:** verification gate
**Suspected files:** internal/verify/verify.go, internal/service/testfirst.go, internal/service/service.go, internal/service/gate.go, internal/config/config.go, internal/engineapi/engineapi.go

The configured Go-only gate skips the frontend suite, making frontend test-first runs ungateable and producing a false diagnosis when new tests pass outside the gate.

**Verification (triage recommends):** test-first — A mixed Go/frontend project should execute vitest and report a frontend test outside the configured gate as a coverage error rather than a vacuous specification.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-029 — Resolve sibling paused triage runs when accepting a newer triage

**Implements:** SPEC-067

Fixes B-008.

## Reported

What happened: a triage run aborted mid-batch (generation loop) still paused at its gate holding its partial proposals — reasonable, no error discards work. But after a SECOND complete triage of the same bugs was accepted, the partial sibling stayed decidable, offering accept on classifications already applied by the newer run. The human had to notice and order the reject; an unattended operator could have double-applied B-004.

Expected: the same courtesy artifact stages already have — accepting a triage resolves sibling paused triage runs whose bugs it covered, recorded as superseded. The stage machinery (resolveSuperseded) exists; triage accept does not use it.

## Triage

**Component:** triage lifecycle

A paused sibling can reapply classifications already accepted by a newer triage, creating a preventable double-decision hazard.

**Verification (triage recommends):** test-first — Accept a complete triage covering the same bugs and assert that an older paused sibling is marked superseded and no longer decidable.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-030 — Mark all accepted unimplemented tasks as spec debt and wire spec_settle to document them

**Implements:** SPEC-038

Fixes B-036.

## Reported

What happened: 25 of the plan's 29 tasks carry no Implements field — every task born from bug promotion, which is ALL of v0.5.0 — yet spec-debt reports zero. The debt marker is applied only to amendment-born tasks (the Extend flow); promoted tasks are invisible to it, so spec_settle looks at a board full of undocumented as-built behavior and sees nothing to settle. The traceability spine has a blind spot exactly where the project does most of its work: the spec is one full release stale (budget_lift, repetition detector, contract-aware caps, advisor governance, webhook notifications, tree restore, provenance — none covered) while the machinery built to catch that reports clean. Same family as B-009 and B-034: a counter reading the wrong source.

Expected: any accepted task with no Implements wires — whatever flow created it — wears spec-debt, and the settle prompt documents it as-built with a Covers: field wired back, exactly as the amendment flow already does. With the marker honest, one spec_settle run brings the spec current after every release; without it, "0 debt" is the most expensive kind of lie — the one that cancels the cure.

## Triage

**Component:** spec-debt traceability
**Suspected files:** internal/artifact/trace.go, internal/stage/extend.go, internal/mcp/tools.go, internal/service/guide.go

The traceability system exempts promoted bug tasks from the no-Implements debt path, causing an entire release of undocumented behavior to report clean.

**Verification (triage recommends):** test-first — Promote a bug into an accepted task without Implements and verify spec-debt and spec_settle expose it with a Covers edge.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-031 — Draft and expose an advisor-attributed redo note on failed-run decision cards

**Implements:** SPEC-053

Fixes B-031.

## Reported

Jose's proposal, watching T-020's test fail twice: the decision card on a failed run says what happened ("the gate is still green, so the new test asserts nothing that is not already true") and then leaves the person to distill the lesson and write the redo note by hand. Every chain that converged after failing this week (T-002, T-018, excercise-tracker's T-132) converged BECAUSE of a hand-written redo note — the one link of the failure cycle the harness does not carry, though every input lives in the run record.

Expected: when a run fails (or a test-first lands green), the advisor drafts a redo note from bounded inputs — task body, failure detail, gate tail, diff summary — and it arrives as an editable draft on the decision card ("Retry with this note") and in run_get for MCP operators, attributed as advisor-drafted. Recommends, never decides: the person or operator edits, accepts or discards; only under yolo may the autopilot relaunch with it unedited. Same governance as B-016's advisor contract; same seat configuration as B-017.

## Triage

**Component:** advisor and run decision cards
**Suspected files:** internal/service/service.go, internal/service/advisor.go, internal/runlog/runlog.go, internal/mcp/tools.go, frontend/src/lib/runview.ts

Failed runs currently expose the failure but do not carry the bounded, editable advisor recommendation needed to make the next retry actionable.

**Verification (triage recommends):** test-first — A failed run with task, failure, gate-tail, and diff inputs should produce an editable redo draft in RunGet and the decision-card model without auto-deciding.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-032 — Raise or remove the 2048-token cap for json:decomposition contracts

**Implements:** SPEC-042

Fixes B-012.

## Reported

outputCapForContract caps ALL json:* contracts at 2048, including json:decomposition, whose 2–5 subtasks each carry a body that may legitimately exceed 2k tokens.

Where: internal/agent/agent.go:1109

Suggested fix: Either restrict the 2k cap to known classification contracts (json:triage) or raise the cap for json:decomposition, and add a test for the decomposition case.

Found by glm52 reviewing T-002 in run r-20260815-130948-qka6 (verdict: approve).

## Triage

**Component:** agent output caps
**Suspected files:** internal/agent/agent.go

The shared json:* cap can truncate legitimate decomposition responses containing subtasks with bodies longer than 2048 tokens.

**Verification (triage recommends):** test-first — Exercise outputCapForContract with json:decomposition and a larger declared limit, expecting a cap that accommodates decomposition bodies.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-033 — Thread the contract through applySampling and enforce its output cap

**Implements:** SPEC-042

Fixes B-011.

## Reported

applySampling (used by contract repair calls) still uses outputCap without the contract, so a repair of a json:triage turn runs with the full declared cap (e.g. 20000) instead of 2048.

Where: internal/agent/agent.go:1346

Suggested fix: Thread turn.Contract into applySampling and call outputCapForContract there too.

Found by glm52 reviewing T-002 in run r-20260815-130948-qka6 (verdict: approve).

## Triage

**Component:** agent contract repair sampling
**Suspected files:** internal/agent/agent.go

Contract repair requests ignore the turn contract when setting MaxTokens, allowing JSON triage repairs to use an excessive output cap.

**Verification (triage recommends):** test-first — Trigger a JSON contract repair with a large configured max_tokens and assert the repair request uses the contract-specific cap.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-034 — Run repetition detection in the unsupported-streaming fallback

**Implements:** SPEC-042

Fixes B-013.

## Reported

The unsupported-streaming fallback path (ChatStream returns ErrUnsupported) does not run the repetition detector on the assembled Chat response, so a loop is only caught there if OnDelta/OnReasoning are both nil.

Where: internal/agent/agent.go:718

Suggested fix: Run a repetitionDetector over resp.Choices[0].Message.Content in the ErrUnsupported fallback before returning.

Found by glm52 reviewing T-002 in run r-20260815-130948-qka6 (verdict: approve).

## Triage

**Component:** LLM generation streaming
**Suspected files:** internal/agent/agent.go

The ErrUnsupported fallback emits assembled content without applying the repetition detector, allowing repetitive responses to pass undetected.

**Verification (triage recommends):** test-first — Make ChatStream return ErrUnsupported with repetitive assembled content and verify chatMaybeStreaming returns a repetition error.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-035 — Remove unused accumulated response storage from the repetition detector

**Implements:** SPEC-042

Fixes B-014.

## Reported

The text field accumulates the full response (d.text += s) but is never read; it is dead storage that grows unboundedly for long replies.

Where: internal/agent/repetition.go:15

Suggested fix: Remove the text field since Repeated() returns d.repeated, not d.text.

Found by glm52 reviewing T-002 in run r-20260815-130948-qka6 (verdict: approve).

## Triage

**Component:** agent
**Suspected files:** internal/agent/repetition.go

The detector retains the entire streamed response unnecessarily, causing avoidable unbounded memory growth during long replies.

**Verification (triage recommends):** build-only — The issue is dead internal state with no externally observable behavior; compilation verifies removal does not break references.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-036 — Align the ProjectNext if statement with the preceding assignment

**Implements:** SPEC-051

Fixes B-015.

## Reported

The if statement has an extra leading tab (over-indented relative to the assignment above it), a gofmt violation.

Where: internal/mcp/tools.go:534

Suggested fix: Remove the extra tab so the if aligns with the preceding entry assignment.

Found by glm52 reviewing T-003 in run r-20260815-132712-vggm (verdict: approve).

## Triage

**Component:** MCP tools
**Suspected files:** internal/mcp/tools.go

The extra indentation is an isolated, actionable formatting defect in internal/mcp/tools.go.

**Verification (triage recommends):** build-only — This is a formatting-only gofmt violation with no runtime behavior to test.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-037 — Make release guidance count only accepted work included in the latest release

**Implements:** SPEC-061

Fixes B-034.

## Reported

What happened: after cutting v0.5.0 (tag created, notes promoted, main merged), the guide still says "Cut a release — 8 accepted task(s) await shipping". acceptedUnreleased() counts accepted tasks whose recorded Branch is non-empty and not main — a test of branch-name persistence, not of release reachability. Deleting all 22 merged ducklab/* branches changed nothing: the names live in the task/run records. The step is immortal — it will offer a release forever, and its count (8) bears no relation to what v0.5.0 actually shipped (22 tasks).

Expected: released work stops counting. Either the cut records the released task set (or tag) and the counter excludes tasks whose accept commit is an ancestor of the latest release tag (git merge-base --is-ancestor), or the accept record gains a released_in field stamped at cut. The guide's numbers must come from the same truth the release itself computed.

## Triage

**Component:** release tracking
**Suspected files:** internal/service/guide.go, internal/mcp/tools.go, internal/service/release.go, internal/release/release.go

The guide and MCP status derive unreleased work from persistent branch names instead of the release inventory or commit ancestry, so released tasks remain permanently actionable.

**Verification (triage recommends):** test-first — After cutting a release, accepted tasks included in its shipped inventory must no longer appear in guide or MCP unreleased counts.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-038 — Skip human-only guide steps and start the first lawful mechanical task

**Implements:** SPEC-049

Fixes B-037.

## Reported

What happened: yolo autonomy, autopilot on, 3 tasks sitting in Todo — started: 0, last_action "needs you: Promote B-034 to a task, or park it". The autopilot follows the guide's first step; when that step is a human decision it idles entirely, even though startable mechanical steps (the Todo tasks) sit right below. One unpromoted bug becomes head-of-line blocking for a whole unattended session — under yolo, the mode built for overnight autonomy, a single human gate turns the autopilot into a decoration.

Expected: idling at a human gate means idling ON THAT DECISION, not on the world — the autopilot scans past human-only steps (bug promotions, verifications, release cuts) to the first MECHANICAL step it may lawfully start, and its status names both: "waiting on you for B-034; meanwhile started T-025". The human queue and the machine queue are different queues.

## Triage

**Component:** autopilot
**Suspected files:** internal/service/autopilot.go, internal/service/guide.go, internal/service/autopilot_test.go

A single human-gated first guide step blocks unattended mechanical work despite startable Todo tasks, defeating yolo autonomy.

**Verification (triage recommends):** test-first — Enable yolo autopilot with a human-only promotion step followed by Todo mechanical tasks and assert it starts the mechanical task while retaining the human wait status.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-039 — Resolve launcher seats from the roster and display seat provenance

**Implements:** SPEC-026

Fixes B-035.

## Reported

What happened: T-024's TDD launch started its test stage with glm52 as implementer although the project roster pins implementer = luna. The TddLaunch chips prefill from the Settings mode line-up (a glm52 pick saved from some earlier run), and launching without touching the chips sent that prefill as an EXPLICIT per-run ducklings override — which outranks the roster by design. The person believed the roster would decide; the form had already decided from a different memory, with no indication on the chip of where its value came from. Two seat memories (Settings mode defaults, project roster), silent precedence, invisible provenance. The chained build carried ['luna','glm52'] while the test ran glm52 — same launch, two different seat sources.

Expected: the launcher prefills from the RESOLVED roster (project pins first, then global), and Settings mode line-ups apply only where explicitly saved; an untouched chip should mean \"the roster decides\", not \"repeat my last override\". At minimum each chip shows its provenance (roster / Settings / picked now) so a silent decision becomes a visible one. Small-operator corollary: the same precedence applies to MCP launches with omitted ducklings — omitted must mean roster, and the record should say which source seated each role.

## Triage

**Component:** launcher roster resolution
**Suspected files:** frontend/src, internal/config, internal/service

Untouched launcher chips silently turn stale Settings defaults into explicit overrides that outrank the project roster, causing the wrong duckling to execute a role.

**Verification (triage recommends):** test-first — Launch a TDD run with a project implementer pin and a conflicting saved Settings lineup, then verify the untouched chip and submitted seats use the roster and expose provenance.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-040 — Prioritize Verify guidance over Reopen for fixed bugs awaiting verification

**Implements:** SPEC-051

Fixes B-038.

## Reported

What happened: with B-004, B-007 and B-018 sitting in fixed (awaiting the human's fixed→verified judgement), the guide's next steps read "Reopen B-004 — send it back for more work", three in a row. Reopen is the exception path — for a fix that missed expectations — yet it is offered as the day's plan while the ordinary next step for a fixed bug, Verify, appears nowhere. A literal operator (the autopilot follows this list; so would Elena) is being advised to unwind good work three times.

Expected: a fixed bug's guide step is "Verify B-004 — confirm the fix answers the report" (the I2 judgement, human-only), with Reopen mentioned as the alternative inside that decision, not as the headline. Reopen earns the headline only when something signals dissatisfaction — a reopened sibling, a failed verification, a human note.

## Triage

**Component:** guide guidance
**Suspected files:** internal/service/guide.go, internal/service/guide_test.go

The guide deterministically recommends the exceptional Reopen action for ordinary fixed bugs instead of the required human-only Verify decision.

**Verification (triage recommends):** test-first — Build a guide snapshot with fixed bugs and assert each presents Verify as the headline while Reopen is only an alternative.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-041 — Unify task status derivation and scope recycled-ID history to per-task body changes

**Implements:** SPEC-063

Fixes B-039.

## Reported

What happened: after the engine restart with T-023's fix (historical runs must not contaminate recycled task IDs) active, the board shows 39 todo / 1 accepted — every legitimately accepted task (T-001..T-038) reverted to todo, presumably because adding T-039/T-040 regenerated the plan and re-stamped every task body, making the status derivation discount ALL prior runs as \"historical\". Meanwhile the launch guard still derives from the raw run records: the autopilot (T-038's skip working correctly) picked falsely-todo T-001 and the launch refused with \"T-001 is already accepted; its work is committed\". Two derivations of the same fact now disagree, the board lies about 38 tasks, and the autopilot starves between them.\n\nExpected: the recycled-ID discount keys on a per-task identity change (its own body edit), never on a whole-plan regeneration; and there is exactly ONE status derivation shared by the board, the guard, and the autopilot. When two rules must read the same history, they must be the same rule.

## Triage

**Component:** task status derivation

A plan regeneration falsely reverts accepted work and causes the board, launch guard, and autopilot to disagree, blocking reliable operation.

**Verification (triage recommends):** test-first — Regenerate a plan after accepting tasks, then add tasks with recycled IDs and verify the board, launch guard, and autopilot all report the same accepted state.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-042 — Reject acceptance when the committed checkout omits required ignored files

**Implements:** SPEC-060

Fixes B-040.

## Reported

What happened: the unanchored pattern "build/" in .gitignore matched internal/build/ — the very package B-033's task created. The accept's git add -A respects gitignore, so the accepted commit shipped WITHOUT its core package; the gate stayed green because the untracked files still sat on disk. Three lies at once: a fresh clone would not compile, a git clean would have deleted the version system, and the accepted diff on record was not the work reviewed as running. Found only because a later explicit git add refused the path. Hand-fixed in 8c11234 (pattern anchored to /build/, package committed, arch whitelist updated).

Expected: an accept must not leave referenced work untracked in silence — after committing, check for untracked files under paths the committed code imports or touches and refuse naming them ("internal/build/ is ignored by .gitignore:10 but the committed code imports it"). Cheapest honest form: the gate must pass from a clean checkout of the accept commit, not from the working tree — the tree can hide what the commit lacks.

## Triage

**Component:** acceptance gate
**Suspected files:** internal/service/service.go

Acceptance can record a green run while its commit silently omits ignored source files, producing an unreproducible and uncompilable fresh checkout.

**Verification (triage recommends):** test-first — Accept a change importing an ignored package and verify the gate detects the missing file or fails from a clean checkout.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-043 — Add a safe engine version command and prevent live engine identity overwrite

**Implements:** SPEC-061

Fixes B-041.

## Reported

What happened: running ducklab-engine --version (or version) does not print-and-exit — the flag is unrecognized and the binary starts a full engine, which listened on a fresh port and OVERWROTE engine.json with its own pid/port/token before being killed. The real engine kept running but became unreachable to every new client: the file that names it now named a corpse. One curious human (or agent) asking \"which version are you?\" can decapitate the discovery mechanism.\n\nExpected: ducklab-engine --version / version prints build.Semver() + Provenance() and exits without binding a port or touching engine.json — and, independently, an engine should refuse to overwrite an engine.json whose recorded pid is still alive, so a second engine can never silently steal the identity of a live one.

## Triage

**Component:** engine startup and daemon discovery
**Suspected files:** cmd/ducklab-engine/main.go, internal/daemon/daemon.go

The unrecognized version command starts a second engine and unconditionally replaces engine.json, making the live engine undiscoverable.

**Verification (triage recommends):** test-first — Run ducklab-engine version and --version with an existing live engine.json, asserting version output, no listener or file mutation, and refusal to overwrite the live record.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-044 — Count accepted no_changes builds as settled and block their automatic relaunch

**Implements:** SPEC-063

Fixes B-042.

## Reported

What happened: T-036's fix was already in the tree (landed incidentally in an earlier commit). Under yolo, the autopilot launched it; the build found nothing to do (no_changes), passed, was accepted — and because a no_changes run does not count as built in the status derivation, T-036 stayed todo, so the autopilot relaunched it. Three identical empty runs in four minutes (23:35, 23:38, 23:39), and it would have burned all of max_tasks on air. A task whose work already exists can NEVER be settled by building it: the loop's own success condition is unreachable.

Expected: an ACCEPTED no_changes build settles the task — the acceptance is precisely the judgement that the work already exists, and the record carries it; the derivation should count it. Belt and suspenders: the autopilot refuses to relaunch a task whose previous run ended no_changes without a human note in between — repeating a question the tree already answered is the machine version of asking until you like the answer.

## Triage

**Component:** autopilot and task status derivation
**Suspected files:** internal/service/autopilot.go, internal/service/guide.go, internal/service/runs.go

An accepted no_changes build currently leaves its task todo, making autopilot repeatedly spend task slots launching an already-complete task.

**Verification (triage recommends):** test-first — Accept a no_changes build and verify the task becomes settled and autopilot does not launch it again without an intervening human note.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-045 — Isolate state environment variables for every verify_run process tree

**Implements:** SPEC-065

Fixes B-047.

## Reported

What happened: T-043's red test (for B-041) executed ducklab-engine version; the missing subcommand booted a full engine that inherited the gate's environment — the ENGINE'S OWN env — so its startup recovery read the real registry, found the running T-043 "orphaned", and checkpointed it: the run killed itself, identically on two attempts, and the spawned engine lingered as an orphan process for five minutes. The gate's child processes hold the keys to the harness that runs them: real XDG state, real registry, real engine.json. Any test or tool that spawns a product binary — or any binary that reads state on boot — can mutate the live system from inside a gate.\n\nExpected: verify_run scrubs the state environment for the gate process tree — XDG_CONFIG_HOME/XDG_DATA_HOME/XDG_STATE_HOME (and platform equivalents) pointed at a per-run throwaway — so a spawned binary sees an empty world; plus B-041's guard on the other side (an engine refuses recovery/engine.json when the recorded pid is alive). Defense on both doors: the gate must not hand out the master keys, and the engine must not accept a stranger claiming its house.

## Triage

**Component:** gate execution

A gate child can recover and mutate the live engine registry, causing self-termination and orphaned processes.

**Verification (triage recommends):** test-first — Run a gate-spawned state-reading binary and assert it receives per-run XDG state paths rather than the harness paths.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-046 — Bound streamed run state and debounce summary-only board refreshes

**Implements:** SPEC-062

Fixes B-044.

## Reported

What happened, twice in one day: after hours of heavy runs the desktop turns sluggish (275MB app RSS + 672MB webkit, low CPU — drowning in data, not compute). Two mechanisms compound: (a) the runs store keeps every streamed delta and reasoning string per run per turn for the life of the window — display-only state that is never trimmed, and a single 7M-token build streams megabytes into it (the B-014 disease, frontend edition); (b) the board refetches its entire payload — tasks, bugs WITH full histories and bodies, reports, gate — on every run status transition, a design sized for human-paced clicks that becomes a refetch storm when the autopilot chains runs every couple of minutes with 41 bugs on the board.

Expected: (a) finished turns drop their delta/reasoning strings (the message event already carries the durable content) and live ones carry a length cap; (b) the board's transition-triggered reload fetches summaries (the bug LIST needs no histories — detail on selection), debounced under bursts. A desktop that watches an autonomous night must be sized for the night, not for the click.

## Triage

**Component:** desktop runs/board state
**Suspected files:** frontend/src/App.tsx, frontend/src/api/client.ts

Unbounded client-side streamed content and repeated full-board fetches predictably exhaust memory during autonomous long-running sessions.

**Verification (triage recommends):** test-first — Simulate long streams and burst run transitions; assert completed payloads are discarded, live buffers cap, and reloads coalesce to summary requests.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-047 — Attribute yolo auto-accept decisions to the autopilot instead of human

**Implements:** SPEC-049

Fixes B-043.

## Reported

What happened: under yolo autonomy, T-036's three auto-accepted builds each recorded the event human {action: accept} in the same second as run_end — the record swears the person decided three times in three seconds while they were doing nothing of the kind. The whole audit design (REQ-021: decisions attributed as mcp:client, never human; signed bug moves; decide reasons on the record) rests on actor honesty, and the autopilot's own accepts break it. Attribution precedent exists everywhere else: auto:tdd, auto-triage, mcp:claude-code — the yolo accept is the one liar left.

Expected: an accept the autopilot makes under yolo records auto:yolo (or autopilot) as the actor, with the autonomy level noted — human is reserved for a human. An audit trail is only as good as its worst signature.

## Triage

**Component:** audit attribution
**Suspected files:** internal/service, internal/engine

Yolo acceptance corrupts the audit trail by attributing automated decisions to a human, violating the system's actor-attribution invariant.

**Verification (triage recommends):** test-first — Run an auto-accept under yolo autonomy and assert its recorded actor is auto:yolo with the autonomy level, never human.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-048 — Unify omitted-mode resolution across desktop, autopilot, and MCP launches

**Implements:** SPEC-044

Fixes B-045.

## Reported

What happened: reviewing tonight's builds, T-039 and T-040 (origin: autopilot) ran pair while T-042 (launched over MCP/desktop without an explicit mode) ran solo — same project, same phase, same settings. Three surfaces resolve an omitted mode from three sources: the desktop launcher prefills from Settings phase defaults (build=pair), the autopilot's empty-mode requests resolve to the project's habit, and an MCP test_build with mode omitted falls to the engine's hard default (solo). The person reads the run list and concludes the autopilot changed behavior; the truth is the doors disagree. Sibling of B-035 (seats): every omitted parameter needs ONE resolution rule shared by every entrance.

Expected: omitted mode resolves identically everywhere — Settings phase default, else project habit, else solo — and the run records which source decided it (mode: pair (settings)), so a mixed night of human, agent and autopilot launches reads as one system, not three.

## Triage

**Component:** launcher mode resolution

Each launch surface applies a different defaulting rule, causing identical omitted-mode runs to execute differently and misleading operators.

**Verification (triage recommends):** test-first — Launch equivalent omitted-mode requests through each entrance and assert settings, project-habit, and solo fallback precedence plus recorded provenance.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-049 — Recover orphaned restart checkpoints after a deadline and expose operator resume

**Implements:** SPEC-012

Fixes B-046.

## Reported

What happened: T-043's test run checkpointed at 00:15:39 with pending engine_restart — but the engine (started 00:05:26) never restarted; some restart request checkpointed the runs and then did not complete. The run sat parked mid-gate for 8+ minutes reading as \"very slow\" (the human's actual report), no notification fired (B-020), and when found, its only legal action was abort: resume is reserved for the engine's own startup recovery, so a minute of good work died to an orphan checkpoint. The record never says who requested the restart.\n\nExpected: a checkpoint-for-restart carries a deadline — if the engine is still alive N seconds later, it un-checkpoints its runs and resumes them itself, recording restart_abandoned; the restart REQUEST lands in the record with its requester; and a checkpointed run offers resume to operators, not only to the reborn engine — the recovery path should not care who walks it.

## Triage

**Component:** engine restart recovery

A failed restart can indefinitely park active work and discard progress because neither timeout recovery nor operator resume is available.

**Verification (triage recommends):** test-first — Request a restart without stopping the live engine, advance the deadline, and assert runs resume with requester and restart_abandoned recorded.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-050 — Preserve shared Go caches while isolating gate state and ignore download chatter in compile-red detection

**Implements:** SPEC-065

Fixes B-048.

## Reported

What happened: T-045 (B-047's fix) isolates the gate's process tree by pointing XDG_* AND HOME at a fresh TempDir removed after the gate exits. On Linux HOME determines GOPATH, GOMODCACHE and GOCACHE — so every verify_run now downloads the go toolchain (go.mod pins 1.25.0) plus every module and rebuilds every package cold, per gate, with the cache deleted afterward: minutes per verify, a hard network dependency, and download noise that T-024's compile-red detector misreads as \"the test specification does not compile\" (T-049's test-first died to exactly that false verdict).\n\nExpected: isolation of ENGINE STATE, not of build caches — the scrub keeps XDG/HOME pointed at the throwaway but explicitly sets GOPATH/GOMODCACHE/GOCACHE (and GOTOOLCHAIN's cache) to the real shared locations: content-addressed build caches carry no engine identity and are exactly what makes a gate fast and offline-capable. And the compile-red detector must ignore go's download chatter when classifying output.

## Triage

**Component:** gate execution

The gate's state-isolation change makes every verification slow, network-dependent, and capable of falsely rejecting valid test specifications.

**Verification (triage recommends):** test-first — Run verify with isolated XDG/HOME and assert Go cache paths remain shared and download output is not classified as a compile failure.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-051 — Prevent reject cleanup from restoring paths over commits landed after the run snapshot

**Implements:** SPEC-064

Fixes B-050.

## Reported

What happened: T-050's test run was rejected AFTER its legitimate work had been landed by hand in three commits (b3ed87c, 926e462, 6acc1d4). The reject restored the working tree to the run's snapshot — which predates those commits — so the tree silently rewound past HEAD: a committed test file showed as deleted, the classifier fix reverted in-tree, and the suites went green against the OLD world while HEAD held the new one. Recovered with git checkout -- . — but only because someone knew to look. The restore answers \"what did the RUN change?\" with \"whatever differs from my snapshot\", and everything committed since the snapshot becomes collateral.\n\nExpected: restore-on-reject reconciles against HEAD, not the raw snapshot — it reverts only paths the RUN touched beyond their state in HEAD, or refuses with a named reason when HEAD has advanced past the snapshot (\"3 commits landed since this run began; restoring would rewind them in the tree\"). A cleanup must never be able to un-apply history it did not create — the tree-custody rule, now for the engine's own janitor.

## Triage

**Component:** run cleanup
**Suspected files:** internal/run/run.go, internal/engineapi/engineapi.go

Reject cleanup can silently make the working tree diverge from HEAD by reverting history created after its snapshot.

**Verification (triage recommends):** test-first — Start a run, commit intervening changes to HEAD, reject it, and assert the committed files remain unchanged in the worktree.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-052 — Expose every project roster pin in Settings and label overridden defaults

**Implements:** SPEC-026

Fixes B-052.

## Reported

What happened: the desktop Settings page reads as THE configuration — and it showed "solo implementer: terra" while every solo run seated luna, because a hand-edited project.toml [roster] pin (implementer = luna) silently outranks it. The Settings UI has a "this project" section for exactly three roles (triager, advisor, scribe); the engine honors project pins for MORE roles than the UI can display or edit, so file-level edits create a government no screen admits exists. The person's stated assumption — all configuration the engine honors is exposed in the desktop — is the correct design contract, and it is broken.\n\nExpected, two layers: (a) parity — every project.toml key the engine reads has a place in Settings (the "this project" roster section grows implementer/reviewer/architect pins, showing current file values including hand-made ones); (b) effective-value honesty — where an "all projects" default is overridden by a project pin, the default's own row says so ("solo implementer: terra — overridden for ducklab: luna (project)"), so the two truths are never shown as one. Config without a surface is state without a witness.

## Triage

**Component:** Settings roster

Settings omits engine-honored project roster state, causing users to see and edit configuration that does not govern runs.

**Verification (triage recommends):** test-first — Seed project.toml roster pins and assert Settings shows every pin plus project-overridden effective values.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-053 — Record and expose provenance for each seated role

**Implements:** SPEC-026

Fixes B-051.

## Reported

What happened: T-050's closure ran solo with luna implementing, while Settings' solo line-up says terra — and the human had to ask an assistant to learn why: the project roster pins implementer=luna (project.toml), which outranks Settings after T-039's unification. The run record proves the WHAT (roster: implementer luna) and, since T-048, the mode's WHY (mode_source: request) — but no seat says where it came from. The exact question the person asked ("cómo llegó luna ahí") has its answer in config archaeology instead of on the card.\n\nExpected: each seated role carries its source like the mode does — roster entries annotated project | settings | request | spread (e.g. implementer: luna (project)) in state.json, run_get and the run view's seat chips. B-035's other half: a silent decision made visible, now on the record instead of only in the launcher.

## Triage

**Component:** run records

This is a distinct observability gap: resolved roster seats lack the source metadata already recorded for mode.

**Verification (triage recommends):** test-first — Launch with project, settings, request, and spread seat sources and assert state/run_get provenance.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-054 — Record and expose clean-checkout acceptance gate results on accepted runs

**Implements:** SPEC-060

Fixes B-049.

## Reported

What happened: T-049's build was aborted before its own gate ran, so the run's verdict honestly reads UNVERIFIED — and it was then accepted, with B-040's acceptance machinery running the FULL gate from a clean checkout of the resulting commit and passing (it had rejected two earlier attempts, so the green is meaningful). The record has one verdict slot and it belongs to the run's own gate: the strongest verification the work ever received — reproduction from a fresh worktree — is invisible, and the runs list shows "UNVERIFIED · accepted" for work more proven than any pre-B-040 PASSED.

Expected: the acceptance verification lands on the record — a gate_reproduced event with its result, surfaced beside the verdict (e.g. "UNVERIFIED · reproduced green at accept") in run_get and the runs list. Two different questions deserve two visible answers: did THIS run prove its work, and was the ACCEPTED COMMIT proven. Today the second answer, the one that matters most, is the one the record swallows.

## Triage

**Component:** run acceptance verification
**Suspected files:** internal/engineapi/engineapi.go, internal/engineapi/routes_table.go, frontend/src

The clean-checkout acceptance result is successfully produced but lost from the run record and operator-facing status.

**Verification (triage recommends):** test-first — Accept a run whose own gate is UNVERIFIED after a passing clean-checkout gate, then assert run_get and the runs list report reproduced green.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-055 — Make clean-checkout acceptance verification enforce stage-appropriate red or green results

**Implements:** SPEC-060

Fixes B-056.

## Reported

What happened: T-051's test-first landed a correct assertion-red test; the human clicked Accept twice and both times the card returned to Waiting — because B-040's acceptance verification runs the full gate from a clean checkout and refuses anything red, while a test-first commit is red BY DESIGN (the committed failing test IS the deliverable). The two honesty mechanisms are mutually exclusive as implemented: TDD requires the accepted commit to be red by exactly the new test; the checkout gate requires it green. Since the checkout gate landed, no test-first can be accepted — by human or auto:tdd — and the only offered escape, reject, discards correct work.\n\nExpected: the acceptance verification is stage-aware. A build or stage accept must be green from the clean checkout, as today. A TEST-FIRST accept must be RED from the clean checkout — and structurally red: not a compile failure (compileFailure on the output), the red reproducing the specification. A test commit that comes back GREEN from the checkout is the failure ("the committed test passes from a clean checkout — it asserts nothing that is not already true"). Same rigor, correct polarity per stage.

## Triage

**Component:** acceptance verification

The clean-checkout gate currently makes every valid test-first acceptance impossible by requiring green instead of structurally valid assertion-red.

**Verification (triage recommends):** test-first — Accept a TEST-FIRST commit with an assertion failure, a compile failure, and a passing test from clean checkouts; only the assertion-red case should pass.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-056 — Reconnect stale desktop event streams and expose stream health

**Implements:** SPEC-062

Fixes B-053.

## Reported

What happened: after an engine restart, the desktop kept a working HTTP channel but its SSE subscription died silently. Result: two queued runs showed (fetched over HTTP) while the actually RUNNING run — started after the restart, announced only via the stream's run_start — did not exist for the store: Now said nothing is running while the engine ran. The ✓ engine badge stayed green throughout, because it vouches for HTTP health while saying nothing about the stream. Two channels, one indicator, and the one that died is the one every live view depends on.\n\nExpected: the stream's liveness is first-class — the desktop tracks last-event age (heartbeats exist), resubscribes automatically when the stream goes quiet past a threshold with a full run resync after reconnect, and the engine badge reflects BOTH channels (\"engine ✓ · stream reconnecting\") so a dead stream is never invisible. A view that watches live work must know when it went deaf.

## Triage

**Component:** desktop event stream

A silently dead live-event channel makes running work invisible while falsely reporting the engine as healthy.

**Verification (triage recommends):** test-first — Simulate a silent SSE disconnect after engine restart, then verify reconnect, full run resync, and degraded badge state.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-057 — Recover stale desktop engine bindings and surface failed actions

**Implements:** SPEC-062

Fixes B-054.

## Reported

What happened: after an engine restart rotated port and token, an already-open desktop kept its launch-time binding (window.ducklab is injected once, at startup; the frontend cannot re-read engine.json and nothing re-injects it). The person aborted a run and configured a terra relaunch; both actions died against the dead binding — no error surfaced, no toast, nothing — and the engine carries no trace of either. They discovered it only because an assistant checked the record. Silent action loss is the worst UI failure class: the person believes they acted.\n\nExpected: (a) every failed action surfaces its failure where the click happened; (b) the desktop heals its binding — Wails re-reads engine.json and re-injects (or proxies) when requests start failing, with the reconnect logic engineclt already has (reconnect_test.go) extended to the frontend's channel; (c) until healed, the UI declares itself read-only stale rather than accepting clicks it cannot deliver. Related: B-053 (the stream half of the same split-brain).

## Triage

**Component:** desktop binding

Desktop commands can be silently lost after an engine restart, causing users to believe actions succeeded when the engine never received them.

**Verification (triage recommends):** test-first — Simulate rotated engine credentials and verify abort/relaunch report failure, enter stale read-only state, then recover after reinjection.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-058 — Add per-run seating and turn overrides to MCP task launchers

**Implements:** SPEC-021

Fixes B-055.

## Reported

What happened: executing the human's explicit choice — relaunch T-052's test with terra — was impossible over MCP: stage_start and bug_triage gained ducklings/mode/agent_turns overrides (T-014), but the task launchers (test_build, test_only, run_start) still accept none of them (run_start got mode and note only). The operator had to hand the seating back to the desktop, the exact dependency B-005 exists to remove.\n\nExpected: the task launchers accept the same per-run overrides as the stages — ducklings (implementer first, reviewer after), agent_turns — flowing into TestFirstRequest and the chained build's RunRequest, recorded with their provenance like mode already is. One override contract across every door, finally including the doors used most.

## Triage

**Component:** MCP tools

The MCP task launchers lack the per-run override contract already available on stage and triage tools, preventing remote operators from seating runs.

**Verification (triage recommends):** test-first — Invoke test_build, test_only, and run_start with ducklings and agent_turns and assert requests, chained builds, and provenance retain them.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-059 — Route implementer distress to the advisor and expose reviewer-safe operational summaries

**Implements:** SPEC-057

Fixes B-058.

## Reported

What happened: T-058's implementer ended her turn admitting she was fighting fs_patch (28 failures, brake tripped) — and the reviewer, correctly blind to her transcript (SPEC-004's decorrelation: judge the work, not the rationalization), evaluated the partial diff with no idea why it was partial. The distress signal existed, structured and honest, and no organ consumed it: the next round refights the same tool. The human's instinct — someone should help — is right; making the REVIEWER that someone would break I2 (a coaching reviewer becomes co-author, then reviews its own advice).\n\nExpected, two clean channels: (a) distress signals — brake refusals, per-tool failure streaks, an end-of-turn admission — trigger the ADVISOR's B-031 craft: draft the corrective note (\"rewrite with fs_write; fs_patch chokes on this file\") that rides the next round or the redo, help from the organ built for helping; (b) the reviewer receives an operational summary as DATA, never prose — \"28 patch fails, brake tripped, diff partial\" — so its verdict can distinguish wrong-design from wounded-execution without ever reading a rationalization. The judge stays a judge; the counselor gets a pager.

## Triage

**Component:** run orchestration

Distress telemetry is produced but not routed to either the advisor or the reviewer-safe operational summary channel.

**Verification (triage recommends):** test-first — Simulate tool-failure brake and end-of-turn distress; assert advisor receives a corrective-note request while reviewer receives only structured metrics.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-060 — Unify canonical roster seats and resolution

**Implements:** SPEC-003, SPEC-004, SPEC-005, SPEC-006, SPEC-007, SPEC-024, SPEC-043, SPEC-044

Replace positional global mode line-ups with canonical role-keyed `mode_seats` and make one resolver the authority for roster, launch, and MCP seating, so project pins, global mode seats, common-role fallbacks, and per-run picks have explicit precedence and provenance.

**Deliverables:**
- Persist canonical global mode seats as mode → real role name → ordered duckling-ID list, replacing positional `mode_ducklings`
  - Include assignable per-mode advisors and retain mode-independent `triager` and `scribe` as global role pins.
  - Provide an automatic, repeatable, one-way migration from valid legacy positional configuration.
- Deliver a single roster resolution service returning every seat’s effective ordered list and provenance
  - Resolve precedence as per-run pick, project role pin, global mode seat, then global role fallback.
  - Treat a project pin as replacement of the entire ordered list; unpinned project roles inherit Global.
  - Report provenance as `project pin`, `global mode seat`, or `global role fallback`.
- Route engine runs, launcher prefill, and MCP roster/launch consumption through the canonical resolution service
  - Preserve per-run selections as the highest-priority, non-persistent override.
  - Remove remaining production reads of positional mode line-ups.
- Enforce roster cardinality on edits and launches
  - Reject council without critics, split with fewer than two workers, and tournament with fewer than two contestants.
- Update specification document 02 and add configuration/service tests
  - Assert repeatable migration, scope resolution and provenance, global fallback and common-role behavior, per-run precedence, and cardinality rejection.

**Out of scope:** Desktop roster-board interaction, Settings UI replacement, and MCP roster mutation endpoints.

### T-062 — Expose canonical roster management through MCP

**Implements:** SPEC-021, SPEC-024, SPEC-044  
**Depends on:** T-060

Expose Global and Project roster management through an MCP settings/roster tool using the same canonical service contract as the board and run seating. The tool must make project-pin boundaries explicit and return actionable errors rather than silently coercing invalid roster edits.

**Deliverables:**
- Add MCP roster reads for Global and Project scopes
  - Return effective ordered seat assignments, duckling identity, and per-seat provenance for every board-addressable seat.
- Add MCP assignment, replacement, removal, and unpin operations for every board-addressable role
  - Accept and preserve ordered lists for multi-slot roles.
  - Restrict Global operations to global seats and role pins; restrict Project writes to project pins.
- Enforce canonical service validation and inheritance semantics in MCP responses
  - Return actionable errors for unknown ducklings, invalid roles, and minimum-cardinality violations.
  - Ensure unpin restores the effective Global assignment and replacement does not merge with an existing project list.
- Update specification document 07 and add MCP tests
  - Assert board/run-service parity, project pin replacement and unpin inheritance, multi-slot list edits, and validation failures.

**Out of scope:** Desktop UI changes, new MCP launch override semantics, and alternate roster persistence formats.

### T-063 — Render scoped roster boards

**Implements:** SPEC-003, SPEC-004, SPEC-005, SPEC-006, SPEC-007, SPEC-024  
**Depends on:** T-060

Deliver a desktop Roster view for inspecting effective Global and Project seat assignments without changing them. This makes the canonical T-060 roster resolver visible while preserving Settings and all mutation behavior.

**Deliverables:**
- A desktop navigation entry beside Settings opens a read-only Roster view.
  - The view has a `Global | Project · <name>` scope selector and obtains each scope/mode through the existing roster/settings API and `RosterGet`.
- A Flock column lists every registered duckling with local/remote status and known per-run cost.
- Mode boards render the effective seats for Council, Solo, Pair, Split, Tournament, and Common.
  - Render only real roles; preserve ordering for critics, workers, and contestants.
  - Include Council architect/critics; Solo implementer/advisor; Pair implementer/advisor/reviewer; Split architect/workers/reviewer; Tournament contestants/judge; and Common triager/scribe.
- Project-scope rendering distinguishes inherited and pinned assignments.
  - Inherited Global seats are dashed, muted ghosts labelled `global`.
  - Project pins are solid, marked `pinned`, and expose the overridden Global value on hover.
  - A Project board with no pins states that plainly.
- Desktop specification document 08 describes the read-only Roster view, and desktop tests cover scope switching, ghost versus pinned rendering, multi-slot ordering, and the no-pins notice.

**Out of scope:** Roster mutation, drag-and-drop, keyboard assignment, and Settings changes.

**Assumption:** This amendment replaces T-061; if the amendment mechanism cannot remove accepted plan items, the operator will retire T-061 after these replacement tasks are assigned.

### T-064 — Assign roster seats from the board

**Implements:** SPEC-003, SPEC-004, SPEC-005, SPEC-006, SPEC-007, SPEC-024  
**Depends on:** T-063

Make the Roster board editable through equivalent pointer and keyboard flows, with all changes scoped to the selected Global or Project roster. Reuse T-060’s canonical mutation and validation behavior so board assignments resolve identically everywhere else.

**Deliverables:**
- Dragging a duckling from the Flock onto a seat assigns that duckling at the selected scope.
  - Critics, workers, and contestants accept multiple cards and retain their displayed order.
  - Cards can be removed from a seat.
- A keyboard/click assignment flow covers every drag operation.
  - A user can select a seat and choose a duckling from the Flock in accordance with desktop specification document 08 §8 accessibility.
- Global-scope changes write only Global canonical mode seats and role pins.
- Project-scope changes create or replace only Project pins, and an unpin control returns a seat to its inherited ghost.
  - Project edits never mutate Global assignments.
- Board validation renders engine-provided cardinality errors and both-sides warnings.
  - Cover Council’s minimum one critic, Split’s minimum two workers, Tournament’s minimum two contestants, Pair implementer/reviewer overlap, and Council architect self-critique.
- Desktop specification document 08 and desktop tests cover drag assignment, keyboard assignment, removal, multi-slot ordering, scope-correct writes, unpinning, and error/warning rendering.

**Out of scope:** Retiring or otherwise redesigning Settings, launcher changes, and MCP roster management.

### T-065 — Retire Settings lineup chips and prefill launchers

**Implements:** SPEC-024, SPEC-044  
**Depends on:** T-064

Remove Settings’ positional mode-seat controls now that the Roster board owns roster assignment. Have desktop run launchers resolve and display canonical roster seats with provenance while preserving per-run overrides as ephemeral choices.

**Deliverables:**
- Settings no longer offers positional mode line-up chips and instead links users to the Roster board.
- Task runner, TDD launch, and stage launchers prefill seat chips from the canonical roster resolver.
  - Each prefilled or selected chip displays `project`, `global`, or `picked now` provenance.
- Per-run launcher picks remain run-local overrides and never write back to Global or Project roster boards.
- Desktop specification document 08 updates the Settings section for the Roster link and launcher-prefill behavior.
- Desktop tests prove Settings lacks positional chips, each launcher prefills resolved seats with provenance, and a per-run pick does not persist to the roster.

**Out of scope:** MCP roster management, which remains T-062.

### T-066 — Expose duckling scorecards from engine evidence

**Implements:** SPEC-016, SPEC-021, SPEC-024, SPEC-052

Provide one engine read endpoint and typed client that return a comparable scorecard for every registered duckling, combining declared configuration with explicitly sourced internal evidence. This gives the Roster a single authoritative source without inventing, fetching, or silently estimating evidence.

**Deliverables:**
- A scorecard read API and typed client return one row per registered duckling with id, provider, locality, model, configured input/output cost per Mtok, capabilities, configured roles/notes, measured evidence, latest bench evidence, and declared external index evidence.
  - Derive locality from the provider `base_url`: loopback, LAN, and `.local` hosts are local; all other hosts are remote.
  - Represent unavailable evidence as absent/null rather than zero.
- Measured evidence is aggregated from ducklab’s internal report record per duckling: runs, pass rate, average cost per run, average wallclock, and tokens.
  - Preserve whether token values are estimated; do not combine estimated and measured values without that distinction in the response.
- Bench evidence contains the latest result per suite for each duckling using the data surfaced by the Bench view.
- Strict duckling configuration accepts an optional external index declaration, including coding score, source, and as-of date, and round-trips those declared values with provenance.
  - Do not fetch external rankings or synthesize an index when configuration omits it.
- Update the as-built API/configuration specification sections for the scorecard endpoint and external-index configuration.
- Service and API tests assert locality classification, measured aggregation, latest-per-suite bench selection, external-index provenance round-trip, and absent evidence remaining absent.

**Out of scope:** Changing provider discovery, querying third-party ranking services, changing Bench execution or report collection, or adding Roster filtering and presentation.

### T-067 — Filter and sort the Roster flock by scorecard evidence

**Implements:** SPEC-026  
**Depends on:** T-066

Add evidence-driven filtering and ordering to the Roster board’s Flock column while retaining the existing seating behavior. Operators can narrow candidates and compare the active ordering value directly on each card.

**Deliverables:**
- The Flock column supports text filtering by duckling id or model plus provider, locality, vision, native-tools, and minimum-context filters.
- The Flock column provides ascending and descending sorting by input cost, output cost, measured pass rate, average cost per run, bench score for a selected suite, external coding index, and context size.
  - Sort missing evidence last in both directions.
  - Source all displayed values from the T-900 scorecard client.
- Each flock card displays its active sort value and applicable missing-evidence marks: “no runs yet”, “no bench”, and “no index”.
- Filter and sort selections persist for the desktop session only, not in project configuration or roster assignment state.
- Existing assignment, drag-and-drop, and keyboard seating flows remain functional when the flock is filtered.
- Desktop tests assert every filter narrows the flock, each sort orders scorecards with missing values last, and a card’s displayed sort value matches its scorecard.

**Out of scope:** Altering assignment resolution, persisting personal view preferences per project or across sessions, adding new scorecard evidence, or automatically seating a duckling.

### T-068 — Suggest seat-aware flock candidates

**Implements:** SPEC-021, SPEC-026  
**Depends on:** T-067

When an operator selects or focuses a seat, prioritize the most relevant evidence in the Flock and expose the same non-binding recommendations through MCP. Suggestions aid selection only; they never change an assignment.

**Deliverables:**
- Selecting a seat through its drop box or focus applies role-aware candidate ordering using scorecard evidence.
  - Implementer and architect: bench score or pass rate, then cost.
  - Reviewer and judge: pass rate, then cost.
  - Advisor: cost, then latency.
  - Local roles retain their existing ordering and receive no role-driven reorder.
- The top three eligible cards for a role-aware ordering are marked “suggested for <role>” and show a concise evidence reason, such as “pass rate 92% over 14 runs · $0.31/run”.
  - Suggestions remain informational: click, keyboard, and drag-and-drop assignment behavior is unchanged.
- The MCP roster `get` response includes a `candidates` array for each seat containing the same suggested duckling ids and reason text used by the desktop board.
- Tests cover each role’s ranking and tie-break rule, top-three suggestion labeling and reason text, local-role non-reordering, and MCP/desktop candidate parity.

**Out of scope:** Automatic assignment, new role types, changing roster resolution, or exposing recommendations based on undeclared external or web-fetched data.

### T-069 — End the turn after repeated fs_patch-brake refusals on one file

**Implements:** SPEC-042

Fixes B-057.

## Reported

What happened: T-058's build hit the fs_patch brake on tools.go (working as designed: instant refusals, remedy named, zero file churn — a huge upgrade over the 83-failure massacres it was built for). But the implementer generated 20+ fresh fs_patch variations against the same refusal before finally following the remedy, burning call budget on a wall that had already spoken. The brake protects the world; nothing concludes the loop.\n\nExpected: refusals count toward their own escalation — after N refused attempts on the same tool+file (say 5 more past the brake), the TURN ends with the remedy as the parting instruction, the same way the gate brake ends a run with orders to stop and explain. A wall that has said no six times should stop accepting knocks and close the door.

## Triage

**Component:** tool dispatch
**Suspected files:** internal/tools/tools.go, internal/tools/fs.go, internal/tools/fspatch_test.go

B-029 added the per-file failure brake but does not escalate repeated refusals into terminating the futile turn.

**Verification (triage recommends):** test-first — Exercise repeated refused fs_patch calls for one file and assert the threshold ends the turn with the rewrite remedy.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-070 — Reset the fs_patch per-file brake when the model fs_reads the braked file, restoring a one-probe window

**Implements:** SPEC-042

Fixes B-060.

## Reported

What happened: T-058's second implementer (terra) genuinely mismatched her first anchors on tools.go, tripped the 5-failure brake — and then RECOVERED: her later fs_patch calls carried search strings verified to match the file exactly once. The brake refused them anyway: once tripped it blocks every patch to that file unconditionally, without testing any, so a model that follows the harder path (fixing its anchors instead of pivoting) is punished precisely for improving. Two models on the same file read as \"the tool is broken\"; the truth is genuine mismatches first, then a brake that never forgives.\n\nExpected: the brake's own remedy is its reset condition — it prescribes \"read the full section\"; an fs_read of the braked file after tripping resets the streak, so compliance re-opens the door (one probe's worth: fail again within the window and the brake returns harder). The refusal keeps teaching, the prescription becomes enforceable, and a model that sharpened its anchors gets to prove it.

## Triage

**Component:** tool dispatch / fs_patch brake
**Suspected files:** internal/tools/tools.go, internal/tools/fspatch_test.go

The brake check at Execute never clears fsPatchFailStreak except via a successful patch it now blocks, so a model that follows the prescribed fs_read remedy is permanently refused; the fix hooks the reset into fs_read of the braked path.

**Verification (triage recommends):** test-first — Trip the brake with 5 failing patches on a file, fs_read it, then apply a search string that matches exactly once — expect success; fail again within the window and the brake re-engages

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-071 — Add [verify] link_deps and setup keys to project.toml so checkout preparation is declared, and make gate failures name the missing path

**Implements:** SPEC-060

Fixes B-061.

## Reported

What happened: the acceptance gate runs from a clean checkout (B-040), and twice now it died because the checkout lacked an in-tree, gitignored artifact the gate command references: frontend/node_modules ("npx tsc not found") and then .venv ("/bin/sh: .venv/bin/pytest: not found", excercise-tracker's plan-extend accept). Both were cured by growing a hard-coded table in linkInstalledDeps (node_modules justified by package.json, .venv by Python markers). The table covers exactly the two ecosystems in use today; the next stack fails in a new costume — a CMake project's gate says `cmake --build build && ctest` and build/ is gitignored; Meson has builddir/, vcpkg has vcpkg_installed/. The engine cannot enumerate every ecosystem's costume in advance.

Two distinct remedies hide in the class, and the table conflates them: DEPENDENCIES (node_modules, .venv) are safe to borrow from the live tree — they carry no product code; BUILD PRODUCTS (CMake's build/) must NOT be borrowed — linking them would hand the gate binaries compiled from the old code, and the honest move is to regenerate them in the checkout.

Expected: the project declares its checkout preparation instead of the engine guessing — [verify] in project.toml gains link_deps = [".venv"] (borrow: for installed dependency trees) and setup = "cmake -B build" (prepare: run in the checkout before the gate, for compiled stacks). The hard-coded table stays as the zero-config default for the common costumes. Per B-052's contract, both keys surface in the desktop Settings alongside the gate command. And the gate's failure should teach the door: when the gate command references a path the commit does not carry, say "the gate references build/, which the commit does not include — declare it in setup or link_deps" instead of the raw shell error.

## Triage

**Component:** clean-checkout gate / project config
**Suspected files:** internal/service/service.go, internal/config/config.go, frontend/src/views/Settings.tsx

linkInstalledDeps (service.go:2086) is a hard-coded two-ecosystem table with no config surface in config.Verify (config.go:295), so any other stack's gitignored artifact fails the accept gate with a raw shell error — confirmed by two real accept failures.

**Verification (triage recommends):** test-first — Set link_deps=[".venv"] in project.toml, accept a commit whose gate calls .venv/bin/pytest, and assert the clean checkout symlinks it; a setup="cmake -B build" run must execute in the checkout before the gate; a gate referencing unlinked build/ must fail with the 'declare it in setup or link_deps' message

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-072 — Route request_changes on a plan_extend proposal back into the amendment with its own fragment, note, and solo mode

**Implements:** SPEC-038

Fixes B-062.

## Reported

What happened: a plan_extend (r-20260818-012151-xrgs, solo, $0.03) drafted T-060..T-062 as a proposal. I sent request_changes with a small note ("add Depends on: T-060 to T-061 and T-062"). The engine relaunched the PLAN STAGE (r-...-r6e7) — in council, not solo — against the approved store, which holds T-001..T-059 only; the architect could not read T-061/T-062 (they exist only in plan.md.proposed), paused with a question, and the advisor correctly explained that the amendment door only adds new tasks and cannot edit existing ones. The revision path for an amendment is therefore unusable: the note asked for an edit to the amendment's own fragment, and the rerun could see neither the fragment nor a way to edit it. Aborted; relaunched plan_extend with the dependency instruction folded into the change text (which works, but the operator had to know the trick).

Expected: request_changes on an amendment run reruns the AMENDMENT with the operator's note — the architect receives its own previous fragment (Params.Drafts / RoleTexts, the stand-pat machinery from B-001) as the draft to revise, plus the note, and emits the corrected fragment; same mode as the original run (solo), not the stage's configured council. Small-operator-first: an operator that says "add a dependency to the two tasks you just proposed" must be understood, not sent to reconcile a store.

**Deliverables:**
- request_changes on a plan_extend run rebuilds the follow-up from the persisted StageRequest (stage_request.json): the original Extend text plus the operator note, instead of the blank StageStart{revise} the MCP decide handler issues today (internal/mcp/tools.go:750-759)
- the amendment rerun keeps the original run's mode and rounds (solo, one round) rather than defaulting to the stage council
- runExtend/buildExtendPrompt receives the run's own previous fragment (via the stand-pat Drafts machinery / prior proposal) as the draft to revise, so the architect edits T-060..T-062 instead of reconciling the approved plan store
- a test asserts the whole path: extend proposal -> request_changes with a note -> new run whose StageRequest carries extend+note in solo, and whose prompt contains the note and the previous fragment; the original run's gate resolves as superseded

## Triage

**Component:** plan amendment revision (stage/extend)
**Suspected files:** internal/mcp/tools.go, internal/service/stages.go, internal/stage/stage.go, internal/stage/extend.go

request_changes on an amendment discards the original Extend request and relaunches a bare plan revision against the approved store, and neither the rerun's mode nor the amendment's own fragment survives, which the verify-run evidence in stages.go/stage.go/extend.go confirms structurally.

**Verification (triage recommends):** test-first — Extend-run revision is engine behaviour: assert that deciding request_changes on an extend gate relaunches a solo extend run whose prompt carries the operator note and the prior T-060.. fragment (stage/service-level tests, no model needed via injected Execute).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-073 — Expose TaskRemove as MCP task_remove and CLI `ducklab task remove`, and teach plan_extend the retire path

**Implements:** SPEC-021

Fixes B-064.

## Reported

What happened: a plan amendment split the accepted-but-unstarted T-061 into T-063..T-065 and named T-061 superseded; the amendment door cannot remove a task, so the operator had to retire it. The engine has the right door — TaskRemove (refuses tasks with accepted or open runs, cleans plan + DB + dependency references) — but it is exposed only to the desktop ("remove from plan"): `ducklab task remove` does not exist and there is no MCP tool. I called DELETE /v1/projects/{id}/tasks/{task} by hand with the bearer token; an MCP operator (Elena) could not.

Expected: `task_remove` on the MCP surface and `ducklab task remove <id>` on the CLI, both over the same TaskRemove with its refusals, attributed like every other MCP action; and plan_extend's contract should say "the amendment cannot remove tasks — retire superseded ones with task_remove", so the architect stops writing that instruction into an Assumption. B-005 parity: whatever the desktop can do to the plan, the operator can.

**Deliverables:**
- MCP `task_remove` tool in toolList with project_id/task_id schema, dispatched in the tools/call switch, attributed to the MCP operator like every other action
- `Engine` interface gains TaskRemove and the fake in mcp_test implements it; a test calls task_remove and asserts the engine saw project+task id
- CLI `taskCmd` gains a `remove` verb calling the DELETE route via the engine client, printing the refusal reason verbatim on error
- plan_extend prompt (buildExtendPrompt rules) states the amendment cannot remove tasks and that superseded tasks are retired with task_remove
- Refusals from Service.TaskRemove (accepted run, open run) surface unchanged through both new paths

## Triage

**Component:** MCP + CLI surface parity (task lifecycle)
**Suspected files:** internal/mcp/tools.go, internal/mcp/mcp.go, internal/cli/cycle.go, internal/engineclt/engineclt.go, internal/stage/extend.go, internal/service/bugs.go

TaskRemove exists engine-side with refusals and an HTTP route but is reachable only from the desktop, leaving MCP operators and CLI users unable to retire superseded tasks — a B-005-style surface parity gap, not covered by any open bug.

**Verification (triage recommends):** test-first — MCP tools/call task_remove against fake engine expects TaskRemove invoked with attribution; `ducklab task remove T-061` expects DELETE /v1/projects/{id}/tasks/T-061; extend prompt asserts the retire-superseded instruction text

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-074 — Serialize git worktree add/remove per repository so concurrent contestant workspaces cannot race .git/worktrees

**Implements:** SPEC-033

Fixes B-066.

## Reported

What happened: T-063's clean-checkout acceptance gate went red on TestGitWorkspacesAreIndependent (internal/strategy/tournament_test.go:339): "git worktree add … -b ducklab/wt/r-test-c: exit status 128: fatal: failed to read .git/worktrees/r-test-d/commondir: Success" — two worktree adds racing in the same repository. The test passes locally and on the retry; the diff under acceptance was frontend-only. The flake is the symptom of a real hazard: tournament (and split) create one worktree per contestant in parallel, and git's worktree metadata is not safe for concurrent `worktree add` on one repository — under load a contestant will fail to start with a git error unrelated to its work.

Expected: workspace creation serialised (a mutex around `git worktree add` / `worktree remove` per repository — creation is fast, only the runs inside must be parallel), and the test asserting independence of the workspaces AFTER serial creation. Until then acceptance gates can go red on this flake and need a retry.

**Deliverables:**
- A per-repository mutex (keyed by repo path, shared across callers) serializes WorktreeAdd/WorktreeAddDetached/WorktreeRemove/PruneWorktrees, so two worktree ops on the same repo never run concurrently in-process
- The serialization is placed so both the strategy workspace factory and the service's clean-checkout detached worktree (internal/service/service.go:2037) share the same lock, while contestant work inside the worktrees stays fully parallel
- TestGitWorkspacesAreIndependent keeps launching workspace creation from concurrent goroutines and asserts the two workspaces produce distinct patches after creation succeeds
- A stress test creating N (e.g. 8) worktrees concurrently on one repository passes repeatedly (go test -run ... -count=20) with no exit-128/commondir failure
- go test -race over internal/strategy and internal/vcs is clean

## Triage

**Component:** strategy workspaces / vcs worktrees
**Suspected files:** internal/strategy/workspace.go, internal/vcs/vcs.go, internal/strategy/tournament_test.go, internal/service/service.go

Confirmed: NewGitWorkspaceFactory issues `git worktree add` concurrently per contestant and git's .git/worktrees metadata is not concurrency-safe, so a per-repo mutex around the vcs worktree ops is the right, testable fix.

**Verification (triage recommends):** test-first — TestGitWorkspacesAreIndependent (internal/strategy/tournament_test.go:339) already drives two concurrent factory calls on one repo and flakes with exit 128; a looped/-count run of concurrent worktree creates is the reproduction the fix must make reliably green.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-075 — Differentiate human turns in chat transcription

**Implements:** SPEC-062

Render human-authored chat transcript turns with a human representation rather than the duckling duck graphic, while retaining the existing duck representation for duckling turns.

**Deliverables:**
- Chat transcription visually identifies human turns with a human icon or avatar
  - Replace the duck graphic only where the transcript turn author is the human
  - Preserve the existing duckling graphic for consultant/duckling messages
- The human representation is applied consistently to all rendered human turns, including historical conversation entries
- Frontend tests assert author-specific visual selection for human and duckling transcript turns

**Out of scope:** Redesigning the chat layout, changing author labels, adding user profile/avatar configuration, or changing non-chat uses of duck graphics.

**Assumption:** The current transcript renderer already distinguishes human and duckling authors in its turn data.

### T-076 — Show the human avatar on human turns of reopened chat transcripts

**Implements:** SPEC-062

Fixes B-081.

## Reported

Upon opening a past chat and inspecting its transcript human turns are shown with the duck representation instead of the human representation.

**Deliverables:**
- the root cause of human chat turns losing their human identity on a reopened (resynced) chat is identified — live vs past path, since both feed buildTurns but only the past one is reported broken
- a reopened past chat renders human turns with the human representation (👤), not DuckAvatar
- a test in frontend/src/lib/runview.test.ts (or the lane component tests) asserts a human message event from a recorded chat produces a turn block rendered as human
- consultant/duckling turns in the same transcript still render their duck avatar unchanged

## Triage

**Component:** run view / conversation lane
**Suspected files:** frontend/src/lib/runview.ts, frontend/src/components/ConversationLane.tsx, frontend/src/views/RunView.tsx

Avatar choice in ConversationLane keys on block.role === "human", and chat human messages are recorded with role "human", so the defect is that on the past-transcript path (resyncRun → buildTurns over the full event log) the human turn either loses that role or is absorbed into a consultant's block; I could not finish tracing why live chats differ from reopened ones before running out of tool calls, so the exact line is unverified — but the derivation is pure and testable, and no open bug covers it.

**Verification (triage recommends):** test-first — buildTurns over a recorded chat event log (run_start, human message, consultant turn_start/message, human message) must yield blocks with role "human" that ConversationLane renders as the human avatar; a derivation-level unit test in runview.test.ts reproduces without a DOM.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-077 — Replace the current human-turn avatar

**Implements:** SPEC-054  

Render 🧑 instead of 👤 for turns authored by the current human in chat transcripts. This makes the human-turn marker consistent wherever the active chat UI presents it.

**Deliverables:**
- The current human’s chat turns display the 🧑 avatar rather than 👤
  - Update the shared/current chat-turn rendering path, including reopened transcripts if it is reused there
- Frontend tests assert that current-human turns render 🧑 and do not render 👤
  - Preserve distinct avatars and styling for non-human roles

**Out of scope:** Changing avatars for ducklings, historical non-current-human identities, or other UI iconography.

### T-078 — Add a request-changes (revise) affordance for a drafted release in the Release view

**Implements:** SPEC-062

Fixes B-083.

## Reported

A release has been drafted, but there is no way to make or request changes to the release. Only option shown is Cut release.

**Deliverables:**
- The draft notice (or its area) in Release.tsx offers a way to enter revision text and request changes, alongside Cut
- Requesting changes calls client.releasePlan(projectId, bump, reviseText) and surfaces the started run (e.g. via the release-planned 'Drafting — watch the release run' path)
- The revise path sends a non-empty revise string and does not offer the action when the text is blank
- A test in reviewrelease.test.tsx asserts releasePlan is called with the revise text when a person requests changes on a drafted release
- The Cut path and the 'a draft is waiting' sidebar copy still behave as before

## Triage

**Component:** frontend/releases
**Suspected files:** frontend/src/views/Release.tsx, frontend/src/views/reviewrelease.test.tsx

The backend and API client already support release revision, but Release.tsx exposes only Cut, so a drafted release cannot be revised from the desktop despite the sidebar copy saying 'cut or revise'.

**Verification (triage recommends):** test-first — Render Release with a drafted release, submit revision text, assert client.releasePlan called with (projectId, bump, revise text) — mirroring existing releasePlan/cut tests in reviewrelease.test.tsx

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-079 — Add a file-picker button beside the reference-documents input in the Cycle view

**Implements:** SPEC-068

Fixes B-095.

## Reported

Right now, to add a reference document you have to write the full path of the file in the multiline textbox. We need to profide a file picker control to be able to find the file and add it as a reference.

**Deliverables:**
- A ChooseFile method on the desktop Picker binding (cmd/ducklab-desktop/picker.go) that opens the system file dialog filtered to .md/.txt, returns the chosen absolute path, and treats cancel as empty-string-not-error, mirroring ChooseDirectory
- The binding's FQN is exported (e.g. ChooseFileFQN) and wired into window.ducklab in cmd/ducklab-desktop/main.go alongside chooseDirectory
- A browse/pick control next to the reference-documents textarea in frontend/src/views/Cycle.tsx (visible once the refs door is open) that appends the picked path as a new line in refsText
- Typed paths in the textarea keep working unchanged, and the frontend still builds when window.ducklab.chooseFile is absent (CLI/browser use degrades gracefully)
- go build ./... and the frontend build compile cleanly

## Triage

**Component:** desktop Cycle view / reference-documents input
**Suspected files:** frontend/src/views/Cycle.tsx, cmd/ducklab-desktop/picker.go, cmd/ducklab-desktop/main.go

The Cycle stage card forces users to hand-type absolute reference paths into a textarea while the desktop already has a native Picker binding pattern (ChooseDirectory) that a ChooseFile sibling can extend to close exactly this friction.

**Verification (triage recommends):** build-only — The fix is a native OS file dialog plus a UI button; the verification gate is `go test ./...` with no frontend test harness, and a native dialog cannot be driven headless — an automated check would only grep source, pinning the implementation not the bug.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-080 — Announce the accept's commit step in the run transcript before the commit runs

**Implements:** SPEC-069

Fixes B-096.

## Reported

When the ducklings have finalized their work the gate runs once to accept, then after commit, but while the commit is running there is usually a time that the user have only seen the first gate passed but there is no signal about what is going on. I expect that when committing a message is emitted in the transcript with the moving cog indicating that the commit is in process before the next gate's turn. That way the user watching the transcript knows what is going on.

**Deliverables:**
- RunAccept's commit path appends a gate_started (or equivalent transcript) event announcing the accept/commit step BEFORE CommitWithTrailer runs, so the transcript shows activity during the commit
- The pre-commit announcement's detail text does not name a sha that does not exist yet (no 'committed <sha>' wording before the commit lands)
- A service test asserts the accept flow emits the announcement before the commit event, covering both the dirty-tree commit path and the already-clean path
- The existing post-commit 'reproducing the gate from a clean checkout' announcement still fires and still closes on gate_reproduced

## Triage

**Component:** run transcript / accept flow
**Suspected files:** internal/service/service.go, frontend/src/lib/runview.ts, internal/service/lifecycle_test.go

The backend already announces the accept phase but only after CommitWithTrailer returns ('committed 6b8c92e; reproducing…'), leaving the commit itself silent in the transcript — moving/adding the announcement ahead of the commit closes the reported gap.

**Verification (triage recommends):** test-first — Accept a dirty-tree run via RunAccept and assert a gate_started (phase accept) event is appended before the commit is created / before the 'committed <sha>' event exists

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-081 — Warn on plan proposals whose accept rewrites task bodies carrying accepted history

**Implements:** SPEC-070

Fixes B-094.

## Reported

Jose accepted a spec-alignment plan trusting the gate, and only discovered afterwards that 64 tasks had reverted to todo: the proposal card showed the textual diff but said nothing about what accepting would do to derived task statuses. sections_removed warns what an accept ERASES from the document; nothing warns what it resets on the BOARD. When a plan proposal rewrites the SUBSTANCE of task bodies that carry accepted history (post-B-093: traceability edits are exempt), the card should say so: this accept rewrites N task bodies whose accepted history will stop counting — never blocked, never hidden.

**Deliverables:**
- A plan proposal that rewrites the substance of N task bodies carrying accepted run history sets the run's warning to name the N and the consequence (accepted history stops counting), alongside the existing sections_removed/sections_gutted warnings
- The comparison uses the post-B-093 normalized task-body hash, so an Implements-only edit (traceability change) produces no warning
- The warning is set for the plan kind only and never blocks the proposal — the accept path is unchanged
- A test in internal/service (taskhash_test.go or a stages test) asserts the warning appears for a substance rewrite and is absent for a traceability-only edit

## Triage

**Component:** lifecycle / stage proposal gate
**Suspected files:** internal/service/stages.go, internal/service/taskhash_test.go, internal/runlog/runlog.go

This is the board-side counterpart to the existing sections_removed/sections_gutted document warnings: the same proposal card already warns what an accept erases from the document, and the fix adds a deterministic warning about what an accept resets on the board, reusing the taskBodyHashes/runsForCurrentTaskBodies machinery that already exists.

**Verification (triage recommends):** test-first — Build a project with a task that has an accepted run (recorded TaskBodyHash), propose a plan that rewrites that task's substance, and assert the run's warning names the body rewrite and the count; a plan that only edits Implements lines must warn nothing.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-082 — Make the accept-phase gate_started event render as a live gate turn in the transcript

**Implements:** SPEC-069

Fixes B-097.

## Reported

A previous task was supposed to make the commit signal show on the transcript when the commit is being invoked. When I go to the transcript of a run after the fix was implemented and I don't see the commit signal on the transcript.

**Deliverables:**
- A frontend test feeds an accept-phase gate_started event (phase 'accept', no round) followed by gate_reproduced and asserts the transcript lane shows the commit step
- The gate lane in buildTurns settles on gate_reproduced (not only round_gate) so accept-phase gates close
- A viewing-a-run transcript shows 'committing accepted work' while the accept commit is in flight and settles when the clean-checkout gate reproduces
- The existing round_gate gate behaviour remains intact (regression test stays green)

## Triage

**Component:** run transcript / accept flow
**Suspected files:** frontend/src/lib/runview.ts, frontend/src/components/runview.test.tsx, internal/service/service.go

The backend emits the pre-commit gate_started announcement (service.go:2054, 2115), but the front-end gate-as-turn logic only handles gate_started keyed by round and settles on round_gate (runview.test.tsx:491-508); the accept flow settles via gate_reproduced, so its announcement never appears as a live transcript signal — I could not read the gate_started branch of buildTurns before tool calls ran out, so the exact drop point is inferred, not confirmed.

**Verification (triage recommends):** test-first — buildTurns fed gate_started{phase:accept} → gate_started → gate_reproduced must show the commit step; today the lane settles only on round_gate, so the accept signal is dropped or stuck

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-083 — Carry images through ChatStart and ChatSend to a vision-capable consultant

**Implements:** SPEC-054

Let a person attach screenshots to a chat's opening message and to follow-up messages, so the consultant can advise on UI matters from the picture itself rather than from a description of it. The wire and turn machinery already exist (provider `Message.Images`, `Turn.Images`, the triage vision gate in bugs.go); this task threads the same path through chat.

**Deliverables:**
- `ChatStartRequest` and the `chatSendRequest` body each accept an optional `images` array of data URLs (`data:image/...;base64,...`), decoded and validated by the service.
  - Reuse the bug-attachment discipline from `internal/service/bugattach.go`: image MIME types only, a per-image byte cap, and a small bound on count and total bytes (the 6<<20 total the triager uses is the ceiling) — violations are `invalid_request` errors naming the offending image.
- ChatStart and ChatSend refuse images when the seated consultant duckling does not declare `vision: true`, with an error that says to pick a seeing duckling.
  - Mirror the triage gate at `internal/service/bugs.go:293`: consult the duckling's declared `Caps.Vision` from the registry, never probe the endpoint.
- The consultant's next reply turn receives the message's images as `Turn.Images`, so the vision model is shown the picture with the dossier prompt.
  - `executeChatTurn` gains the images of the message it is answering; images ride only the turn that replies to the message that carried them — the text replay in `chatPromptFor` is unchanged.
  - A `warning` event notes how many images were shown, as triage does.
- The human `message` event on the run record carries the image data URLs, so a reopened transcript can show what was sent.
  - Frontend rendering of those images is NOT part of this task; the record just keeps them.
- The OpenAPI document and the generated TypeScript client reflect the new optional fields (`make api`; the `api-check` target must pass).
- Go tests in `internal/service/chat_test.go` (and engineapi as needed) assert: images reach `Turn.Images` for a vision duckling, a text-only duckling gets the refusal error, an oversized or non-image payload is rejected, and the human message event records the images.

**Out of scope:** MCP chat tools, CLI chat commands, persisting images as files under `.ducklab/`, re-sending earlier images on later turns, and any frontend work (that is T-901).

**Assumption:** the consultant duckling's `Caps.Vision` is declared in config, as triage already assumes; no capability probing is added.

### T-084 — Add an image file-picker action to the chat controls

**Implements:** SPEC-054
**Depends on:** T-083

The initiate-chat controls — `ChatAbout`, rendered in the guide rail, the board, and the run view — gain an attach-image action with a file picker, so the person can show the consultant a UI problem instead of describing it. The same attach action serves the reply box of a paused chat in the run view if that box lives outside `ChatAbout`; if it is the same component, one implementation covers both.

**Deliverables:**
- An "add image" button beside the message input opens a file picker (`<input type="file" accept="image/*" multiple>`) and reads each chosen file as a data URL (FileReader), held in component state.
  - Follow the file-picker pattern T-079 established beside the reference-documents input in the Cycle view for styling and test ids.
- Each attached image renders as a small thumbnail chip with its filename and a remove control, so a wrong pick never reaches the record.
- Starting (or sending) the chat includes the attached images in the request payload from T-900, then clears the attachment state.
- The attach button is disabled with an explanatory title when the selected duckling does not declare vision capability, and re-enables when a seeing duckling is picked.
  - The fleet list already carries duckling capabilities to the frontend; read the declared vision flag from there rather than adding an endpoint.
- Vitest coverage asserts: picking a file shows its chip, remove drops it, the start/send call carries the data URLs, the button is disabled for a text-only duckling, and a non-image file is refused with a visible error.

**Out of scope:** rendering images inside transcript bubbles on the chat run view, drag-and-drop or paste upload, image resizing/compression, and any engine changes (T-900 owns the payload contract).

**Assumption:** the chat reply box in `RunView` either is or can share `ChatAbout`; if it is a separate inline form, the same picker markup is duplicated there — no new shared component is extracted.

### T-085 — Classify image-input provider rejections and pre-flight the declared vision claim so an mmproj-less llama.cpp chat fails with guidance, not a raw 500

**Implements:** SPEC-071

Fixes B-100.

## Reported

I attached an image and started a chat and got this error.

**Deliverables:**
- A chat started with images against a provider that rejects image parts no longer dies as a raw 'chat stream: 500' — the failure names the cause (model/server has no vision projector, e.g. mmproj) and the remedy (start the server with --mmproj, or pick a truly seeing duckling).
- validateChatImages (or an equivalent gate) consults the probed/cached vision capability, not only the declared config flag, so a wrongly-declared seeing duckling is refused before the provider is called.
- The duckling probe (or an on-first-use check) records the actual vision answer for local OpenAI-compatible endpoints so the declared vision:true claim is verified, not trusted.
- Go tests in internal/service/chat_test.go (and duckling/provider as needed) assert the classified error text and the refusal path against a fake mmproj-less endpoint.

## Triage

**Component:** chat vision / provider capability
**Suspected files:** internal/service/chat.go, internal/duckling/duckling.go, internal/provider/openaicompat.go, internal/config/config.go

The chat image path validates only the declared vision flag ('declared, not probed'), so a local llama.cpp server without its mmproj projector accepts the image at validation then rejects it at request time with a 500 'image input is not supported', and the run fails with no actionable guidance.

**Verification (triage recommends):** test-first — ChatStart with images against a fake provider that answers 500 'image input is not supported - hint: ... mmproj' should surface a classified capability error naming the fix (attach mmproj / re-seat a seeing duckling), and validateChatImages should gate on the probed claim, not only the declared flag.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-086 — Add a consultant seat to Roster Common and pre-select it in the guide chat box

**Implements:** SPEC-072

Fixes B-101.

## Reported

There is currently no consultant seat for a duckling in the Roster. There should be a consultant seat in the common area of the roster and the duckling selected there should be pre-selected in the guide chat box duckling selection.

**Deliverables:**
- The engine treats "consultant" as a common role: isCommonRole and role validation accept it, and the Common scope pin resolves for chat launches (tests in internal/service extend the existing triager/scribe cases)
- Roster view's Common board lists consultant beside triager and scribe (columnsFor in frontend/src/views/Roster.tsx)
- ChatAbout's duckling select is pre-filled with the resolved common consultant when one is pinned, while remaining a free pick otherwise
- A test asserts the pre-selection (component test) and a backend test asserts consultant pins resolve as common-role pins
- No unpinned behavior changes: with no consultant pin the select still shows the placeholder

## Triage

**Component:** roster common / chat launcher
**Suspected files:** internal/service/roster.go, internal/config/config.go, frontend/src/views/Roster.tsx, frontend/src/components/ChatAbout.tsx, frontend/src/components/GuidePanel.tsx, frontend/src/lib/seats.ts

The common-role set is hard-coded to triager and scribe (roster.go isCommonRole, Roster.tsx columnsFor) and ChatAbout always starts with an empty select, so the consultant seat cannot be pinned in Common nor pre-selected in the guide chat.

**Verification (triage recommends):** test-first — Backend: isCommonRole('consultant') must resolve like triager/scribe (roster_seats_test-style assertions); frontend: ChatAbout rendered with a resolved common roster pre-selects the consultant duckling instead of 'pick a duckling…'

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-087 — Add a fixed Recent runs strip to the guide rail

**Implements:** SPEC-051, SPEC-062

The guide rail today scrolls as one tall column and ends with the ask chatbox; an operator who wants "what just happened" must leave for Records → Runs. This task restructures the `<aside>` as a flex column — running/autopilot/next-steps/chat in a scrollable middle region — and pins a "Recent runs" strip below the chat, listing the last 10 completed runs for the project with a verdict glyph and a link each.

**Deliverables:**
- The guide rail's `<aside data-testid="guide-rail">` is a flex column whose middle region (hide button, running, autopilot, next steps, ask chatbox) scrolls while a new pinned strip stays fixed at the bottom.
  - In `frontend/src/components/GuidePanel.tsx`: the aside becomes `flex flex-col` with a bounded height (`h-full`/`max-h-full`, as fits the existing layout contract with App.tsx), the current content wraps in a `min-h-0 flex-1 overflow-y-auto` region, and the runs strip renders after it as a non-scrolling footer (`shrink-0`).
  - The collapsed pill (`guide-pill`) path is untouched and still renders; opening and hiding the rail must not change.
  - The strip stays visible whether the ChatAbout form is closed or open-and-grown; the chat growing may shrink the scrolling region but never pushes the strip off.
- A "Recent runs" section (`data-testid="rail-recent"`) lists up to the last 10 completed runs for the current `projectId`, newest first.
  - Sourced from the `useRuns` store: `client.runs(projectId)` already populates the store with the project's full run list (App.tsx refresh and project-switch effect), so no new fetch is needed; derive `Object.values(runs).filter(r => r.project_id === projectId && (r.status === "done" || r.status === "failed"))`, sort by `started_at` descending, and take 10.
  - Each line is one compact row: a verdict glyph plus the run's label (`task_id || stage || id`, the same fallback `RailRun` already uses).
  - Glyph mapping, reusing `verdictStatus`/`Verdict` from `frontend/src/lib/colors.ts`: green when `verdict === "PASSED"` **or** `accepted === true`; red when `status === "failed"` or `verdict` is `"FAILED"`, `"ABORTED"` or `"BUDGET_EXCEEDED"`; otherwise the literal text `UNVERIFIED` in the warning role. Status colour is paired with a shape/text, never colour alone (the colours module's own rule).
  - Each line links with `routeHref({ name: "run", id: run.id })`.
  - No dates, no status prose, no filter, no pagination; with zero completed runs the section still renders with a single muted "no completed runs" line (the rail already returns `null` when there are neither steps nor active runs, so the strip can never orphan-render on an empty project).
- Tests in `frontend/src/components/guidepanel.test.tsx` assert the strip from a seeded `useRuns` fixture.
  - A store with 12 completed runs (mixed `PASSED`/`accepted`, `failed`/`FAILED`, and no-verdict statuses) renders exactly 10 lines, newest first; green/red/UNVERIFIED glyphs land on the right rows; each line's `<a>` href equals `#/runs/<id>`.
  - A structural assertion that the strip is pinned: `rail-recent` is a direct flex-child of `guide-rail` outside the `overflow-y-auto` region, and still renders when the chat form is open (open `chat-about`, then assert `rail-recent` remains).
  - Existing guide-rail tests (collapse, steps, chat preselection) must keep passing unchanged.
- `go build ./...`, `go test ./...`, `cd frontend && npm run build` and `npm test` all stay clean.

**Out of scope:** the Records → Runs view and run detail page; dates, costs, modes or filtering on the strip; any engine, API or store-shape change; live/queued runs in the strip (they already have the `rail-running` section at the top).

**Assumption:** "Last 10" is ordered by `started_at`, the only ordering the Runs view itself uses; there is no completion timestamp guaranteed on older records (`ended_at` is optional).

### T-088 — Show run stage beside the task id in the Recent runs rail entries

**Implements:** SPEC-073

Fixes B-102.

## Reported

In the newly built recent runs section (T-087), a test task is undistinguishable from a build task. In the Reports -> Run view the tasks are prepended with the type of run (test, build, chat) etc. I expect the recent runs run name to be as in the following examples:  ✓ T-087 test  ,  ✓ T-087 build, ✓ B-098 triage

**Deliverables:**
- RecentRun in frontend/src/components/GuidePanel.tsx renders the run's stage alongside its label for task runs (e.g. '✓ T-087 test' / '✓ T-087 build')
- Stage runs (chat, plan, triage) and bug triage runs remain identifiable in the recent-runs row, e.g. '✓ B-098 triage', without duplicating the stage when the label already IS the stage
- The existing recent-runs test in frontend/src/components/guidepanel.test.tsx is updated/extended to assert the stage appears for task runs
- The naming matches the stage ordering used elsewhere in the rail (RailRun already appends 'build · pair' for live runs)

## Triage

**Component:** frontend guide rail / recent runs
**Suspected files:** frontend/src/components/GuidePanel.tsx, frontend/src/components/guidepanel.test.tsx

RecentRun labels completed runs with only task_id or stage, so a test run and a build run for the same task are indistinguishable — a presentation gap in the T-087 recent-runs section, fixable in one component with a render test.

**Verification (triage recommends):** test-first — Render GuideRail with a completed run {task_id: 'T-087', stage: 'test'} and assert the rail-recent row text contains both 'T-087' and 'test' (existing guidepanel.test.tsx already renders rail-recent and asserts labels, which currently pin the buggy 'T-12'/'review' output).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-089 — Rewrite Recent runs indicators to verb-first labels and em-dash unverified glyph

**Implements:** SPEC-062, SPEC-046

The Recent runs strip (T-087 / T-088) currently labels rows ID-first ("T-12 test", "B-098 triage") and renders the literal word `UNVERIFIED` as the unverified glyph. This task flips the label to begin with the run's action verb and appends the related work identifier per the action, and replaces the unverified glyph with an em dash — both retaining the existing green / red / yellow colors, ordering, links, and typography.

**Deliverables:**
- The unverified result glyph in `RecentRun` renders `—` (em dash, U+2014) instead of the literal string `UNVERIFIED`, while the warning/yellow color is preserved.
  - In `frontend/src/components/GuidePanel.tsx` `RecentRun`: the `glyph` computation changes from `role === "warning" ? "UNVERIFIED" : statusIcon(role)` to `role === "warning" ? "—" : statusIcon(role)`. The `✓` (good) and `✕` (critical) glyphs and their colors are unchanged.
  - The `aria-label` on the glyph `<span>` stays a descriptive word (e.g. `unverified`), not the em dash, so a screen reader still conveys the result — the visible character changes, the accessible label does not.
- Each `RecentRun` row's label begins with the run's stage (the action verb) and appends the related identifier per the action.
  - **triage:** append the bug ID from `run.subject` (the engine-sourced field, SPEC-046) or, when `subject` is absent, from `run.task_id` if it starts with `B-` — producing `triage B-099`. When `subject` is a count phrase like `3 open bugs`, it is still appended (`triage 3 open bugs`), since it is the relevant identifier.
  - **test** and **build:** append `run.task_id`, producing `test T-088` or `build T-088`.
  - **other verbs** (chat, plan, review, spec, intake, etc.): show the verb alone unless `run.task_id` or `run.subject` provides a relevant identifier, then append it.
  - The label falls back to `run.id` when `run.stage` is empty, as before.
- Tests in `frontend/src/components/guidepanel.test.tsx` are updated to assert the new verb-first label order and the em-dash glyph.
  - The 12-run fixture's `links.map(link => link.textContent)` expectation changes from ID-first (`"T-12 test"`, `"B-098 triage"`, …) to verb-first (`"test T-12"`, `"triage B-098"`, …).
  - The triage fixture row (r-8) is updated to carry `subject: "B-098"` (with `task_id` cleared to `""`) so the engine-sourced `subject` path is exercised, not only the `task_id` fallback.
  - Glyph assertions for the two unverified rows (r-8, r-7) change from `toContain("UNVERIFIED")` to `toContain("—")`.
  - Existing assertions for `✓` (r-12 PASSED, r-11 accepted) and `✕` (r-10 failed, r-9 FAILED, r-6 BUDGET_EXCEEDED) remain unchanged.
  - The pinned-footer structural assertions (flex column, `overflow-y-auto` sibling, chat-open persistence) remain unchanged.
- `go build ./...`, `go test ./...`, `cd frontend && npm run build` and `npm test` all stay clean.

**Out of scope:** the Records → Runs view and run detail page; the live-run `RailRun` component and its label; any engine, API, or `Run` type change; the three status roles (good / critical / warning) and their CSS variable colors; `runLabel` in `frontend/src/lib/runview.ts` (used by the Runs view, not the rail).

**Assumption:** The `subject` field on a triage run already carries the bug ID(s) from the engine (SPEC-046, `triageSubject` in `internal/service/bugs.go`), so no API or store change is needed to surface the bug ID for triage labels.

### T-090 — Emit the pre-commit gate event before staging/committing in RunAccept

**Implements:** SPEC-069

Fixes B-098.

## Reported

Committing message emitted seemingly at the same time with the committed gate message. I expected that after the gate verifying got green the committing would be emitted to indicate this is whats going on now during the delay between the first gate green and the after commited gate signal.

**Deliverables:**
- The 'committing accepted work before clean-checkout verification' gate_started event fires before git.AddAll/CommitWithTrailer, not after CreateBranch
- A test asserts the committing event precedes the commit in the accepted-run event order
- git.AddAll and commit failures still abort the accept with the event already logged

## Triage

**Component:** accept/run-lifecycle UX
**Suspected files:** internal/service/service.go

Service.emitGateStarted is invoked after branch creation but the event is placed relative to commit ordering, and the log-order test pins the fix without being cosmetic-only.

**Verification (triage recommends):** test-first — RunAccept on a dirty tree asserts the 'committing accepted work' gate_started event is recorded before the commit succeeds (event order in the run log).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-091 — Show the advisor's in-flight work on the ask_human question card and account it to the run's budget

**Implements:** SPEC-074

Fixes B-099.

## Reported

When an ask_human turn shows the question in the desktop, the advisor's activity is not shown. An indication in the ask_human card should show that the advisor's is trying to prepare an answer, and also it's activity should be tracked agains the budget for that run.

**Deliverables:**
- The human_needed event (or pending data) for a question names the advisor seat being consulted, so the UI knows who is preparing the answer
- buildPending returns an advisor-pending indicator when a question has no advice yet, clears it when advice/advice_failed arrives, and never shows it for non-question pending kinds
- RunView and Board question cards render 'advisor X is preparing a recommendation' while pending, replaced by the advice card or the failure cause on arrival
- A test asserts advisor chat cost/tokens for a paused-question consult land in the run's spend record (fix the accounting if the assertion fails)
- Board.tsx's pending-question panel (line ~1220) shows the same indicator as RunView

## Triage

**Component:** advisor question card (desktop + run record)
**Suspected files:** internal/service/lifecycle.go, internal/service/advisor.go, frontend/src/lib/runview.ts, frontend/src/views/RunView.tsx, frontend/src/views/Board.tsx

pauseForQuestion launches the advisor silently (lifecycle.go:525) so the card shows a bare question with no working indicator, and advisor spend must be verifiably charged to the run.

**Verification (triage recommends):** test-first — buildPending over [human_needed(kind=question)] with no advice event should expose an advisor-pending state; RunView renders it as a working indicator, replaced when advice arrives

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-092 — Remove the reopen-task next step from the guide

**Implements:** SPEC-051, SPEC-075

Fixes B-103.

## Reported

Guide rail have a Reopen an accepted task area that occupies real estate in the guide rail and is not necessary remove if from the the guide rail.

**Deliverables:**
- The reopen-task step block (single and grouped cases) is removed from internal/service/guide.go Next()
- Next() no longer returns any step with ID reopen-task regardless of accepted task state
- Existing guide tests in internal/service/guide_test.go are updated so they assert no reopen-task step appears
- No other guide steps (new, classify, promote, verify-bug, build, spec-debt) change behaviour

## Triage

**Component:** guide
**Suspected files:** internal/service/guide.go, internal/service/guide_test.go

The guide rail's reopen suggestion is emitted by Next() in internal/service/guide.go; the request is to stop surfacing it, a small logic change verifiable by the existing guide tests after updating their expectations.

**Verification (triage recommends):** test-first — Next() output is deterministic: accepted redoable tasks must no longer yield a reopen-task step; update TestTheGuideSurfacesReopenDoors and TestTheGuideGroupsReopenableTasks to assert absence.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-093 — Thread the request context through validateChatImages to the VerifyVision probe

**Implements:** SPEC-071

Fixes B-105.

## Reported

internal/service/chat.go validateChatImages calls s.ducklings.VerifyVision(context.Background(), ...) instead of the request context. The vision verification can make a real network probe; detached from the caller it cannot be cancelled — a hung endpoint keeps the probe alive after the person gave up. Pass the request ctx through.

**Deliverables:**
- validateChatImages accepts a ctx parameter and passes it to s.ducklings.VerifyVision instead of context.Background()
- Both call sites (ChatStart ~line 125, ChatSend ~line 216) pass their existing request ctx
- No other use of context.Background() remains in the image-validation path
- A test (or equivalent verification) shows a cancelled ctx aborts the vision probe with ctx.Err rather than blocking

## Triage

**Component:** chat service
**Suspected files:** internal/service/chat.go

Confirmed at chat.go:61 the vision probe detaches from the caller, so it cannot be cancelled on client disconnect; the request ctx is already in scope at both call sites.

**Verification (triage recommends):** test-first — Call validateChatImages with a cancelled/deadline ctx and a stubbed VerifyVision (or blocking httptest endpoint) expecting the probe to abort with ctx.Err; if duckling config wiring proves unmockable, downgrade to build-only.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-094 — Close a superseded committing gate block with a neutral state instead of an unearned green check

**Implements:** SPEC-069

Fixes B-106.

## Reported

runview.ts case gate_started (T-090 fix) closes an open gate block with gate:"green" when the next gate_started supersedes it. For the committing block, green means only handed off, not verified — and if AddAll/CommitWithTrailer aborts acceptance, the transcript would still show a green check on committing that never completed. Close a superseded block with a neutral done state (no check), or green only when the handoff event confirms the commit landed (the reproduction gate_started detail carries the sha).

**Deliverables:**
- In runview.ts case gate_started, a superseded open gate block closes with a neutral done state (no green check) rather than gate:"green"
- Green is shown for the committing block only when the superseding event confirms the commit landed (e.g. its detail carries the committed sha)
- A test in runview.test.ts builds two sequential gate_started events (committing then reproduction) and asserts the first block is done with a non-green gate
- A test covers the confirmed-handoff path if the green-on-sha option is implemented
- Existing announced-gate tests (baseline close on gate, reproduction close on gate_reproduced) still pass

## Triage

**Component:** accept transcript / runview
**Suspected files:** frontend/src/lib/runview.ts, frontend/src/lib/runview.test.ts

runview.ts:509-512 marks any superseded gate block green, but for the committing phase green means only handed off, so an aborted accept would still display a green check on a commit that never completed; B-098 is the fixed predecessor whose closing behavior this refines, not a duplicate.

**Verification (triage recommends):** test-first — buildTurns([gate_started(committing), gate_started(reproduction)]) yields first block gate === "green"; assert it is not green unless the handoff confirms the commit landed

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-095 — Add gofmt -l to the project gate and settle existing formatting drift

**Implements:** SPEC-076

Fixes B-107.

## Reported

T-091 (commit 9f2c20d) landed internal/service/advisor.go with misaligned struct-literal fields; gofmt -l internal/service currently flags advisor.go and advisor_governance_test.go, both from duck-authored commits. The project gate (go test + frontend) proves behaviour, not formatting, so every accepted run can leave drift a human contributor would be nitpicked for. Add a gofmt -l check to the ducklab project gate (cheap, deterministic) and settle the existing drift once.

**Deliverables:**
- The [verify] tests gateway command in .ducklab/project.toml includes a gofmt -l check that fails on any output
- gofmt -l over the repo reports no files (advisor.go and advisor_governance_test.go drift settled)
- Gate command remains valid and runnable end-to-end (go test + frontend chain preserved)

## Triage

**Component:** verify gate config
**Suspected files:** .ducklab/project.toml, internal/service/advisor.go, internal/service/advisor_governance_test.go

Deterministic formatting gap in the configured gate command with two known drifted files; cheap config edit plus one gofmt -w pass, no behavior at stake.

**Verification (triage recommends):** build-only — The fix edits a gate command string in project.toml; a forced test would grep that string and pin the implementation, not the behavior.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-096 — Per-provider concurrency caps in the run queue with the reason recorded on the run

**Implements:** SPEC-025, SPEC-030

Give providers an optional `max_concurrent` and teach the queue to count live runs per provider, so two runs seated on a one-slot local endpoint queue honestly — with a reason that names the provider and the runs holding it — instead of serializing invisibly at inference.

**Deliverables:**
- `config.Provider` gains `MaxConcurrent int` (`toml:"max_concurrent" json:"max_concurrent,omitempty"`); zero/omitted means unlimited, and the strict TOML parser, save round-trip, and `ProviderView` (or its equivalent fleet API shape) carry it untouched.
  - The field lives beside `Kind/BaseURL/APIKeyEnv/Headers` in `internal/config/config.go`; existing config round-trip tests in `internal/config/config_test.go` gain a case asserting a set value survives load/save and an absent key stays zero.
- The engine applies starter defaults when no explicit `max_concurrent` is set: a provider whose `base_url` resolves to loopback/LAN/`.local` (the same locality derivation already used for scorecards) is treated as cap 1, other providers as cap 8.
  - The default is computed at queue-consultation time from live config, not persisted, so editing `base_url` or the cap takes effect without a migration.
- `runQueue` counts running runs per provider id and `canStart` refuses when ANY provider in the submitting run's resolved roster is at its cap.
  - A run occupies every provider its roster seats: the set of provider ids behind the run's resolved roster ducklings (the same resolution that already lands in the run record's `roster`) is attached to the `queued` item at submit time.
  - Counts are reserved in `reserve` and released in `done`, exactly like `perProj`; the existing global limit, per-project rule, working-tree `held` hook, chained front-jump, `poke`, `remove`, and `drain` behavior are untouched.
- The queued reason for a provider hold names the provider and the runs holding it, in the existing style: `waiting for a slot on provider X (held by r-…)`.
  - `submit`'s reason selection gains this third branch alongside "engine at max_concurrent_runs" and the working-tree holds; the string is produced once, in the queue, and is what both the `run_queued` event and the run record carry.
- `runlog.Run` gains a `queued_reason` field, written to `state.json` when a run queues and cleared when it starts.
  - `submit` already sets `Status = "queued"` and appends the `run_queued` event with the reason; the same string is now stored on the run record so `GET /v1/runs` and `GET /v1/runs/{id}` return it without replaying events.
  - `start` clears the field before writing `running`; rehydration from disk preserves it like any other state field.
- Go tests in `internal/service/queue_test.go` (plus config tests) assert: two runs sharing a cap-1 provider — the second queues with the provider-named reason and is promoted by `done` the moment the first finishes; runs sharing a cap-8 hosted provider start in parallel up to 8; a run whose roster spans two providers is refused when either is at cap; an explicit `max_concurrent` overrides the locality default; `queued_reason` is set on queue and cleared on start; and all pre-existing queue tests still pass unmodified.

**Out of scope:** Any desktop rendering (that is T-097); per-turn or per-call provider throttling inside a running run; fairness/aging policy beyond the existing waiting-line order; persisting the waiting line across restarts; MCP surface changes.

**Assumption:** The roster-to-provider resolution the run record already performs (visible as `roster` on `runlog.Run`) happens at or before queue submit time for every run kind — build, test-first, stage, chat — so a provider set can be attached to the `queued` item; where a run's seats genuinely cannot be known until later, that run contributes no provider holds rather than guessing.

### T-097 — Show the queued reason in the desktop and expose provider max_concurrent in Settings

**Implements:** SPEC-062, SPEC-025
**Depends on:** T-096

Make a queued run look alive: everywhere running runs already show, a queued run shows the engine's reason verbatim, and the person can see and edit the per-provider cap that produced it.

**Deliverables:**
- The frontend `Run` type in `frontend/src/api/client.ts` gains `queued_reason?: string`, matching the engine field from T-096; `docs/openapi.json` / `frontend/src/api/generated.ts` are regenerated via `make api` if the route table generation covers it, never hand-edited.
  - **Assumption:** the run record's OpenAPI entry picks the new field up from `runlog.Run`; if the route table lists fields explicitly, the addition lands in `internal/engineapi/routes_table.go` instead.
- Everywhere a running run renders, a queued run renders its `queued_reason` verbatim: the Now inbox, the Runs list, and the guide rail's Recent runs strip.
  - `frontend/src/views/Runs.tsx` already filters `queued` into the running bucket; the row gains the reason text (e.g. under or beside the status chip) with no rewording — "engine at max_concurrent_runs", "another run holds this project working tree", or "waiting for a slot on provider X (held by r-…)" arrive from the engine as-is.
  - The same string shows for queued entries in `frontend/src/views/Now.tsx` and the Recent runs rail component, following each surface's existing layout conventions for secondary text; no new status colors or chips are invented — queued keeps its current visual treatment plus the reason.
  - The run store (`frontend/src/store/runs.ts`) needs no new derivation: the field rides the run record it already holds.
- The provider editing surface in the desktop (the providers section of `frontend/src/views/Ducklings.tsx`, which already round-trips `ProviderView` through `client.providers()`) gains a `max_concurrent` numeric field: empty/0 means unlimited, and the form posts it through the same provider save path it uses today without wiping other fields.
  - The field is labeled to make the semantics plain (concurrent runs this endpoint will seat at once; blank = unlimited).
- Frontend tests assert: a queued run with `queued_reason` renders that exact string in the Runs list and in the Now inbox (extend `frontend/src/views/runs.test.tsx` and `frontend/src/views/now.test.tsx`); a queued run without the field renders as before; the provider form round-trips a `max_concurrent` value and preserves it when editing an unrelated field.

**Out of scope:** Queue position indicators, per-provider live occupancy dashboards, CLI rendering of `queued_reason` (the record carries it; printing it in the CLI is a separate task), changing how `run_queued` events render in transcripts, and any engine behavior.

**Assumption:** The existing provider save path used by `Ducklings.tsx` tolerates new keys on the provider object end-to-end once the engine config and API shapes accept them; no new endpoint is needed.

### T-098 — Stamp every run-spawned process tree with DUCKLAB_RUN_ID and DUCKLAB_PROJECT_ID

**Implements:** SPEC-009, SPEC-065

Two suites sharing one test database can both pass green while corrupting each other — the run-vs-human variant already happened on excercise-tracker. This task gives every process the engine spawns for a run a stable identity in its environment, so a project can self-isolate (`DATABASE_URL=test_db_${DUCKLAB_RUN_ID}`, a per-run docker compose project name). The variables are injected at the single place each process tree is built — `verify.Run` and the shell tool's exec — never per call site, and they are ALWAYS present: human-invoked paths get a `manual-<timestamp>` identifier so scripts need no fallback.

**Deliverables:**
- `verify.Run` (internal/verify/verify.go) stamps `DUCKLAB_RUN_ID` and `DUCKLAB_PROJECT_ID` onto the environment of every gate process it launches, alongside the existing isolated-state variables.
  - The identity arrives as a parameter (a small struct or two strings), merged into the env `isolatedStateEnvironment` builds, so the gate, the accept-path clean-checkout gate, and the `[verify] setup` command (which already reuses `verify.Run`, service.go:2206) are all covered by one change.
  - Every `verify.Run` call site passes the run's identity: service.go:1636 (build gate), service.go:2207/2214 (accept clean-checkout + setup), gate.go:170, modes.go:466/519/556, testfirst.go:393/487/516, and the `verify_run` tool (internal/tools/exec.go:113, from `ectx`).
  - An empty run id becomes `manual-<unix-timestamp>` inside `verify.Run` itself, so the variable is set even for human-invoked gates (`ducklab project gate` / handleGateRun) and no script ever needs a fallback.
- The shell tool's exec path stamps the same two variables on every command it runs.
  - `RunShell` (internal/tools/tools.go:835) is the single construction point: it appends the variables to `ectx.ShellEnv` (or `os.Environ()` when nil), so the `shell` tool and skill scripts (internal/tools/skill.go:253) both inherit them.
  - `tools.ExecContext` gains a `ProjectID` field beside the existing `RunID`; the service populates both when building run contexts, and `Service.SkillRun` (internal/service/skills.go:227) sets `RunID` to `manual-<timestamp>` (or leaves it empty for the RunShell default) so a person testing a skill still gets a defined value.
- The `[run]` preflight and app launch commands (internal/service/app.go:79 and :105) run with both variables set.
  - **Assumption:** AppStart is human-invoked and has no run id, so it passes `manual-<timestamp>`; a docker compose preflight keyed on the variable still gets a stable, unique-per-invocation project name.
- The README's project.toml section (~line 180) and the matching docs/spec page document the pattern with the excercise-tracker example: `DATABASE_URL=test_db_${DUCKLAB_RUN_ID}` in `[verify].tests`, and a compose preflight using a per-run project name; the docs state the engine guarantees identity only — provisioning and teardown stay the project's.
- Go tests assert: a gate command sees both variables (e.g. a gate of `echo $DUCKLAB_RUN_ID` or `env` captured from a configured test gate); a `[verify] setup` command sees them; a shell tool invocation sees them; and two concurrent `verify.Run` processes with different run ids each observe their own id (run two gates in goroutines that write `$DUCKLAB_RUN_ID` to distinct files, assert the contents differ and match what was passed).

**Out of scope:** dropping the one-run-per-working-tree queue hold (phase 3); any database/port provisioning or teardown by the engine; resource declarations in project.toml beyond documenting the pattern; desktop or MCP surface changes; changing what T-045/T-050's isolated-state environment already scrubs or preserves.

**Assumption:** contestant worktree gates in tournament/split (modes.go) run per contestant within ONE run, so they share that run's id — per-contestant isolation inside one run is the project's to compose from the run id, not a second engine variable.

### T-099 — Prove the per-repo worktree mutex serializes one repo and never blocks another

**Implements:** SPEC-033

T-074 put a per-repo-path mutex around `git worktree add/remove/prune` in internal/vcs/vcs.go (keyed by normalized absolute path, `Abs` + `EvalSymlinks`), and the existing tests race adds on one repo. The unproven half of the contract is the scoping: the lock must cover ONLY the git metadata mutation on THAT repository — contestants working in different repos, and contestants working inside their trees, must never queue behind it.

**Deliverables:**
- A test in internal/vcs/vcs_test.go races two goroutines adding worktrees on one real repo and asserts both succeed with no git error.
  - `TestConcurrentWorktreeAdds` already does this with 8 workers; extend or confirm it covers the add/remove pair the change names, rather than duplicating it.
- A new test asserts worktree operations on DIFFERENT repositories do not block each other.
  - Two repos, each with a git stub (the existing fake-git script pattern at vcs_test.go:122) whose `worktree add` sleeps; start both adds together and assert total elapsed is closer to one sleep than two — or instrument entry/exit and assert the critical sections overlapped.
  - The same test asserts the mutex is keyed by the normalized path: two `vcs.Git` values naming the same repo through different spellings (symlink, `..`, trailing slash) still serialize — the existing fake-git lock-dir test covers independent `Git` values; add the spelling-variant case if absent.
- The lock's scope is verified to wrap only the `g.run("worktree", …)` invocation in `WorktreeAdd`, `WorktreeAddDetached`, `WorktreeRemove`, and `PruneWorktrees` — no contestant work (patch application, builds, gate runs) happens under it; if any caller is found holding the lock across non-git work, narrow it.

**Out of scope:** worktree-per-run execution (phase 3); reaping or orphan-recovery policy; serializing non-worktree git operations; any change to tournament/split orchestration beyond what the tests require.

**Assumption:** the T-074 mutex implementation and its two existing tests are in the tree and passing; this task closes the coverage gap rather than re-building the mechanism — if verification shows the mutex missing or wrongly keyed, the task grows to fixing that first.

### T-100 — Two-pass adopt survey: inventory turn, coverage diff, and run-record surfacing

**Implements:** SPEC-029, SPEC-022

Adopt surveys (intake with `adopt=true`, and the first spec of an adopted project) gain a leading solo inventory turn whose result is checked deterministically against the proposed document, so a draft that misses an inventoried surface says so on the record instead of passing silently.

**Deliverables:**
- A solo architect inventory turn runs before the draft turn on exactly the two adopt paths: intake with `Adopt: true`, and a spec run whose approved requirements carry `Origin: "adopted"` with no existing spec sections (the detection already used at `internal/stage/stage.go:216` and `:353`). No other stage or mode is affected.
  - The turn answers a JSON contract riding the existing `json:*` contract machinery (as `json:triage` does), returning a flat list of items `{name, kind, evidence-path}` where kind covers route/handler groups, schema entity clusters, services, clients, integrations, and config surfaces.
  - The list is capped at 60 items; when the cap trims the list, the inventory event names the cap — trimming is never silent.
  - The inventory turn reads the tree with the architect's normal toolbelt; a contract failure follows the existing repair-then-fail path rather than degrading to a guessed inventory.
- The parsed inventory is recorded on the run: a `survey_inventory` event (items, kinds, capped flag) appended to the run's event log, plus the inventory written as a JSON file beside the run's brief (falling back to the run directory when the run has no brief file), so the draft turn and later readers work from the same recorded list.
- The draft turn's prompt carries the recorded inventory as a checklist with the instruction that every item is either covered by a section or named in the document as deliberately out of scope — the checklist is injected by the engine from the recorded artifact, not re-typed by the inventory turn.
- When the proposal lands, the engine computes the coverage diff and records it: an item counts as covered when its name or its evidence-path basename appears in some section body or in an explicit out-of-scope line of the proposed document; the check is lexical string matching only.
  - Unaccounted items are stored on the run record (`PendingData`, following the `unread_refs` precedent) and surfaced like the `sections_removed` warning: visible, never blocking the accept path.
  - Computation lives beside the existing proposal-time warning computation in `internal/service/stages.go` and runs only for the two adopt paths.
- The reviewer/critique prompt for adopt runs receives the unaccounted list, so critique aims at the named gaps; when nothing is unaccounted the prompt is unchanged.
- Tests assert: an adopt run whose draft omits an inventoried item records that item as unaccounted on the run record; covering the item in a section body or naming it in an explicit out-of-scope line clears it; a capped inventory's event names the cap; a non-adopt intake and a non-first spec run produce no inventory turn and no coverage data.

**Out of scope:** desktop rendering of the unaccounted list or the inventory (T-901); semantic or fuzzy matching beyond lexical; auto-generating sections for unaccounted items; any change to the references machinery; applying the inventory pass to plan, review, or non-adopt stages.

**Assumption:** The cap is fixed at 60 items as the change suggests ("e.g. 60"); it is a constant in the engine, not a configurable setting.

### T-101 — Surface survey inventory and unaccounted coverage on proposal cards and the run view

**Implements:** SPEC-062
**Depends on:** T-100

The desktop shows what the engine recorded: the proposal card names unaccounted surface areas, and the run view lets a person read the full inventory with its evidence paths.

**Deliverables:**
- The proposal card in the Cycle view gains a line, reusing the `unread_refs` pattern and styling, reading "N surface areas unaccounted: X, Y, Z" when the run record carries unaccounted inventory items; the line is absent when the list is empty or the run is not an adopt survey.
  - Names shown come from the run record's `PendingData`; the card never recomputes coverage.
  - The line is informational only — Accept and Reject behave exactly as before.
- The same line renders on the proposal card in RunView, sharing the Cycle view's component or helper rather than duplicating the formatting.
- The run view exposes the recorded inventory as a folded (collapsed-by-default) block listing each item with its kind and evidence path, read from the inventory artifact the engine recorded on the run.
- Frontend tests assert: a run record with unaccounted items renders the naming line on both cards; an empty list renders nothing; the folded block lists every inventory item with its evidence path.

**Out of scope:** engine-side computation of coverage (T-900); any blocking or gating on unaccounted items; MCP or CLI surfacing; restyling the existing `unread_refs` line.

### T-102 — Add dev-only engine connection fallback via ?engine=&token= query params or VITE_ env when window.ducklab is absent

Fixes B-108.

## Reported

window.ducklab (baseUrl+token) is injected only by the Wails shell, so `npm run dev` in a plain browser has no way to connect to a real or fake engine — a frontend contributor must build and install the whole desktop to see any change live, and cmd/fake-engine (built precisely so the frontend can be exercised without a model, GPU or repo) is reachable only through the playwright fixtures. A dev-only fallback — read ?engine=&token= query params (or VITE_ env) when window.ducklab is absent in dev builds — would make the induction loop: go run ./cmd/fake-engine, npm run dev, open the URL. Contributor friction found while preparing the docs for collaborators.

**Deliverables:**
- When window.ducklab is absent in a dev build, App resolves connection from ?engine= and ?token= URL query params (and/or VITE_DUCKLAB_ENGINE / VITE_DUCKLAB_TOKEN env) instead of erroring
- The fallback is gated to dev builds (import.meta.env.DEV) so production/desktop behavior is unchanged
- The existing 'no engine connection details' error still appears when neither window.ducklab nor query/env params are present
- A vitest test covers: query params produce a working EngineClient; absence of both sources still surfaces the error
- README or contributor docs gain the induction loop: go run ./cmd/fake-engine, npm run dev, open http://localhost:5173/?engine=...&token=...

## Triage

**Component:** frontend connection bootstrap
**Suspected files:** frontend/src/app/App.tsx

window.ducklab is injected only by the Wails shell, so npm run dev in a browser dead-ends before any engine connection, blocking the fake-engine induction loop the fix should close.

**Verification (triage recommends):** test-first — Vitest render of App with window.ducklab undefined and location.search='?engine=http://x&token=t' must construct the EngineClient instead of the 'no engine connection details' error

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-103 — Apply task-id renames to body prose in PlanTaskIDs, not just Depends-on lines

Fixes B-114.

## Reported

AssignIDs+RewriteReferences remap placeholder ids (T-900, T-901, SPEC-900) in section ids and field lines (Implements:, Depends on:) but not in running prose, so an architect who writes .that is T-901. or .the engine field from T-900. inside a deliverable ships those stale ids into the accepted document. Evidence in the CURRENT plan.md: the fresh T-096/T-097 pair arrived citing T-900/T-901 in body text (hand-patched post-accept, commit refs this bug), and the older chat-images tasks still carry the same scars around lines 1408/1806/1821/1826 — the pattern repeats on every extend that cross-references its sibling. Fix: RewriteReferences applies the remap to body text too (word-boundary replacement of mapped ids), with a test where two placeholder tasks cite each other in prose.

**Deliverables:**
- PlanTaskIDs applies the renamed map to task Body prose (word-boundary aware, so T-900 inside T-9000-like text is untouched), not only to the Depends-on field
- A test in internal/stage/ids_test.go feeds two placeholder tasks that cite each other in body prose and asserts no placeholder id survives the renumber
- Existing tests (TestRenumberingCarriesDependenciesWithIt, overlapping-id handling) still pass
- Implements/Depends-on field rewriting behaviour is unchanged

## Triage

**Component:** stage / plan id renumbering
**Suspected files:** internal/stage/ids.go, internal/stage/ids_test.go

PlanTaskIDs renumbers tasks (T-900→T-096) but only remaps the Depends-on field via remapDeps, leaving stale placeholder ids in running prose of accepted plans; RewriteReferences never sees task-level renames.

**Verification (triage recommends):** test-first — PlanTaskIDs with two produced tasks T-900/T-901 citing each other in prose: after renumber to T-096/T-097, body must not contain T-900/T-901

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-104 — Add build/test default-mode selects to Settings wired to the existing buildMode/testMode state and save path

Fixes B-117.

## Reported

The person asked where to pick the preferred mode for test runs and build runs. Nowhere: the engine supports it end to end — Defaults.BuildMode/TestMode in config, GET/PUT /v1/defaults/modes carries build_mode/test_mode, and resolveBuildMode is the unified omitted-mode rule (settings win, then the project [modes] habit, then solo) — and Settings.tsx even LOADS the values into buildMode/testMode state and SAVES them back in the PUT... but no selector ever calls setBuildMode/setTestMode. The value round-trips invisibly; today it is editable only by hand in config.toml. Fix: two labeled selects (build runs open in / test runs open in: solo|pair|tournament|split, blank = project habit then solo) in the Settings section that already owns the modes payload, wired to the existing state and save path; a test pins that changing the select lands in the PUT body. The per-project [modes] table can stay config-only for now, but say so in the field help text.

**Deliverables:**
- Two labeled selects ("build runs open in" / "test runs open in") render in the Settings section owning the modes payload, with options solo|pair|tournament|split plus a blank choice meaning project [modes] habit then solo
- Each select's onChange calls setBuildMode/setTestMode and marks the form touched so the shared Save button applies
- Changing a select and saving lands the chosen value in the modeDefaultsSet PUT body as build_mode/test_mode
- Help text on the controls states the per-project [modes] table stays config.toml-only for now
- A frontend test asserts the select-to-PUT-body round-trip (fails before the fix, passes after)

## Triage

**Component:** frontend settings
**Suspected files:** frontend/src/views/Settings.tsx

The engine honors Defaults.BuildMode/TestMode end-to-end and Settings loads/saves the values, but no UI control ever calls the setters, so the preference is invisible and editable only by hand in config.toml.

**Verification (triage recommends):** test-first — Render Settings with a stubbed client, change the default-build-mode select, click settings-save, assert modeDefaultsSet body carries build_mode/test_mode — budget.test.tsx already has this exact mock-and-assert pattern.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-105 — Remove the 'your team' roster section from Settings

Fixes B-118.

## Reported

The your team section in Settings edits the GLOBAL ROLE PINS — rung 4 of seat precedence (project mode seat, project role pin, global mode seat, global role pin) — through eight dropdowns, while the Roster board is the canonical seat surface and its Common section already edits the same pins for the roles where they matter (triager, consultant, scribe, advisor). Two editors over one datum is a silent-overwrite risk (full-payload saves), and the section footer itself says role assignments are managed on the Roster board. The person asked what practical utility the section has; the honest answer was almost none, and a read-only replacement adds no value either. Scope: DELETE the team section from Settings — its dropdowns and the roster summary; move the verification card (gate + link_deps display) that currently rides at the top of that section to a sensible surviving home; change the Settings default section (team was the landing tab) to the most-used remaining one; keep the role-pin data and resolver rung untouched (Roster board Common remains its editor; non-common role pins become config-file-only, which is fine because a global implementer pin is a trap the UI should not offer). Tests: settings tests that reference the team section are rewritten with this reasoning, and a test pins that the removed dropdowns no longer ride the settings save payload so a stale form cannot wipe pins the Roster board wrote.

**Deliverables:**
- Settings renders no fn-* function rows or roster-select-* dropdowns; the 'team' nav entry is gone
- The verification card (gate, setup, link_deps) still renders in a surviving section
- Settings no longer lands on the removed section; the default is a remaining one
- A rewritten test asserts the dropdowns are absent and the settings save payload never includes role pins
- Roster board (Ducklings.tsx) remains the only UI editor of role pins; roster data and resolver rung logic untouched

## Triage

**Component:** settings-ui
**Suspected files:** frontend/src/views/Settings.tsx, frontend/src/views/budget.test.tsx

A second editor over the global role pins duplicates the Roster board and risks silent overwrite; the fix is a UI deletion plus card relocation, cleanly testable by asserting the removed widgets don't render or leak into the save payload.

**Verification (triage recommends):** test-first — render Settings: assert fn-*/roster-select-* absent, team nav entry gone, verification card relocated to a surviving section, and the save payload carries no role-pin fields

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-106 — Move the running section to the top of the Now view

Fixes B-119.

## Reported

The running section should be at the top of the Now view instead of at the bottom. When I open the Now view I expect to see what's currently running at the top of the view.

**Deliverables:**
- In frontend/src/views/Now.tsx the now-running section renders as the first section inside now-view, ahead of now-waiting, now-verify, now-reopened, and now-failures
- A component test renders Now with at least one active run plus one fixed bug/waiting run and asserts now-running's DOM position precedes the other sections
- Existing Now view tests (waiting, verify, quiet, footer) keep passing unchanged

## Triage

**Component:** desktop Now view
**Suspected files:** frontend/src/views/Now.tsx

The screenshot and Now.tsx both show the running section rendered last in the Now view column, after waiting/verify/reopened/failures; moving the now-running block to the top of the returned JSX is a one-spot reorder with an honest DOM-order test.

**Verification (triage recommends):** test-first — Render Now with a fixed bug and an active run in the store; assert now-running precedes now-verify (compareDocumentPosition or child order of now-view).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-107 — Suggest a measurably stronger seat at FAILED-run and distress decision points when deliverable progress stalls

Fixes B-120.

## Reported

Watching T-100 climb past 13 turns raised it: when the measurable signals say a duckling is at capacity on a task, ducklab should SUGGEST escalating to a seat whose scorecard is provably stronger — never route silently (the house law: suggested, never routed; an auto-escalation ladder is the failover pattern ducklab deliberately is not).

The trap this must respect, by name: B-098. From the outside, at-capacity and badly-briefed look identical — same climbing turns, same red gates. Luna failed T-090 three times on a vague report and fixed it in one pass once the report carried anchors; naive escalation would have burned a 20x-cost seat against the same bad prompt. The suggestion therefore ALWAYS offers both diagnoses.

Design:
- WHERE: existing decision points only — the FAILED run card (beside Retry with this note) and the distress pause. Never mid-turn, never automatic.
- WHAT IT SHOWS: evidence, side by side — the struggling seat's signals this run (turns vs the mode median, consecutive red gates, brake refusals) and the candidate's scorecard (in-seat pass rate with the Wilson floor, cost/run). Three actions: relaunch with the stronger seat; improve the task body (brief-quality failures wear the same costume); continue as-is.
- WHAT CARRIES OVER: test-first makes escalation cheap and honest — the committed red test is the portable definition of done; the new seat starts fresh against the same red, no failed diff carried.
- TRIGGER: thresholds over what is already measured (e.g. agent turns exceed 2x the mode median, or N consecutive red round gates), with the firing threshold named in the run event — narrated, never silent.
- CANDIDATE RULE: only a seat measurably stronger on THIS kind of work (same-role scorecard, minimum runs, Wilson lower bound above the current seat's) — no candidate, no suggestion; the card says why when asked.

Sibling of the queued capability-asymmetric-seating idea (report-card SUGGESTED pre-run); this is the same principle mid/post-run with failure evidence in hand. Reported while r-20260822-215745 (T-100) was live at 13+ turns.

UPDATE (same day, verified in r-20260822-215745): the person watching the transcript spotted the sharpest trigger signal, and the record already structures it — deliverables_report events carry a `missing` array per implementer report. The run shows the exact at-capacity signature: missing [1,2,3,4,5,6] -> [4,6] -> [] -> [6] -> [6] -> [6] -> [], i.e. the checklist CONVERGED and then STUCK on one item across three consecutive reports, while the implementer hit its calls/reply cap three times and consulted the advisor five times. A stuck deliverable (same item missing in N consecutive reports) is a better escalation trigger than raw turn count: turns measure effort, a non-converging checklist measures progress. Make it the primary trigger, with turns-vs-median and red-gate streaks as secondaries — all three named in the firing event.

**Deliverables:**
- stuck-deliverable detection: same deliverable id in the missing/undelivered set across N consecutive deliverables_report events is the primary escalation trigger, with turns-vs-mode-median and consecutive red round gates as named secondaries
- an escalation_suggestion run event fires at run FAILED and at the distress pause (never mid-turn, never automatic), naming which of the three thresholds fired and their values
- the suggestion candidate comes from same-role scorecards ranked by Wilson lower bound with a minimum-runs floor, and is only emitted when the candidate's lower bound exceeds the current seat's; no candidate emits nothing but the card can explain why
- the suggestion payload carries both diagnoses side by side (seat-at-capacity signals this run vs task-brief quality) and offers the three actions: relaunch with stronger seat, improve task body, continue as-is
- tests cover: stalled-checklist run emits the event with the stuck item named; converging run at equal turn count emits nothing; no-stronger-candidate case emits no suggestion

## Triage

**Component:** escalation-suggestion
**Suspected files:** internal/strategy/execute.go, internal/strategy/rubberduck.go, internal/service/candidates.go, internal/service/roster.go, internal/service/lifecycle.go

New observability-plus-suggestion feature over already-recorded deliverables_report/distress events and the existing Wilson-ranked candidate machinery, with concrete trigger semantics spelled out in the report.

**Verification (triage recommends):** test-first — Trigger logic is pure computation over recorded run events: feed a run record whose deliverables_report missing-arrays stall on one item across N reports and assert an escalation-suggestion event fires naming all three triggers, with a candidate drawn from RankCandidates Wilson-floor ordering.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-108 — Build a ducklab-mcp.mcpb bundle (manifest + linux binary) as a release artifact so the official MCP registry can list ducklab

Fixes B-112.

## Reported

registry.modelcontextprotocol.io only accepts servers installable from trusted sources: npm/PyPI/NuGet/OCI, a remote URL, or an MCPB bundle attached to a GitHub release. ducklab mcp serve is a locally built Go binary, so ducklab cannot be listed yet. Path of least resistance: build an .mcpb bundle (manifest + linux binary) as a release artifact in the release flow, then publish docs/mcp-registry/server.json with mcp-publisher. The draft server.json and the full send kit are in docs/mcp-registry/.

**Deliverables:**
- A make target or script (e.g. `make mcpb`) cross-builds the linux/amd64 ducklab binary and assembles ducklab-mcp.mcpb per the MCPB spec (manifest.json + binary)
- The bundle manifest declares `ducklab mcp serve` as the stdio command, matching internal/cli/mcp.go's invocation contract
- An automated e2e/shell check builds the bundle, extracts it, and verifies the manifest and binary and that the binary answers the manifest's declared invocation
- The release flow attaches the .mcpb to GitHub releases and docs/mcp-registry/server.json's vX.Y.Z placeholder and README publishing steps are updated to point at the real artifact

## Triage

**Component:** release packaging / MCP distribution
**Suspected files:** Makefile, docs/mcp-registry/server.json, docs/mcp-registry/README.md, internal/cli/mcp.go

ducklab mcp serve already exists as a stdio MCP server; the only gap is that no release flow produces the MCPB bundle the official registry requires, which is verifiable by building and extracting the bundle.

**Verification (triage recommends):** test-first — An e2e-style shell check can run the bundle target, unzip the .mcpb, assert manifest.json plus the linux binary are present, and invoke the binary per the manifest's declared stdio command (mirroring e2e/ac_test.sh).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-109 — Export acceptance receipts and add ducklab proof verify

Fixes B-113.

## Reported

An accept already records everything a third party would need — the committed sha, the gate command, its exit code, duration, and the clean-checkout reproduction verdict (acceptance_gate on the run record). But the only consumer is the operator who trusts their own engine: there is no artifact someone WITHOUT the engine can verify. Utility appears at trust boundaries, ranked: (1) compliance/audit evidence for client work — a payroll-logic change carries a receipt presentable to an auditor without repo access; (2) the launch-post skeptic — `ducklab proof verify` lets a stranger check the built-itself claim in one command instead of trusting committed JSONL; (3) contributor PRs — verify the gate passed on THIS exact diff without re-running a gate that cannot run in CI (local models, GPU); (4) future multi-machine swarm — the gate decider verifies runs executed elsewhere.

SCOPE (deliberately the cheap core): export the acceptance data a run already records as a standalone receipt file — facts only: base/head shas, diff sha256, gate command, exit code, duration, reproduction verdict, accepted-by and when — plus `ducklab proof verify <receipt>` that re-derives the hashes against the repo and exits 0/1. OUT of scope for now, noted as extensions: GPG signatures, mock-integrity and test-mutation gates, receipts for non-accept events.

Priority note: AFTER the MiEmpresa plan and parallelism phase 1 — this is a when-the-boundary-appears feature, and two of the three boundaries (contributors, swarm) just entered the roadmap. Inspired by loki-mode evidence receipts (github.com/asklokesh/loki-mode); the mechanism here reuses what acceptRun already proves.

**Deliverables:**
- Accepting a run writes a standalone receipt file containing base/head shas, diff sha256, gate command, exit code, duration, reproduction verdict, and accepted-by/timestamp
- `ducklab proof verify <receipt>` re-derives the receipt's hashes against the repo and exits 0 on match, 1 on any mismatch or missing field
- A tampered receipt (altered sha or exit code) makes proof verify exit 1
- A Go test round-trips an accept-produced receipt and asserts both exit-0 and exit-1 paths

## Triage

**Component:** acceptance/proof
**Suspected files:** internal/service/service.go, internal/runlog/runlog.go, cmd/ducklab/main.go

Additive trust-boundary feature exporting data acceptRun already records (GateReproduced, CommitSHA, actor) as a verifiable receipt; no duplicate among open bugs and no existing proof/receipt code.

**Verification (triage recommends):** test-first — Receipt write + `proof verify <receipt>` exit 0 on matching repo state and 1 on a tampered hash — round-trip is executable.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-110 — Leave the TDD chain build's mode empty unless explicitly picked so RunStart resolves it via resolveBuildMode

Fixes B-121.

## Reported

T-101: the person has Defaults.BuildMode=pair (verified via GET /v1/defaults/modes), yet the TDD-chained build r-20260822-235346 ran SOLO with mode_source=request — the desktop test launcher fills ChainBuild.Mode explicitly (apparently with its own test-phase mode or a hardcoded default) instead of leaving it empty, so RunStart never consults resolveBuildMode and the person's setting is silently overridden. An unchained RunStart with no mode (T-098) correctly resolved to pair. Fix: the chain config leaves Build.Mode empty unless the person explicitly picked a mode for the build leg in the launcher; mode_source on the run record already proves which path answered, so the regression test can assert a chain launched without an explicit pick lands with mode_source=settings, not request.

**Deliverables:**
- TddLaunch/Board/Now only put an explicit mode on the chain build request when the person picked one in the launcher; the build leg defaults to empty mode
- accepting a TDD test whose chain build carries no explicit mode produces a chained build run with mode resolved via resolveBuildMode and mode_source=settings (or project/fallback), never a hardcoded solo with mode_source=request
- an explicit build-leg pick in the launcher still lands on the chained run with mode_source=request
- a regression test asserts the chained run's mode/mode_source in both the unpicked and picked cases

## Triage

**Component:** tdd chain launch
**Suspected files:** frontend/src/components/TddLaunch.tsx, frontend/src/views/Board.tsx, frontend/src/views/Now.tsx, internal/service/testfirst.go, internal/service/service.go

TddLaunch seeds buildCfg.mode from the settings default, so the chain always marks the build leg as an explicit request; RunStart then never consults resolveBuildMode and silently overrides the person's BuildMode with whatever the launcher prefilled.

**Verification (triage recommends):** test-first — TDD-chain a test run with Defaults.BuildMode=pair and no explicit build-mode pick, accept it, then read the chained build run's state.json: mode should be pair with mode_source=settings, not solo with mode_source=request.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-111 — Attach gate output to the final gate event and the failed run record

Fixes B-122.

## Reported

Build r-20260822-235346 (T-101) failed its final gate (exit 1) and the gate event payload carries only {cmd, exit, gate} — no output field — so the desktop showed a FAILED run with nothing to explain it (verify: no output). The round_gate and the accept reproduction (gate_reproduced) both carry output; the final verify is the odd one out, and it is precisely the event a person reads when a run dies. Fix: the final gate event carries the gate output like its siblings (bounded the same way), and the run record failure field gets the tail so the FAILED card can quote it. The diagnosis that required re-running the whole suite by hand today — survey-inventory.test.tsx failing only in full-suite company — should have been one glance at the card.

**Deliverables:**
- the final-gate AppendEvent("gate", …) in internal/service/service.go (~line 1651) includes the bounded gate output, matching the shape of the gate_reproduced events (gate, command, exit_code, output, duration_s)
- output is bounded the same way sibling gate outputs are bounded (no unbounded event payloads)
- when the final verdict is FAILED, rs.run.Failure carries the tail of the gate output so the FAILED card can quote it
- a test drives a run with a failing final gate and asserts both the gate event's output field and the run record's Failure tail

## Triage

**Component:** run gate events / verification
**Suspected files:** internal/service/service.go, internal/service/testfirst.go

Confirmed in source: the final verify event at internal/service/service.go:1651-1655 records only {gate, cmd, exit} while gate_reproduced (service.go:2099/2159) records full output, so a FAILED run explains nothing.

**Verification (triage recommends):** test-first — Run a build whose final gate exits 1; assert the persisted "gate" event data carries "output" and the failed run's Failure field quotes the output tail (sibling gate_reproduced test at lifecycle_test.go:606 is the pattern).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-112 — Emit advice_started and animate advisor drafting in the question card and seat panel

Fixes B-124.

## Reported

While a question card shows qwen38-max is preparing a recommendation, nothing else moves: no duck-in-action, no token counter for the advisor, no way to tell drafting from hung. Cause: the advisor draft is a non-streaming one-shot with no turn events — the seat panel and streaming counters are driven by turn_start/deltas that this path never emits, and the tracker records the spend only on completion.

Fix, layered: (1) emit advice_started {advisor, question_id} when the draft begins (advice / advice_failed already close the arc), and register it in events.ts; (2) the question card animates the preparing line with the house cog (cog-turn, respects prefers-reduced-motion) while advice_started is open, and stops on advice/advice_failed; (3) the run view seat panel marks the advisor seat active during that window. STRETCH, separate decision: make the one-shot use ChatStream so the +streaming token counter ticks for the advisor like any turn — worth it only if the cog alone proves insufficient, since oneShotChat (B-123) is deliberately simple.

Done means: watching a question with an advisor seated, the preparing line visibly spins from the moment it appears, and stops with either the recommendation or the failure note; a person can tell drafting from dead at a glance.

**Deliverables:**
- adviseQuestion (internal/service/advisor.go) appends an advice_started event with advisor and question_id before the one-shot draft begins, on both the paused-question and inline consult paths if they share the arc
- advice_started is registered in frontend/src/api/events.ts so it is stored and replayed
- buildPending in frontend/src/lib/runview.ts derives advisorPending from an advice_started with no matching advice/advice_failed for that question_id (not merely from the absence of advice), so the window opens at draft start
- the preparing line in RunView.tsx (data-testid advisor-pending) carries the cog-turn animation while that window is open and stops on advice or advice_failed
- a Go test asserts advice_started precedes advice/advice_failed in the event log, and a runview.test.ts case asserts the pending window opens on advice_started and closes on advice/advice_failed

## Triage

**Component:** advisor / run view
**Suspected files:** internal/service/advisor.go, frontend/src/api/events.ts, frontend/src/lib/runview.ts, frontend/src/views/RunView.tsx, frontend/src/app/index.css

The advisor draft is a non-streaming one-shot that emits no lifecycle event until completion, so the UI has no signal to animate; adding advice_started closes that gap and the frontend layers render it.

**Verification (triage recommends):** test-first — Go test asserts adviseQuestion writes advice_started before advice/advice_failed in events.jsonl; runview.test.ts asserts advisorPending derives from an open advice_started and closes on advice/advice_failed — the cog spin itself is existing CSS (cog-turn already respects prefers-reduced-motion).

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-113 — Run build and test-first stages in per-run git worktrees outside the repo

**Implements:** SPEC-033, SPEC-025, SPEC-008

Make every build/test-first run execute in its own worktree and branch so the person's working tree never contains unsigned work, and free the queue to run builds in parallel. Document stages keep today's serialized in-tree path (D3).

**Deliverables:**
- RunStart/TestStart for build and test-first stages create a worktree at `~/.local/state/ducklab/worktrees/<project-id>/<run-id>/` on branch `ducklab/<task-id>-<run-id-suffix>` (D4: outside the repo).
  - Creation goes through the existing per-repo worktree mutex (`vcs.Git.WorktreeAdd`, T-099); base is current default-branch HEAD, recorded on the run as its base sha.
  - Run record persists worktree path, branch, and base sha, exposed through the run API for the desktop.
- The run's ExecContext roots fs, shell, and verify in the worktree.
  - `[verify] link_deps` and `setup` populate the worktree exactly as the accept checkout does today (reuse the SPEC-060 machinery); run-identity stamping (T-098) is unchanged.
- The queue drops the one-run-per-working-tree hold for worktree-capable runs only.
  - Provider caps (T-096/097) and `max_concurrent_runs` still govern; document stages keep the working-tree hold and the serialized in-tree path.
- Cleanup failure leaves evidence: a worktree that cannot be removed is logged and named on the run record, never silently orphaned.
- Tests assert:
  - a build run writes nothing to the person's working tree (byte-identical before/after while live);
  - two builds from the same repo run concurrently under the mutex without corrupting `.git/worktrees`;
  - a document stage still acquires the working-tree hold and serializes against other tree users.

**Out of scope:** accept-path changes (T-901); document stages in worktrees; converging tournament/split contestant workspaces onto this lifecycle; cross-machine workers.

**Assumption:** `~/.local/state/ducklab/` follows the existing engine state-dir convention; if a config override for state dir exists, the worktree root honors it.

### T-114 — Accept becomes a merge proof: rebase, gate the rebased sha, ff-only merge

**Implements:** SPEC-060, SPEC-008, SPEC-009, SPEC-020

(Dependency note, night operator 2026-08-24: T-113's contract landed on main in three verified slices — df7d72e, 5b74a84, f6e6e58 — outside the accept record, because the running engine refuses isolated-run acceptance until this very task ships. The depends-on line is released so this task can start; the dependency itself is satisfied in substance.)

Enforce D1: nothing lands that did not reproduce AS MERGED. acceptRun for a worktree run rebases the run branch onto current default HEAD and runs the gate from a clean checkout of the rebased sha; only green merges. Textual rebase success alone never lands work.

**Deliverables:**
- acceptRun for a worktree run: commit in the worktree branch, rebase onto current default HEAD, run the gate from a clean checkout of the rebased sha (reuse `verifyAcceptedCommit`), and on green fast-forward-only merge into the default branch.
  - Acceptance receipt names the rebased sha; a non-ff merge is refused, never forced.
  - Stage polarity preserved: build accept requires green; test-first accept requires the committed test's assertion-red — reproduced on the rebased sha.
- Fast path: when default HEAD equals the run's recorded base sha, the rebase is trivial and skipped — the existing clean-checkout reproduction already covers the merged result, and no second gate run is added beyond today's.
- Red after rebase: the merge does not happen; the run pauses at the human gate with the full gate output attached (B-122 style) and the divergence named — base sha vs default sha.
- Rebase conflict (D2, human-decides): abort the rebase cleanly, recovering any stale `REBASE_HEAD`/`MERGE_HEAD` state; the run pauses at the gate naming the conflicting files and both shas.
  - The paused card offers exactly: resolve by hand in the worktree (path shown) or reject. NO automatic agent resolution loop.
- REJECT for a worktree run drops the worktree and branch with no restore surgery — the person's tree was never touched.
- Tests pin the law:
  - two parallel builds from the same base where the second semantically conflicts with the first (no textual conflict) goes RED at its merge gate and never lands on default;
  - a textual conflict pauses at the human gate with files named and leaves no `REBASE_HEAD`/`MERGE_HEAD` behind;
  - reject leaves the person's tree byte-identical;
  - the fast path (default unmoved) adds no second gate run;
  - the receipt carries the rebased sha, not the pre-rebase sha.

**Out of scope:** automatic conflict resolution (possible phase 3.1, only as an explicit button with a re-run gate, never silent); changing the document-stage accept path; tournament/split contestant mechanics.

### T-115 — Worktree hygiene on engine start and the desktop worktree surface

**Implements:** SPEC-033, SPEC-062, SPEC-025

(Dependency note, night operator 2026-08-24: T-113 landed on main — df7d72e, 5b74a84, f6e6e58 — and T-114 as 80e8e29, outside the accept record; depends-on released, dependency satisfied in substance.)

Keep worktree state honest across crashes and restarts, and make the desktop show where a run actually lives. Hygiene patterns studied from wallfacer's git-worktrees internals (MIT) — credit in comments; the merge-proof discipline remains ours.

**Deliverables:**
- Engine-start hygiene: prune worktrees not matching any known run and run `git worktree prune`; reattach with `--force` when a directory vanished but its branch persists; GC worktrees of decided (accepted/rejected) runs.
  - Hygiene operations take the per-repo worktree mutex (T-099) like every other worktree mutation; comments credit wallfacer (MIT) for the pattern.
- The desktop run card for a worktree run shows a worktree badge with branch and worktree path while the run is live.
- The paused-conflict card renders the conflicting files, both shas, and the two lawful options (resolve by hand at the shown path, or reject).
- Queue reason strings stay truthful: a build no longer reports waiting on the working tree; a document stage still does.
- Tests assert:
  - startup prunes an orphaned worktree directory and reattaches a vanished-directory branch with `--force`;
  - a decided run's worktree and branch are GC'd;
  - one build and one document stage CAN run simultaneously and the document stage still holds the tree (queue reasons pinned);
  - desktop vitest: badge renders branch/path for a worktree run, and the conflict card lists files with both options.

**Out of scope:** cross-machine workers; converging tournament/split workspaces onto this lifecycle (later phase); any change to queued-run persistence across restarts.

### T-116 — Block run implementers from writing .ducklab/project.toml and surface governance-key changes at review and the human gate

Fixes B-138.

## Reported

Commit da96e60 (2026-08-16, 'ducklab: T-033', run r-20260816-203213-ppe3) contains, buried in the diff of a task whose contract was 'Thread the contract through applySampling and enforce its output cap' (SPEC-042 — sampling, nothing else):

  -autonomy = "guarded"
  +autonomy = "yolo"

in .ducklab/project.toml. The change is unrelated to the task's deliverables, was not requested, and passed review and the human gate unnoticed. Consequence: for EIGHT DAYS every run resolving autonomy from project settings ran unattended — including r-20260824-003539-zdln, where the operator went to answer an ask_human question and found the advisor had auto-answered in their name (B-137). The operator's statement 'I never selected unattended' is literally true.

Remediated 2026-08-24: autonomy restored to guarded via PATCH /v1/projects/ducklab.

The defect to fix is the missing guard, in two layers:
1. WRITE: .ducklab/project.toml is the project's GOVERNANCE config (autonomy, verify command, budgets, seats). A run's implementer should not be able to write it at all (same spirit as tests_modified tracking and the reviewer toolbelt: structural, not advisory). If a task legitimately needs a config change, it should go through a declared channel that surfaces at the gate.
2. DIFF/GATE: any candidate diff touching governance keys must be called out at review and at the human gate in plain words — 'this diff changes project autonomy from guarded to yolo' — the way tests_modified already is. A one-line TOML change in a 40-file diff is invisible; the harness knows the semantics and must speak them.

Evidence: git show da96e60 -- .ducklab/project.toml; git log -S 'autonomy = "yolo"' -- .ducklab/project.toml (single introduction).

Related: B-137 (what the silent yolo did downstream), B-129 (how unnoticed config drives seat surprises).

**Deliverables:**
- The fs_write tool (write guard in internal/tools) refuses writes to .ducklab/project.toml during runs, the same structural way tests_modified/harness paths are handled, with the refusal message naming the declared channel (PATCH /v1/projects) instead
- A test asserts an implementer-role fs_write of .ducklab/project.toml is refused and that the refusal is recorded on the run
- Candidate-diff analysis detects changes to governance keys (autonomy, verify, budgets, seats) in .ducklab/project.toml and produces a plain-words callout (e.g. 'this diff changes project autonomy from guarded to yolo') the way tests_modified is produced
- The governance callout is present in both the reviewer payload and the human-gate/accept surface for the run
- A test asserts a diff flipping autonomy guarded->yolo in project.toml yields the callout in the gate payload

## Triage

**Component:** write-guard / gate diff surfacing
**Suspected files:** internal/tools/fs.go, internal/tools/tools.go, internal/config/config.go, internal/service/candidates.go, internal/service/gate.go, internal/service/modes.go

A silent one-line autonomy flip in project.toml rode an unrelated task diff through review and the human gate and ran the project unattended for eight days; the fix is a structural write block plus semantic surfacing of governance keys at the gate — B-137 is the downstream symptom, not a duplicate, and exact file placement of the guard vs. gate annotation is inferred (search tooling was partially unresponsive) but the write guard clearly lives in internal/tools (fs.go/tools.go, IsHarnessPath) and gate/diff assembly in internal/service.

**Verification (triage recommends):** test-first — Both layers are assertable: fs_write of .ducklab/project.toml from an implementer turn must be refused, and a candidate diff flipping autonomy must appear as a plain-words governance warning in the review/gate payload — both are unit-testable against the write guard and the diff annotation code.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-117 — Make scorecard pass_rate count ACCEPTED as success for document stages

Fixes B-127.

## Reported

atom-local has written the release notes for all seven shipped releases and drafted accepted requirements/spec sections — and its scorecard says 4.76% overall (architect 12%, consultant 0%), because stage runs end UNVERIFIED by design (their gate is a human decision, not a test) and pass_rate counts anything that is not PASSED as a failure. The metric is honest for build/test seats (atom-local implementer 0/8 is real signal: long tool-chains at ~630s/run do not converge before caps) and defamatory for document seats, where the meaningful outcome is ACCEPTED, not PASSED.

Fix: pass_rate becomes stage-aware — for runs whose stage cannot produce a gate verdict (intake/spec/plan/release/chat/triage), the success signal is accepted (and superseded-by-revision counts as neither, like aborted), while build/test keep the gate verdict. measured_by_role already splits by role, so the seat suggestions and the Roster cards inherit the honest number automatically. A test pins: a scribe with seven accepted release runs and zero PASSED verdicts scores high, not 4.76%; an implementer with 0/8 green gates still scores 0.

Found while drafting an honest comment about Qwen3.8-27B from real scorecard data — the board called the release-notes author a 5% model, and the number was the lie.

**Deliverables:**
- Row pass_rate in internal/report no longer treats UNVERIFIED document-stage runs (intake/spec/plan/release/chat/triage) as failures: for those stages, run.Accepted is the success signal
- Document-stage runs resolved as superseded are excluded from both numerator and denominator, as aborted runs are
- Build/test stages continue to use the gate verdict (PASSED/FAILED/UNVERIFIED) unchanged
- Measured and MeasuredByRole scorecards (internal/service/scorecard.go) return the corrected rate, so Roster cards and seat suggestions (internal/service/roster.go, RosterSuggest) inherit it
- A test pins: scribe with seven accepted release runs scores high (not ~4.76%); an implementer with 0/8 green gates scores 0

## Triage

**Component:** scorecard/report pass_rate
**Suspected files:** internal/report/report.go, internal/service/scorecard.go, internal/service/roster.go, internal/report/comparable_test.go, internal/service/scorecard_evidence_test.go, internal/service/candidates.go

The report aggregation run-by-stage logic and scorecard/roster readers are the defamation source: for gate-free stages the honest outcome is ACCEPTED, and superseded/aborted must be neutral, while build/test keep gate verdicts — all pure arithmetic, testable.

**Verification (triage recommends):** test-first — Build report rows from synthetic runs: a scribe with 7 accepted release runs and verdict UNVERIFIED must score ~100%, not 4.76%; an implementer 0/8 green gates stays 0; a superseded run counts in neither numerator nor denominator.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-118 — Clear board state on project switch and render fetch failure instead of stale cross-project rows

Fixes B-128.

## Reported

Opening MiEmpresa showed two triaged reports that talk about ducklab — they are ducklab's B-125 and B-126 (the only two triaged bugs anywhere), while GET /v1/projects/mitimesheet/bugs returns {items: [], total: 0}. Cause in Board.tsx load(): the fetch is correctly keyed by projectId, but on a project switch the PREVIOUS project's bugs/tasks state stays mounted until the new fetch resolves — and if that fetch REJECTS, the failure branch only appends a problem line while the stale arrays keep rendering, so another project's bugs sit under the new project's name indefinitely. Cross-project data under the wrong header is the worst kind of stale. Fix: clear tasks/bugs (and show the loading state) synchronously when projectId changes, before fetching; on fetch failure render the failure INSTEAD of stale foreign data, never alongside it. A test pins: switch project while the second fetch is pending or failing — the board never shows rows from the first project under the second's name.

**Deliverables:**
- When projectId changes, tasks and bugs (and any selection pointing at them) are cleared synchronously before the new fetch, so the board shows its loading state rather than the previous project's rows
- When the tasks/bugs fetch rejects, the failure message renders INSTEAD of the card columns — no stale rows from any project remain on screen alongside the error
- A fetch that resolves after projectId has already changed does not write its results into the now-current project's board (stale-generation guard or equivalent)
- A test in board.test.tsx switches projectId while the second project's fetch is pending, and asserts the first project's cards are gone before and after the second fetch settles
- A test asserts the rejection branch shows board-error and no board-cards from the previous project

## Triage

**Component:** frontend Board view
**Suspected files:** frontend/src/views/Board.tsx, frontend/src/views/board.test.tsx

Board.load() never clears tasks/bugs on a projectId change and its rejection branch only appends a problem line, so another project's rows render under the new project's name indefinitely — verified by reading the component.

**Verification (triage recommends):** test-first — Render Board with project A resolved, rerender with projectId B whose tasks/bugs fetch is pending then rejects: assert A's cards never appear under B and board-error shows with zero stale cards.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-119 — Stop offer­ing apr­ovals shell policy cannot honor: fix the not-in-allow­list message and the ask guidance

Fixes B-139.

## Reported

r-20260824-031558-hznm: the implementer asked 'may I run git cherry-pick with approval?', the human answered approved, the implementer retried, and the static shell allowlist blocked it again — producing a SECOND identical question. The question's own options ('Approve `git cherry-pick ...`') promise an approval that changes nothing: there is no mechanism by which a human answer extends the shell policy for a command, one run, or one invocation.

Two coherent fixes, pick one deliberately:
1. Honor the promise: an approved-commands channel — the answer flow can whitelist an exact command string for this run (recorded on the run, one-shot, exact match), so 'approved' means executable.
2. Keep the invariant (REQ-008/SPEC-008: git mutations are the engine's exclusive job) and make the QUESTION honest: when the asked-about command is permanently outside policy, the question card should say so ('this command is never runnable by a model; ask for the outcome instead') and the ask_advisor/ask_human prompt guidance should steer models to ask for OUTCOMES (files recovered, state provided) rather than command approvals.

Either way, the current shape — an approval option that is a no-op — burns a human round-trip and teaches models that approval is noise. Night instance resolved by the operator materializing the needed content into the run worktree by hand.

**Deliverables:**
- ShellPolicyCheck's not-in-allowlist error no longer invites ask-for-approval; it says the command is not runnable by a model in this policy and to ask for the outcome/decision instead
- AskHuman.Description says asking is for decisions the task underdetermines, not for shell-command approvals
- Implementer prompt ask guidance steers toward outcome/decision questions rather than command approvals
- Unit tests in tools_test.go/exec_test.go assert the corrected message/guidance wording and reject the old promise
- Fix option chosen deliberately per the bug: keep the invariant, make the question honest (option 2), not the approved-commands channel (option 1)

## Triage

**Component:** tools/shell-policy + ask_human guidance
**Suspected files:** internal/tools/tools.go, internal/tools/exec.go, internal/agent/agent.go, internal/tools/tools_test.go, internal/tools/exec_test.go

The shell policy error and ask guidance promise an approval channel that does not exist, burning human round-trips; the honest fix is to make the message tell models to ask for outcomes, and the invariant stays.

**Verification (triage recommends):** test-first — ShellPolicyCheck on a guarded policy with a non-allowed command must return a message that names 'not model-runnable' and does not say 'ask the human for approval'; AskHuman.Description must not extend the same promise; assertible as deterministic string tests.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-120 — Add a 'landed' terminal resolution so operator-landed rejects stop counting as FAILED

Fixes B-140.

## Reported

Night of 2026-08-23/24: the installed engine refuses isolated-run acceptance (pre-T-114), so the operator landed accepted work manually — diff audited, full gate reproduced from a clean checkout, commit on main with the Ducklab-Run trailer — and closed each run with reject + a landing note. Consequences visible this morning:

1. The Runs board shows a wall of x failed for runs whose code is on main (T-116: 971cf8c; T-113 slices: df7d72e, 5b74a84, f6e6e58). The reject reason says 'ACCEPTED in substance' but no surface reads reasons.
2. Scorecards count these as in-seat failures: terra's implementer pass rate takes hits for landed work, and gemini37flash's reviewer record shows 0% over 6 runs where most 'failures' were landings. Evidence-based seating (the roster suggestions) is being fed corrupted evidence — B-127's skew in a new costume.

Fix direction: a distinct terminal resolution — e.g. 'landed' (or reject with resolution=landed) — that (a) renders on the board as its own state, not x failed; (b) counts as a PASS (or at least not a failure) in per-seat scorecard math; (c) records the landing commit sha so the trailer link is bidirectional. The night's landing notes all begin 'Work ACCEPTED in substance and landed on main' and name the commit — enough to backfill these runs once the state exists.

**Deliverables:**
- A way to close a run as landed (reject accepting a resolution value or equivalent) that records resolution=landed instead of verdict=FAILED
- Run record stores the landing commit sha so the trailer link is bidirectional
- report PassRate/scorecard math treats landed runs as pass (or excluded) rather than failure
- The runs list/board renders landed as its own state, not 'x failed'
- A test pins: a run closed with resolution=landed does not lower the seat's pass rate

## Triage

**Component:** run lifecycle / scorecards
**Suspected files:** internal/service/service.go, internal/report/report.go, internal/service/scorecard.go, internal/engineapi/engineapi.go, internal/engineclt/engineclt.go, internal/cli/cli.go

Reported normal; reproduces deterministically as RunReject setting FAILED which report.PassRate counts against every seat; distinct from B-127's stage-aware pass_rate fix.

**Verification (triage recommends):** test-first — A run closed via reject-with-landed-resolution must record resolution=landed, keep Verdict non-FAILED (counted as pass or excluded), and scorecard PassRate must not drop — all assertable on service + report layers.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-121 — Run launcher seats the wrong ducklings: roster entries mapped positionally into mode seats, and filter(Boolean) shifts seats on 'default'

Fixes B-129.

## Reported

T-113's runs seated implementer/reviewer with ducklings from OTHER roles, while the correctly configured pair seats (implementer=terra via project mode seat, reviewer=glm52 via global mode seat) were never used. roster_sources on the runs says implementer/reviewer came from 'request' — the launcher sent them.

Evidence from the record:
- r-20260823-220227-536j and r-20260823-221035-lxa7: implementer=qwen38-max (the pair ADVISOR), reviewer=k3 (the ARCHITECT).
- r-20260823-225333-eopp and r-20260823-230731-j7xi: implementer=luna (the CONSULTANT), reviewer=terra (the configured IMPLEMENTER, seated as reviewer).

Two compounding defects, one root cause — the launcher treats the roster entries array as if it were already in mode-seat order:

1. Prefill maps positionally from role-ALPHABETICAL entries. GET /v1/projects/{id}/roster?mode=pair returns 8 entries ordered advisor, architect, consultant, implementer, judge, reviewer, scribe, triager. Both prefills seed chips with roster.map(e => e.duckling): frontend/src/components/RunLauncher.tsx:54 (PhaseConfig effect) and frontend/src/components/RunLauncher.tsx:233 (LaunchConfig effect). For pair (seat 0 = implementer, seat 1 = reviewer per frontend/src/lib/seats.ts:46-51), chip 0 gets entries[0] = the advisor and chip 1 gets entries[1] = the architect. That is exactly runs 536j/lxa7.

2. 'default' shifts the seats instead of deferring to the engine. onLaunch sends ducklings: chosen.filter(Boolean) (frontend/src/components/RunLauncher.tsx, the run-start button). Clearing both chips to 'default' leaves chosen = ['', '', luna, terra, '', glm52, ...]; filter(Boolean) compacts it and the engine receives [luna, terra, ...] — consultant and implementer slide into seats 0 and 1. That is exactly runs eopp/j7xi. The engine already does the right thing with an EMPTY position (internal/service/testfirst.go:421-425 skips '' and falls back to the resolved roster), so filter(Boolean) is precisely what breaks 'default'.

Fix direction:
- Seed chips by projecting entries into mode-seat order: for each seat i, find the entry whose role == seatLabel(mode, i) (or use rolesForMode) — never by array position.
- Preserve positions on launch: send '' for a defaulted seat instead of filtering it out, so the engine resolves that seat from the roster as designed.
- A test that launches pair from a full 8-role roster and asserts the request carries the implementer/reviewer entries (or empties), not entries[0]/entries[1], would have caught both.

Engine is NOT at fault: resolution precedence (internal/service/modes.go:157-211) and the positional request contract behave as documented; roster_sources correctly recorded 'request'.


