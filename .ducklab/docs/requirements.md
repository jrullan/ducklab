---
kind: requirements
version: 2
updated_at: 2026-08-14T23:26:06Z
run_id: r-20260814-231255-ym4e
ducklings: [beelink-local, k3, atom-local, glm52]
based_on: f7c41bc479704126
origin: adopted
approved_by: mcp:claude-code
---

## REQ-001 — Three-binary client-server architecture

**Priority:** must

The system deploys as three separate binaries: `ducklab-engine` (daemon that owns all runs, binds 127.0.0.1 only with bearer token authentication), `ducklab` (stateless CLI client), and `ducklab-desktop` (stateless Wails v3 desktop client). Clients communicate with the engine via HTTP + SSE API.

**Assumption:** From README.md §79-85, the three cmd/ directories, and status.md AC-4.

## REQ-002 — Three-stage artifact lifecycle

**Priority:** must

The system implements three lifecycle stages that write structured Markdown+YAML artifacts: intake (brief → requirements), spec (requirements → spec), and plan (spec → milestones and tasks). Each stage writes a `.proposed` file requiring human acceptance before promotion to `.ducklab/docs/`.

**Assumption:** From `internal/stage/stage.go` defining only Intake, Spec, and Plan as valid stages, and README.md §108-110.

## REQ-003 — Solo mode execution

**Priority:** must

Solo mode runs a single implementer with full toolbelt until the gate is green, with configurable round limits. This is the baseline against which all other modes are measured.

**Assumption:** From `internal/strategy/strategy.go` SoloScript and `internal/service/modes.go` case "solo".

## REQ-004 — Pair mode execution

**Priority:** must

Pair mode runs an implementer followed by a decorrelated reviewer. The reviewer sees only the diff with the implementer's transcript omitted (OmitRole) and authorship anonymized, to prevent adopting the author's rationalization. Runs until gate is green and verdict is approve.

**Assumption:** From `internal/strategy/pair.go` PairScript and `internal/service/modes.go` case "pair".

## REQ-005 — Tournament mode execution

**Priority:** must

Tournament mode runs N contestants concurrently in isolated git worktrees, arbitrated by a blind judge. Exactly one green candidate short-circuits with no judge call. Multiple green or all red candidates invoke the judge. A green candidate is applied byte-identically.

**Assumption:** From `internal/strategy/tournament.go` ExecuteTournament, `internal/service/modes.go` runTournament, and status.md AC-19-20.

## REQ-006 — Split mode execution

**Priority:** must

Split mode decomposes a task into subtasks with disjoint file ownership, runs them concurrently in separate worktrees, then integrates by deterministic file copy with no model involvement. Overlapping ownership is rejected deterministically and retried once with the conflict named.

**Assumption:** From `internal/strategy/split.go` ExecuteSplit, ValidateOwnership, and `internal/service/modes.go` runSplit.

## REQ-007 — Council mode for artifact stages

**Priority:** must

Council mode supports multiple models on artifact stages (intake, spec, plan, review). An architect drafts, N critics (each a different duckling) critique the draft without seeing each other's critiques (OmitRole: reviewer), an optional human turn, then the architect revises. Council is the default mode for artifact stages; solo is available as a cheaper alternative.

**Assumption:** From `internal/strategy/council.go` CouncilScript and ArtifactScript, and `internal/stage/stage.go` BuildPrompt.

## REQ-008 — Git-based version control integration

**Priority:** must

The system requires a git repository and creates work branches with configurable prefix (default `ducklab/`), captures tree snapshots before runs, commits only on explicit human acceptance, restores working tree on rejection/failure. Models never execute git commands directly.

**Assumption:** From README.md §104-120, status.md AC-7, and git_diff tool being read-only.

## REQ-009 — Deterministic gate execution

**Priority:** must

Verification gates run commands whose exit code is the only verdict signal. Gate modes: tests, build, lint, custom, none. Auto-detection for Go, Python, npm, Cargo, TypeScript. Models never decide gate outcomes.

**Assumption:** From `internal/verify/verify.go` Detect() and Run() functions, and README.md §163.

## REQ-010 — Traceability spine

**Priority:** must

