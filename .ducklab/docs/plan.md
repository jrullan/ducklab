---
kind: plan
version: 0
approved_by: 
---

## M-001 — Reported bugs

### T-001 — Expose budget lifting through MCP and identify the invalid kind field in lift errors

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


