---
kind: plan
version: 14
updated_at: 2026-08-25T11:31:39Z
run_id: r-20260825-112559-6ex6
ducklings: [k3, terra, atom-local, luna, glm52]
based_on: bccf4dbbbfe129bc
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

### T-122 — Sync the person's checkout with the ref when acceptWorktreeRun advances the default branch

Fixes B-148.

## Reported

Found chasing 'adjust seats still shows wrong models after T-121': the T-121 accept fast-forwarded main to 61df20f, but the person's checkout (which IS on main) kept its pre-accept FILES. Consequences observed:
1. make desktop compiled the OLD RunLauncher.tsx while -ldflags stamped the binary main@61df20f — false build provenance; the operator installed twice and both binaries showed the pre-fix behavior with a post-fix version string.
2. The divergence accumulated silently across native accepts (T-121's frontend files, and service.go/fs.go/governance_guard_test.go from another accepted run) until the working tree was ~370 lines behind its own HEAD.
3. git status shows the drift as staged modifications, which reads as local work — inviting an accidental commit that would REVERT accepted work.

B-144's chain commit had the same shape (ref moved, tree not); this is the accept-path twin.

Fix: when the registered project checkout is on the default branch and CLEAN for the touched paths, acceptWorktreeRun updates the working tree along with the ref (a guarded 'git -C <checkout> merge --ff-only' / checkout of the new sha); when the checkout is dirty on those paths, do not touch it — announce on the accept result and the run card: 'main advanced to <sha>; your checkout is behind and was left untouched'. A test pins: after a worktree accept, a clean default-branch checkout contains the accepted files; a dirty one is untouched but the accept result carries the warning.

Operator remediation applied: git checkout HEAD -- <paths>, desktop rebuilt from true source.

**Deliverables:**
- When the registered checkout is on the default branch and clean for the touched paths, the accept path advances the working tree to the new sha (guarded ff-only merge or checkout), not just the ref
- When the checkout is dirty on those paths, the accept leaves the tree untouched and the accept result carries a warning naming the new sha and stating the checkout was left behind
- The warning is recorded on the run record/event stream so the run card can surface it
- A test asserts the clean-checkout case: after a worktree accept, the default-branch checkout contains the accepted files
- A test asserts the dirty-checkout case: files are untouched and the accept result carries the warning

## Triage

**Component:** run accept / worktree landing
**Suspected files:** internal/service/service.go, internal/service/worktree.go, internal/vcs/vcs.go

Accept fast-forwards main without updating a clean checkout on that branch, producing stale-source builds stamped with the new sha and silent drift that reads as local work; B-144 is the chain-commit twin on a different path, not a duplicate. Note: search output was heavily polluted by .ducklab/runs logs, so the exact file holding RunAcceptAs/acceptWorktreeRun (service.go vs a dedicated accept file) is unconfirmed — worktree.go and vcs.go are confirmed relevant.

**Verification (triage recommends):** test-first — Accept a worktree run in a fixture project whose checkout sits on main: assert the clean checkout contains the accepted files after accept, and that a dirty checkout is untouched with the warning carried on the accept result

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-123 — Flag diffs that touch frontend/src without a dist rebuild as desktop_stale on the run record and surface them at the human gate

Fixes B-147.

## Reported

T-121 (the launcher seat-projection fix, commit 61df20f) changed frontend/src/components/RunLauncher.tsx only. The accept landed, the engine was restarted, and the operator still saw the OLD behavior in 'adjust seats & caps' — because the desktop serves the committed dist bundle, whose last rebuild was T-115's (e75d45d), and the installed ducklab-desktop predated the accept. Nothing in the run, the review, or the gate said 'this change is invisible until the bundle is rebuilt'.

The repo's convention is deliberate 'desktop: rebuild bundle' commits, and make install already warns when the installed desktop predates frontend/src — but that warning fires at INSTALL time on the operator's machine, not at the gate where the decision is made.

Fix direction, mirroring governance_modified (T-116): when a candidate diff touches frontend/src without touching cmd/ducklab-desktop/frontend/dist, set a flag on the run record (e.g. desktop_stale) and carry it into the reviewer payload and the human gate's pending data in plain words: 'this diff changes the frontend source but not the shipped bundle; the desktop will not show it until make desktop runs'. Optionally the same detector powers a post-accept reminder on the run card. A test pins: a diff touching only frontend/src sets the flag; a diff touching both does not.

Related: B-142 (gate surfaces), the desktop-surface house rule (a feature ships WITH its visible surface — this is its build-artifact corollary).

**Deliverables:**
- runlog.Run gains a DesktopStale bool field serialized as desktop_stale
- a detector (sibling to governanceCallouts) returns true when the candidate diff touches frontend/src but not cmd/ducklab-desktop/frontend/dist, and is wired into the run-completion diff handling in service.go (and review.go like governanceModified)
- when set, the human gate's PendingData and human_needed event data carry plain-words text: the diff changes frontend source but not the shipped bundle, and the desktop will not show it until make desktop runs
- tests assert: diff touching only frontend/src sets the flag; diff touching both frontend/src and cmd/ducklab-desktop/frontend/dist does not; diff touching neither does not
- tests assert the flag reaches PendingData/human_needed gate data at the paused-gate branch

## Triage

**Component:** gate / run record (service)
**Suspected files:** internal/service/governance.go, internal/service/service.go, internal/service/review.go, internal/runlog/runlog.go, internal/service/governance_gate_test.go

T-121 landed invisibly because nothing at the gate said the committed dist bundle predated the frontend change; this mirrors the existing governanceModified mechanism, is unit-testable at the detector and gate-payload level, and is a surfacing gap (normal severity), not a duplicate of B-142's rendering bug.

**Verification (triage recommends):** test-first — Unit-testable detector mirroring governanceCallouts: a diff touching only frontend/src/... must set desktop_stale; a diff also touching cmd/ducklab-desktop/frontend/dist must not; the run-completion path must carry the flag into PendingData and the human_needed event.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-124 — Confirm verifyAcceptedCommit populates linked deps and setup before the gate, or close as fixed

Fixes B-146.

## Reported

r-20260824-073421-zstm (T-120): worktree gate green, but RunAccept's clean-checkout reproduction failed with npx unable to find tsc — the acceptance checkout has no frontend/node_modules. createRunWorktree populates deps via linkInstalledDeps (cfg.Verify.LinkDeps) but verifyAcceptedCommit's detached checkout never gets the same call. The asymmetry stayed hidden because the only prior native accept (r-20260824-070527-kpzx) took the fast path, which skips the second gate run; the operator's manual landings always symlinked deps by hand before gating.

The withdraw-on-red machinery behaved correctly (nothing landed) — the defect is only the missing population step.

Fix: verifyAcceptedCommit populates the acceptance checkout exactly as createRunWorktree does (same linkInstalledDeps + setup hook), before running the gate. Test: accept a worktree run whose gate requires a linked dep; reproduction must be green from the clean checkout.

**Deliverables:**
- verifyAcceptedCommit calls linkInstalledDeps with cfg.Verify.LinkDeps before the gate
- verifyAcceptedCommit runs cfg.Verify.Setup before the gate, same as createRunWorktree
- an accept-path test exercises a gate requiring a linked dep from the clean checkout

## Triage

**Component:** accept/clean-checkout reproduction
**Suspected files:** internal/service/service.go, internal/service/worktree.go, internal/service/clean_checkout_deps_test.go

Current source already calls linkInstalledDeps(cfg.Verify.LinkDeps) and the setup hook inside verifyAcceptedCommit (service.go:2469-2477) with covering tests in clean_checkout_deps_test.go, so the reported defect appears already fixed; reproduce before treating as open.

**Verification (triage recommends):** test-first — accept a run whose gate needs a linked dep; reproduction must be green from the clean checkout (asserted by TestAcceptedCheckoutLinksDeclaredDependency / TestAcceptedCheckoutRunsDeclaredSetupBeforeGate)

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-125 — Cancel a test-first run's then_build chain on reject, land the chained red-test commit on the run branch, and exclude linked deps from every commit stager

Fixes B-144.

## Reported

Sequence on 2026-08-24 ~07:14: test run r-20260824-071217-re6j (T-120, then_build:true) PASSED its stage and was REJECTED by the operator with a request for one more test. Thirty seconds later the chained build r-20260824-071442-aibo started anyway, and its chain step committed 07696bb 'chained: the test landed red' containing (a) the REJECTED test file and (b) frontend/node_modules — the link_deps symlink again (B-143's second door) — directly onto the main REF while the person's working tree stayed at the previous content, leaving HEAD==main==07696bb with the tree diverging (test file 'modified', symlink 'deleted').

Three defects:
1. Rejecting a test-first run must CANCEL its then_build chain. A rejected test is not a contract; building against it wastes a run and, worse, lands it.
2. The chain's red-test commit went to the person's branch ref from the run worktree's content. Under the worktree regime the committed-red-test step must live on the RUN's branch (like every other run commit post-T-114), reaching default only through acceptance.
3. The chain's commit staged link_deps artifacts — same fix as B-143: one staging helper that excludes run.LinkedDeps, used by EVERY stager (accept, chain, any future committer).

Remediation applied: aborted aibo, reset local main to 3d2d27d (07696bb was never pushed), bug audit preserved. Tests pin: reject cancels the chain (no build run appears); the chain commit for a fresh test-first lands on the run branch, not default; no linked path in any chain commit.

**Deliverables:**
- RunReject (or the reject path) clears/neutralizes ChainBuild so no chained build is started for a rejected test-first run; a test asserts no build run appears after reject
- chainBuild's red-test accept commits to the run's worktree branch (via acceptWorktreeRun path), not the default ref; a test asserts the chained test commit lands on the run branch and default stays put
- one shared staging helper that always excludes run.LinkedDeps, used by the worktree accept stager and every other committer; a test asserts a linked-dep path is absent from any chain commit
- existing accept/chain tests (accept_worktree_test.go, bug_loop_test.go, testfirst_status_test.go) still pass

## Triage

**Component:** service/testfirst chain + accept
**Suspected files:** internal/service/testfirst.go, internal/service/service.go, internal/service/worktree.go, internal/vcs/vcs.go

A rejected test-first run still fired its then_build chain and committed the rejected red test plus the link_deps symlink onto main, diverging the ref from the working tree — three related defects in the test-first chain/accept path that are reproducible as automated tests.

**Verification (triage recommends):** test-first — Rejecting a then_build test-first run must produce no chained build run; a chained red-test commit must land on the run branch not default; no LinkedDeps path appears in a chain commit.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-126 — Stop executeTestFirst from cleaning up the worktree when pausing at a gate

Fixes B-145.

## Reported

r-20260824-072822-ydvp (T-120 test stage) paused UNVERIFIED at its gate; RunAccept then failed with 'worktree acceptance cannot find .../r-20260824-072822-ydvp' — the worktree was already gone.

Cause: T-114 removed the deferred cleanup from executeRun (worktrees are retained at human gates; accept/reject clean up after the decision) but executeTestFirst kept its 'defer cleanupRunWorktree' from slice A (internal/service/testfirst.go, executeTestFirst). When the test-first goroutine returns at a pause, the defer removes the worktree and the pending gate becomes undecidable.

Fix: mirror the build path — executeTestFirst must not clean up when pausing at a gate (retain_worktree), and the test-first accept/reject paths clean up after the decision. Test: a test-first run paused at its gate still has its worktree; accepting it lands the committed red test per stage polarity.

T-114's pinned tests covered build runs only; this is the test-first twin they missed.

**Deliverables:**
- executeTestFirst (internal/service/testfirst.go:364) no longer defers cleanupRunWorktree into a gate pause: a paused test-first run's worktree survives the goroutine's return
- the test-first pause paths (testfirst.go ~560 and the chainBuild fallback ~729) record retain_worktree in PendingData, mirroring the build path at service.go:1779/1833
- RunAccept on a paused test-first worktree run reaches acceptWorktreeRun, lands the committed red test, and cleans up the worktree after the decision; RunReject cleans up via the shared path at service.go:3283
- a regression test asserts: test-first run paused at its gate still has its worktree, and accepting it lands the commit per stage polarity (the test-first twin of T-114's build-run pins)

## Triage

**Component:** test-first worktree lifecycle
**Suspected files:** internal/service/testfirst.go, internal/service/service.go, internal/service/worktree.go

Confirmed in source: executeTestFirst keeps `defer s.cleanupRunWorktree` (testfirst.go:364) while its gate pause (testfirst.go:560-574) omits retain_worktree, so the defer deletes the worktree and acceptWorktreeRun (service.go:2357) fails with exactly the reported 'worktree acceptance cannot find' error; not a duplicate of B-143/B-144/B-146, which are distinct accept-path defects.

**Verification (triage recommends):** test-first — Run a test-first run to its UNVERIFIED gate pause, assert the worktree still exists on disk, then RunAccept and assert the red-test commit lands per stage polarity — mirrors the T-114 build-run pins.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-127 — Render the phase:final gate result on its card and key the sidebar gate box on the final gate event

Fixes B-142.

## Reported

r-20260824-054151-2d2n (T-115): the record says gate_started phase:final -> gate exit_code:0 -> verdict PASSED. The desktop showed: three red round gates (the B-135 false reds), the final gate card stuck on its announcement ('running the full gate - the verdict is its exit code') with NO result ever rendered on it, and the sidebar gate box saying 'x tests failed' - while the run header says passed. The operator cannot reconcile the screen with the verdict without opening events.jsonl.

Two surface defects:
1. The final gate card must render its outcome (exit code, green/red, duration) when the gate event lands - B-122 put the output on the event; the card never consumes it.
2. The sidebar gate box reads the wrong source - it reflects a round_gate (red) instead of the phase:final gate. The code comment in strategy/execute.go:487 warned exactly about consumers taking 'whichever happened to come last' under the round_gate/gate split; the sidebar is that consumer.

Fix: the final card consumes its own gate event (result + output tail); the sidebar box keys on the phase:final gate event, falling back to 'no final gate yet' while rounds are in flight. A vitest pins: run with three red round gates then a green final renders a green gate box and a resulted final card.

Related: B-122 (final gate announcement/output), B-131 (engine decisions with no surface), B-135 (why the round reds were false tonight).

**Deliverables:**
- The final gate card settles and renders its outcome (exit code, pass/fail colour, duration, output tail) when the phase:final 'gate' event lands, instead of staying on its gate_started announcement
- buildGate/the gate reducer consumes the field names the service actually emits on the final gate event (exit_code, command, output, duration_s — not exit/cmd) and distinguishes the phase:final gate from round_gate events
- The sidebar gate box keys on the phase:final gate event and shows a 'no final gate yet' state while only round_gate events have landed, so red rounds under a PASSED verdict no longer read as a failed run
- A vitest pins: three red round_gate events followed by a green phase:final gate event renders a resulted final card and a green sidebar gate box (and the inverse: red final after green rounds reads red)

## Triage

**Component:** desktop run view gate surfaces
**Suspected files:** frontend/src/lib/runview.ts, frontend/src/components/GateCard.tsx, frontend/src/views/RunView.tsx, frontend/src/components/ConversationLane.tsx

The engine emits the final verdict as a 'gate' event (service.go:1691, with exit_code/command/output/duration_s and a phase:final gate_started), but buildGate in runview.ts reads mismatched fields (d.exit/d.cmd), the lane settles gates only on round_gate, and the sidebar takes whichever gate-ish event came last — so a PASSED run shows a stuck final card and a red gate box; directly reproducible as a vitest over the event stream.

**Verification (triage recommends):** test-first — Vitest: feed gate_started{phase:final} plus a gate event{exit_code:0, command, duration_s} after three red round_gate events; expect the final card green with exit code/duration and the sidebar box green instead of 'x tests failed'.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-128 — Advisor contract counts dots, not sentences: useful answers discarded over file paths, 'I recommend' is banned, and the discarded text is not logged

Fixes B-133.

## Reported

r-20260824-000718-5wkp: ask_advisor failed with 'advisor contract violation after repair: expected 2-8 sentences, got 9; proceed with your best judgement' — the implementer lost its lifeline at exactly the distress moment that summons the duck, and paid for two advisor calls whose output was thrown away.

Four defects in internal/service/advisor.go:

1. advisorSentenceCount (advisor.go:460-467) counts every '.', '!', '?' rune. In this codebase an advisor answer is dense with dotted identifiers: 'Use fs_read on lifecycle_test.go.' counts as THREE sentences; 'go test ./internal/service', 'service.go:1182', 'rs.done', 'e.g.' all inflate the count. A genuinely 4-sentence note easily 'counts' 9+, and which seats survive the contract is a function of their path-citing style, not their verbosity. (Interface-first: qwen38-max passed for weeks by luck; k3 cites paths and died on arrival.)

2. advisorViolation (advisor.go:431) rejects any answer containing the substrings 'we need' or 'i recommend' as 'preamble or deliberation'. An ADVISOR whose job is recommending is banned from saying 'I recommend', and 'we need' occurs naturally mid-advice ('...the queue tests we need to keep...'). Overbroad substring ban on the seat's core vocabulary.

3. The discarded answer is not persisted: llm.jsonl seq 131 records only {"error": "advisor contract violation after repair: ..."} with the question — the overlong text that was rejected is nowhere, so the operator cannot audit what was lost or judge whether the contract was right to reject it. (B-123's logFailedOneShot pattern covers chat one-shots; this path drops the text.)

4. The penalty lands on the wrong party: the implementer gets 'proceed with your best judgement' — the harness converts a formality miss by seat A into lost help for seat B mid-task.

Fix direction:
- Count sentence BOUNDARIES: terminator followed by whitespace-plus-capital or end of text, after masking inline code spans, paths (tokens containing '/' or an extension), and 'e.g./i.e.'. A test table with dotted-identifier prose belongs next to it.
- Soft enforcement on overlength: accept-with-warning (or truncate at the 8th boundary) for up to ~2x the cap; hard-reject only empties and runaways. A one-sentence overage should never outrank having an answer.
- Drop the 'i recommend'/'we need' substring ban or scope it to a leading-preamble check (first sentence only, with actual deliberation markers).
- Persist the rejected text on the failure record (the B-123 pattern), so the discard is auditable.

### T-129 — Yolo auto-answers ask_human via the advisor, but the consent surface never says so and the answer is recorded as an unattributed 'human' event

Fixes B-137.

## Reported

r-20260824-003539-zdln: the implementer asked a question (delete the node_modules symlink?), the operator went to answer it, and the run had already continued — advisor k3's draft was auto-submitted 2ms after drafting (advice -> advice_taken -> human, seq 1191-1193). internal/service/advisor.go:78-92 is explicit design: 'Under yolo the draft IS the answer... with the decider on the record.'

Three gaps:

1. CONSENT: the yolo checkbox tooltip promises only gate behavior ('a green gate accepts itself; reviewer dissent and UNVERIFIED still wait for you' — frontend RunLauncher). It never discloses that ask_human questions are auto-answered by a model. The operator opted into unattended accepts, not into a model answering in their name.

2. ATTRIBUTION: the submitted answer lands as event type 'human' with {action: 'answer'} and NO author field. advice_taken (adjacent event) holds the truth, but any consumer of the human event — including the desktop's question card and the transcript — shows the answer as the person's. For a product whose credo is recorded, ATTRIBUTED decisions, a model speaking under the 'human' event type unmarked is an integrity defect. Fix: author/by field on the answer event ('advisor:k3 (yolo)') propagated to every surface that renders answers.

3. QUALITY EXPOSURE: the auto-answer contained a false premise — 'because it's untracked it never appears in the run's diff' — disproven by r-20260824-000718-5wkp whose diff contains exactly that untracked symlink (B-136). An attended human could catch that; unattended, the implementer inherits the error. Mitigation: the auto-answer path should at least surface the question+answer as a notification so the operator can correct it while the run continues, and the question card should render 'answered by <advisor> under yolo' as a distinct state.

Related: B-136 (the symlink in the diff), B-131 (invisible escalation events — same pattern of engine-side decisions with no desktop surface).

### T-130 — The advisor seat cannot be chosen (or seen) at launch: pair/solo launchers render no advisor chip and the positional RunRequest.Ducklings cannot carry one

Fixes B-132.

## Reported

Launching T-113 from the run-again card with terra/gemini37flash, the operator could not seat an advisor and concluded nobody was seated. The engine in fact resolved advisor=k3 via 'project mode seat' (r-20260824-000718-5wkp, roster_sources) — the rubber duck was on duty, invisibly. Two gaps, one per layer:

1. UI: the launcher renders exactly fixedSeats(mode) chips — pair=2, solo=1 (frontend/src/lib/seats.ts:12-19), with seatLabel defining only implementer/reviewer for pair (seats.ts:46-51). But the advisor is a real seat of every task mode (rolesForMode, seats.ts:71-91: solo [implementer, advisor], pair [implementer, advisor, reviewer], tournament includes advisor). There is no affordance to see or pick it at launch, and no indication of what the roster will resolve for it.

2. Wire: RunRequest.Ducklings is positional and only positions 0 (implementer) and 1 (reviewer) mean anything (internal/service/testfirst.go:410-414; tournament/split read the list as contestants in internal/service/modes.go). An advisor pick cannot be expressed in the request at all.

Fix direction — same root as B-129 and best fixed together: make the launch request carry role-keyed seats (e.g. seats: {implementer, reviewer, advisor}) instead of a positional list, keep '' meaning 'roster resolves'. Then the launcher shows the advisor chip on task modes, prefilled with what the roster WOULD resolve (so silent resolution becomes visible), with 'default' preserved. The run-again card inherits it for free.

Workaround meanwhile: the advisor is configurable only via the Roster board (mode seats / role pins), not per-run.

### T-131 — escalation_suggestion events have no desktop surface: the run emits them, the person never sees them

Fixes B-131.

## Reported

r-20260823-230731-j7xi emitted THREE escalation_suggestion events (stuck_deliverable twice, turns_over_2x_mode_median once, all at distress_pause points) and the operator watching the run in the desktop concluded escalation had never fired. The only frontend reference to the event type is the vocabulary entry in frontend/src/api/events.ts:276 — no component renders it, so the suggestion (thresholds fired, diagnoses, candidate, actions relaunch_with_stronger_seat / improve_task_body / continue_as_is) is invisible outside events.jsonl.

This breaks the feature's purpose (a suggestion nobody sees suggests nothing) and the desktop-surface rule: every feature ships with its desktop surface in the same arc. T-107 shipped the engine half only.

Fix direction: render escalation_suggestion in the run view as an evidence card at the pause boundary — thresholds fired, the diagnoses map (seat_at_capacity vs task_brief_quality vs the third cause B-129 exposed: mis-seated), the candidate with its Wilson floor when present, and the three actions as affordances (relaunch prefilled with the stronger seat, open task body, continue). Related: B-130 (escalation blind to runs that die without evidence).

### T-132 — Escalation is blind to the loudest distress: runs that die of turn/budget exhaustion produce no trigger evidence, and cross-run failure repetition on one task is not a trigger at all

Fixes B-130.

## Reported

T-113 accumulated four runs — two hard failures by the same seat, then a pass only after an (accidental, B-129) reseat — and no escalation_suggestion was ever emitted. The operator expected one; the design cannot produce one for this shape of failure.

Why it stayed silent, from the code:
- All three triggers are INTRA-run and read structured evidence from the run's own event stream (internal/strategy/execute.go:107-132, re-derived at the run boundary in internal/service/service.go:1932-1950): a deliverable item reported stuck 3x in the same run, 3 consecutive red round gates in the same run, or run turns > 2x the mode median.
- r-20260823-220227-536j FAILED with 'implementer used all 24 of its turns calling tools and never answered (28 tool calls, no text)'. It never reported deliverables and never reached a round gate — zero trigger evidence. Note the 24 is the per-reply call loop, not the run-level Turns the median trigger measures (that budget shows turns=1), so turn-loop exhaustion is invisible to all three triggers.
- r-20260823-221035-lxa7 ABORTED on wallclock (1807s >= 1800s). Budget deaths likewise emit none of the evidence escalation reads.
- Nothing anywhere counts FAILED RUNS PER TASK across runs — the exact pattern the operator saw. B-129 compounds it: each relaunch changed the implementer seat, so even a per-seat cross-run counter would have been reset by the accidental reseat.
- Independent silencer, working as designed: escalationCandidatesFor (internal/service/candidates.go:313-344) requires a same-role candidate whose Wilson floor strictly exceeds the current seat's with minRuns evidence. With luna seated (77% over 456 implementer runs, the fleet's highest floor) nobody qualifies, so for the current run silence is correct.

Suggested direction:
- Treat a run that dies without answering (turn-loop exhaustion, budget/wallclock exceeded mid-stage) as distress evidence in itself — it is the loudest signal a seat can send, and today it is the one signal escalation cannot hear.
- Add a cross-run trigger evaluated at LAUNCH time: N (say 2) failed/aborted runs of the same task+stage, regardless of seat, surfaces the suggestion — 'this task has failed here twice; strongest implementer by Wilson floor is X, or improve the task body' — before the next run spends money, not after it dies.
- Keep the stronger-candidate gate as is; it correctly separates 'escalate' from 'reseat/rebrief'.

### T-133 — Make bare `ducklab release` print usage instead of starting a plan run

Fixes B-126.

## Reported

Typing `ducklab release` with no verb launched a release plan run (r-20260823-150403, rejected and cleaned) — discovered while checking the CLI syntax for the README. The run command already learned this lesson: a typo should not cost tokens, unknown run subcommands print usage instead of starting a model run. The release noun (and any other noun whose empty verb currently defaults to an action that spends) should do the same: bare noun prints its verbs, spending requires saying so.

**Deliverables:**
- releaseCmd with verb "" prints usage (plan and cut forms) to stderr and returns exit code 2
- releaseCmd no longer calls client.ReleasePlan when the verb is empty
- Explicit `ducklab release plan [--bump …]` and `release cut <version>` keep their current behavior
- A test in internal/cli asserts the empty-verb usage path and that the plan verb is unchanged

## Triage

**Component:** cli
**Suspected files:** internal/cli/cycle.go

In internal/cli/cycle.go releaseCmd treats an empty verb the same as "plan", launching a token-spending release run, while the bug report and run precedent demand a bare noun print usage.

**Verification (triage recommends):** test-first — Calling releaseCmd with an empty verb must exit with usage code and not call ReleasePlan; table-testable like the runCmd usage paths.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-134 — Route the rubber-duck consult cap through role_turns/TurnCaps and reconcile the advertised advisor default

Fixes B-125.

## Reported

The person asked where the calls/reply 6 came from on T-112. Answer: internal/strategy/rubberduck.go consultAdvisor builds its Turn inline with MaxTurns: 6, hardcoded. Three problems: (1) configured [defaults] role_turns never applies — applyRoleTurns walks SCRIPT turns and the consult turn is fabricated at distress time, so the knob and the no-cap tick are both ineffective for consults (the same inline-path-bypasses-the-rule family as B-115 and B-123); (2) ScriptRoleTurns advertises advisor: 1 and Settings shows it as what each role gets — the display says 1, consults run 6; (3) the number is reasonable (the advisor investigates with tools before advising) but reasonable-and-hidden is still hidden. Fix: consultAdvisor derives its cap through the same turnsFor(advisor, default) path as script turns so config and the calls-lift apply; reconcile ScriptRoleTurns advisor entry with the consult default so Settings shows the truth; a test pins that a configured role_turns.advisor reaches the consult turn.

**Deliverables:**
- consultAdvisor no longer hardcodes MaxTurns: 6; it derives the cap via strategy.CapFor(params.TurnCaps, RoleAdvisor, consultDefault) so configured role_turns.advisor and a per-run AgentTurns override (including the negative no-cap tick) both reach the consult turn
- ScriptRoleTurns["advisor"] is reconciled with the consult default (both 6, or a single shared constant) so the Settings display matches what consults actually run
- A test pins that a configured role_turns.advisor value reaches the consult turn (e.g. advisor=20 ⇒ consult MaxTurns=20; default ⇒ 6)
- A test pins that a per-run AgentTurns override / no-cap lift flowing through params.TurnCaps applies to the advisor consult turn
- Existing consult behaviour is unchanged otherwise: missing advisor seat still skips, duck failure still degrades to no-op for the run

## Triage

**Component:** strategy/rubberduck
**Suspected files:** internal/strategy/rubberduck.go, internal/service/defaults.go, internal/service/roleturns_test.go, internal/strategy/execute.go

The consult turn is fabricated at distress time (rubberduck.go:205) so applyRoleTurns' script walk never sees it, making role_turns.advisor and the calls-lift dead knobs for consults while Settings advertises advisor: 1 against a real cap of 6 — hidden but harmless-valued, hence low.

**Verification (triage recommends):** test-first — Set defaults.role_turns.advisor=20, trigger a distressed implementer turn with a stub runner, assert the consult turn's MaxTurns is 20 (and that params.TurnCaps no-cap lift applies); today it stays 6.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-135 — Legacy chain_build records relaunched via RunView reinterpret positional ducklings through the new role mapping

Fixes B-149.

## Reported

Residual minor from T-130's reviewer (approved with this finding on record): Run.chain_build's type omits seats, so relaunching a PRE-T-130 test-stage record maps its positional ducklings through the new role projection — pair index 1 becomes advisor instead of reviewer, silently dropping the reviewer. Fix per the reviewer: add seats?: Record<string,string> to the chain_build type (frontend/src/api/client.ts:119) and prefer chain.seats over roleSeats(chain.mode, chain.ducklings) in RunView's test-stage relaunch when present. Only affects relaunches of historical records.

### T-136 — Signpost discard on document-stage reject with a reason and surface the revise verb in next[], MCP/CLI, and desktop

Fixes B-150.

## Reported

Incident: the v0.8.0 release draft (r-20260824-182104-tmr7) was returned with a detailed revision note — through POST /v1/runs/{id}/reject. The run died FAILED, no revise spawned, and only the .proposed surviving on disk allowed recovery via release cut. The operator's note was a textbook request-changes; the API accepted it as a discard reason with a silent 204. Any operator — human, MCP client, or CLI — can repeat this: reject takes free text, and writing a long reason FEELS like requesting changes.

Guards, layered:
1. API: when /reject on a DOCUMENT-stage run (intake/spec/plan/release) carries a non-empty reason, the response body states plainly: 'draft discarded; a reason this detailed usually means request-changes — that door is <action>'. Not a refusal, a signpost on the receipt.
2. Discoverability: the paused document run's next[] must name the revision action explicitly (today the operator must know the stage-specific revise flow exists); MCP and CLI surfaces render all three verbs.
3. Desktop: on draft gates the primary affordance is Request changes; the reject button reads 'Discard draft' — the verb says the consequence.
4. Optional test pins: rejecting a document run with a reason returns the signpost; next[] on a paused document gate includes the revise action.

Not in scope: changing reject's semantics — explicit verbs stay explicit; the fix is making the right verb findable and the wrong one self-describing.

**Deliverables:**
- Rejecting a paused document-stage run (intake/spec/plan/release) with a non-empty reason still discards, but the response is no longer a bare 204: the body states the draft was discarded and names the stage's request-changes door (the revise action); reject semantics are unchanged
- runNext (internal/service/service.go:3172) includes the stage-specific revise action in Next for paused document-stage gates, so run_get over MCP and the CLI render accept/revise/reject without the operator knowing the flow
- Desktop draft gates make Request changes the primary affordance and label the reject button 'Discard draft' (exact desktop component file not verified during triage — suspected under frontend/src run-detail/gate components)
- A Go test asserts the signpost body on reasoned document-stage reject and its absence (or plain 204) on reasonless reject and non-document stages
- A Go test asserts next[] on a paused document gate contains the revise action

## Triage

**Component:** run gate verbs (reject vs revise) on document stages
**Suspected files:** internal/engineapi/engineapi.go, internal/service/service.go, internal/runlog/runlog.go, internal/engineclt/engineclt.go, internal/mcp/tools.go, internal/cli/cycle.go

Confirmed in code: handleRunReject (engineapi.go:1421) discards via Service.RunReject (service.go:3458) and answers 204 with no body regardless of stage or reason, while Next is derived centrally by runNext (service.go:3172) and revise exists only as a hidden CLI/MCP stage flow (cli/cycle.go, mcp/tools.go:774) — a reproducible, recoverable UX trap, so normal severity with testable API/next[] pins and an eyeballed desktop relabel.

**Verification (triage recommends):** test-first — Guard 4 names the pins: POST /v1/runs/{id}/reject with a non-empty reason on a paused intake/spec/plan/release run returns the signpost body, and a paused document gate's next[] includes the revise action — both assertable at service/handler level.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-138 — Config API writes slices and tables

The project configuration API (`project set` / PATCH) currently refuses any non-scalar key because `config.SetKey`'s reflection walk only registers scalar kinds and `assign()` rejects the rest. This teaches it to write slice-valued and table-valued keys, which is the foundation every later config-editing surface depends on. Fixes B-152 gap 0.

**Deliverables:**
- Slice-valued keys are settable through `SetKey` on the CLI, the HTTP PATCH, and the MCP project-set path
  - `verify.link_deps`, `shell.allow_prefixes`, `shell.deny`, `git.protected_paths`, and the `[remote]` lists (e.g. `remote.allow_mcp_verbs`) name the initial set; `walk()` registers `[]string` fields and `assign()` parses one documented encoding (comma-separated) rather than failing "holds a slice"
  - emptying a slice is possible and distinct from a typo (e.g. `key ""` clears, missing value errors)
- Table-valued (map-kind) keys are settable leaf-by-leaf
  - `roster.<role>` and `modes.<stage>` walk into map fields; a role or stage typo is still rejected with the full key list
- The strict-parsing contract holds: unknown keys, malformed values, and reserved identity fields keep their exact refusal behavior
- Round-trip tests assert each named list and each map leaf above writes, persists to `project.toml`, and reloads; plus a test that scalar-only behavior is unchanged

**Out of scope:** adding or removing configuration keys themselves (T-140 owns `[remote]`, T-141 owns `[github]` revival); the doctor; any desktop surface. **Assumption:** `[remote]`/`[github]` leaf shapes may land in later tasks; this task only removes the "slice/table cannot be written" refusal so it composes.

### T-139 — Config doctor: deterministic findings

A pure engine check that reads the project config and the repo and emits structured findings — exact key, proposed value, reason — with no model involvement and fully deterministic output. It closes B-153 part 1 and gives the consultant (T-142) and the Settings surface (T-142) something honest to read.

**Deliverables:**
- A `config.Doctor` (or `service`) function returning an ordered `[]Finding{Key, Proposed, Reason}` closed over the enumerated rules:
  - frontend present but `verify.link_deps` missing its `node_modules`
  - a git remote configured but no `[remote]` or `[github]` sections
  - `[github]` section present but unconsumed (no PR/remote verb uses it)
  - verify command empty
  - budgets zero
  - shell allowlist missing the project's detected toolchain
  - seats unconfigured for modes in use
- HTTP surface: a route with request/response on `routes_table.go` plus an engine-client method, and an MCP `config_doctor` tool returning the same findings
- Each finding carries the exact dotted key and the proposed value, not free text; a finding with no proposed fix is still structured with `Proposed` empty
- Tests assert every rule fires on a fabricated project, that output is byte-identical across two runs (determinism), and that a clean project returns an empty list

**Out of scope:** applying any fix (that is the T-142 proposal-card path, via the T-138 API); new findings beyond the closed list; consulting the seat. **Implements:** SPEC-024, SPEC-021

### T-140 — Remote awareness, orphan audit, and recovery

**Depends on:** T-138

A new `[remote]` config section and the engine's awareness of it: ahead/behind in the project header, a local-only badge on accepts unreachable from remote refs, an orphan audit with a loud warning, and a recovery door that runs only on the person's click. Implements B-151 layers 1–2 and B-152 gaps 1–2.

**Deliverables:**
- `[remote]` section on the project config: `name`, `fetch_on_open` (default false), `allow_mcp_verbs` list — writable through the T-138 extended API and readable through the normal load path
- Ahead/behind counts relative to the configured remote surface on the project header/status response; fetch happens only when `fetch_on_open` is true or on explicit verb
- A `local_only` badge field on accepted runs whose `commit_sha` is unreachable from remote refs, computed by the audit
- An orphan audit at engine start and project open: every accepted run's `commit_sha` is checked for reachability from any branch; orphaned shas produce a loud warning event naming the shas
- A recovery endpoint offering exactly two doors — cherry-pick chain or restore-as-fresh-commit — executed only on an explicit person-initiated call; its decision is recorded and attributed
- Tests: a fabricated orphaned accept is flagged; reachability drives the badge; each recovery door lands the expected commit shape; audit failure warns without blocking startup

**Out of scope:** pull/push/PR verbs (T-141); doctor rule authoring (T-139); any fetch that the person did not ask for. **Implements:** SPEC-008

### T-141 — Remote verbs: pull, push, PR with receipts

**Depends on:** T-140

First-class but explicitly human-initiated remote actions: pull as fetch plus ff-only, push of the current or a task branch, PR creation via `gh` with a push-plus-compare-URL fallback, and scribe-drafted PR bodies from the run record. Implements B-151 layers 3–4 and the scribe half of B-153.

**Deliverables:**
- `pull`: fetch then ff-only merge; divergence is presented to the person, never merged automatically; the result is recorded with actor attribution
- `push`: pushes the current branch or an named task branch; refusal when no `[remote]` is configured
- `pr`: uses `gh` when authenticated; otherwise falls back to push plus a compare URL; `[github]` is revived as PR settings — `pr_base` defaulting to `git.base_branch`, `pr_draft`, `pr_tool`, `pr_body_by_scribe` — writable via the T-138 API
- `pr_body_by_scribe` true routes drafting through the scribe seat reading the run record (task titles, deliverables, receipt shas); the draft is attached to the PR request
- Guardrails enforced in the verb handlers: every remote action requires an explicit call; autopilot and yolo paths cannot reach them; credentials come only from the user's git credential helper or `gh auth` and are never persisted
- MCP exposure of the three verbs is gated on `remote.allow_mcp_verbs` naming them; absent the list, MCP cannot call any remote verb
- Tests: divergence returns a person-decision prompt rather than a merge; attribution is recorded on each verb; compare-URL fallback fires when `gh` is unavailable; autopilot-originated requests are refused

**Out of scope:** Settings editors (T-142); automatic periodic fetch; any server-side credential storage. **Implements:** SPEC-008, SPEC-036, SPEC-021

### T-142 — Consultant as configuration expert, and the settings surface

**Depends on:** T-138, T-139

The consultant reads `config_doctor` findings and the config and becomes the configuration expert: proactive at adopt-end, on project-open-with-findings, and on config-shaped run failures; it proposes amendment cards that apply only on the person's click through the T-138 API. Settings gains the remote/git editor and a read-only diagnostics panel. Implements B-153 layers 2–3 and B-152 gaps 4–5.

**Deliverables:**
- Consultant prompt gains access to `config_doctor` findings (T-139) and the current project config, and prioritizes findings in chat with reasons
- Proactive entry points: adopt-end offer, project-open-with-nonempty-findings offer, and a config-shaped run failure card that pre-seeds the finding into the chat
- Config amendment proposal cards rendered in desktop chat: `key`, `old`, `new`, `why` (the why drafted by the scribe seat); the Apply control is a person click that calls the T-138 config API and is recorded with attribution — never auto-applied
- Desktop Settings gains a remote/git section editor (name, fetch_on_open, allow_mcp_verbs, PR settings `[github]`) written through the T-138 API, plus a read-only diagnostics panel: remote reachable, `gh` auth status, credential helper status
- A reusable slice-valued key editor pattern in Settings (list editing of `shell.allow_prefixes`, `shell.deny`, `git.protected_paths`, `verify.link_deps`, `remote.allow_mcp_verbs`) routed through the same API
- Tests: a proposal-card click applies exactly through the config API and writes the audit attribution; diagnostics is read-only (no setter wiring); a failure card with a config finding pre-seeds the consultant chat

**Out of scope:** free-form config chat that mutates without a card; restructuring Settings beyond the remote/git section and the diagnostics panel; new doctor rules. **Implements:** SPEC-054, SPEC-024, SPEC-062

**Assumption:** across the milestone — `[remote]` holds a single named remote (multi-remote `[[remote]]` tables are not required), and the doctor's closed rule list is the seven findings enumerated in the amendment.

### T-143 — Staging exclusion leaves excluded paths STAGED: a dirty index blocks the accept's rebase, so every non-fast-path accept fails

Fixes B-154.

## Reported

First non-fast-path accept in production (r-20260825-114809-dsgi, T-139): T-125's staging exclusion kept frontend/node_modules out of the commit but left it ADDED in the index; git rebase refuses with a dirty index, so the accept failed with 'cannot rebase: You have unstaged changes'. Fast-path accepts never hit it (no rebase), which is why T-138 landed clean. Fix: the exclusion must UNSTAGE (rm --cached) excluded paths after AddAll, leaving them untracked; test: accept a worktree run with a linked dep against a MOVED default branch — the rebase must proceed. Operator unblocked dsgi via restore --staged.

### T-144 — acceptWorktreeRun retry is not idempotent: after a failed rebase, the retry re-runs the commit step and dies on 'nothing to commit'

Fixes B-155.

## Reported

Same incident, second wall: after the rebase failure, retrying the accept re-ran AddAll+commit; the run commit already existed (442f25e), git commit exited 1, accept aborted before reaching the rebase. Fix: the commit step detects an existing run commit (HEAD authored by this run / clean tree) and skips forward; test: accept, fail it mid-rebase (simulated), retry succeeds end to end. Operator unblocked via soft reset to base and a fresh retry, which then landed 4e1f240 through the full rebase→re-gate→ff path.

### T-145 — ConfigFailureCard can never render: config_amendment events are not emitted on the run-failure path it listens for

Fixes B-160.

## Reported

T-142 reviewer finding (major, glm52), filed per the ratchet doctrine at accept 4bb86f9. The config-shaped run failure card exists in RunView but its trigger event never fires on actual run failures — the surface is unreachable. Wire the failure path to emit the event (the failure card offering 'ask the consultant' with the doctor finding pre-seeded, per T-142's contract) and add the vitest that exercises the card from a simulated config-shaped failure. This is also the remnant of T-142 deliverable 2.

### T-146 — config_amendment emission is untested: no Go test verifies ChatStart emits amendment events when the doctor has findings

Fixes B-161.

## Reported

T-142 reviewer finding (major, glm52), filed at accept 4bb86f9. Add Go tests pinning: ChatStart with doctor findings emits config_amendment; without findings emits none; the event payload carries key/old/new/why. Small, test-only task.

### T-147 — Adopt-end and project-open consultant offers are unwired or untested (adopt-config-offer, project-config-offer)

Fixes B-162.

## Reported

T-142 reviewer finding (major, glm52), filed at accept 4bb86f9. The two proactive consultant offers exist as components/test-ids but their triggers from adopt-end and project-open-with-findings are not verified end to end. Wire and pin each with a vitest per affordance. This is the remnant of T-142 deliverable 6.

### T-148 — ValueKey misses 3-level nested map keys (mode_seats.pair.implementer): doctor finding keys cannot round-trip through the config API

Fixes B-163.

## Reported

T-142 reviewer finding (minor, glm52), filed at accept 4bb86f9. The map-handling branch of ValueKey handles two levels; doctor findings can name three-level keys. Extend the walk one level with the same strict-typo behavior, plus round-trip test.

### T-149 — config doctor emits duplicate findings when both root and frontend package.json exist

Fixes B-156.

## Reported

T-139 reviewer residual (approved with finding on record): detectedTools appends 'npm ' twice when root and frontend package.json both exist, producing duplicate shell.allow_prefixes findings. Dedupe the tools slice. One-line fix + a test case with both manifests.

### T-150 — The spent card omits wall clock: the run's most expensive dimension — the human's waiting time — is the one not shown

Fixes B-157.

## Reported

The run view's spent card shows '$3.58 · 9.6M tokens · 9 turns' plus per-duckling token/cost rows, but not elapsed time. The run record already carries wallclock_ms and the budget tracks wallclock_s against its cap, so this is purely a rendering gap.

Rationale (Jose, 2026-08-25): wall clock is the operator's own cost — attention held while waiting to test and decide. A card that prices the model's work but not the human's wait misprices the run. Fix: the spent header gains the elapsed time ('$3.58 · 9.6M tokens · 9 turns · 47m'), live-ticking while the run is in flight, final on done; consider a per-round or per-stage breakdown later, not in this pass. A vitest pins the header rendering with a wallclock value.

Context link: the gate ratchet doctrine — accept-with-findings vs relaunch — is argued in wall-clock terms; the card should show the number the doctrine optimizes.

### T-151 — Mark Landed asks the user for forensics the system can do itself, and its always-visible fields invite record falsification

Fixes B-158.

## Reported

The run header for a not-accepted run permanently shows two free-text fields (landing commit SHA, landing note) and a Mark Landed button. Jose's questions expose the defects: where would a user get that sha? (git archaeology); do they know what to type? (no placeholder guidance beyond field names); do they know WHEN this applies? (only the rare work-reached-main-outside-the-engine case); and should it always be visible? (no — an ever-present 'make this failure count as a pass' control is a records-integrity hazard, since landed counts as success in scorecards).

Root cause: the feature was born from the night operator's manual-landing workflow (B-140), where the operator authored the landing commit and knew its sha. It shipped operator-shaped.

Inversion: ducklab can usually do the forensics itself — landing commits carry the 'Ducklab-Run: <id>' trailer by convention, so the engine can search the default branch for a commit whose trailer names this run and OFFER: 'this run's work appears on main as <sha> — mark landed?' with the sha pre-filled and the evidence shown. Design:
1. DEFAULT: no fields visible. When the trailer scan finds a match for a not-accepted run, a card offers the pre-filled one-click confirm.
2. DISCLOSED EXPERT PATH: behind a 'more actions' door, the manual fields remain for landings without a trailer — with placeholder text saying what belongs there and when this is legitimate.
3. GUARDS: the sha must exist and be reachable from the default branch (warn otherwise); the action stays recorded and attributed; a landed resolution always names its evidence (trailer match or manual attestation).
Tests: trailer scan surfaces the offer with the right sha; manual path rejects an unreachable sha with the reason; no fields render when there is no match and the door is closed.

### T-152 — Concurrency knobs have no desktop surface: max_concurrent_runs and per-provider max_concurrent live only in the engine TOML and need a restart

Fixes B-164.

## Reported

With parallel runs now the operating norm (wave scheduling, worktree isolation, merge-proof accepts), the two knobs that govern parallelism are invisible and inert in the desktop: defaults.max_concurrent_runs (engine config.toml, default 2) and each provider's max_concurrent. Settings edits autonomy/autopilot but not these; changing them means hand-editing ~/.config/ducklab/config.toml and restarting the engine.

Fix direction: (1) expose both in Settings (engine section for the global cap with the engine's CPU-derived ceiling shown as context; provider cards for per-provider caps), writable through the existing settings-save path; (2) make the queue read the value live (it consults s.cfg on canStart — a settings write should take effect without a restart, or the card must say 'applies on restart' in plain words per the usability doctrine); (3) the queued_reason already names the cap ('engine at max_concurrent_runs') — link that reason text to the setting so the operator can act where they learn.

Tests: settings write round-trips; a queued run starts when the cap is raised live (or the restart-required label renders); provider cap edit honored by the next admission.

### T-154 — The failure-path config_amendment event renders two competing cards: Apply-amendment and ask-the-consultant for the same finding

Fixes B-165.

## Reported

T-145 reviewer residual (minor, glm52), filed at accept per the ratchet doctrine. ConfigAmendmentCard and ConfigFailureCard both key on config_amendment; on the failure path the user sees a direct Apply button AND the consultant door for the same finding — two affordances, unclear precedence, and per the usability doctrine the failure moment should lead with understanding (consultant) before mutation (apply). Fix per the reviewer: filter the configProposals map so failure-path events render only the ConfigFailureCard; the amendment card remains for chat/doctor-originated proposals. One vitest pins the exclusivity.

### T-155 — Timing-sensitive tests flake under parallel gate load: TestShellContextKillsTheWholeGroupAtTimeout failed a gate for an unrelated one-line diff

Fixes B-166.

## Reported

First cost of 4-concurrent runs in production (2026-08-25): T-149's gate (a one-line doctor dedup) went red on TestShellContextKillsTheWholeGroupAtTimeout (internal/xplat) — a process-group-kill timing assertion. The test passes 3/3 immediately afterward on the same machine: CPU contention from four simultaneous full gates pushed the timing past its tolerance. A flaky test under load undermines every parallel verdict — false reds burn redo wall-clock and erode trust in the gate.

Fix directions, pick deliberately: (a) make the assertion load-tolerant (poll-until with a generous deadline instead of a fixed window); (b) mark genuinely timing-bound tests to run serially (Go's t.Parallel discipline inverted — a build tag or -short exclusion the gate honors); (c) as policy, gates could retry a red ONCE when the only failures are in a known timing-sensitive list — least preferred, masks real regressions. (a) is the honest default.

Evidence: run r-20260825-171613-qbne failure log; go test -count=3 green solo minutes later.

### T-156 — ProviderSet cap-raise poke path is untested: no test verifies a queued run is admitted when a provider max_concurrent rises

Fixes B-167.

## Reported

T-152 reviewer residual (minor, glm52), filed at accept per the ratchet. The engine-level cap raise has its live-admission test; the PROVIDER cap raise via ProviderSet pokes the queue but nothing pins it. Add the Go test: queue a run against a provider cap of 1, raise to 2 via ProviderSet, assert admission without restart.

### T-157 — Fake engine drifted behind the app: six newer endpoints unknown, breaking the documented frontend dev flow

Fixes B-169.

## Reported

From the visual audit (docs/ux-audit-visual-2026-08.md, I-2). tasks?summary=true, bugs?summary=true, /v1/providers, /v1/defaults/budget, roster?mode=, /skills are unknown to cmd/fake-engine, so Tasks, Bugs, Settings panels, Roster council and Skills all error in the README's dev flow. Fix: bring fake-engine up to the routes it fakes AND pin parity — a test that walks routes_table entries the frontend consumes and asserts the fake engine answers each (the desktop_coverage_test pattern extended to the fake).

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is Go only: cmd/fake-engine/** plus one parity test (it may live beside the routes table consumer). Do NOT touch frontend/**.

### T-158 — The Now card speaks four unexplained terms to the novice, and the amber warning beside 'passed' says nothing

Fixes B-171.

## Reported

Audit III-1 (docs/ux-audit-visual-2026-08.md). 'build · T-001 pair ⚠ passed waiting 0s' is the first thing a new user must decide on. Add one plain sentence per pending card ('This task finished and passed its tests — it is waiting for your decision', variants for question/dissent/unverified), keep chips as detail, and give the ⚠ its word or tooltip (it appears to mark passed-with-caveat/unverified — say which). Also III-2: $0.0000 renders as a glitch; use $0.00 or 'nothing spent yet'. Vitest per card variant.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is the Now view only (frontend/src — the files rendering the Now screen and its pending cards) plus their tests. Do NOT touch cmd/**, the Roster or Skills views, or shared error components.

### T-159 — Roster filter chips are unlabeled and the Skills header is an implementation dump

Fixes B-172.

## Reported

Audit V-1/V-2 (docs/ux-audit-visual-2026-08.md). (a) The flock filter chip row gets a caption ('filter the flock:'); (b) Skills' header paragraph becomes a purpose sentence ('a recipe your models can read — or run — when a task calls for it') with the internals (paths, shadowing) behind a 'how it works' disclosure. The roster mode subtitles are the register to imitate.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is the Roster and Skills view copy only (chip-row caption, Skills header + disclosure) plus their tests. Do NOT touch cmd/**, the Now view, or shared error components.

### T-160 — User-facing errors leak developer debris: raw GET /v1/ paths and an ApiError: prefix around otherwise-perfect plain sentences

Fixes B-170.

## Reported

Audit II-1 (docs/ux-audit-visual-2026-08.md). The good half already exists ('it is older than this app. Restart the engine.'); wrap it in one error-card component: plain sentence first, method/path/status behind a details disclosure, no ApiError: prefix. Also II-2: an errored fetch must terminate its Loading state (Skills shows both at once). Vitest pins the card and the exclusivity.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is the frontend error-presentation surface: the shared error card/component, api/client.ts error text, and the views' error+loading exclusivity, plus tests. Do NOT touch cmd/**, internal/**, lib/format money helpers, or WaitingCard.

### T-161 — Browser dev against a REAL engine fails silently: no CORS, misleading 'session died' banner

Fixes B-173.

## Reported

Audit I-1 (docs/ux-audit-visual-2026-08.md). ?engine=&token= against a real engine yields cross-origin failures the app reports as a dead session. Either document the flow as fake-engine-only, or add opt-in loopback CORS (--allow-origin, like the fake engine has) so real-data frontend dev and visual audits are possible.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is the engine HTTP layer (opt-in loopback CORS flag, off by default, mirroring cmd/fake-engine's --allow-origin) and the frontend dev docs that describe the browser dev flow, plus tests. Do NOT touch frontend/src/** or internal/service.

### T-162 — TestProjectRecoveryDoors/cherry-pick-chain flakes under concurrent gate load

Fixes B-174.

## Reported

Failed T-157's gate (run r-20260825-180750-mbpu) with 'cherry-pick landed <sha>, want original <sha>' while three gates ran concurrently; 3/3 green in isolation on the same tree. Same class as B-166 (contention flake), different test. Make it tolerant of load the way T-155 treated the shell-timeout test — the sha expectation likely races on timestamps or shared tmp state.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is internal/service remote_test.go (make TestProjectRecoveryDoors load-tolerant) only. Do NOT touch cmd/**, frontend/**, or other packages.

### T-163 — Roll the ErrorCard out to the remaining views — only Skills adopted it

Fixes B-177.

## Reported

Residue from T-160 (accepted under the gate ratchet): the ErrorCard (plain sentence first, method/path/status behind a closed details disclosure, no ApiError: prefix) exists and Skills uses it, but Settings, Roster, Tasks, Bugs and any other view rendering fetch errors still print raw error strings. Adopt the card everywhere an ApiError is shown, and give each adoption the error-terminates-loading exclusivity check Skills got.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is ErrorCard adoption in Settings, Roster, Tasks and Bugs views (plus their tests) ONLY. Do NOT touch Now.tsx, WaitingCard.tsx, lib/format, cmd/**, or internal/** — the Now surface belongs to a concurrent run; if it needs the card too, note it in your summary instead of editing it.

### T-164 — WaitingCard/Now duplicate a zero-dollar money helper and declare a function between imports

Fixes B-175.

## Reported

Residue from T-158 (accepted under the gate ratchet): cardMoney in components/WaitingCard.tsx and nowMoney in views/Now.tsx are the same three-line helper, and nowMoney is declared mid-import-block in Now.tsx. Fold the zero case into lib/format money() (or a moneyOrZero there), delete both locals, restore the import block.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is lib/format (fold the zero-dollar case into money() or add moneyOrZero), WaitingCard.tsx, Now.tsx and their tests ONLY. Do NOT touch other views, ErrorCard, cmd/**, or internal/**.

### T-165 — killrepro test marker is shared across concurrent gates: parallel runs could pgrep each other's sleeper and falsely fail

Fixes B-168.

## Reported

T-155 reviewer residual (minor, glm52), filed at accept per the ratchet. The xplat_killrepro_sleeper marker is identical in every worktree, so two gates running the test simultaneously can see each other's orphan in pgrep; the new 10s tolerance widens the exposure window. Fix: suffix the marker with the run/test PID (os.Getpid()) so each gate matches only its own processes; test stays load-tolerant.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane is the killrepro test marker in internal/xplat (make the marker unique per gate, e.g. PID suffix) plus its tests ONLY. Do NOT touch frontend/** or other packages.

### T-166 — Pin committer/author dates in cherry-pick recovery so the orphan SHA is preserved

Fixes B-176.

## Reported

Residue from T-162 (accepted under the gate ratchet): TestProjectRecoveryDoors no longer asserts the cherry-picked recovery keeps the original SHA, because equality was a same-second timestamp coincidence, not a contract (B-174). If identity preservation IS wanted for the recovery door, the fix is in internal/vcs — pin GIT_COMMITTER_DATE/author date from the orphan when cherry-picking — and then the assertion comes back as a real contract. If not wanted, close this.

**Deliverables:**
- Git.CherryPick in internal/vcs/vcs.go reads the orphan's author date (and name/email if needed) and exports GIT_AUTHOR_DATE and GIT_COMMITTER_DATE from it for the cherry-pick invocation, so the recovered commit reproduces the original SHA
- TestProjectRecoveryDoors cherry-pick-chain in internal/service/remote_test.go asserts landed == sha again as a stable contract
- The SHA assertion holds even when the test forces a delay (e.g. sleeps or fakes the clock) between the orphan commit and the recovery
- RestoreAsFreshCommit behaviour is unchanged: it still records a new commit with a different SHA

## Triage

**Component:** vcs
**Suspected files:** internal/vcs/vcs.go, internal/service/remote_test.go

The cherry-pick recovery door works and content is verified; only SHA identity is unpinned because git cherry-pick uses wall-clock committer date, so the fix is to pin dates in internal/vcs and restore the dropped assertion as a real contract.

**Verification (triage recommends):** test-first — TestProjectRecoveryDoors cherry-pick-chain subtest re-asserts landed == sha, which fails today whenever the cherry-pick crosses a second boundary

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-167 — Now decides through an evidence drawer: conclusion and verdict up front, technical detail behind it, decision buttons never lost

Fixes B-179.

## Reported

Adoption from the UI benchmark (design refs: ~/wiki/Desarrollo/ducklab/benchmark-p3-editor-en-jefe.md item 3, benchmark-p4-galley.md). A waiting card's 'review' affordance opens a side DRAWER (not a page navigation): top strip of three tiles — tests (passed/failed/skipped with a plain note), reviewer verdict (one quoted sentence), cost so far vs ceiling; then a summarized diff (files + plain one-line summary each + net lines); then collapsed-by-default sections for cost breakdown per seat and raw logs. The Accept/Reject/etc buttons stay visible while the drawer is open — the decision never loses its context. Include a line stating evidence freshness (whether tests ran on the final revision) when the data allows. Vitest pins: drawer opens from the card, tiles present, technical sections closed by default, decision buttons still rendered.

IDENTITY RULE: keep ducklab's vocabulary exactly as it is (ducklings, flock, Now, seats, runs, landed). Adopt the PATTERN described, never the source prototype's naming. House voice: plain sentence first, technical detail behind disclosure.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: frontend/src/components/WaitingCard.tsx, a NEW EvidenceDrawer component (+ its test), and WaitingCard's test. Do NOT touch Now.tsx, RunLauncher.tsx, Roster.tsx, Settings.tsx, or internal/** — render the drawer from within WaitingCard so no view wiring is needed.

### T-168 — Launching work becomes a modal that pre-answers everything: mode cards with when-to-use and cost estimated from this project's history, seats prefilled from the roster

Fixes B-180.

## Reported

Adoption from the UI benchmark (design ref: ~/wiki/Desarrollo/ducklab/benchmark-p4-galley.md item 10 — Grok's launch modal). The launch flow presents as a modal: (1) what to work on — existing task preselected, quick brief, or a bug from the board; (2) mode choices as cards, each with ONE plain line of when to use it AND an estimated cost range computed from this project's past runs of that mode (fall back honestly to 'no history yet for this shape' when there is none); (3) seats already filled from roster resolution, shown not asked — include the microcopy principle: the user is never asked to type a model name. Launch button + defaults link. Vitest pins: preselection, estimate-or-honest-fallback, seats displayed prefilled.

IDENTITY RULE: keep ducklab's vocabulary exactly as it is (ducklings, flock, Now, seats, runs, landed). Adopt the PATTERN described, never the source prototype's naming. House voice: plain sentence first, technical detail behind disclosure.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: frontend/src/components/RunLauncher.tsx (and TddLaunch if needed), Now.tsx wiring for the modal trigger, plus their tests. Do NOT touch WaitingCard.tsx, EvidenceDrawer, Roster.tsx, Settings.tsx, or internal/**.

### T-169 — Roster suggestions show their arithmetic, ducklings get evidence portraits, and an empty seat explains itself

Fixes B-181.

## Reported

Refinement (NOT a rebuild — Jose rates ducklab's roster above every benchmark entrant; keep its structure and the mode subtitles untouched). Design refs: ~/wiki/Desarrollo/ducklab/benchmark-p4-galley.md items 2-3. Three additions: (1) each seat suggestion states its arithmetic in one sentence — not the criterion but the numbers ('luna: lowest cost per accepted run ($0.02 vs terra $0.11); glm52 produced findings in 81% of reviews'), computed from the same evidence the suggestion already uses; (2) each duckling card gains a one-line evidence portrait derived from its measured record (accept rate, send-back rate, cost per accept — rendered as prose, honest about small samples: under ~15 runs say 'early numbers'); (3) when a mode seats no one for a role, the empty seat says why in a sentence instead of rendering blank. Vitest pins each.

IDENTITY RULE: keep ducklab's vocabulary exactly as it is (ducklings, flock, Now, seats, runs, landed). Adopt the PATTERN described, never the source prototype's naming. House voice: plain sentence first, technical detail behind disclosure.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: frontend/src/views/Roster.tsx and its tests only. Do NOT touch WaitingCard, RunLauncher, Now.tsx, Settings.tsx, or internal/**.

### T-170 — Budget ceilings show what actually happened against them: hits last month, a suggested adjustment, and where the money went

Fixes B-182.

## Reported

Adoption from the UI benchmark (design ref: ~/wiki/Desarrollo/ducklab/benchmark-foreman-opus48.md — Foreman screen 9). The budgets & limits section stops showing bare numbers: (1) next to each ceiling, what happened against it recently — 'N runs hit this ceiling in the last 30 days' with a one-line suggested adjustment when the data warrants it; (2) a 'where the money went' summary: accepted work / rejected work / failed runs with amounts, honestly computed from the run log; (3) keep the edit affordances as they are. If the engine lacks an aggregate endpoint, add a small read-only one (GET budget stats) following the routes_table pattern — or compute client-side from the existing runs list if that is simpler and correct. Tests on whichever layer computes.

IDENTITY RULE: keep ducklab's vocabulary exactly as it is (ducklings, flock, Now, seats, runs, landed). Adopt the PATTERN described, never the source prototype's naming. House voice: plain sentence first, technical detail behind disclosure.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: frontend/src/views/Settings.tsx (budgets section) + its tests, and IF needed one new read-only endpoint (routes_table + engineapi + service + a Go test). Do NOT touch WaitingCard, RunLauncher, Now.tsx, Roster.tsx, or any other view.

### T-171 — The desktop nav moves to a left sidebar rail — a stable spine for a four-lane app

Fixes B-184.

## Reported

Adoption from the UI benchmark, Jose's explicit direction (design refs: ~/wiki/Desarrollo/ducklab/benchmark-*.md — Grok's letter rail, GPT-Sol's sidebar with project card, Foreman v2's 'a desktop app with a stable spine beats tabs when four runs are live').

Migrate the desktop's top navigation to a LEFT SIDEBAR rail. Scope is the shell ONLY — view contents do not change.

The sidebar carries, top to bottom: (1) the ducklab identity block; (2) the active project (name + branch, switcher preserved); (3) the primary nav — Now (with its waiting-count badge), Work, Records — each with its existing sub-navigation behavior preserved; (4) the settings area (Settings, Roster, Skills, Projects) where the gear used to lead; (5) a footer with what the current footer says (engine status, waiting summary). Keyboard: keep every existing shortcut working; adding single-key nav jumps is welcome but optional. The main content area must keep its current width behavior (no view should reflow beyond gaining the rail).

IDENTITY RULE: ducklab vocabulary and voice exactly as they are. This is a re-housing, not a redesign: nav labels, badges and status copy move house unchanged.

Tests: vitest pinning the rail renders all primary items with the Now badge, the settings area is reachable, and the footer status is present; the existing view tests and desktop coverage tests must stay green — that is the proof the views were not touched.

SCOPE NOTICE: this run has the repo to itself, but stay in lane anyway: frontend/src/app/App.tsx, nav/shell components (new Sidebar component welcome), styles, and their tests. Do NOT modify the individual views' internals (Now.tsx content, Roster.tsx, Settings.tsx sections, etc.) beyond what their mounting requires.

### T-172 — Budget history polish: hide zero-hit ceiling rows, gate the suggested adjustment, use the shared money formatter

Fixes B-183.

## Reported

Residue from T-170 (accepted under the gate ratchet). Three touches in Settings' budgets section: (1) ceiling rows with 0 hits in the last 30 days should not render — a list of '0 runs hit this ceiling' four times is noise; (2) 'Suggested adjustment' should appear only when the data warrants it (e.g. >=2 hits in the window), not on a single hit; (3) 'where the money went' formats with toFixed(2) — use the shared moneyOrZero/money from lib/format. Adjust the existing vitest accordingly.

### T-173 — The documents surface narrates the chain: three stages that say who writes and what you approve, sections with their live state, proposals presented as decisions

Fixes B-186.

## Reported

Documents phase 1 — 'the chain visible' (design doc: ~/wiki/Desarrollo/ducklab/propuesta-documents-ui.md, sections 1-2; comparative refs in benchmark-documents-sintesis.md). FRONTEND ONLY: the engine already provides everything — client.traceShow (walk Up/Down from any id), client.traceCheck (deterministic coverage with non-normative awareness), and the artifact get/promote/proposal methods. Do NOT add engine endpoints. Do NOT edit client.ts unless a method you need is genuinely missing, and then add only that method.

IDENTITY RULE: ducklab vocabulary and house voice — plain sentence first, technical detail behind disclosure, jargon explained at point of use.

Rework the documents surface (the Work-side lifecycle view — Cycle.tsx renders it today) from file-list to narrated thread: (1) stage headers teach in one line each — reqs: 'you write this; nobody codes from it' / spec: 'ducklings draft; you agree behavior' / plan: 'cut into tasks; you birth them' (phrase in house voice, these are direction not literal strings); (2) each plan section shows its live state derived from traceShow Down + task status ('T-042 landed' / 'waiting at its gate' / 'no task born yet'); (3) a pending proposal renders as a decision — 'a run proposes changing this section — read it and decide' with the existing promote/discard actions; (4) coverage from traceCheck surfaces as a plain line ('every normative section has work behind it' or 'N sections have no task yet'), never as raw JSON. Vitest pins headers, live states, proposal decision, coverage line.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Cycle.tsx (and its test file), plus a new documents-local component if you need one UNDER views/. Do NOT touch RunView.tsx, Runs.tsx, Now.tsx, components/ shared files, or client.ts.

### T-174 — Every run answers 'why does this run exist': the requirement sentence quoted, the chain as breadcrumb, and no-spine stated honestly

Fixes B-187.

## Reported

Documents phase 1 — 'the chain visible' (design doc: ~/wiki/Desarrollo/ducklab/propuesta-documents-ui.md, sections 1-2; comparative refs in benchmark-documents-sintesis.md). FRONTEND ONLY: the engine already provides everything — client.traceShow (walk Up/Down from any id), client.traceCheck (deterministic coverage with non-normative awareness), and the artifact get/promote/proposal methods. Do NOT add engine endpoints. Do NOT edit client.ts unless a method you need is genuinely missing, and then add only that method.

IDENTITY RULE: ducklab vocabulary and house voice — plain sentence first, technical detail behind disclosure, jargon explained at point of use.

RunView gains a compact origin panel: (1) the requirement sentence this run ultimately serves, QUOTED in italics (walk traceShow Up from the run's task to the requirements section; quote its text or title); (2) a breadcrumb of the chain (plan section ← spec section ← requirement) where each crumb navigates; (3) when the walk finds no spine, say so plainly — 'this run has no document behind it — worth knowing' — never render an empty panel; (4) place it in the right rail near the existing budget/team panels, details behind disclosure if long. Vitest pins quoted sentence, breadcrumb, and the no-spine sentence.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/RunView.tsx (and its tests). Do NOT touch Cycle.tsx, Runs.tsx, Now.tsx, shared components/, or client.ts.

### T-175 — Task cards carry their origin line: the plan-section sentence that bore them, clickable — and orphans say so

Fixes B-188.

## Reported

Documents phase 1 — 'the chain visible' (design doc: ~/wiki/Desarrollo/ducklab/propuesta-documents-ui.md, sections 1-2; comparative refs in benchmark-documents-sintesis.md). FRONTEND ONLY: the engine already provides everything — client.traceShow (walk Up/Down from any id), client.traceCheck (deterministic coverage with non-normative awareness), and the artifact get/promote/proposal methods. Do NOT add engine endpoints. Do NOT edit client.ts unless a method you need is genuinely missing, and then add only that method.

IDENTITY RULE: ducklab vocabulary and house voice — plain sentence first, technical detail behind disclosure, jargon explained at point of use.

Wherever task cards/rows render in the Work tasks surface: (1) each task shows one muted origin line — the title/sentence of the plan section it was born from (traceShow Up), clickable to the documents surface; (2) a task with no spine shows 'no document behind this task' instead of nothing; (3) a task promoted from a bug shows both parents ('from bug B-xxx · plan §y') when both exist — the promote linkage already exists in the data. Build the line as a small component components/OriginLine.tsx so later surfaces can reuse it. Vitest pins origin render, orphan sentence, dual parents.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: the tasks list surface (locate where task cards render — likely within the Work views), components/OriginLine.tsx (new) and tests. Do NOT touch Cycle.tsx, RunView.tsx, Now.tsx, or client.ts.

### T-176 — The Requirements tab renders spec sections, and SPEC-008 renders twice on one page

Fixes B-190.

## Reported

Production finding (Jose's screenshot, documents surface, 2026-08-25). The Requirements tab — whose own header says 'you write this; nobody codes from it' — displays SPEC-008 and SPEC-026 cards full of code identifiers. Either the stage filter is broken or section-to-stage attribution is. Separately, SPEC-008 appears twice in the same scroll (once with 'no task born yet', once without). Fix the filter/attribution, dedupe the render, and pin both with vitest against a fixture that mixes stages.

LANE NOTICE: this run shares the repo with one concurrent run. Your lane: frontend/src/views/Cycle.tsx and its tests only. Do NOT touch App.tsx, Sidebar, Now.tsx, Records views, or client.ts.

### T-177 — The pre-sidebar utility panel (autopilot, next steps, recent runs) is homeless in the new layout

Fixes B-196.

## Reported

Flagged by Jose on the first production render after T-171. The middle column — autopilot start, next steps ('Cut a release', 'Triage the open bugs', 'Promote B-178...'), 'ask how & why', Recent runs — was designed to BE the left rail of the old top-nav layout. With the real sidebar in place it floats as a second rail between nav and content: unanchored, with its own 'hide' link, and duplicating jobs other surfaces own. Recommended dissolution (doctrine: each job lives where its surface is): next steps are DECISIONS -> they belong on Now (they are the 'what needs me' list by definition); Recent runs belongs to Records (and the sidebar's Now badge already signals activity); autopilot start/stop is an operating control -> sidebar, near the engine status; 'ask how & why' -> sidebar footer or command palette. Alternative if dissolution is too large for one pass: collapse the panel into a toggleable utility drawer anchored to the sidebar. Either way the middle column disappears as a permanent layout zone. State the choice taken in the run summary.

LANE NOTICE: this run shares the repo with one concurrent run. Your lane: frontend/src/app/App.tsx, components/Sidebar.tsx, and the views that RECEIVE relocated jobs (Now.tsx for next steps, Runs/Records surface for recent runs), plus tests. Do NOT touch Cycle.tsx or client.ts. Note: B-194 (subnav grouping under the wrong parent) lives in your territory — fix it as part of the layout work and say so in your summary.

### T-178 — The documents surface has no working frame: the header scrolls away, and finding one section means reading them all

Fixes B-197.

## Reported

Flagged by Jose on production render. Two faces of one failure — the frame does not hold the reader: (1) the stage tabs, page header and actions scroll off with the content; they must stay pinned while sections scroll beneath. (2) There is no way to FIND a section: no filter, no index, no jump-by-id. Nobody reads all requirements linearly; the design question is minimum interaction and minimum time to land on one known section (requirement, spec, or plan task alike). Direction: a pinned compact index (ids + titles, type-ahead filter that narrows as you type, showing breaks/no-task markers inline) with the content pane scrolling to the selected section; wire section ids into the existing command palette so typing SPEC-026 anywhere jumps there. Filters for the working states (breaks only / no task yet) belong on the index, not as more prose. Vitest pins: sticky frame, type-ahead narrowing, jump-to-section, palette entries.

=== TASK FRAMING (D1 — Documents skeleton) ===
BINDING DESIGN REFERENCE: docs/desktop-blueprint.md, sections 2 and 3 — read it FIRST; where this brief and that file disagree, the file wins.

This task implements the Documents page skeleton as one coherent change, NOT as isolated patches. Acceptance criteria — ALL of these, which correspond to filed bugs landing together here:
1. (this bug, B-197) Pinned frame: stage control + index pane fixed while detail scrolls; index rows are `id — title` with type-ahead narrowing and inline state markers (break / no task yet / proposal pending); selecting a row brings its section into the detail pane.
2. (B-189, rail part) The right-hand traceability rail is REMOVED as a zone. The only ambient remnant is one health line ('N breaks in the spine') placed in the frame; it will link to a ledger view built in a later task — for now it may link to nothing but must exist. Inline markers on index rows and section cards carry the in-place information.
3. (B-195) One control owns stage switching; the teaching lines become captions ON it; the three inert stage cards disappear. Nothing inert may look pressable.
4. (B-192, order part) Reading order: 'what you asked for' — the person's words — renders FIRST and open in the detail pane; the redraft form moves BELOW the document behind a plain affordance (its internal redesign is a later task — just relocate it).
Vitest pins every criterion. Keep every existing passing behavior (dedupe, stage filtering from T-176) intact.

LANE NOTICE: this run shares the repo with one concurrent run (T-177, which owns App.tsx/Sidebar/Now/Records). Your lane: frontend/src/views/Cycle.tsx, new components UNDER views/ if needed, and tests. Do NOT touch App.tsx, Sidebar, Now.tsx, Records views, or client.ts.

### T-179 — Sidebar top stacks three redundancies: app name the window bar already shows, a project card the selector already covers, and a bare branch label that reads as jargon

Fixes B-198.

## Reported

Flagged by Jose. (1) The app name repeats the window title bar; the identity block shrinks to the glyph or disappears. (2) The informational 'project ducklab' block does nothing the project selector below it does not — one control owns showing AND switching the project. (3) 'main' renders bare under the project — to a user, meaningless; it is the git branch (added by T-171 following the benchmark prototype uncritically). Correct rule: branch is EXCEPTION-state information — hidden when the project sits on its base branch, shown with words when it deviates ('on branch chore/release-0.3.48 — not the base') because that is when it changes decisions. Vitest pins: no app-name text node, single project control, branch hidden on base / worded when deviating.

=== TASK FRAMING (S1 — sidebar structure) ===
BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1 — read it FIRST; it wins over this brief on conflict.

Acceptance criteria, landing together here:
1. (this bug, B-198) No app-name text node; one project control (selector shows AND switches — the informational card goes); branch as exception-state only (hidden on base branch, worded when deviating: 'on branch X — not the base').
2. (B-194) Subnav renders visually attached to its own parent (Work's children under Work), never adjacent to a sibling heading — pin DOM order with vitest.
Note: T-177 just landed AutopilotControl into the sidebar — preserve it and its tests.

LANE NOTICE: this run shares the repo with one concurrent run (T-178, which owns Cycle.tsx). Your lane: components/Sidebar.tsx, App.tsx if mounting requires, and tests. Do NOT touch Cycle.tsx, views/, or client.ts.

### T-180 — Restore the cherry-pick SHA assertion: T-166 pinned commit identity but nothing proves it

Fixes B-203.

## Reported

Follow-through on B-176/T-166 (commit 8bfec63 pins author+committer identity and dates from the orphan so recovery recreates the original object id). The assertion T-162 removed from TestProjectRecoveryDoors/cherry-pick-chain must come back as a real contract now that the implementation honors it, replacing T-162's explanatory comment. Also verify it holds under parallel load (the original flake scenario, B-174) — run the package with -count and parallel gates locally before trusting it. Until this lands, B-176's 'fixed' cannot honestly be verified.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: internal/service/remote_test.go (and internal/vcs tests if you add one) ONLY. Do NOT touch frontend/** or other packages.

### T-181 — T-177 left the dissolution half-done: a floating utilities drawer overlaps content and the whole GuideRail is embedded in Now, duplicating its running list

Fixes B-200.

## Reported

Real-render finding (Jose's screenshot post-T-176/177) against docs/desktop-blueprint.md section 1, which this bug finishes. Three defects, one landing: (1) the 'Utilities' drawer — whose content is links saying where things moved — floats OVER the Now content and must be REMOVED entirely, along with the sidebar's 'hide utility drawer' control; a signpost is not a dissolution. (2) Now embeds the full GuideRail as a card (own hide, own running list, next steps) ABOVE Now's native running section — the same runs render twice on one screen. Now's native sections absorb the jobs (next steps as a proper Now section; nothing embedded wholesale). (3) Recent runs live in Records natively, not via an embedded panel. Vitest pins: no utilities drawer, exactly one running list on Now, next steps as a native Now section.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: components/GuidePanel.tsx (remove/reduce), views/Now.tsx, views/Runs.tsx, app/App.tsx, and their tests. Do NOT touch components/Sidebar.tsx, views/Cycle.tsx, internal/**, or client.ts.

### T-182 — The sidebar's config block grew to five entries (Ducklings appeared) — the target is one

Fixes B-201.

## Reported

Real-render finding. Post-T-177 the bottom block shows Settings, Ducklings, Roster, Skills, Projects — one MORE engine-domain entry than before, while the blueprint (section 1) and the pending Configuracion proposal target ONE Settings entry. Until the consolidation arc gets its go, the rule is: no new config entries; fold Ducklings back under wherever it lived (Settings section or Roster). Vitest pins the bottom block's entry list.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: components/Sidebar.tsx and its tests ONLY (T-179 just landed there — rebase onto its state and preserve its tests). Do NOT touch App.tsx, GuidePanel, views/, or client.ts.

### T-183 — T-182 hid Roster, Skills and Projects without giving them a door — the rooms are unreachable

Fixes B-204.

## Reported

Critical regression found by Jose on production render. T-182 collapsed the sidebar's config block to one Settings entry and the code comment says the engine-domain rooms 'belong behind Settings' — but Settings' subnav (my ducklings, providers, budgets & limits, autopilot & autonomy, remote & git, appearance & alerts, engine) gained NO entries for Roster, Skills or Projects. The Roster board — seats, modes, flock evidence, the surface the operator rates above every benchmark prototype — has no path from the UI at all. FIX (interim until the Configuracion arc): Settings' subnav gains Roster, Skills and Projects entries so every room keeps a door; the existing routes/views are untouched — this is linking, not moving. NEW BLUEPRINT-CLASS RULE, pin it with a vitest reachability test: every routed view must be reachable from the sidebar tree — a room without a door is a failing test, not a code review question.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: components/Sidebar.tsx, views/Settings.tsx (subnav wiring only), and tests. Do NOT touch App.tsx, GuidePanel, Now.tsx, Runs.tsx, Cycle.tsx, or client.ts.

### T-184 — The settings space reorganizes around the user's three questions — mechanical re-housing, Roster intact

Fixes B-208.

## Reported

C1 — the Settings consolidation (Jose's go, 2026-08-25). BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1 (one Settings entry) — this task builds what lives BEHIND that entry.

Reorganize the settings space around the user's three questions, as a MECHANICAL RE-HOUSING (no internal redesign of any section — that is a later arc):
1. 'Who works for you — and how far they may go': the Roster board moves here INTACT as the area's heart (it is the operator's favorite surface; move, do not redesign), with providers ('add a provider first...' teaching preserved), ducklings config, skills, budgets & limits (with its history features), autopilot & autonomy.
2. 'Your projects': project management + per-project repo/remote & git config.
3. 'Your preferences': appearance & alerts, engine section.
Rules: section names speak user, not engine domain; every existing deep link redirects to the new home; the reachability test (T-183) keeps passing and is EXTENDED to the new tree; every moved section keeps its existing tests green — that is the proof nothing was internally touched.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: views/Settings.tsx, views/Roster.tsx ONLY if mounting requires (no internal changes), components/Sidebar.tsx subnav wiring, App routes if needed, and tests. Do NOT touch views/Now.tsx, views/Cycle.tsx, components/EvidenceDrawer.tsx, GuidePanel, internal/**, or client.ts.

## Amended after the first attempt (2 red rounds, empty diff — read this FIRST)

The first attempt failed on legacy route compatibility. The binding ROUTES CONTRACT, learned from that failure:
- Every existing route name stays VALID and renders its view: now, board, cycle, runs, reports, review, release, bench, settings, ducklings, roster, skills, projects. You re-house NAVIGATION, not routes. A user's bookmark to any of them still works.
- The pre-existing route/view tests are the contract: make them pass by KEEPING routes and views alive, never by rewriting or deleting the tests. Red 'legacy route compatibility' tests mean YOUR change is wrong, not the tests (the advisor already told you this).
- Implementation shape that fits the contract: the Settings view gains a grouped category menu (three user-question groups containing the existing sections/rooms as entries); ducklings/roster/skills/projects remain their own views reachable through that menu (and their routes). This is wiring and grouping — the smallest change that satisfies. Do NOT move component files, do NOT rewrite App routing wholesale, do NOT fold views into Settings.tsx bodily.
- Work in THIN slices: land the grouped menu first, run the tests, then adjust labels. If a step reds the suite, revert that step rather than accumulating.

### T-185 — The render contract: [render] in project.toml, captures attached to runs at the gate, shown in the EvidenceDrawer

Fixes B-209.

## Reported

R1 — the render contract (Jose's decision: render is a PROJECT CONFIG PARAMETER, sibling of [run] and [verify]).

Engine: project.toml gains an optional [render] section — command (how to start the renderable thing; empty = reuse [run]), url, ready (health check; empty = reuse [run].health), scenes (list of routes/states to capture), viewport (default 1440x900), timeout_s (default 120), artifacts (glob of images the command itself produced, for non-web apps — the contract is 'give me PNGs', the engine is never a browser). {engine}/{token} interpolate in url for the dev-CORS flow (T-161). When a run reaches its gate and the project declares [render], the engine executes the contract in the RUN'S WORKTREE and attaches the captured images to the run. A render failure is a gate CAVEAT with its reason, never a red gate. Frontend: the EvidenceDrawer shows attached captures under a 'how it looks' strip. Dogfood: ducklab's own project.toml declares [render] with scenes for Now, Work/Documents, and Records/Runs using the vite+--allow-origin flow (playwright is vendored in frontend/node_modules — the render command drives it via a small script; the engine only runs the command and collects files). Go tests for contract parsing/execution/attachment; vitest for the drawer strip.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: internal/** (config, service gate hook, run attachments), cmd if needed, .ducklab/project.toml, a capture script under scripts/ or frontend/, components/EvidenceDrawer.tsx + its test. Do NOT touch views/Settings.tsx, views/Now.tsx, views/Cycle.tsx, Sidebar, or GuidePanel.

### T-186 — The task lists Vitest pins (no utilities drawer, exactly one running list on Now, next steps as a…

Fixes B-205.

## Reported

The task lists Vitest pins (no utilities drawer, exactly one running list on Now, next steps as a native Now section) but no new assertions were added to pin these behaviors; only the old guidepanel tests were deleted.

Where: frontend/src/views/now.test.tsx:1

Suggested fix: Add explicit assertions: query that no utility-drawer testid exists, that exactly one running list renders on Now, and that now-next-steps is a native section.

Found by glm52 reviewing T-181 in run r-20260825-223247-zm5p (verdict: approve).


(reviewer finding from T-181) Add the missing vitest pins for the dissolution acceptance criteria: no utilities drawer anywhere, exactly ONE running list on Now, next steps as a native Now section, recent runs native in Records. Tests only.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: test files ONLY (views/now.test.tsx, components/Sidebar.test.tsx additions, views/runs tests). Do NOT touch any non-test source file.

### T-187 — Documents detail pane: verified claims, summary-first cards, redraft machinery behind one plain line

Fixes B-211.

## Reported

D2 — Documents cards & claims layer. BINDING DESIGN REFERENCE: docs/desktop-blueprint.md sections 2-3. Builds ON the D1 skeleton (T-178, just landed): do not restructure the frame — this task is the DETAIL PANE's content quality.

Acceptance criteria, landing together:
1. (B-191) A section claiming 'implements REQ-xxx' renders the claim only if the target exists; otherwise 'claims REQ-008 — no such requirement exists', styled as a break. Card and trace check can never disagree on screen.
2. (B-192, disclosure part) Section cards are summary-first: one plain sentence visible, body behind a disclosure; unexplained markers ('As-built: yes') get a point-of-use explanation or are dropped.
3. (B-193) The redraft machinery's default face is ONE plain line ('k3 drafts, glm52 critiques until it approves — about $1' — computed from the actual seats, not hardcoded); seats/rounds/caps appear only on disclosure; helper copy speaks human (no ids/diff/gate jargon).
Vitest pins each criterion.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Cycle.tsx (+ its test, + local components under views/). Do NOT touch Settings.tsx, Sidebar, Now.tsx, Runs.tsx, EvidenceDrawer, internal/**, or client.ts.

### T-188 — The runs list deserves pagination — honest counts, filters preserved, newest first

Fixes B-210.

## Reported

Flagged by Jose (2026-08-25). With 120+ runs the Records list renders everything in one scroll. Add pagination: a sensible page size (~25), an honest count line ('showing 25 of 122'), state filters (all/running/waiting/landed/failed) preserved across pages, newest first. If the runs list endpoint lacks limit/offset (or cursor) support, add it engine-side following the routes_table pattern. Vitest pins page size, count line, and filter+page interaction; Go test for the endpoint parameters if added.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Runs.tsx (+ runs tests), and the runs list endpoint in internal/engineapi ONLY if limit/offset support is missing — do NOT touch internal/service beyond that endpoint, and do NOT touch Cycle.tsx, Settings.tsx, EvidenceDrawer, or config internals (a concurrent run owns internal/** broadly — if your endpoint change would exceed one handler, note it and do the frontend against the existing API instead).

### T-189 — Documents closes: a real ledger behind the health line, one finder, and filters wired to truth

Fixes B-219.

## Reported

D3 — Documents closing landing. BINDING DESIGN REFERENCE: docs/desktop-blueprint.md sections 2-3. Builds on D1+D2 (landed). Acceptance criteria — ALL, landing together:
1. (B-189, remainder) The LEDGER view exists: every spine break listed with the two ways out (create the missing piece -> births a task; mark non-normative or amend -> opens the document flow). Table shape: what the document says / what exists / since when / settle it. The health line ('N breaks in the spine') LINKS to it — no more dead link. With zero breaks the health line stays quiet or says so plainly.
2. (B-214) The 'Jump to section' palette is REMOVED — the pinned index filter owns finding (give it a keyboard focus shortcut). This also removes the broken overlapping dropdown.
3. (B-215) The index chips (Breaks / No task yet) filter over markers derived from THE SAME data the counts come from (trace/check + traceShow join) — one source of truth. Pin with a test that flows through the same join code the view uses, not a parallel fixture map.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Cycle.tsx, a new ledger view/component under views/, App route for it if needed, and tests. Do NOT touch Settings.tsx, Sidebar, Now.tsx, EvidenceDrawer, internal/**, or client.ts.

### T-190 — The legacy seat-dropdown roster embedded in the Ducklings room duplicates the Roster board — two control surfaces over the same state

Fixes B-212.

## Reported

Jose's direction (2026-08-25, screenshots): the Ducklings settings room still renders 'Roster for this project' — advisor/architect/consultant/implementer/judge/reviewer/scribe dropdowns with 'chosen by the engine' — a pre-Roster-board relic he thought was eliminated. The Roster board (which still exists and stays) is THE roster UI. Two parallel write paths to seat state invite contradictory edits. Remove the embedded block from the Ducklings room; keep its useful teaching lines only if the Roster board lacks them. Blueprint section 1 (amended) is binding: one control surface per piece of state. Vitest pins the Ducklings room renders no seat controls.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1 (one control surface per piece of state).

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: the Ducklings settings room (views/Ducklings.tsx or wherever the embedded 'Roster for this project' block renders) and its tests ONLY. Do NOT touch views/Roster.tsx (the Roster board stays as-is), Settings.tsx, Sidebar, Cycle.tsx, EvidenceDrawer, or internal/**.

### T-191 — Settings expands no sidebar subnav: one door outside, all corridors inside via the category menu

Fixes B-213.

## Reported

Jose's direction (2026-08-25): having Ducklings/Roster/Skills/Projects both as sidebar sub-menu AND inside Settings is redundant. Target (blueprint section 1, amended): the sidebar shows the single 'Settings' entry with NO expanded sub-items; every room — my ducklings, providers, budgets & limits, autopilot & autonomy, remote & git, appearance & alerts, engine, roster, skills, projects — navigates exclusively through the category side-menu inside the Settings view. This supersedes T-183's interim sidebar doors (which were correct as an emergency door, not as the destination). The rule-6 reachability test is UPDATED to walk settings rooms through the internal menu instead of expecting sidebar entries. Vitest pins: sidebar renders no settings sub-items; every room reachable via the internal menu.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1 (Settings expands NO sidebar subnav). Note: T-184 just landed the grouped three-question category menu inside Settings — preserve it; your job is only the sidebar side and the reachability-test update.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: components/Sidebar.tsx + its tests, and the reachability test. Do NOT touch views/Settings.tsx beyond what removing sidebar entries requires, and do NOT touch Ducklings.tsx, Cycle.tsx, EvidenceDrawer, or internal/**.

### T-192 — The Settings frame must hold its rooms: category menu persists, rooms render inside — and the menu gets real visual hierarchy

Fixes B-220.

## Reported

Jose's render findings on T-184's landing (2026-08-25 screenshots). Two defects, one landing — the Settings FRAME:
1. STRUCTURAL: the re-housed rooms (Roster board, Skills, Project management) open as full-page teleports — clicking them LEAVES Settings entirely: the category menu vanishes and all context is lost. The Settings frame must persist: the category menu stays as a fixed column and the selected room renders INSIDE the remaining content area (mount the existing room views within the Settings layout; do not modify their internals). Routes stay valid — a direct route to roster renders the same framed presentation.
2. VISUAL: the category menu is unstyled stacked text — no hierarchy. Give it the house treatment: group labels as small muted uppercase captions (they are labels, not links), entries with consistent casing and naming in house voice, spacing between groups, no awkward two-line wraps (shorten the first group label if needed: the full question can live as a tooltip/caption).
Vitest pins: menu persists while a room is open; room renders within the frame; group labels are not links; reachability test stays green.

BINDING DESIGN REFERENCE: docs/desktop-blueprint.md section 1.

LANE NOTICE: this run shares the repo with three concurrent runs. Your lane: views/Settings.tsx, app/App.tsx if mounting requires, and tests. Do NOT touch views/Ducklings.tsx (a concurrent run owns it), views/Roster.tsx internals, views/Cycle.tsx, components/Sidebar.tsx, EvidenceDrawer, or internal/**.

### T-193 — The documents health line reports a false all-clear: '0 breaks — the spine is intact' while trace/check reports 16

Fixes B-222.

## Reported

Render-pass finding (R1's own captures, 2026-08-25). The page says '0 breaks in the spine — the spine is intact' and offers the ledger, while GET /v1/projects/ducklab/trace/check returns 16 errors (SPEC-008, 061-063, 068-076 unimplemented_spec + T-139..142). The D3 rewiring reads the wrong field/shape of the trace/check response (the API returns {errors:[...]}). Also the coverage line ('N sections have no task yet') vanished entirely. A false all-clear is worse than the original wall. Fix the join against the REAL response shape; pin with a test that feeds the actual API payload shape (errors array), not a hand-built fixture; restore the coverage line. Also render raw markdown in section summaries while in Cycle: cards show '**Priority:** must' unparsed and concatenated — render the emphasis or strip the markers, and separate the Priority line from the body (second criterion, same landing).

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Cycle.tsx, views/Ledger.tsx if the same join feeds it, and tests. Do NOT touch Settings.tsx, client.ts events, internal/**.

### T-194 — The SSE stream never connects in the browser dev flow: EventSource cannot send the bearer header

Fixes B-223.

## Reported

Render-pass finding: all six captures show '⚠ engine · stream connecting' and the Tasks board sits at 'Loading board… · all 0'. In the --allow-origin dev flow the app authenticates REST via the Authorization header, but EventSource cannot set headers — the stream request arrives tokenless, 401s, and retries forever; board data that rides the stream never arrives. Fix: the stream endpoint accepts the token as a query parameter (scoped to the SSE route; constant-time compare), and the client uses it when it builds EventSource URLs. Wails flow unchanged. Go test for query-token auth on the stream route; vitest for the EventSource URL construction.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: internal/engineapi (stream route auth), frontend/src/api/client.ts + events.ts (EventSource URL), and tests. Do NOT touch views/**, Sidebar, or EvidenceDrawer.

### T-195 — The framed Roster board is strangled: mode sections truncate and seats disappear inside the Settings frame

Fixes B-224.

## Reported

Render-pass finding: inside the Settings frame, the Roster board's mode sections (Common/Council/Solo/Pair/Split/Tournament) render as clipped one-liners ending in '…' with NO seat cards visible — the category menu column plus the flock column leave the modes no width. Fix within the frame contract: the room area must give the board its full layout (let the mode sections wrap/stack vertically when narrow, or allow the category menu to collapse to icons while inside a wide room) — choose the smallest change that shows every seat, and state it in the summary. Vitest pins: seat cards visible in the framed presentation at 1440x900.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: views/Settings.tsx (frame layout), views/Roster.tsx layout classes ONLY if wrapping requires (no logic changes), and tests. Do NOT touch Cycle.tsx, client.ts, events.ts, or internal/**.

### T-196 — The render capture script gives the app server no time to boot: one goto, then it dies

Fixes B-225.

## Reported

Dogfood finding from the first two real gate renders (T-194, T-193 accept warnings). The render capture script spawns Vite and then issues a SINGLE page.goto against the ready URL — a booting server yields net::ERR_CONNECTION_REFUSED and the whole capture aborts (surfaced correctly as a gate warning, but no photos). Fix in frontend/render-captures.mjs: poll the ready URL (retry loop with short sleeps) until it answers or the contract timeout expires, THEN navigate scenes; treat per-scene goto failures as per-scene failures (skip and note) rather than aborting the batch. Also kill the spawned server on every exit path. Node-level test or a dry-run mode if practical; otherwise the next gate's warning-free capture is the proof.

LANE NOTICE: this run shares the repo with one concurrent run. Your lane: frontend/render-captures.mjs only. Do NOT touch internal/**, views/**, or client.ts.

### T-197 — Render captures die twice: the dev server's descendants hold the pipes, and the engine discards PNGs on any dirty exit

Fixes B-226.

## Reported

Follow-through on the render contract dogfood (third gate iteration). Current failure: 'exec: WaitDelay expired before I/O complete' — the capture script's spawned Vite child keeps stdout/stderr pipes open after the script exits (kill() reaches the child but not its descendants / the inherited pipes), Go's exec surfaces the WaitDelay error, and the engine treats ANY command error as render-failed — discarding PNGs that may exist. Two-sided fix, one landing:
(a) frontend/render-captures.mjs: spawn the dev server DETACHED in its own process group with piped (not inherited) stdio, and on every exit path kill the negative pgid so no descendant holds a pipe.
(b) internal/service/render.go: the contract is 'give me PNGs' — after the command ends (cleanly or not), if the artifacts glob matches files, ATTACH them and report success with a note about the dirty exit; only report render-failed when there are no artifacts. Go test: command writes a PNG then exits nonzero -> captures attached with note.

LANE NOTICE: this run shares the repo with one concurrent run. Your lane: frontend/render-captures.mjs, internal/service/render.go + render_test.go. Do NOT touch views/**, client.ts, Settings, or Sidebar.

### T-198 — One stray 404 bricks the whole desktop: staleness inference dims every surface with pointer-events-none and never says why

Fixes B-228.

## Reported

Root cause of Jose's field report ('after the sixth or seventh settings tab, clicks do nothing'): client.ts treats ANY 404 as 'the engine does not know this route — it is older than this app', flips the global stale state, and App.tsx responds by dimming MAIN with pointer-events-none opacity-60 — silently disabling every click in the app. Clicking through settings rooms until one room's fetch 404s (for any reason, including a missing RESOURCE rather than a missing ROUTE) bricks the session. Reproduced with Playwright: main carries 'pointer-events-none opacity-60', every nav entry inherits pe:none. Fix, two halves: (1) staleness must not be inferred from arbitrary 404s — only a genuine version signal may flip it (compare the engine's advertised version/routes — /v1/health already carries version — or restrict the older-inference to a explicit marker the engine sends on unknown-route 404s, distinguishable from data 404s); (2) when the app DOES disable the surface, the state must be unmissable and worded (banner with the reason and the recovery action) — a silent full-app pointer-events-none is a trap that violates house rules 1 and 3. Vitest: data-404 does not flip stale; unknown-route marker does; the dimmed state renders its banner.

LANE NOTICE: your lane: frontend/src/api/client.ts (stale inference), app/App.tsx (dim state + banner), engineapi if the unknown-route marker needs adding (one small header/body marker on the mux's 404), and tests. Do NOT touch views/** or Sidebar.

### T-199 — Fake engine drifted again: seven endpoints unknown — the parity test does not walk the real routes

Fixes B-229.

## Reported

Repro against the fake engine 404s: /v1/defaults/modes, /v1/defaults/engine, /v1/defaults/autopilot, /v1/projects/{id}/app, /v1/projects/{id}/autopilot, /v1/projects/{id}/gate, /v1/projects/{id}/autonomy. B-169/T-157 was supposed to pin parity, but the drift returned within a day — the parity test clearly enumerates a hand-kept list instead of walking the REAL routes table. Fix: the parity test iterates internal/engineapi's routes_table (the single source) filtered to GET routes the frontend consumes, and asserts the fake engine answers each with 2xx and plausible shape; add the seven missing handlers now.

LANE NOTICE: your lane: cmd/fake-engine/** and its test. Do NOT touch internal/engineapi (read-only import of the table is fine), frontend/**.

### T-200 — F2a — plan sections declare their lanes: owned files become a validated contract that briefs inherit

Fixes B-231.

## Reported

Documents phase 2, engine half (Jose's standing go; design: docs/desktop-blueprint.md + the phase-2 section of the Documents proposal; benchmark convergence 3/3 — every strong prototype surfaced plan sections owning files). Three pieces, one landing: (1) PARSING — a plan section may declare the files/directories it owns (an 'Owns:' line or frontmatter key in the section, following the existing artifact section conventions; document the chosen syntax in the section template); (2) VALIDATION — trace/check (or a sibling deterministic check) reports when two sections with live/pending tasks claim overlapping paths, as a break kind ('lane collision'); (3) INHERITANCE — when a run starts for a task born from a section with a lane, the engine appends the lane to the run's brief automatically (the LANE NOTICE the operator writes by hand today, generated: which paths this task owns, and that concurrent runs own others). Go tests for all three: parse, collide, inherit.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: internal/artifact/**, internal/service (brief building + check), and their tests. Do NOT touch frontend/**, cmd/fake-engine (a concurrent run owns it), or engineapi routes beyond exposing the check result if trivial.

### T-201 — F2b — approving a plan is a Now decision: a card with coverage evidence, and a drawer that says what approving means

Fixes B-232.

## Reported

Documents phase 2, surface half (Jose's go; design refs: docs/desktop-blueprint.md section 3 'at decision time' + the GPT-Sol pattern from the benchmark synthesis). When the project's PLAN artifact has a pending proposal awaiting agreement, Now shows a decision card alongside the run cards: (1) headline in house voice ('The team turned the agreed spec into N tasks — it is waiting for you to approve the scope before any duckling touches code'); (2) evidence line computed from real sources — criteria covered (traceCheck), tasks proposed and how many can run in parallel (plan sections), lane collisions ('files with two owners: 0' — consume the F2a check if the engine exposes it by then, otherwise compute from section lane declarations client-side; a concurrent run is landing F2a in internal/**); (3) actions: Approve (artifact promote — if task birth is a separate step today, say what approving does honestly on the card), Ask for changes (the existing proposal discard/revise signpost), and Examine opening the EvidenceDrawer in a plan variant whose first line is 'you approve these tasks being born and their lanes — you are not approving code yet'. Vitest pins: card appears only with a pending plan proposal, evidence lines from fixture data flowing through the SAME join the view uses, drawer variant first line, and card absence when no proposal waits.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: frontend/src/views/Now.tsx, components/WaitingCard.tsx or a sibling PlanCard component, components/EvidenceDrawer.tsx (plan variant), and tests. Do NOT touch internal/** (a concurrent run owns it), cmd/fake-engine, Cycle.tsx, Settings.tsx, or Sidebar.

### T-202 — Dedupe staging exclusions and make accept staging immune to ignored/symlinked link_deps

Fixes B-227.

## Reported

Found cutting v0.9.0 (run r-20260826-035917-h6ul, deterministic across retries). The release-accept staging step runs `git add -A -- . :^node_modules :^frontend/node_modules :^.venv :^.venv :^frontend/node_modules` — exclusions duplicated (link_deps entries concatenated with defaults) — and git exits 1: 'The following paths are ignored by one of your .gitignore files: frontend/node_modules, node_modules'. In a run worktree the link_deps are SYMLINKS, which changes how the pathspec engine treats them. Sibling of T-143's staging-exclusion fix — this is the release-accept path variant. Fix: dedupe exclusions, and stage in a way that cannot trip over ignored/symlinked link_deps (e.g. git add -A without pathspecs plus git reset of exclusions, or --ignore-errors semantics that T-143 chose). Go test: release accept in a worktree with link_deps symlinks and default excludes. NOTE: v0.9.0 was promoted and tagged BY HAND (commit 7054095) as recovery — the engine's release record for v0.9.0 may need reconciling with the hand landing; make the fix idempotent against that state.

ESCALATED TO CRITICAL — second and third incidents: (2) T-199's TASK accept (not release) failed the same family: staging mounted nothing and commit died on 'nothing added to commit but untracked files present (frontend/node_modules)'; hand-landed as 479deb6. (3) NEW INTERACTING CAUSE: the [render] contract writes .ducklab-render-captures/*.png INSIDE the worktree; they leak into the run's DIFF as binary patches and sit untracked at staging time — capture output must be excluded from both the diff and the staging pathspec (or written outside the tree, e.g. the run's evidence dir directly). The staging fix and the capture-location fix belong together. Also: /land accepts any sha without validating it contains the run's changes — the operator briefly recorded a WRONG sha; consider a sanity check (sha exists and touches the run's files) or an explicit force flag.

=== TASK FRAMING (accept-pipeline hardening — lands B-227 AND B-207 together) ===

## NARROWED after one thrashing attempt (read this FIRST — the four-criteria version was too wide)

This task now lands ONLY the staging fix (criteria 1+2). Atomicity and /land validation moved to their own task — do NOT touch them.

1. STAGING: deduplicate exclusion pathspecs; staging succeeds in link_deps worktrees (symlinked node_modules/.venv). Reproduce both observed failures as Go tests: (a) release-accept dying 'paths are ignored' exit 1; (b) task-accept committing nothing ('nothing added to commit but untracked files present'). The intermittency correlates with untracked artifacts existing at staging time — pin that.
2. RENDER CAPTURES: .ducklab-render-captures is excluded from BOTH the run diff and staging (or capture output moves outside the worktree into the run evidence dir). Evidence never pollutes the change. One Go test.

Work in thin slices; if a step reds the suite, revert the step.

LANE NOTICE: internal/service (accept/staging paths), internal/vcs if the staging helper lives there, internal/runlog only if the capture dir moves. Do NOT touch frontend/**, cmd/**, or the /land endpoint.

### T-203 — The project chat has no door and seats nobody: the consultant hides in the command palette and the roster's pick is not pre-seated

Fixes B-236.

## Reported

Field finding by Jose. (a) 'Ask how & why — chat about the project' lost its visible entry when the utility panel dissolved; it survives only inside the command palette, which a user cannot discover. Give it a worded door where asking-about-the-project belongs (candidates: the sidebar footer near engine status, or a Now affordance; choose ONE, justify in summary — no new nav entries). (b) Opening any chat (project or task) must pre-seat the ROSTER-RESOLVED consultant (luna today) — the user was asked to pick a duckling the record already knows; a field the record can fill is a system failure. Vitest: door renders and opens the chat; chat opens with the resolved consultant seated.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: the chat entry/launch surfaces (App.tsx, Sidebar footer or Now, the chat view/component) + tests. Do NOT touch internal/** (T-202 owns it), Cycle.tsx, or Settings.tsx.

### T-204 — The configuration-amendment card replaces the consultant's answer instead of accompanying it

Fixes B-237.

## Reported

Field finding by Jose: he opened a chat, asked, and got no answer — a configuration amendment card rendered on top instead. The amendment card (config doctor findings) must ACCOMPANY the conversation as a side note ('the doctor also found N configuration findings — review them'), never preempt or replace the reply; and the consultant must still answer the question that was asked. If the reply genuinely failed, the chat must say so plainly (house rules: every state carries its reason). Vitest: with amendment events present, the reply still renders and the card renders beside/below it, not instead of it.

LANE NOTICE: this run shares the repo with two concurrent runs. Your lane: the chat view/components rendering replies and config_amendment cards + tests. Do NOT touch internal/**, App routing, or Settings.tsx.

### T-205 — Make accept atomic: commit merge succeeds before checkout advance, downgrade advance errors to warnings

Fixes B-207.

## Reported

Incident during T-181 accept: the API returned an internal error ('advance registered checkout to b0c33af: git checkout ... failed') while the landing commit b0c33af had already merged and the bug ladder proceeded. Residue: deleted dist/ assets and half-updated .ducklab files in the registered working tree. Likely cause: the operator's git pull on the same checkout raced the engine's post-land advance — the run-vs-human collision class. Fixes to consider: (a) the advance takes/retries under a repo lock and reports a WARNING (not an internal error) when the landing itself succeeded; (b) the advance is atomic (stash-advance-restore or worktree-switch) so a race leaves no partial state; (c) the error message says what the human should do ('your checkout raced the landing — run git status'). Repro guidance: accept a run while a concurrent git pull runs on the registered checkout.

SECOND SYMPTOM (same incident): the run stayed paused-at-gate with its Accept card still offered in Now even though the commit had merged — a second accept could have been attempted against already-landed work. Recovered via POST /land with the landed sha. The fix must make accept atomic from the run-state perspective too: if the merge succeeded, the run transitions regardless of checkout-advance failures, which downgrade to a warning.

SPLIT OUT of the T-202 hardening after it proved too wide: this bug now owns ONLY (a) accept atomicity (merge landed => run transitions; checkout-advance failure downgrades to a worded warning; advance atomic under concurrent pull) and (b) /land sha validation (reject a sha that does not exist or does not touch the run's files unless force:true). Launches AFTER the narrowed T-202 lands (same internal/service territory).

**Deliverables:**
- Accept merges the run commit to the default branch and only then advances the registered checkout
- If workspace advance fails (dirty or raced), accept still returns 200 and transitions the run
- Advance failure is downgraded to a warning on run.Warning and AcceptResult.Warning
- Test simulates a raced git pull on the registered checkout during accept
- Test asserts the run transitions and carries a warning instead of internal error

## Triage

**Component:** accept
**Suspected files:** internal/service/service.go, internal/service/accept_worktree_test.go

Accept currently errors on checkout advance, leaving the run paused despite the merge landing — a race class that can be reproduced with a concurrent git pull.

**Verification (triage recommends):** test-first — B-207 is reproduced by timing a concurrent `git pull` during accept; fix converts error to warning and run transitions anyway

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-206 — The engine never notices a run is taking twice as long as its kind: wall-clock vs history joins the escalation thresholds

Fixes B-239.

## Reported

Jose's design (2026-08-26), validated twice tonight by human-clock detection (T-184 first attempt, T-202 wide attempt — an hour each before a person noticed). The engine already computes historical run duration per mode/project (the launch modal's 'usually done in 10-15 minutes' estimate). Add a proactive escalation trigger: when a live run's wall clock exceeds a threshold relative to that history (default 2x the mode's average on this project; configurable in budgets & limits), fire the existing escalation_suggestion flow — pause with diagnoses (seat at capacity / brief too wide) and the actions card — WITHOUT waiting for a budget death. Honest-data guards: no trigger when history has fewer than ~5 runs of that shape ('no history yet'); the suggestion states the numbers ('47m so far; runs of this shape average 11m'). Go tests: trigger at 2x with history, silence without history, threshold configurable.

TERRITORY: internal/service escalation/budget region — queue behind the narrowed T-202 (same lane).

### T-207 — run_list punishes big asks (over-limit falls to the DEFAULT of 20) and analytics have no honest path: aggregates belong to the engine

Fixes B-242.

## Reported

Field finding by Jose: he asked the consultant for the project's first-run pass rate; the duckling requested >100 runs from run_list and silently received 20 (over-limit input falls back to the DEFAULT instead of clamping to the max of 100) — the answer was computed over 20 runs and presented as project truth. Three fixes: (1) CLAMP, never default — limit>max returns max, and the result SAYS it ('showing 100 of 1179; use offset to continue'); (2) pagination params (offset or cursor) on run_list so complete iteration is possible when genuinely needed; (3) the real path for analytics — portion doctrine: a small model must not haul 1179 records through its context to do deterministic arithmetic. Add an engine-computed aggregate the toolbelt can READ (project stats: first-run pass rate, accept rate, cost totals per mode/duckling — cousins of the existing scorecards/report data), so the consultant answers stats questions from ONE tool call. Go tests: clamp behavior with honest count line, pagination, aggregate correctness against a fixture runlog.

TERRITORY: internal/tools (run_list), internal/service or report (aggregate), MCP surface if the tool schema changes. Disjoint from T-206's escalation region — may run parallel.

### T-208 — An ended or aborted chat lingers in Now wearing accept-framing it can never satisfy

Fixes B-244.

## Reported

Field finding by Jose: he aborted a chat and it stays in Now with no way out; the chat view shows 'conversation ended · not accepted'. Chats are not accepted — a finished conversation awaits NO decision. Fix: (1) ended/aborted chats leave the Now inbox on their own (the record stays in Records like any run); if a lingering card is wanted for context, it carries a one-click dismiss ('done with this') — never an accept frame; (2) the chat view's terminal state speaks its own language ('conversation ended · N tool calls · $x') without 'not accepted'; (3) the 'failed, awaiting your call' bucket excludes chats the user aborted themselves — aborting IS the call. Vitest pins: aborted chat absent from Now (or dismissible), terminal copy without accept vocabulary.

LANE NOTICE: this run shares the repo with concurrent runs. Your lane: frontend Now/WaitingCard chat-state handling + the chat view terminal header + tests. Do NOT touch internal/** (concurrent runs own tools/service), Cycle.tsx, Settings.tsx.

### T-209 — The doctor's standing finding gatecrashes every chat and renders a raw struct at the user

Fixes B-245.

## Reported

Field finding by Jose: mid-chat about run pagination, a configuration amendment card appeared for the standing 'github configuration present but unused' finding — unrelated to the question or the chat. Two fixes: (1) RELEVANCE — the doctor's card joins a chat only when the conversation touches configuration (or the finding is NEW since last shown); standing findings live in their home (Projects/Settings) with at most a quiet one-line pointer, and a shown finding can be dismissed for the session; (2) HUMAN RENDERING — the card shows 'old {false false false gh false}' raw; old/new values render as words ('github integration is configured but nothing uses it — the amendment turns it off') with the raw values behind disclosure. Vitest pins: unrelated chat shows no amendment card; values rendered worded with disclosure.

LANE NOTICE: this run shares the repo with concurrent runs. Your lane: the chat view's config-amendment card rendering/gating + tests. Do NOT touch internal/**, Now.tsx (a concurrent run may own it via the chat-lifecycle task — coordinate by NOT touching WaitingCard), Cycle.tsx.

### T-210 — Manual /land leaves the task in a phantom 'blocked' lane — and blocked never says why

Fixes B-235.

## Reported

NARROWED after a grinding attempt — this task lands ONE thing:

POST /v1/runs/{id}/land performs the SAME task-state advancement that accept performs (task completion, plan annotation, whatever accept writes), so a manually landed run never leaves its task in a phantom 'blocked' lane. Go test: land a run manually, assert the task leaves the blocked state.

Do NOT reconcile existing tasks (the operator will, using the fixed /land). Do NOT add blocked-reason UI (moved to its own task). Do NOT investigate the autopilot path (moved elsewhere). Thin slices; revert any step that reds the suite.

LANE NOTICE: your lane: internal/service land/task paths + tests ONLY. Do NOT touch frontend/**, engineapi routes, or vcs.

ESCALATED TO CRITICAL with live evidence: the phantom-blocked T-181 was picked up by the AUTOPILOT and re-run (r-20260826-123740-3s4p) — a task finished 12 hours earlier got a fresh run that landed bb0a7be (benign this time: two test lines; the class is the danger the codebase itself documents at RunRequest.Redo: 'relaunched by an overnight operator that had no idea the task was finished'). ALSO investigate as part of this fix: how did that re-run LAND without a human accept under guarded autonomy — if a path exists where autopilot work auto-lands, name it and gate it. And the reconciliation now covers THREE hand-landed tasks: T-181, T-199, T-206.

### T-211 — A tail-role seat serializes whole runs: provider slots are reserved for the full roster for the run's lifetime, and inconsistently across pauses

Fixes B-218.

## Reported

NARROWED (third attempt — the wide 4-concern version ground twice and was aborted). This task lands ONE thing:

**Provider slots are acquired when a ROLE actually executes and released when its turn ends** — never reserved for the whole roster for the run's lifetime. A run whose implementer/reviewer/advisor are all cloud does not hold a local provider slot for its scribe until the scribe actually runs. Go tests: (a) two runs sharing only a tail-role provider (scribe on a cap-1 local) run concurrently through their build/review phases; (b) the provider cap binds while that role executes (two scribes on a cap-1 provider serialize only their scribe turns).

Do NOT touch: holder bookkeeping across pause/resume (follow-up), chat session slot behavior (follow-up), resume re-acquisition (follow-up). Thin slices; revert any step that reds the suite.

LANE NOTICE: internal/service/queue.go + queue_test.go and the minimal touch points in the run loop that acquire/release. Do NOT touch frontend/**, engineapi routes, or vcs.

### T-212 — No terminal chat belongs in Now, whatever killed it: stream-canceled chats still squat in 'failed, awaiting your call'

Fixes B-249.

## Reported

Follow-up to B-244/T-208 with a real hole (Jose's screenshot): a chat killed by an engine restart records 'provider chat: stream read: context canceled' with status FAILED — not user-aborted — so T-208's exclusion misses it and the card squats in 'failed, awaiting your call' offering 'run it again with changed settings', which is meaningless for a conversation. The clean rule T-208 should have taken: a chat in ANY terminal state (ended, aborted, failed, canceled) leaves Now entirely — its record lives in Records like every run; nothing about a dead conversation awaits a call. Vitest: chats with status failed/aborted/done all absent from every Now bucket.

LANE NOTICE: frontend Now/state classification + tests. Do NOT touch internal/** (T-211 owns the queue region).

### T-213 — The disabled-surface state is still silent: whenever the app dims MAIN it must say why and how out — the undelivered half of B-228

Fixes B-250.

## Reported

B-228's criterion 2 was never delivered (T-198 fixed the 404 inference in client.ts/engineapi but never touched App.tsx) and the operator STILL sees dead Settings tabs in the desktop with no explanation — undiagnosable precisely because the state is mute. Build it now: (1) ANY code path that sets the pointer-events-none/dim state on MAIN must simultaneously render an unmissable banner naming the trigger in words ('the engine reported an unknown route: GET /v1/... — this app may be newer than the engine; restart the engine' / 'the event stream disconnected — reconnecting' / whatever the actual cause) plus the recovery action; (2) inventory EVERY setter of that dim state (stale inference, connection loss, modal backdrops if they share the mechanism) and give each its wording — an unexplained dim must become impossible by construction (a single chokepoint function that takes a reason, asserted non-empty); (3) log the trigger to console with detail for field debugging. Vitest: dim implies banner with non-empty reason, for each trigger.

LANE NOTICE: frontend/src/app/App.tsx + client.ts stale/connection surfaces + tests. Do NOT touch internal/** (T-211 owns the queue), Now.tsx beyond what the banner mount requires.

### T-214 — The Settings frame is a one-way trap: section buttons die once you enter a room — two navigation mechanisms, one owner needed

Fixes B-252.

## Reported

ROOT CAUSE of the dead-tabs symptom, boundary found by Jose and confirmed with content-asserting repro (Playwright, any engine — never Wails-specific): the Settings category menu mixes two mechanisms. SECTIONS (ducklings, providers, budgets, autopilot, repo/remote, appearance, engine) are state buttons that only apply while the route is #/settings; ROOMS (Roster, Skills, Project management) are hash links (#/roster etc). Section->room works (hash navigates from anywhere); room->section is DEAD: the button mutates state the room route never reads — hash stays, content stays, no error, no feedback. Repro: goto #/settings, click 'Roster board' (hash #/roster), click 'providers' -> hash and content unchanged.

FIX — one mechanism owns settings navigation: make sections hash-addressable (e.g. #/settings/<section> or a section param) and render every category-menu entry as a plain link, so any entry works from any route; the frame reads the section from the route. Preserve deep links and the reachability test. Vitest MUST assert CONTENT/route outcomes, not click events: from #/roster, clicking providers lands on the providers section (hash + rendered content asserted); the full 10-entry random-walk navigates everywhere from everywhere.

LANE NOTICE: frontend/src/views/Settings.tsx, app/App.tsx + routes, and tests. Do NOT touch internal/** (T-211 owns the queue region), Now.tsx, or Cycle.tsx.

### T-215 — Expanding one reviewer turn expands them all: transcript expansion state is keyed by role, not by turn identity

Fixes B-253.

## Reported

Field finding by Jose: in a run transcript with several rounds, expanding the LAST reviewer turn expands EVERY reviewer turn — each with dozens of tool calls — losing track of which one he meant to analyze. The expansion state (or the React key) is shared across turns of the same role instead of being unique per turn. Fix: key expansion per turn identity (role+round+turn, or the event's stable id); expanding one turn affects only that turn. While in there: audit the transcript list for other shared/non-unique keys (a 'unique key prop' console warning already exists for ConfigSection — same disease family, B-251 adjacent). Vitest: two same-role turns, expand the second, assert the first stays collapsed.

LANE NOTICE: frontend/src/views/RunView.tsx (+ its tests). Do NOT touch Settings.tsx/App.tsx (a concurrent run owns them) or internal/**.

### T-216 — A budget pause mid-turn destroys work: the interrupted turn's progress is discarded, the resume skips to the wrong role, and even completed work regresses

Fixes B-254.

## Reported

CRITICAL field observation by Jose on T-211's transcript, explaining a full day of grinding: when budget-exceeded lands DURING a reviewer turn, the resume does NOT continue the reviewer — the turn is marked 'turn did not finish', its partial analysis (dozens of tool calls) is DISCARDED, and the run restarts with an implementer turn that never sees the feedback being generated. Worse: the implementer's own prior completed turn (5/5) was followed post-resume by a fresh start ending 2/5 — completed progress regressed as if lost. Since the 5M token ceiling fires on nearly every wide task's first turns, EVERY routine lift pays this tax: money and quality silently burned (10+ lifts today alone).

FIX design: (1) an interrupted turn's partial output (tool calls made, notes, drafts) is PRESERVED and injected as context for the resumed turn ('the reviewer was mid-review; its notes so far: ...'); (2) resume continues with the SAME role that was interrupted (restart the reviewer's turn with its partial context, never skip ahead); (3) completed turns and worktree state must be provably immune to pause/resume — add a test that pauses mid-turn, resumes, and asserts prior completed work (files + recorded turn outputs) is intact and the interrupted role goes again. Go tests for all three; transcript shows 'resumed with partial notes' instead of a bare 'turn did not finish'.

TERRITORY: internal/service run loop + runlog — queue behind T-211 (same region).

### T-217 — Replace the fabricated reviewer checkpoint with a real budget-interruption resume test

Fixes B-260.

## Reported

The purported end-to-end test fabricates an already-paused checkpoint instead of causing a reviewer budget interruption, and it neither verifies the resumed prompt contains the partial notes nor that the checkpoint came from preserved tool/draft progress.

Where: internal/service/budgetlift_test.go:81

Suggested fix: Drive a pair run with a controlled reviewer runner/provider that performs partial work then returns `ErrBudgetExceeded`, reload and resume it, and assert the persisted structured notes and resumed reviewer prompt alongside prior worktree/events.

Found by terra reviewing T-216 in run r-20260826-164440-3olh (verdict: request-changes).

**Deliverables:**
- The service test uses a controlled runner/provider that completes partial work before returning ErrBudgetExceeded during the reviewer turn.
- The test reloads and resumes the paused run through the service lifecycle rather than constructing an already-paused checkpoint.
- The resumed reviewer prompt contains the persisted partial structured notes and prior work context.
- The test asserts prior worktree changes and implementer/tool/events remain preserved across pause and resume.

## Triage

**Component:** service resume tests
**Suspected files:** internal/service/budgetlift_test.go

The current end-to-end test can pass without exercising budget interruption, durable partial progress, or prompt reconstruction, leaving the critical resume path inadequately covered.

**Verification (triage recommends):** test-first — Drive an implementer/reviewer pair where the reviewer returns partial notes with ErrBudgetExceeded, then resume and assert the reviewer prompt and persisted progress.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-218 — Render interrupted-turn checkpoint notes and resumed status in the transcript

Fixes B-259.

## Reported

The UI never consumes the new `turn_interrupted` event or renders the persisted resume notes, so the transcript still presents the interruption only as "turn did not finish" rather than "resumed with partial notes."

Where: frontend/src/components/ConversationLane.tsx:242

Suggested fix: Handle `turn_interrupted` in the run-view event reducer and render its checkpoint notes/status in the resumed turn transcript, with frontend coverage.

Found by terra reviewing T-216 in run r-20260826-164440-3olh (verdict: request-changes).

**Deliverables:**
- The run-view reducer consumes turn_interrupted events and associates checkpoint notes with the interrupted turn.
- ConversationLane renders persisted partial notes and a resumed-status indication instead of only "turn did not finish".
- Frontend coverage asserts interruption events, notes, and resumed transcript rendering.
- Existing incomplete-turn rendering remains correct when no resume notes are present.

## Triage

**Component:** conversation transcript
**Suspected files:** frontend/src/lib/runview.ts, frontend/src/components/ConversationLane.tsx, frontend/src/components/runview.test.tsx

This is an actionable, reproducible frontend data-rendering gap that hides persisted interruption context from users.

**Verification (triage recommends):** test-first — Feed a turn_interrupted event with persisted notes through the run-view reducer and assert the transcript displays the notes and resumed status.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-219 — A desktop launch surface requests mode=solo, silently overriding the configured 'build runs open in: pair' and the pinned seats

Fixes B-258.

## Reported

Field incident (Jose, T-216): he launched a build from the desktop and the run came up 'solo (request)' with solo's seats (terra/qwen38-max) — while Settings clearly configures 'build runs open in: pair' and the Pair board pins luna implementer, k3 advisor, terra reviewer. The launch REQUEST itself carried mode=solo, so some launch surface (find it: the bug/task card's start-run path, or a launcher preselection) hardcodes or wrongly preselects solo instead of honoring the default phase mode. Fix: every launch surface resolves its default mode from the configured phase modes (build->pair here) and sends NO explicit seats, letting the roster pins resolve; the launcher SHOWS what will run ('pair — luna builds, terra reviews, k3 advises — from your pins') before the click, per the launch-modal contract already landed in T-168. Vitest: with build=pair configured, every launch entry point produces a pair request; a regression test on the specific surface found guilty.

### T-220 — Render captures still live inside the worktree and now block the accept's branch switch: move them out of the tree for good

Fixes B-248.

## Reported

Third captures-vs-git interaction (after diff pollution and staging confusion, both fixed in T-202): T-210's accept failed with 'untracked files would be overwritten by checkout: .ducklab-render-captures/... could not detach HEAD'. The definitive fix T-197 deferred: the engine collects captures DIRECTLY into the run evidence directory (outside the worktree) or removes the capture dir immediately after collection. Go test: accept succeeds with a populated capture dir present at gate time.

ESCALATED: the captures now reached the REGISTERED checkout — after T-212's accept, .ducklab-render-captures/*.png sat STAGED in the main repo's index (cleaned by hand). Fourth git interaction. The fix is not optional exclusions anymore: captures write OUTSIDE the worktree, period.

WIDENED (2026-08-26, Jose's go): the accept↔human-tree relationship is this bug's real arena, and today produced a fifth incident of the class: accepts advanced main across the day while the person's checkout AND index stayed frozen pre-accept (the warning "your checkout is behind and was left untouched" fired and nothing ever reconciled). Consequences observed: (1) a `git commit -a` from that tree would have silently REVERTED T-219 and earlier landed fixes; (2) `make desktop` bundled the stale Now.tsx, shipping a desktop with an already-fixed bug live. Recovered by hand (git restore of 9 files to HEAD).

Acceptance criteria, expanded:
1. Render captures write OUTSIDE the worktree, period (original scope).
2. Accept reconciles the human checkout when it is CLEAN and on the default branch (fast-forward index+worktree); it warns and leaves untouched ONLY when the tree is dirty. The warning must then say what is at risk: a commit from this tree reverts landed work, and builds read stale sources.

### T-221 — Refuse to synchronize any dirty default-branch checkout, including .ducklab

Fixes B-267.

## Reported

The cleanliness check excludes `.ducklab/**`, so a registered default-branch checkout with local changes under that directory is still advanced despite the requirement to leave any dirty tree untouched.

Where: internal/service/service.go:2807

Suggested fix: Check the whole checkout without excluding `.ducklab`, or explicitly establish that no user-visible checkout changes can exist there before synchronizing.

Found by terra reviewing T-220 in run r-20260826-193858-czxz (verdict: request-changes).

**Deliverables:**
- The acceptance cleanliness check considers the entire registered default-branch checkout, including .ducklab paths.
- A dirty checkout is never synchronized after the default branch advances, regardless of which paths are locally modified.
- A regression test covers a local .ducklab modification and verifies the accepted files remain untouched and the warning is emitted.

## Triage

**Component:** acceptance checkout synchronization
**Suspected files:** internal/service/service.go, internal/service/accept_worktree_test.go

The current touched-path-only check can overwrite or partially advance a user-visible dirty checkout, violating the requirement to leave any dirty tree untouched.

**Verification (triage recommends):** test-first — Accept a commit while the registered default checkout has a local .ducklab change and assert the checkout remains untouched with a behind warning.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-222 — Triage proposes decomposition and promote can birth N tasks with disjoint lanes: portion the food to the duckling's size

Fixes B-241.

## Reported

NARROWED SCOPE (operator, 2026-08-26) — engine half ONLY, exactly two criteria. The UI surface (proposal rendering, promote button saying "creates N tasks") is deliberately OUT of this task; it gets filed at this task's gate.

1. TRIAGE may propose a decomposition: when a bug's scope spans multiple concerns, the triage output may include a split proposal — [{title, acceptance (<=2 criteria), owns}] — persisted on the bug record. The proposal NEVER auto-applies (triage recommends; a person decides).
2. PROMOTE with a stored proposal births N tasks (one per portion, each carrying its Owns: lane into the plan section) instead of one; the bug moves to in_progress and is eligible for fixed only when ALL its tasks are accepted. Promote without a stored proposal behaves exactly as today.

Go tests: a triage that stores a proposal; a promote that creates N plan sections with disjoint Owns; the all-tasks-accepted gate on the bug ladder; a promote with no proposal unchanged.

Do NOT touch: the desktop UI, B-240's escalation-time split, the 2x wallclock trigger (B-239), or existing single-task promote behavior.

TERRITORY: internal/service (triage/promote), internal/artifact (plan section creation). Original full write-up lives in B-241.

### T-223 — A single provider 520 mid-stream kills the whole run: transient upstream errors should pause as weather, not fail terminal

Fixes B-255.

## Reported

T-215's first attempt died terminal on 'provider chat: chat stream: 520 (Z.AI via openrouter)' during the reviewer's stream — one transient upstream error destroyed a run with a completed implementation turn. The codebase already treats transient exhaustion as resumable weather and has provider_retry machinery; a 5xx mid-stream should get the same treatment: bounded retries, then a weather pause offering resume/reseat (the fallback-duckling flow), never a terminal FAILED for weather. Go test: a stubbed provider returning 520 once mid-stream -> run retries/pauses as weather and completes on the next attempt.

TERRITORY: internal/service provider/stream error handling — queue behind T-211.

### T-224 — Enforce one checkout root throughout turns, gates, and accept staging

Fixes B-268.

## Reported

Observed on r-20260826-202217-aqsd (T-222, stage=test, solo terra). Forensics, all verified:

1. createRunWorktree ran: the worktree exists at ~/.local/state/ducklab/worktrees/ducklab/r-20260826-202217-aqsd, branch ducklab/T-222-aqsd, base 8ba94de, registered in `git worktree list` and in state.json.
2. The TURN executed against the REGISTERED PROJECT CHECKOUT instead: 10 successful fs_patch calls wrote internal/service/promote_carries_test.go in /home/jrullan/dev/ducklab (file mtime 20:26:18Z, inside the turn window); the transcript's fs_list/search results contain bin/ducklab-engine — gitignored artifacts that exist only in the main checkout (12 matches); the run gate (red, honest: "specifies work that does not exist") also saw that tree. The worktree copy of the file still has only the two old tests.
3. The ACCEPT operated on the worktree: stageRun found nothing (its mixed reset is the worktree reflog's "reset: moving to HEAD"), IsClean()==true so no commit was made, and "reproducing chained red test 8ba94de" reproduced the BASE HEAD — green — so the accept refused with "the committed test passes from a clean checkout — it asserts nothing that is not already true". That message is FALSE here and misleads the human into distrusting a good test: the test never traveled in any commit.
4. Outcome: the run is paused at its gate; the deliverable (+81 lines, TestPromotingStoredSplitProposalCreatesPortionedTasksAndWaitsForAllAcceptance) sits UNCOMMITTED in the human's checkout — the run-vs-human collision class: one careless discard destroys it.

Not yet explained — the investigation's starting point: runRoot(run, entry.Path) should have returned the worktree (WorktreePath is assigned synchronously before the exec closure runs), and the IDENTICAL flow used its worktree correctly an hour earlier (T-221 test run r-20260826-195927-2hmh, 19:59, chained bd9cd81 with content). Same engine process (started 18:38Z), same binary — so the divergence is data/state-dependent, not code-version. Reproduce/diagnose from the two runs' records.

Adjacent lead, possibly same family: the registered checkout's INDEX now holds staged .ducklab entries (runs/*/receipt.json, bugs/attachments/B-262/*.png) — something engine-side runs `git add` against the registered checkout. Roots are being crossed between worktree and project in more than one place.

Acceptance criteria:
1. A turn's ExecContext.ProjectRoot, its gate, and its accept staging provably reference the SAME tree — assert it at accept time: if the run records a WorktreePath, refuse to stage anywhere else, and if the turn executed elsewhere, say so instead of blaming the test.
2. The clean-checkout guard message distinguishes "the committed test is vacuous" from "the commit contains no test changes at all" (diff empty vs HEAD) — the second wording would have named this bug instantly.

Related: B-265 (worktree/project two-truths for plan reads — same schism, read side), ducklab run-vs-human collision doctrine, B-227/B-248 (accept staging family).

**Deliverables:**
- Accept refuses to stage when the recorded WorktreePath differs from the turn execution or gate tree, and reports the root mismatch explicitly.
- ExecContext.ProjectRoot, gate evaluation, and accept staging resolve to the same recorded worktree for every turn.
- The clean-checkout guard distinguishes an empty test diff from a committed test that is behaviorally vacuous.
- Regression coverage reproduces the cross-root scenario and confirms test changes remain in the worktree and are not stranded in the human checkout.

## Triage

**Component:** run worktree and acceptance synchronization
**Suspected files:** internal/service/service.go

The engine crossed registered checkout and run-worktree boundaries, stranded valid changes in the human tree, and emitted a false diagnostic that could cause data loss.

**Verification (triage recommends):** test-first — Run a test-first turn with a registered WorktreePath and verify execution, gating, and accept all use that worktree rather than the project checkout.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-225 — Reword or remove the unexplained 'N/M passed' stat in the Now footer

Fixes B-202.

## Reported

Real-render finding. The footer stat reads as alarming and unexplained — passed of WHAT (all-time run gates? tests? bench)? Either word it plainly ('288 of 526 gates passed all time') if it earns its place, or remove it; a number without a sentence violates the house rules. Decide what job the stat does; if none, rule 4 applies.

**Deliverables:**
- The NowFooter stat either reads as a full phrase naming what passed (e.g. '1 of 2 runs passed, all time') or is removed entirely — no bare 'N/M passed'
- now.test.tsx footer tests updated to assert the new wording (or assert the stat's absence if removed)
- The today/all-time spend figures in the footer are unchanged and still covered by the existing tests
- If kept, the wording says what the denominator is (finished runs, all time), not gates or tests

## Triage

**Component:** Now view footer
**Suspected files:** frontend/src/views/Now.tsx, frontend/src/views/now.test.tsx

NowFooter at Now.tsx:471-480 renders '{passed}/{finished} passed' with no noun or timeframe, a copy bug fixable in one component with the existing footer test as the verification harness.

**Verification (triage recommends):** test-first — Render Now with mixed-verdict runs and assert the footer either spells the stat out ('1 of 2 runs passed all time') or drops it; now.test.tsx already pins '1/2 passed' at line 258 and must be updated either way.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-226 — Show the effective per-provider run cap (1 local, 8 remote, or configured) instead of 'unlimited' in the Providers list

Fixes B-217.

## Reported

Found while diagnosing why T-184 sat queued with 2/4 engine slots free: with max_concurrent=0 the engine defaults LOCAL providers (IsLocalHost, which includes LAN IPs) to ONE run at a time and remote to 8 (queue.go providerCap), but the Settings providers row renders 'unlimited concurrent runs' for those same providers. The row must state the EFFECTIVE cap and its origin: '1 at a time — local default', '8 at a time — remote default', or the configured number. Bonus honesty: when a run queues on a provider slot, the desktop should surface the queued_reason the engine already produces ('waiting for a slot on provider X held by Y') on the run row — the reason existed and was invisible in the UI. Vitest pins effective-cap wording and queued-reason rendering.

ESCALATED with the operator's screenshot of the provider editor: the edit form itself promises 'concurrent runs this endpoint will seat at once (blank = unlimited)' — a FALSE contract; the engine maps blank to 1 for local providers and 8 for remote (queue.go providerCap defaults). DESIGN DECISION inside the fix: either (a) make blank truly unlimited (dangerous for a local GPU — the conservative default exists for a reason), or (b) keep the defaults and make BOTH the editor and the rows tell the truth: 'blank = 1 at a time on local providers (protects the GPU), 8 on remote'. Recommendation: (b) — honest defaults over honored lies. The spinner should show the EFFECTIVE value as placeholder, not the word 'unlimited'.

POLICY INPUT from the operator: caps must follow the serving backend — llama.cpp providers seat 1 (raising it degrades service), vLLM batches happily at 4+. The honest-defaults fix should TEACH this at point of use: the provider row/editor hints 'llama.cpp servers typically seat 1; vLLM handles several' when the backend is recognizable, and the default stays conservative.

**Deliverables:**
- Providers row in Ducklings.tsx renders '1 at a time — local default' when max_concurrent is blank and base_url is local (same locality regex as Roster.tsx)
- Renders '8 at a time — remote default' when blank and base_url is remote
- Renders '<N> concurrent runs' (or '<N> at a time') when max_concurrent is set; runs.test-style guard keeps singular/plural honest
- Form label '(blank = unlimited)' updated to state the actual default behavior (engine default: 1 local / 8 remote)
- Vitest cases cover local, remote, and configured providers

## Triage

**Component:** frontend ducklings/providers copy
**Suspected files:** frontend/src/views/Ducklings.tsx, frontend/src/views/ducklings.test.tsx, frontend/src/views/Roster.tsx

UI claims unlimited concurrency while the engine enforces 1 (local) or 8 (remote) when max_concurrent is blank; the fix is a copy/locality-computation change viewable and testable in Ducklings.tsx, reusing the Roster.tsx locality regex.

**Verification (triage recommends):** test-first — Vitest renders a provider row with blank max_concurrent and a local base_url, asserts '1 at a time — local default'; a remote row asserts '8 at a time — remote default'; a configured row asserts the number.

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-227 — Pluralize 'N runs hit this ceiling' and add a two-decimal money variant for budget aggregates

Fixes B-185.

## Reported

Residue from T-172 (accepted under the gate ratchet). (1) 'N runs hit this ceiling' needs the singular form at N=1. (2) 'where the money went' aggregates use money()'s four-decimal sub-dollar precision ($0.7500) — right for per-run costs, odd for monthly aggregates; give aggregates a two-decimal variant in lib/format and pin both in budget.test.tsx.

**Deliverables:**
- The ceiling-hits line renders '1 run hit this ceiling' at N=1 and 'N runs' otherwise
- lib/format gains a two-decimal money variant (e.g. money2) used by the budget money/activity aggregates
- Per-run costs still render with money()'s four-decimal sub-dollar precision
- budget.test.tsx pins the singular form and both aggregate and per-run decimal behaviors

## Triage

**Component:** frontend budget settings
**Suspected files:** frontend/src/views/Settings.tsx, frontend/src/lib/format.ts, frontend/src/views/budget.test.tsx

Cosmetic copy/formatting residue from T-172, exactly localized by the reporter to Settings budget copy and lib/format money(), with an existing test file to pin both fixes.

**Verification (triage recommends):** test-first — budget.test.tsx already asserts the hit-count copy and money strings; add cases with 1 hit (expects '1 run hit this ceiling') and sub-dollar aggregates (expects '$0.75' not '$0.7500'), while per-run money() keeps four decimals

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-228 — Show available update in the sidebar footer with a checkpointed restart action

Fixes B-199.

## Reported

Design decided with Jose (blueprint section 1, committed): when a newer desktop version exists than the one running, the sidebar footer — the system-truth zone — shows one worded line ('update ready — 0.9.1 · restart when idle'); silent when current. Its action performs the engine's checkpointed restart (the /v1/restart contract) and says so plainly ('restarts after the active runs finish — nothing is lost'). The existing blocking-mismatch banner (engine older than the app) is unchanged — broken-now interrupts, available-later does not. Needs: a version-comparison source (installed vs running — the build info endpoints likely suffice), the footer line, the action wiring, vitest for silent-when-current / worded-when-newer / action calls restart.

NARROWED FOR THIS RUN (operator, 2026-08-27) — exactly two criteria; the landed-vs-serving comparison (engine behind repo HEAD) is DEFERRED to a follow-up filed at this task's gate, because it needs a new repo-head source; this task uses ONLY data the API already serves.

1. The sidebar footer renders one worded exception line, silent when everything is current: 'update ready — <version> · restart when idle' when the available desktop version is newer than the running one, and 'engine built from uncommitted sources' when /v1/health reports dirty: true (the field already exists). One line, worded, never two banners.
2. The update line's action calls the engine's checkpointed restart (POST /v1/restart with a requester) and labels it plainly ('restarts after the active runs finish — nothing is lost'). Vitest: silent-when-current, worded-when-newer, worded-when-dirty, action calls restart.

Do NOT touch: the existing blocking version-skew banner (engine older than app — broken-now interrupts, available-later does not), the health endpoint, the engine.

Original full write-up lives in B-199.
- The version comparison uses the build-info source (health endpoint version vs the desktop-injected window.ducklab.version)
- The existing blocking engine-mismatch banner (engine older than the app) remains unchanged
- Frontend vitest covers silent-when-current, worded-when-newer, and action-invokes-restart

## Triage

**Component:** desktop sidebar footer
**Suspected files:** frontend/src/components/Sidebar.tsx, frontend/src/app/App.tsx, frontend/src/api/client.ts, cmd/ducklab-desktop/main.go

A decided blueprint feature (footer update notice is absent while the health version endpoint and /v1/restart contract exist) with explicitly specified vitest coverage, so it is reproducible-as-absent and test-first.

**Verification (triage recommends):** test-first — vitest on Sidebar: no line when running==available; worded line ('update ready — 0.9.1 · restart when idle') when newer; clicking calls POST /v1/restart

This section is the triager's reading, not the reporter's. Check it rather than assume it.

### T-229 — The history_duration trigger counts queued and paused time as work: elapsed should be active wall clock only

Fixes B-246.

## Reported

Calibration finding from the trigger's FIRST production firing (T-210: '30m so far; runs of this shape average 15m' — but a third of those 30m were queue waits and budget-pause dances, and the run was healthily mid-review). The elapsed side of the comparison should count ACTIVE time only (exclude queued and paused stretches), or the trigger over-fires on any run that waited for a slot. The historical average should use the same definition (symmetry). Also worth adding to the suggestion card: the run's current stage ('reviewer mid-read, round 1, no red gates') so the human's continue-vs-narrow call is one glance. Go test: a run with long queued time does not trigger at 2x; one with long active time does.

FIELD INCIDENT (2026-08-26, r-20260826-214453-prgc / T-222 relaunch): the trigger fired 3 SECONDS after a resume — the run had paused 16 minutes on an ask_human question (the human's own answer latency), that paused time counted as work, and history_duration escalated a perfectly healthy run into another pause immediately after the human's answer. The pause/resume machinery itself behaved exactly as designed (same-role same-turn re-entry, checkpoints intact — B-254 fix verified working in this same record). This bug is a PREREQUISITE for the escalation-as-pause design (escalation timeline/pause bug): with elapsed counting paused/queued time, every slow human answer buys a spurious escalation pause right after resume. Fix restated: elapsed = active execution time only (sum of running intervals), never queued or paused time.

### T-230 — Adjust-seats shows empty seats: the rail fetches the roster for mode=solo regardless of the phase being tuned, and the one-shot pre-seed bakes the blanks in

Fixes B-264.

## Reported

Symptom (reported by Jose): select a task on the board, open the rail, click "adjust seats & caps" — the seat dropdowns are NOT pre-selected with the pinned roster.

Cause, three parts:
1. Board.tsx:929-933 — the rail fetches /v1/projects/{id}/roster?mode={runMode}, where runMode is useState("solo") and is only ever updated by the plain RunLauncher (onModeChange, Board.tsx:1104). The TddLaunch flow never updates it, so the roster handed down is resolved for SOLO.
2. RunLauncher.tsx rosterSeats() projects that solo roster onto the build phase in PAIR: roles advisor/reviewer are absent from a solo resolution, so those seats project to "" — unselected dropdowns.
3. The pre-seed effect (RunLauncher.tsx:81-92) is one-shot, guarded by value.ducklings.length === 0. Once it bakes ["luna","",""], a later correct roster never refills; the blanks are permanent for the session.

Latent collision with T-219/B-258/SPEC-026: the pre-seed turns roster resolution into EXPLICIT seats on the request (provenance project/global, not picked now). After T-219 lands (untouched = no override), this UI would still send explicit seats derived from a roster resolved for the WRONG mode — re-introducing the phantom override through another door.

Fix direction: fetch the roster per the MODE OF THE PHASE being tuned (test and build each ask for their own); display the resolution as the selected value with its provenance chip; send the engine only genuine picks (provenance "picked now"), leaving untouched seats omitted so the single launch path resolves them. The one-shot guard must key on the roster identity/mode, not on ducklings.length.

Related: B-258 (T-219, engine-side single launch path), SPEC-026, B-261 (decision surfaces carrying their own seat overrides).