Requirements → spec → tasks form a directed graph via `Implements:` fields. The system detects orphans (unimplemented requirements, untraceable specs) and blocks artifact acceptance on trace violations.

**Assumption:** From README.md §99-120, status.md AC-40, and `internal/artifact/trace.go`.

## REQ-011 — Role-based toolbelt restriction

**Priority:** must

Seven roles with distinct tool access: architect (read-only plus ask_human), implementer (full toolbelt including writes, shell, verify), reviewer (read-only), judge (fs_read, git_diff), triager (fs_search, fs_read, bug_read, git_log), scribe (fs_read, artifact_read), human (no tools). Scripts may narrow but never widen a role's ceiling.

**Assumption:** From `internal/tools/roles.go` roleToolbelts map and `internal/strategy/strategy.go` Turn.ResolveToolbelt.

## REQ-012 — Run state persistence

**Priority:** must

Every run creates `.ducklab/runs/<id>/` containing: `state.json` (run metadata, status, verdict), `events.jsonl` (tool calls, deltas), `llm.jsonl` (full provider payloads), `transcript.md` (conversation record), `diff.txt`, `tests.txt` (gate artifacts). Runs survive engine restarts and are rehydrated on startup.

**Assumption:** From README.md §99-120, status.md AC-11, and `internal/runlog/` structure.

## REQ-013 — Autonomy levels

**Priority:** must

Four autonomy modes controlling write guard behavior: manual (every write pauses for approval), guarded (test file writes auto-approve, source requires approval), auto (all writes auto-approve), yolo (write guard disabled). Default: guarded.

**Assumption:** From `internal/config/config.go` Autonomy type and ValidAutonomies, and README.md reference.

## REQ-014 — Skills system

**Priority:** must

Skills are directories under `.ducklab/skills/` containing `SKILL.md` (YAML frontmatter + body). Two forms: documentation-only (no `entry`), and executable (with `entry` script). Project skills shadow global. Pending skills (written by unaccepted run) cannot be executed or loaded until committed, but reviewing them as diffs is permitted.

**Assumption:** From `internal/skill/skill.go`, status.md AC-52,53,55,57, and README.md §174-188.

## REQ-015 — Shell execution policy

**Priority:** must

Shell commands run under three policies: deny (no execution), guarded (deny-list + allow-prefix matching), allow (runs anything). Network access separate flag. Timeout enforced. Output truncated at configured byte limit.

**Assumption:** From `internal/config/config.go` shell configuration and README.md §159-173.

## REQ-016 — Benchmarking suites

**Priority:** must

The system runs versioned task suites measuring (task, duckling, mode) cells. Results are structurally reproducible JSON with: task ID, duckling, mode, verdict, tokens (marked if estimated), cost, wallclock, per-cell error tracking. Comparison requires matching suite versions.

**Assumption:** From `internal/bench/bench.go`, status.md AC-60, and README.md §27-30.

## REQ-017 — Eight-state bug lifecycle

**Priority:** must

Bug tracker with eight states: open → triaged → in_progress → fixed → verified → closed (plus duplicate, wontfix branches). State transitions are checked; nothing returns to open. Fixed bugs require gate re-verification before closing.

**Assumption:** From `internal/bug/bug.go` transitions table and status.md AC-45-47.

## REQ-018 — Review stage with structured findings

**Priority:** must

Review stage executes after task acceptance, producing `.ducklab/docs/reviews/<task>.md` with structured findings (severity, file, line, issue, fix). Solo mode uses one reviewer; council mode adds critics and an optional human turn. One pass only (MaxRounds: 1).

**Assumption:** From `internal/review/review.go` Record structure, `internal/strategy/strategy.go` ReviewScript, and status.md AC-48.

## REQ-019 — Release note generation

**Priority:** must

`release plan --bump <major|minor|patch>` collects accepted tasks since last tag, groups by milestone, counts unverified work. Version parsing and comparison follow semver strictly.

**Assumption:** From `internal/release/release.go` and status.md AC-58.

## REQ-020 — Test-first mode

**Priority:** must

Dedicated mode where models write failing tests first. Success criterion inverted: gate must be red (Until: `gate == "red"`). Write guard restricts to test paths only. One round maximum. Subsequent implementation run is separate, by different duckling.

**Assumption:** From `internal/service/testfirst.go` TestStart and `internal/strategy/strategy.go` TestFirstScript.

## REQ-021 — MCP server interface

**Priority:** must

`ducklab mcp serve` exposes the engine as an MCP server over stdio. External models read results, approve gates (with recorded reason), answer questions, start work. Decisions attributed as `mcp:<client>`, never `human`.

**Assumption:** From `internal/cli/mcp.go` mcpCmd, `internal/mcp/mcp.go` Server, and README.md §190-197.

## REQ-022 — Contract-based agent protocol

**Priority:** must

Models respond in six contract formats: verdict (structured findings with severity), choice (labeled options with reason), triage (bug classification), sections (markdown sections matching artifact format), freeform (unstructured text), edits (implied by work done). Repair attempts (default 2) for parse failures before contract error.

**Assumption:** From `internal/agent/contract.go` ParseContract and status.md AC-21.

## REQ-023 — Streaming with backpressure

**Priority:** must

Token deltas stream via SSE. Event bus with bounded channels (default 256). Slow subscribers receive `overflow` event and disconnect; runs are unaffected.

**Assumption:** From `internal/bus/bus.go` Subscriber.send and status.md AC-26.

## REQ-024 — Two-tier configuration management

**Priority:** must

Global config (`~/.config/ducklab/config.toml`) and per-project (`.ducklab/project.toml`). Strict TOML parsing rejects unknown keys. Schema versioning in frontmatter. Hand edits preserved on writes for known keys.

**Assumption:** From `internal/config/config.go` and README.md §122-143.

## REQ-025 — Concurrent run queueing

**Priority:** must

`max_concurrent_runs` limit enforces queue. Runs exceeding limit enter queued state, execute when slots free.

**Assumption:** From status.md AC-25 and `internal/service/queue.go`.

## REQ-026 — Desktop app asset bundling

**Priority:** must

Desktop embeds React frontend built with Vite. Frontend communicates with engine via injected connection details (`window.ducklab`). No backend logic in desktop binary—all capability in engine.

**Assumption:** From `cmd/ducklab-desktop/main.go`, README.md §79-85, and status.md AC-27-29.

## REQ-027 — Cross-platform builds

**Priority:** must

`make cross` verifies that the CLI and engine compile for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with CGO_ENABLED=0; it produces no binaries. The desktop requires cgo for Wails and builds natively via `make desktop` only. Producing distributable binaries per platform is future packaging work, not a present capability.

**Assumption:** From README.md §34-77 and status.md AC-15.

## REQ-028 — Project registry

**Priority:** must

SQLite database (`.ducklab/ducklab.db`) tracks projects, requirements, bugs, triage findings. Migrations numbered and applied transactionally.

**Assumption:** From README.md mentioning ducklab.db and `internal/store/` package.

## REQ-029 — Adoption mode for existing codebases

**Priority:** must

Intake stage supports `--adopt` flag that surveys existing code and writes requirements for what the code ALREADY satisfies, not an interview about future work. Requirements carry Priority: must for implemented capabilities or Priority: wont for documented exclusions.

**Assumption:** From `internal/stage/stage.go` BuildPrompt() adopt branch at lines 217-237, and `internal/cli/cycle.go` case "--adopt".

## REQ-030 — OpenAI-compatible provider abstraction

**Priority:** must

The system supports multiple LLM providers through a unified OpenAI-compatible interface. API keys read from environment variables named in config (never stored in config files). Base URL configuration allows hosted and local models.

**Assumption:** From `internal/provider/openaicompat.go` and README.md §122-143.

## REQ-031 — Budget enforcement for USD and turns

**Priority:** must

Budget caps enforce limits on USD cost and turn count. Runs stop when limits are exceeded.

**Assumption:** From `internal/budget/budget.go` Exceeded() checking USD and turns, and status.md AC-12 marking only those two as verified.

## REQ-032 — Bounded script execution

**Priority:** must

Scripts enforce round limits, turn limits per role, tool output size limits, shell command timeouts, and skill execution timeouts. Nothing runs unbounded.

**Assumption:** From `internal/strategy/strategy.go` Script.Validate requiring MaxRounds > 0 and MaxTurns > 0, and README.md §168 "Nothing is unbounded."

## REQ-033 — Isolated contestant workspaces

**Priority:** must

Tournament and split modes create isolated git worktrees for each contestant/subtask via WorkspaceFactory. Workspaces are removed on completion and abort to prevent git state corruption.

**Assumption:** From `internal/strategy/tournament.go` TournamentParams.NewWorkspace and `internal/strategy/split.go` SplitParams.NewWorkspace, and status.md AC-19.

## REQ-034 — Loopback-only binding

**Priority:** wont

Engine binds 127.0.0.1 exclusively. No remote mode. Bearer token rotated on each start. This is load-bearing security, not a preference.

**Assumption:** From README.md §172 "The engine is loopback-only. There is no remote mode." and status.md AC-4.

## REQ-035 — No model decides verdicts

**Priority:** wont

Models never interpret gate output or decide pass/fail. Exit codes are the only signal. This is an architectural invariant, not configurable.

**Assumption:** From README.md §163 "A model never decides a verdict. A gate is a command's exit code." and verify package design.

## REQ-036 — Secrets never persisted

**Priority:** wont

API keys read from environment at call time. No config file, API response, or state file ever contains a credential.

**Assumption:** From README.md §137-138 "--key-env is the **name** of an environment variable, never a key" and §170 "Secrets never touch project state."

## REQ-037 — Plan amendment without redesign

**Priority:** must

The plan stage supports a light amendment path for changes that extend the plan without redesigning it. The architect receives the plan as an outline (ids and titles) plus the spec as a wiring list, and returns only a fragment of new tasks (one to three by design) under a literal placeholder id; the engine merges the fragment by code, assigns real task ids, and places the tasks under their milestone. Amendment turns carry no whole-document contract — the fragment contract in the prompt is the only one that speaks. An amendment that adds nothing fails with the architect's reason; a change that alters what the product IS is refused toward a requirements brief. Tasks no spec section covers wear a spec-debt marker, and a one-click settle run (`spec_settle`) writes a spec revision documenting the delivered ones as built, wiring their `Implements:` upward and surfacing what cannot wire as requirements-debt.

**Assumption:** From `internal/stage/extend.go` runExtend (outline-in, fragment-out, no document contract, spec-debt wiring) and `internal/mcp/tools.go` spec_settle.

## REQ-038 — Fragment-based document updates

**Priority:** must

Updates to an existing artifact (requirements, spec, plan) are fragment-based: the architect receives the document as an outline and returns only the sections it adds or changes; the engine merges the fragment deterministically by code. A section the model does not emit survives exactly as it is, and new sections written under a literal placeholder id (`REQ-900`, `SPEC-900`, `T-900`) receive real ids at merge. The prompt's fragment contract is the only contract on the turn; the whole-document contract is removed so the two never compete. What a model never re-types, a model cannot lose.

**Assumption:** From `internal/stage/fragment.go` runFragment/fragmentPlaceholder and `internal/stage/extend.go` mergeExtension.

## REQ-039 — Section-at-a-time document orchestration

**Priority:** must

A fragment update may be routed through sectioned orchestration for small-context architects: a cheap triage pass names the sections the request touches (existing ids or `NEW:` titles), then each section is updated in its own fresh conversation carrying only the request and that section's full text, while the engine keeps the working memory and merges. Sectioned updates are solo by construction whatever mode was asked, and an update naming more sections than one should touch (cap: 12) is refused as a redesign wearing an update's clothes.

**Assumption:** From `internal/stage/sectioned.go` runSectioned, sectionedPassCap, and soloPass.

## REQ-040 — Context-fit preflight and scaled tool results

**Priority:** must

Each duckling declares its context window. Before a stage spends a token, the engine sizes the opening prompt against every participating seat's declared window: past ~40% it warns that headroom is thin (reseat is one chip-click away), past ~90% it refuses before a token is spent, naming the numbers and the levers; seats with no declared window are skipped. At run time, tool-result caps scale to the acting seat's declared context instead of a flat byte limit, so one tool result cannot consume a small seat's working memory.

**Assumption:** From `internal/service/stages.go` contextFitNotes and `internal/tools/tools.go` SeatContextTokens.

## REQ-041 — Loop rails

**Priority:** must

The harness brakes loops deterministically at tool level, never by asking a model: (a) a consecutive-gate-fail brake — after a run of consecutive red `verify_run` results with no green between, the tool stops running the gate and directs the run to a person or reviewer, with warnings in the final three; (b) an identical-repeat brake — the third identical failing tool call is refused with orders to change something; (c) a per-run cap on model calls per reply, accounted live against the cap, with the last allowed call announced in advance. A call cap can be lifted mid-flight (negative means uncapped); the lift is recorded on the run and a resume re-enters with the ceiling the person chose.

**Assumption:** From `internal/tools/tools.go` ConsecGateFails/lastFailSig/lastFailCount, `internal/tools/exec.go` GateFailLimit, `internal/agent/agent.go` OnCapNear/CapLift, and `internal/runlog/runlog.go` per-run call accounting.

## REQ-042 — Provider resilience and resumable weather

**Priority:** must

Streaming calls are guarded by a stall watchdog — no bytes for a bounded interval declares the stream stalled — not by whole-stream timeouts, so a slow provider is not a dead one. Transient failures (rate limits, 5xx, connection resets, stalls) are retried; exhausted retries classify as provider weather and pause the run resumable rather than failing it. An output-truncated turn on a document stage pauses with the fix named (raise max_tokens or reseat) instead of dying. Document-stage runs persist their request and resume continuing their own life: recorded ceilings, ledger, and seats intact.

**Assumption:** From `internal/provider/openaicompat.go` stall watchdog, `internal/provider/provider.go` transient/weather classification, `internal/agent/agent.go` OnRetry, and `internal/service/stages.go` resumed-stage handling.

## REQ-043 — Declared fallback ducklings and human-decided reseat

**Priority:** must

Each duckling may declare a fallback stand-in for provider weather (never itself). A run paused on provider weather may be reseated onto a fallback only by explicit human decision, never by an automatic router: the reseat swaps every seat the failed duckling held, records a `seat_failover` event (from, to, roles, reason) on the run, persists the swap into a stage run's saved request, and resumes with the ledger intact. Reseat answers availability only; a run paused at a human gate has nothing to reseat.

**Assumption:** From `internal/config/config.go` Fallback, `internal/service/service.go` RunReseat, and `internal/engineapi/routes_table.go` /v1/runs/{id}/reseat.

## REQ-044 — Per-run overrides at launch

**Priority:** must

Any run launch may carry its own overrides of the configured defaults: seat picks (this run's duckling line-up), an agent turn cap applied to every role for this run (negative lifts the cap entirely), and attached screenshots shown to the architect — routed only to vision-capable seats, with a warning when a screenshot meets a seat that cannot see. Overrides are recorded on the run and survive resume.

**Assumption:** From `internal/service/modes.go` per-run line-up and capOverride, `internal/service/service.go` persisted per-run seats, and `internal/stage/extend.go` Images on the architect's turn.

## REQ-045 — Triage-recommended verification strategy

**Priority:** must

The triage contract requires a per-bug verification recommendation: test-first when the bug is reproducible as an automated test (behaviour, crash, wrong data), with a one-line reproduction sketch the test-writer starts from; or build-only when the honest check is eyes (visual, cosmetic, layout, config), with a one-line reason. The triager recommends; a person decides. The task's front door follows the recommendation: a build-only task leads with the build run while test-first stays one click away.

**Assumption:** From `internal/agent/agent.go` triagerPrompt (test_strategy/test_reason), `internal/agent/contract.go` TestStrategy, and `internal/service/next.go` BuildOnly flipping the front door.

## REQ-046 — Subject on taskless runs

**Priority:** must

A run with no task records a Subject naming what it was about — the bug a triage read — where a build would name its task. The subject rides the run record and its listings, so two taskless runs are distinguishable without opening both.

**Assumption:** From `internal/runlog/runlog.go` Subject and `internal/service/bugs.go` triageSubject.

## REQ-047 — Retiring an accepted test-first commit

**Priority:** must

An accepted test-first whose build has not landed may be retired: the engine reverts the test's commit by git, records the retire on the run (revert SHA, test_retired event; the acceptance stays in the record — it happened), and releases the chain's hold on the project. Retirement is refused with its reason when a run for the task is still open, when the build already landed (the test is part of the accepted work), when the run recorded no commit, or when the working tree is dirty.

**Assumption:** From `internal/service/testfirst.go` TestRetire and `internal/runlog/runlog.go` RevertSHA.

## REQ-048 — Signed bug audit trail

**Priority:** must

Every bug status transition is signed and appended to a per-project audit log (`.ducklab/bugs/audit.jsonl`): who moved it (human, mcp:<client>, autopilot, engine), through which door, from and to which states, and when. The log is append-only and best-effort — a line that cannot be written never blocks the move it describes.

**Assumption:** From `internal/service/bugaudit.go` appendBugAudit and `internal/bug/bug.go` AuditEntry.

## REQ-049 — Autopilot

**Priority:** must

An optional per-project autopilot drives the project guide: each time a run settles it asks the guide's computed next steps and acts only when the first step is mechanical — test-first or build — launched through the same service methods and queue as the buttons, with origin "autopilot" on every run it starts. Every other step is a human gate where the autopilot idles. Stop rails are fixed: a per-activation cap on runs started, two consecutive failures switch it off with a recorded reason, a retry carries the failure in hand rather than blind-relaunching, and it never lifts a money cap nor crosses UNVERIFIED. State is in-memory; an engine restart lands the autopilot off.

**Assumption:** From `internal/service/autopilot.go` and `internal/config/config.go` AutopilotMaxTasks/AutopilotMaxFails.

## REQ-050 — Managed application process

**Priority:** must

The engine runs the project's own app as a first-class managed process: `[run]` in project.toml declares the start command, optional URL, preflight environment check, and requirements. Launch runs the preflight first — a failure names what is missing in its own words — then starts the command with output to `.ducklab/app.log`, and tracks pid, health, exit error, and log tail. Stop kills the whole process group. Launch and Stop sit beside the project on every client.

**Assumption:** From `internal/service/app.go` AppStart/AppStatus and `internal/config/config.go` RunApp.

## REQ-051 — Project guide: computed next steps

**Priority:** must

The engine answers "what do I do now?" per project: a computed, ordered list of next steps derived from live state — documents awaiting acceptance, buildable tasks, spec-debt to settle, bugs to triage, releases to cut — each with its reason and the surface it lands on. The guide is the single brain behind the desktop's guide rail, the autopilot, and the MCP operator's status; clients render it, none re-derive it.

**Assumption:** From `internal/service/guide.go` NextStep/projectSnapshot, `internal/service/next.go` ProjectNext, and `internal/mcp/mcp.go` next_steps.

## REQ-052 — Spend reports per mode and per duckling

**Priority:** must

The engine aggregates run history into spend reports groupable by mode, duckling, role, or task, over an optional since window: runs, verdicts, tokens, cost attributed to the ducklings that made the calls (never multiplied across seats), and wallclock, rendered for CLI and desktop. Modes are compared against the solo baseline.

**Assumption:** From `internal/report/report.go` Build/Row and `internal/service/spend.go` backfillSpend.

## REQ-053 — Advisor drafts the answer a paused question waits for

**Priority:** must

When a run pauses on a question, a second model — the advisor — drafts the answer the human should give, asynchronously and without blocking the pause. The recommendation lands on the record as an `advice` event and on the question card; the human still decides (accept the draft, edit it, or answer from scratch). No advice is a degraded card, not a failure. The advisor is decisive by design: it picks one recommendation, cites the project's own spec when that decides the matter, and replies as the answer itself.

**Assumption:** From `internal/service/advisor.go` adviseQuestion/advisorSystemPrompt.

## REQ-054 — Consultation runs (chat)

**Priority:** must

A conversation with a chosen duckling about a subject — a bug whose fix landed and did not fix it, a task that went sideways — is itself a run (stage "chat"): it gets the record, the spend tracking, the live stream, and the transcript for free. The consultant reads and advises with a read-only investigation toolbelt (code, history, records) plus one loop-side act, `bug_file`, and only on the person's explicit word; it does not touch the tree. Its closing duty is to suggest actions from the person's own menu, which the person executes with the buttons that already exist.

**Assumption:** From `internal/service/chat.go` chatToolbelt and ChatStartRequest.

## REQ-055 — Outbound webhook notifications

**Priority:** must

The engine announces run-settled moments to one configured webhook URL: `human_needed`, `run_end`, and `autopilot` stops, signed GitHub-style with the configured secret. Delivery is best-effort by construction — a five-second timeout, one retry, and failures dropped silently — because a dead receiver must never block or slow a run; the record on disk is the source of truth and the webhook is a doorbell.

**Assumption:** From `internal/service/notify.go` startNotifier.

## REQ-056 — A ranged write between patching and rewriting

**Priority:** must

The implementer can replace an exact line range of an existing file, addressed by the line numbers `fs_read` already shows, without re-typing anchor text and without re-emitting the whole file. The write proves the model read what it replaces (the current content of the first line must be supplied and, on mismatch, the actual line is taught back), and the tool warns that numbers below the edit have shifted. The fs_patch brake's remedy names this path first.

**Assumption:** From `internal/tools/fs.go` FSWriteLines; born of B-059 (T-058's implementers failed fs_patch 28 times on a backtick-dense file and feared whole-file fs_write).

## REQ-057 — The advisor as a positioned turn: the rubber duck

**Priority:** must

In pair mode the advisor takes a turn at one deterministic moment — after the implementer's turn is closed on the record and before the reviewer speaks — and only when the harness measured distress in that turn (brake refusals, a streak of failures of one tool, red gates, or an item the implementer itself reports undelivered); never inferred from prose, never on a merely rough turn, and never at all when no advisor is seated. It reads what the reviewer must not (the implementer's reasoning, its tool trace, its report with notes) and answers `none`, a `note` that sends the implementer back to work at once (bounded to two retries per round) before any reviewer turn is spent, or `stop` — the run pauses with its work in place, the record names the advisor, the reason and the reshuffle for the re-run, and the redo note carries them. The reviewer receives the measured telemetry as data only.

**Assumption:** From `internal/strategy/rubberduck.go`, `execute.go`; supersedes the asynchronous consult of REQ-053 for the in-run case (B-058).

## REQ-058 — The implementer may consult the advisor mid-turn

**Priority:** should

An implementer that knows it is stuck — the same tool failing repeatedly, a gate that stays red after several attempts, a choice it cannot make from the code — can call `ask_advisor` and receive the advisor's reply inline, in the same turn, without pausing the run and without a human. Its role prompt says when to use it; with no advisor seated the tool says so and points at the self-help path.

**Assumption:** From `internal/tools/exec.go` AskAdvisor and `ExecContext.OnAskAdvisor`.

## REQ-059 — The deliverables checklist as the implementer's work contract

**Priority:** must

The task's top-level bullets — the plan's or the promotion's words, numbered — are what the implementer must deliver; how is its own. It closes its turn by reporting each by number (`done | partial | not_done | blocked`, with a note for anything short). Anything not done is a distress signal that summons the advisor with the exact question; the reviewer receives ids and statuses as data and the numbered list as a rubric, never the implementer's notes; an approve over items the implementer itself reported undelivered is recorded as a gap; a missing report is data for the reviewer, not distress, and the parse never fails a turn. The desktop renders the report as a checklist at the end of the implementer's own turn — an unreviewed progress report, never a rail card that would read as the result — and flags the gap on the reviewer's verdict.

**Assumption:** From `internal/strategy/deliverables.go`, `frontend/src/components/DeliverablesCard.tsx`, `ConversationLane.tsx`.

## REQ-060 — Acceptance from a clean checkout borrows the tools of the trade

**Priority:** must

The clean-checkout reproduction of an accepted commit links the live tree's installed dependency trees into the checkout — `node_modules` where the commit carries a `package.json`, `.venv` where it carries a Python marker (`pyproject.toml`, `requirements.txt`, `setup.py`, `setup.cfg`, `pytest.ini`, `tox.ini`, `Pipfile`) — never build products. An auto-accept whose reproduction fails pauses at the human gate wearing the error instead of stranding the run as running.

**Assumption:** From `internal/service/service.go` linkInstalledDeps and the yolo accept path; the declared general form is B-061.
