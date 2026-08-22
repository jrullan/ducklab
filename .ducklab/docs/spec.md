---
kind: spec
version: 4
updated_at: 2026-08-22T01:53:34Z
run_id: r-20260820-205447-wbnh
ducklings: [glm52, k3, qwen38-max, atom-local, luna]
based_on: 869ccc65090fa833
origin: adopted
approved_by: human
---

## SPEC-001 — Three-binary deployment architecture

**Implements:** REQ-001
**As-built:** yes

The system deploys as three binaries built from `cmd/` subdirectories:

- `ducklab-engine`: HTTP daemon on 127.0.0.1 with bearer token authentication, owns all run state and execution
- `ducklab`: Stateless CLI that forwards commands to engine via HTTP
- `ducklab-desktop`: Stateless Wails v3 GUI that connects to engine

Clients discover engine via `~/.config/ducklab/engine.json` containing address and bearer token. Token rotates on engine start.

## SPEC-002 — Three-stage artifact lifecycle

**Implements:** REQ-002
**As-built:** yes

Three stages write structured Markdown+YAML to `.proposed` files requiring acceptance:

1. **Intake**: `brief.md` → `requirements.md.proposed`
2. **Spec**: `requirements.md` → `spec.md.proposed`
3. **Plan**: `spec.md` → `milestones.md.proposed` + `tasks/*.md.proposed`

Stages enum: `Intake`, `Spec`, `Plan` in `internal/stage/stage.go`. Acceptance promotes to `.ducklab/docs/`. Rejection deletes proposal.

## SPEC-003 — Solo execution mode

**Implements:** REQ-003
**As-built:** yes

Solo mode runs single implementer with full toolbelt (read/write/shell/verify) until gate green or round limit reached.

Script: `SoloScript` in `internal/strategy/strategy.go`. MaxRounds: 3. Configurable per-run. Baseline for measuring other modes.

## SPEC-004 — Pair execution mode

**Implements:** REQ-004
**As-built:** yes

Pair mode alternates implementer and reviewer:

1. Implementer acts with full toolbelt
2. Gate runs
3. If red: return to implementer
4. If green: reviewer sees diff with `OmitRole: implementer` and anonymized authorship
5. Verdict contract: approve/revise with findings
6. If approve: done. If revise: return to implementer

Script: `PairScript` in `internal/strategy/pair.go`. MaxRounds: 3. Reviewer toolbelt: read-only (fs_read, git_diff, artifact_read).

## SPEC-005 — Tournament execution mode

**Implements:** REQ-005
**As-built:** yes

Tournament runs N contestants concurrently in isolated worktrees:

1. Create N worktrees via `WorkspaceFactory`
2. Run contestants in parallel with identical task
3. Await all completions
4. Count green candidates
5. If exactly 1 green: apply byte-identically, no judge
6. If 0 green or >1 green: blind judge chooses from `{contestant IDs, "none"}`
7. Apply chosen candidate if any

Judge toolbelt: fs_read, git_diff. Judge sees diffs with `OmitRole: implementer`. Script: `TournamentScript` and `ExecuteTournament` in `internal/strategy/tournament.go`. Service integration: `internal/service/modes.go` case "tournament".

## SPEC-006 — Split execution mode

**Implements:** REQ-006
**As-built:** yes

Split decomposes task into subtasks with disjoint file ownership:

1. Decomposer writes subtask list with `ownership: [paths]`
2. `ValidateOwnership` checks for overlaps
3. If overlap: retry once with conflict named in prompt
4. If still overlapping: fail deterministically
5. Create worktree per subtask, run in parallel
6. Integration: deterministic file copy from subtask worktrees to main (no model)
7. Gate runs on integrated result
8. If red: pair mode for up to 2 seam rounds

Script: `ExecuteSplit` in `internal/strategy/split.go`. Decomposer toolbelt: read-only. Implementers: full toolbelt scoped to owned paths. Service integration: `internal/service/modes.go` case "split".

## SPEC-007 — Council mode for artifact stages

**Implements:** REQ-007
**As-built:** yes

Council orchestrates multi-model artifact authoring:

1. Architect drafts artifact
2. N critics (different ducklings) each critique independently with `OmitRole: reviewer`
3. Optional human turn (`ask_human` available to architect)
4. Architect revises considering critiques

Script: `CouncilScript` and `ArtifactScript` in `internal/strategy/council.go`. Default for intake/spec/plan stages. Solo available as `--mode solo` alternative.

Critics toolbelt: read-only. Architect: read-only + ask_human. MaxRounds: 2.

## SPEC-008 — Git integration invariants

**Implements:** REQ-008
**As-built:** yes

Git operations exclusively via engine, never models:

- Tree snapshot before run: `internal/vcs/vcs.go` `SnapshotTree()`
- Restore working tree on rejection: `RestoreTree()`
- Models use `git_diff` tool (read-only)

Repository required at engine start. Dirty tree allowed but snapshotted.

**Assumption:** Work branches with configurable prefix and commit-on-acceptance behavior exist in service layer but paths not verified in this review. Snapshot and restore functions confirmed in `internal/vcs/vcs.go`.

## SPEC-009 — Deterministic gate execution

**Implements:** REQ-009
**As-built:** yes

Gate executes command, exit code is verdict:

- Exit 0: green
- Non-zero: red

Gate modes (auto-detected or explicit):
- `tests`: language-specific test command
- `build`: language-specific build
- `lint`: linter execution
- `custom`: user-provided command
- `none`: always green

Auto-detection: `internal/verify/verify.go` `Detect()` checks for go.mod, package.json, pyproject.toml, Cargo.toml, tsconfig.json.

Models never see gate output interpretation, only color.

## SPEC-010 — Traceability graph enforcement

**Implements:** REQ-010
**As-built:** yes

Directed graph: Requirements → Spec → Tasks via `Implements:` YAML field.

Validation in `internal/artifact/trace.go`:
- Orphan requirements: no spec implements them
- Orphan specs: no task implements them
- Dangling implements: references non-existent ID

Acceptance blocked on trace violations. `Priority: wont` requirements exempt (allowed orphans).

## SPEC-011 — Role-based toolbelt restriction

**Implements:** REQ-011
**As-built:** yes

Seven roles with ceiling tool sets defined in `internal/tools/roles.go`:

- **architect**: fs_read, fs_search, artifact_read, git_diff, git_log, ask_human
- **implementer**: all tools (fs_*, shell, verify, bug_*, skill_*)
- **reviewer**: fs_read, git_diff, artifact_read
- **judge**: fs_read, git_diff
- **triager**: fs_search, fs_read, bug_read, git_log
- **scribe**: fs_read, artifact_read
- **human**: no tools

Scripts specify role per turn. `ResolveToolbelt` narrows from ceiling, never widens.

## SPEC-012 — Run state persistence structure

**Implements:** REQ-012
**As-built:** yes
**Covers:** T-049

Every run creates `.ducklab/runs/<id>/`:

- `state.json`: metadata, status, verdict, timestamps
- `events.jsonl`: tool calls, deltas (one JSON object per line)
- `llm.jsonl`: full provider request/response payloads
- `transcript.md`: markdown conversation
- `diff.txt`: git diff after run
- `tests.txt`: gate output

Engine rehydrates runs from disk on startup (`RecoverRuns` in `internal/service/lifecycle.go`, over `internal/runlog/`). Runs survive restarts: a run found still marked `running` or `queued` on startup is moved to `paused` with `pending_kind: engine_restart` (`markEngineRestart`), the state `RunResume` accepts, and its `next` offers `resume` and `abort`.

A *requested* restart (`RequestRestart`, reached via `POST /v1/restart` or `ducklab engine restart`) checkpoints every active run the same way but with attribution: a `restart_request` event names the requester, the checkpoint's pending data carries `requester` and a `deadline` (the service's restart-recovery deadline), the worker is cancelled, and a queued run is withdrawn from the queue so resume is its only path back. The requesting engine arms a one-shot timer at the moment the checkpoints land: if the restart never completes and that engine is still alive past the deadline, `RecoverAbandonedRestarts` records `restart_abandoned {requester}` and resumes each expired checkpoint itself — a stalled restart un-parks its runs instead of stranding them mid-gate. A checkpoint whose worker is still unwinding defers to a rescheduled pass rather than resuming alongside its still-live self.

## SPEC-013 — Four autonomy levels

**Implements:** REQ-013
**As-built:** yes

Write guard behavior controlled by autonomy level:

- **manual**: every write pauses for approval
- **guarded**: test file writes auto-approve, source writes require approval
- **auto**: all writes auto-approve
- **yolo**: write guard disabled entirely

Default: guarded. Test path patterns: `*_test.go`, `test_*.py`, `*.test.ts`, `tests/`, `__tests__/`.

Configuration: `internal/config/config.go` `Autonomy` enum.

## SPEC-014 — Skills system with pending isolation

**Implements:** REQ-014
**As-built:** yes

Skills are directories under `.ducklab/skills/<name>/` containing `SKILL.md` (YAML frontmatter + markdown body).

Two forms:
- **Documentation**: no `entry` field, injected into prompts
- **Executable**: `entry: <script>` path, invoked via `skill_execute` tool

Precedence: project `.ducklab/skills/` shadows global `~/.config/ducklab/skills/`.

Pending skills written by unaccepted run are visible for review (fs_read can see diff) but cannot be executed or loaded for prompts until committed.

Implementation: `internal/skill/skill.go` with `List`, `Load`, `Execute` checking committed status via `internal/vcs/vcs.go` `PathIsCommitted`.

## SPEC-015 — Three-tier shell execution policy

**Implements:** REQ-015
**As-built:** yes

Shell command execution controlled by policy:

- **deny**: no execution
- **guarded**: deny-list (rm, dd, mkfs, etc.) + allow-prefix matching (git, cargo, npm, pytest)
- **allow**: runs anything

Network access: separate boolean flag (`allow_network`).

Enforcement: timeout (default 30s), output truncation (default 100KB). Configuration in `internal/config/config.go`.

Command runs in project root, inherits PATH.

## SPEC-016 — Versioned benchmark suites

**Implements:** REQ-016
**As-built:** yes

Benchmark suite structure:
- `suite.yaml`: version, tasks list
- `tasks/<id>.md`: task definitions
- `results/<timestamp>.json`: run results

Result schema (per cell):
- task ID, duckling, mode
- verdict (green/red/error)
- tokens (int, `estimated: true` if fallback)
- cost USD
- wallclock seconds
- error message if failed

Suite version must match for comparison. Implementation: `internal/bench/bench.go`.

## SPEC-017 — Eight-state bug lifecycle

**Implements:** REQ-017
**As-built:** yes

Bug states with enforced transitions:

```
open → triaged → in_progress → fixed → verified → closed
  ↓                                ↓
duplicate                      wontfix
```

Transition validation in `internal/bug/bug.go` `transitions` table. No path returns to `open`.

The `fixed → verified` transition is human-controlled via explicit command. The system does not automatically verify bugs when gates turn green.

## SPEC-018 — Review stage with structured findings

**Implements:** REQ-018
**As-built:** yes

Review stage executes after task acceptance, produces `.ducklab/docs/reviews/<task-id>.md`:

Findings schema (YAML array):
- `severity`: critical | major | minor | nit
- `file`: path
- `line`: number
- `issue`: description
- `fix`: recommendation

Modes:
- Solo: one reviewer, one pass
- Council: architect + critics + optional human + architect revision

MaxRounds: 1 (no iteration). Script: `ReviewScript` in `internal/strategy/strategy.go`, record structure: `internal/review/review.go`.

## SPEC-019 — Semver release planning

**Implements:** REQ-019
**As-built:** yes

`release plan --bump <major|minor|patch>` operation:

1. Parse last git tag as semver
2. Increment per bump type
3. Collect accepted tasks since last tag
4. Group by milestone
5. Count unverified work (tasks without green run since last edit)
6. Output version, grouped tasks, warnings

Semver parsing: strict, no `v` prefix in comparison. Implementation: `internal/release/release.go`.

## SPEC-020 — Test-first mode with inverted gate

**Implements:** REQ-020
**As-built:** yes

Test-first mode for writing failing tests:

- Success criterion: `Until: gate == "red"`
- Write guard restricted to test paths only (globs: `*_test.*`, `test_*`, `tests/`, `__tests__/`)
- MaxRounds: 1
- Full implementer toolbelt within test path restriction

Subsequent implementation run is separate, different duckling. Script: `TestFirstScript` in `internal/strategy/strategy.go`, entry: `internal/service/testfirst.go`.

## SPEC-021 — MCP server protocol

**Implements:** REQ-021
**As-built:** yes
**Covers:** T-001, T-003, T-004, T-005, T-007, T-014, T-022, T-058, T-073

`ducklab mcp serve` exposes engine over stdio as MCP server:

Tools exposed to MCP clients:
- `read_result`: get run outcome
- `approve_gate`: accept green run with reason
- `answer_question`: respond to ask_human
- `start_work`: initiate run

Attribution: decisions recorded as `mcp:<client-name>`, never `human`. Server implementation: `internal/mcp/mcp.go`, CLI: `internal/cli/mcp.go`.

Roster `get` responses include each seat's non-binding `candidates` (up to three duckling ids and concise `why` evidence), computed by the engine's one ranking rule (`service.RankCandidates`); clients never re-rank. The rule orders the flock lexicographically by a per-role list of criteria from a named catalog (`coding_index`, `intelligence_index`, `agentic_index`, `pass_rate` in that seat, `pass_rate_overall`, `cost_per_run`, `wallclock`, `bench`, `input_cost`, `output_cost`, `context`; direction fixed per criterion), unknown values after known ones at every level; a duckling whose declared `roles` exclude the seat is never suggested; local models are not compared on price. The engine ships a default list per role (implementer/architect: coding index → pass rate in seat → cost per run; reviewer/judge: pass rate in seat → intelligence index → cost per run; advisor: cost per run → wallclock → agentic index; triager/scribe/human unranked) and `[defaults.candidate_criteria]` overrides it per role — an empty list turns a seat's suggestions off. `GET/PUT /v1/defaults/candidates` expose the effective lists, the defaults, which roles are overridden, and the catalog; the desktop Roster edits them in place ("how seats are suggested"). Scorecards carry `measured_by_role` (the same evidence split by seat held) so the in-seat pass rate is a fact, not an inference.

The engine also exposes `GET /v1/ducklings/scorecards`, returning one configured duckling per row with declared provider/model/cost/capabilities/roles/notes and explicitly sourced measured, latest-per-suite bench, and external-index evidence. An external index is either declared in config or, for ducklings on the OpenRouter provider with no declaration, fetched from OpenRouter's `GET /benchmarks?source=artificial-analysis` (coding, intelligence and agentic indices) under the provider's own key; the fetch is cached on disk for a day and refreshed in the background, matched to the model by permaslug (newest dated release wins), and carries `source` ("artificial-analysis via openrouter (<permaslug>)") and `as_of`. A declared index is never overwritten by a fetch; a local duckling never borrows one. Missing evidence is null/absent.

The `roster` MCP tool accepts `action` (`get | set | unpin`), `scope` (`global | project`), `project_id`, `mode`, `role`, and ordered `ducklings`. `get` returns every board-addressable seat with `role`, ordered `ducklings`, effective `duckling`, and `source` provenance (`global mode seat`, `project pin`, or `global role fallback`); project pins also return the overridden global `default`. Project `set` replaces the complete ordered pin and `unpin` removes it to restore Global inheritance. Global `set` writes mode seats (or mode-independent triager/scribe role pins) and never changes project files. Invalid action/scope, role, duckling, and mode cardinality return field-named actionable errors with `next` guidance. MCP dispatch delegates to the canonical service roster resolver and records operator actions as `mcp:<client-name>`.

The wider operator surface (`internal/mcp/tools.go`):

- `status` answers per project: waiting and active runs, `next_steps` (the guide's own steps, present on every project even when empty), a `documents` map with each lifecycle document's state (`none | draft | proposed | approved` — never a body), task and open-bug counts, `accepted_unreleased`, `unreleased_branches` (the integer count from the service status, matching the OpenAPI contract) and `unreleased_branch_names` (the additive list of branch identities).
- `budget_lift {run_id, kind}` removes one cap (`tokens | usd | turns | wallclock | calls`) from a live or paused run, one-way and attributed to the operator; an unknown kind fails naming the `kind` field and its legal values.
- `run_get` carries the run's `findings` (the reviewer's structured findings) and `redo_note` beside verdict, budget, diff and `next`; `file_findings {run_id}` files a finished run's reviewer findings as attributed bugs, the desktop button's exact equivalent.
- `task_list` rows are compact (id, status, title, `blocked_reason` when blocked, `next`) behind a status summary; bodies are omitted.
- `task_remove {project_id, task_id}` retires an unstarted superseded task; the engine refuses tasks with accepted or open runs. The CLI mirrors it as `ducklab task remove <id>` over the same DELETE route, printing the engine's refusal verbatim, and the plan_extend prompt names the retire path itself: "this amendment cannot remove tasks. Retire superseded tasks separately with task_remove."
- Launchers take per-run overrides: `stage_start`, `bug_triage`, `test_build`, `test_only` and `run_start` accept `ducklings`, `mode` and `agent_turns` (`run_start` also `note`, `verify`, `redo`); `test_build`/`test_only` thread the same overrides into the chained build request.
- `decide` replays an amendment's own persisted request on `request_changes` (see SPEC-038).

## SPEC-022 — Six contract formats

**Implements:** REQ-022
**As-built:** yes

Models respond in structured formats parsed by `internal/agent/contract.go`:

1. **verdict**: findings array (severity, file, line, issue, fix) + reasoning
2. **choice**: options array (label, reason) + selected label
3. **triage**: bug classification (state, severity, labels, reasoning)
4. **sections**: markdown H2 sections matching artifact schema
5. **freeform**: unstructured text (fallback)
6. **edits**: implied by tool use (fs_write, etc.)

Parse failures trigger repair: LLM shown error, allowed 2 retry attempts (configurable). After exhaustion: contract error, run fails.

## SPEC-023 — SSE streaming with overflow handling

**Implements:** REQ-023
**As-built:** yes

Token deltas stream via Server-Sent Events:

- Event bus: `internal/bus/bus.go`
- Subscriber channels: bounded (default 256)
- Slow subscriber handling: send `overflow` event, disconnect subscriber
- Run continues unaffected by subscriber state

Events: `token`, `tool_call`, `tool_result`, `status`, `overflow`.

## SPEC-024 — Two-tier configuration with strict parsing

**As-built canonical roster:** Global defaults persist `defaults.mode_seats` keyed by mode and real role name, with ordered duckling IDs; `defaults.role_pins` carries mode-independent triager and scribe pins. Legacy `mode_ducklings` is migrated one-way and idempotently on load, then removed. Resolution precedence is per-run pick, project role pin, global mode seat, and global role fallback, with provenance reported as `project pin`, `global mode seat`, or `global role fallback`. Writes and launches reject council without a critic, split with fewer than two workers, and tournament with fewer than two contestants.

**Implements:** REQ-024
**As-built:** yes

Configuration sources (precedence order):
1. Per-project: `.ducklab/project.toml`
2. Global: `~/.config/ducklab/config.toml`

Strict TOML parsing rejects unknown keys. Schema version in frontmatter. Hand edits preserved for known keys on write-back.

Structure: `internal/config/config.go`. CLI overrides via flags (not persisted). Each `[duckling.<id>.index]` may declare `coding_score`, `source`, and `as_of` (YYYY-MM-DD); it is round-tripped with provenance and never inferred. When absent for an OpenRouter duckling, the scorecard fills it from OpenRouter's benchmarks endpoint (see the scorecards route) — a fetched index is a scorecard fact, not a config write.

Canonical global seats are persisted as `defaults.mode_seats` (mode → real role name → ordered duckling IDs), with mode-independent `role_pins` for triager and scribe. Legacy `mode_ducklings` is migrated once, written back, and removed. Resolution precedence is per-run pick, project pin, global mode seat, then global role fallback; provenance is reported as `project pin`, `global mode seat`, or `global role fallback`. Council requires at least one critic, split at least two workers, and tournament at least two contestants.

**As-built, project scope (2026-08-18):** project pins share the global shape. `project.toml` carries `[mode_seats.<mode>]` (mode → real role → ordered ids) for pins made on a mode's board column, and keeps `[roster]` / `roster_seats` as ROLE pins — mode-independent (triager, scribe) and the project's own fallback for a mode that pins nobody. Precedence per seat, with the provenance recorded: request → `project mode seat` → `project pin` → `global mode seat` → `global mode seat (council)` for the architect of a document stage run outside council → `global role fallback` → `unseated`. A seat nobody filled stays EMPTY once anything is configured anywhere: optional roles (the advisor) are never invented, and a launch with an empty required seat (implementer for solo/pair, reviewer for pair, architect+reviewer for council, architect+implementer+reviewer for split, implementer+judge for tournament, triager for triage, scribe for release) is refused naming the seat and the door ("assign one on the Roster board, or pass ducklings on the launch"). Only a blank installation — no seat configured anywhere — lets the engine pick a duckling per role, recorded as `engine picked (no seats configured)`, and even then never an advisor. `RosterSetManyMode(project, mode, role, ids)` writes the mode seat when a mode is given and the role pin when it is not; `RosterUnpin` removes the mode seat, or the role pin when that is the only pin behind the card. Runs resolve with the run's mode (they used to resolve with mode "" and never saw the per-mode seats — B-063).

## SPEC-025 — Runtime concurrent run queue

**Implements:** REQ-025
**As-built:** yes

`max_concurrent_runs` configuration enforces in-memory queue:

- Runs exceeding limit enter `queued` state
- Queue drains as running slots free
- On shutdown, queued runs are marked `paused`

Implementation: `internal/service/queue.go` with semaphore. Status exposed via `GET /api/queue`.

**Assumption:** Queue does not persist order or maintain FIFO guarantee across engine restarts. Queued runs are drained and paused on shutdown.

## SPEC-008 — Desktop Roster view

**As-built:** yes

The desktop provides a read-only Roster view beside Settings. It lists the complete Flock and known local/remote provider and per-run cost information, then renders effective Global or Project assignments for Council, Solo, Pair, Split, Tournament, and Common. Project inherited seats are muted dashed ghosts labelled `global`; project pins are solid, labelled `pinned`, and expose the overridden global assignment on hover. The view has no assignment controls.

## SPEC-026 — Editable roster board

Desktop Settings links to the Roster board instead of editing positional mode seats. Task, TDD, and stage launchers prefill seat chips from the canonical resolver — never from a saved settings line-up, so a launch without a touch sends no phantom per-run override — and show `project` or `global` provenance; changing a chip shows `picked now`, is sent only with that run request, and never writes Global or Project roster settings. Settings exposes every roster pin the engine honors, labelling the overridden global default, so a hand-edited `project.toml` pin cannot silently outrank what the page shows.

**Implements:** REQ-024
**As-built:** yes
**Covers:** T-039, T-052, T-053

The desktop Roster board displays the effective Global or Project roster and
supports equivalent HTML5 drag/drop and keyboard flows. Flock cards carry their
IDs in `dataTransfer`; selecting a seat with Enter or Space exposes accessible
`assign <duckling> to <role>` buttons. Seat cards can be removed, ordered
multi-slot seats append assignments in display order, and project pins can be
un pinned to restore the inherited ghost presentation.

Global edits use only the canonical global mode-seat/role-pin mutation. Project
edits use only the project ordered-list mutation and never alter Global. Every
successful mutation re-reads the roster so displayed provenance remains engine
truth. Engine validation errors and warnings are rendered beside the boards;
the pair board also reports implementer/reviewer overlap from effective seats.

Pinned by `roster_assign.test.tsx` and `roster.test.tsx`.

When a seat is selected, the board reorders eligible Flock cards and labels up to three informational candidates with their evidence reason. Selection never writes an assignment; local roles retain the operator's ordering. The board consumes the same `candidates` ids and `why` text exposed by the MCP roster view.

Seat provenance survives onto the run record: every run persists `roster_sources` — per seated role, `roster` (the configured resolution, project pin outranking global) or `request` (a per-run pick) — beside the roster itself, so the record proves not just WHO sat each seat but WHY, and no assistant is needed to learn it.

**Assumption:** the Settings page's coverage of EVERY engine-honored pin (T-052) is pinned by the accepted fix; the frontend breadth was not re-read line-by-line.

## SPEC-027 — CLI and engine cross-platform compile check

**Implements:** REQ-027
**As-built:** yes

The `make cross` target verifies that the CLI and engine compile for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with `CGO_ENABLED=0`. It compiles to the null device and produces no binaries: it is a regression guard for portability, not a packaging step.

The desktop (`ducklab-desktop`) requires cgo for Wails and builds natively via `make desktop` only. Producing distributable binaries per platform is future packaging work (docs/operability-plan.md §1c) and out of this section's scope.

## SPEC-028 — SQLite state database

**Implements:** REQ-028
**As-built:** yes

`.ducklab/ducklab.db` SQLite database schema:

Tables:
- `projects`: project metadata
- `requirements`: requirement artifacts
- `bugs`: bug tracker records
- `triage_findings`: triage stage output

Migrations: `internal/store/migrations/` numbered sequentially, applied transactionally with version tracking.

## SPEC-029 — Adoption mode for existing codebases

**Implements:** REQ-029
**As-built:** yes

Intake stage `--adopt` flag changes prompt:

- Standard: interview about future work
- Adopt: survey existing code, write requirements for what IS (not what could be)

Requirements from adopt mode:
- Implemented capabilities: `Priority: must`
- Documented exclusions: `Priority: wont`

Prompt branch: `internal/stage/stage.go` `BuildPrompt()` lines 217-237. CLI: `internal/cli/cycle.go` recognizes `--adopt`.

## SPEC-030 — OpenAI-compatible provider abstraction

**Implements:** REQ-030
**As-built:** yes

LLM providers configured via unified interface:

```toml
[duckling.example]
model = "gpt-4"
base_url = "https://api.openai.com/v1"
key_env = "OPENAI_API_KEY"
```

`key_env` names environment variable (value read at call time, never stored). `base_url` allows hosted or local models (Ollama, vLLM, etc.).

Implementation: `internal/provider/openaicompat.go` with streaming and non-streaming support.

## SPEC-031 — Budget caps, pause, and per-cap lift

**Implements:** REQ-031
**As-built:** yes
**Covers:** T-008, T-010

`internal/budget/budget.go` enforces four caps — `MaxUSD`, `MaxTokens`, `MaxWallclockS`, `MaxTurns` — with `Exceeded()` returning the breached cap's own message; the run pauses resumable (`pending_kind: budget`) rather than dying, because resume without a lift would re-pause at once.

Resolution order: request limits over `[budget]` in `project.toml` over global defaults, one field at a time (`projectBudget` / `mergeBudget` in `internal/service/defaults.go`, `>0` wins) — every named cap, `max_tokens` included, reaches the run it was set for.

`RunBudgetLift` removes ONE named cap (`tokens | usd | turns | wallclock | calls`) from a live or paused run: one-way, per-cap, recorded as a `budget_lifted` event with attribution; an invalid kind names the field. The `calls` lift feeds the per-reply call cap (SPEC-042).

Chat is the deliberate exception: its limits are built with `MaxWallclockS = 0` and re-zeroed AFTER the project merge (`internal/service/chat.go`), because the wallclock clock measures the person's thinking time between messages, not model work — a project's `max_wallclock_s` must never cap a conversation.

**Assumption:** REQ-031's wording ("USD and turns") predates the token and wallclock caps; the requirement has not caught up with the four-cap budget the code enforces.

## SPEC-032 — Bounded execution guarantees

**Implements:** REQ-032
**As-built:** yes

Nothing runs unbounded:

- **Rounds**: `MaxRounds` required >0, script validation fails otherwise
- **Turns per role**: `MaxTurns` required >0
- **Tool output**: truncated at configured byte limit (default varies by tool)
- **Shell timeout**: default 30s, configurable
- **Skill timeout**: default 60s, configurable

Validation: `internal/strategy/strategy.go` `Script.Validate()`. Enforcement distributed across tool implementations.

## SPEC-033 — Isolated worktree management

**Implements:** REQ-033
**As-built:** yes
**Covers:** T-074

Tournament and split modes use `WorkspaceFactory`:

```go
type WorkspaceFactory func(ctx context.Context, label string) (Workspace, error)
```

Workspace lifecycle:
1. `NewWorkspace(id)` creates git worktree in `.ducklab/worktrees/<id>/`
2. Run executes in isolated workspace
3. `Close()` on completion removes worktree
4. `Abort()` on failure removes worktree + cleans git state

Prevents main worktree corruption. Implementation: `internal/strategy/tournament.go` and `internal/strategy/split.go` use `internal/strategy/workspace.go`.

Git itself does not serialize concurrent `worktree add`/`remove`/`prune` against a repository's `.git/worktrees` metadata, so `internal/vcs` does: `worktreeLocks` holds one mutex per canonical repository path (absolute, symlink-resolved) and `WorktreeAdd`, `WorktreeAddDetached` and `WorktreeRemove` all take it. Concurrent contestant workspaces in one repository queue on the lock instead of racing the metadata.

## SPEC-034 — Loopback-only security model

**Implements:** REQ-034
**Priority:** wont
**As-built:** yes

Engine binds exclusively to 127.0.0.1:

- No remote mode (architectural invariant)
- Bearer token rotates on every engine start
- Token stored in `~/.config/ducklab/engine.json` (mode 0600)
- All clients read token from file

This is load-bearing security, not configurable. Code: `internal/service/service.go` HTTP server initialization.

## SPEC-035 — Exit-code-only verdicts

**Implements:** REQ-035
**Priority:** wont
**As-built:** yes

Gate verdict determination:

- Command runs via `exec.Command`
- Exit code 0: green
- Exit code non-zero: red
- No LLM involvement in verdict interpretation

Models receive only color (`green`/`red`), never raw output. Output logged to `tests.txt` for human review only.

Architectural invariant enforced by `internal/verify/verify.go` design.

## SPEC-036 — Zero credential persistence

**Implements:** REQ-036
**Priority:** wont
**As-built:** yes

Credentials never persisted:

- API keys: read from environment at call time via `os.Getenv(config.KeyEnv)`
- Config stores `key_env` (variable name), never key value
- LLM logs redact authorization headers
- State files contain no secrets

Enforcement: `internal/provider/` reads env on demand, `internal/runlog/` never logs auth material.

## SPEC-037 — Provider resilience and resumable weather

**Implements:** REQ-042
**As-built:** yes

Provider failure is weather, not a verdict.

- Streaming calls have no total-request timeout; a stall watchdog bounds silence within a stream (default 120s: first byte and every inter-chunk gap); non-streaming calls carry their own 300s deadline
- Transient errors (rate limits, 5xx, connection resets, stalls) retry with visible backoff (3 attempts, 500ms doubling to 10s; `OnRetry` lands each failure on the record as it happens); exhaustion wraps `ErrProviderUnavailable` and pauses the run resumable (`pending_kind: provider`) instead of failing it
- Truncated replies (`finish_reason=length`) retry once with a nudge; a document-stage turn truncated twice fails with `ErrTruncated` naming the fix (raise max_tokens or use a higher-cap duckling)
- Thought-only replies (tokens spent, no content) retry up to 3 times with distinct advice for budget-exhausted vs stochastic-empty
- Every failed call is written to llm.jsonl, including the one that killed the run; usage is recorded per attempt
- Stage runs persist their request and resume with recorded ceilings, ledger, and seats intact

`internal/provider/openaicompat.go`, `internal/provider/provider.go`, `internal/provider/provider_test.go`, `internal/provider/stall_test.go`, `internal/agent/agent.go`, `internal/agent/truncation_test.go`, `internal/agent/repair_test.go`.

## SPEC-038 — Plan amendment without redesign

**Implements:** REQ-037
**As-built:** yes
**Covers:** T-030, T-072

The plan stage carries a light amendment path beside full redrafts: `runExtend` in `internal/stage/extend.go` sends the architect the plan as an outline (ids and titles only) plus the spec as a wiring list, and accepts back only a fragment of one to three new task sections written under the literal id `T-900`. The engine merges by code — `mergeExtension` assigns the next free `T-###` id and places each task under the milestone its `**Milestone:**` field named (or the last one), while a bare `M-` heading in the fragment is read as a placement declaration, resolved to an existing milestone or created with a real id. Amendment turns blank out the whole-document contract on every script turn (`script.Turns[i].Contract = ""`) so the fragment contract in the prompt is the only one that speaks. An amendment whose reply parses to zero work items fails with the architect's own reason (`"the architect added no tasks: …"`); a change altering what the product IS is refused in-prompt toward a requirements brief.

`request_changes` on an amendment proposal does NOT relaunch the plan stage: the decide path replays the amendment's own persisted stage request — its extend ref, its change, its solo mode and one-round limit — with the operator's note as `revise`, so the architect revises the fragment it wrote, against the tasks it wrote (T-061/T-062 visible), never a fresh council against the approved store.

Every accepted task no spec section covers wears spec-debt, whatever its origin: `taskSpecDebt` in `internal/service/stages.go` marks amendment-born and bug-promotion-born tasks alike (bug provenance explains why a task exists; it does not document the behavior), so a board of promoted fixes reports its debt instead of reading zero. The one-click `spec_settle` tool in `internal/mcp/tools.go` plus the guide step in `internal/service/guide.go` close that debt: the engine assembles the prompt from the debt itself, a specification revision documents delivered tasks as built, `Covers:` fields wire their `Implements:` upward, and the markers come off. Settle refuses honestly: "no task wears spec-debt" when the board is clean, "N task(s) wear spec-debt but none are built yet" when nothing is settleable.

**Assumption:** REQ-037's settle half delegates the wiring to the normal fragment/spec update machinery; `spec_settle` launches that update rather than a bespoke writer.

## SPEC-039 — Fragment-based document updates

**Implements:** REQ-038
**As-built:** yes

Every update to an existing requirements, spec, or plan document is a fragment, not a redraft: `runFragment` in `internal/stage/fragment.go` presents the document as an outline plus the request (the architect's toolbelt carries `artifact_read` for full text), and the rules demand only sections it adds or changes. Merge is deterministic in `mergeFragment` / `mergePlanFragment`: a section emitted under an existing id replaces that section in place (plan task edits keep id and milestone position; a milestone edit keeps its children), and a section under the placeholder id returned by `fragmentPlaceholder` — `REQ-900`, `SPEC-900`, `T-900` — appends with the next free id via `NextFree`. What the model never re-types is copied by code, which cannot truncate. The whole-document contract is stripped from every turn before execution, so the prompt's fragment contract is the only law; a council that stands pat falls back through the earlier drafts, and a reply with no usable sections fails with the architect's clipped reason. Survey origin front-matter survives every update (`proposed.Front.Origin = base.Front.Origin`).

## SPEC-040 — Section-at-a-time document orchestration

**Implements:** REQ-039
**As-built:** yes

`runSectioned` in `internal/stage/sectioned.go` routes a fragment update through many small conversations for small-context architects. Pass 0 is triage: the outline in, a list out — existing ids or `NEW: <title>` lines — parsed by `parseTriagePass` against actually-existing sections. Cap `sectionedPassCap = 12`: a triage naming more is refused as "a redesign wearing an update's clothes". Each named section then gets one fresh solo conversation (`soloPass`, no document contract, per-pass turn-index offset) carrying only the request and that section's full text; `UNCHANGED` leaves the section alone, an unusable reply never touches the document, and the replacement's id is forced back to the section's own — "the id is not the model's to change". New sections are drafted in their own passes and appended with real ids (plan items ride `mergeExtension` for milestone placement). Coverage-gap and plan-gap hints computed by the traceability spine ride the triage and update prompts, so a small triage cannot under-select. Every pass is solo regardless of the mode requested.

## SPEC-041 — Context-fit preflight and scaled tool results

**Implements:** REQ-040
**As-built:** yes

Each duckling declares `Caps.ContextTokens` (config, `internal/config/config.go`). Before a stage spends a token, `contextFitNotes` in `internal/service/stages.go` sizes the opening prompt (chars/4 ≈ tokens) against every participating seat: at ≥90% of a declared window the stage refuses — `"the opening prompt is ~Nk tokens — P% of <seat>'s declared context"` naming the reseat chip and trimming lever; at ≥40% it warns that headroom is thin; seats declaring no window are skipped, a guess being worse than silence. At run time, tool results scale to the acting seat: `runnerFor` in `internal/service/modes.go` stamps `SeatContextTokens` onto each turn's exec context, and `internal/tools/tools.go` sizes the bound via `resultCapFor(SeatContextTokens)` — scaling down for a small declared window while the flat ceiling `MaxToolResultBytes` (32 KiB) still bounds what one tool call can add to any conversation.

## SPEC-042 — Loop rails

**Implements:** REQ-041
**As-built:** yes
**Covers:** T-002, T-025, T-032, T-033, T-034, T-035, T-069, T-070

Loops brake at tool level, never by asking a model:

- **Consecutive-gate-fail brake** — `GateFailLimit = 10` in `internal/tools/exec.go`: a run of red `verify_run` results with no green between is refused with "a person (or the reviewer) can redirect the work", and the final three reds carry warnings. A green gate resets `ConsecGateFails` to zero; the count lives on the run's exec context (`internal/tools/tools.go`).
- **Identical-repeat brake** — the same failing tool call tracked via `lastFailSig`/`lastFailCount` is refused the third time with orders to change something.
- **Per-file fs_patch brake with a probe window** — `FSPatchFailLimit = 5` consecutive fs_patch failures against ONE file (`fsPatchFailStreak`, fuzzy failures counted per path, other files unaffected) refuse with "stop patching. Use fs_read to see current line numbers, then fs_write_lines to replace the exact range (or fs_write for a full rewrite)". A successful `fs_read` of the braked file deletes its streak, restoring a one-probe window; five new failures re-engage the brake. `FSPatchRefusalLimit = 5` further refusals on the same braked file appends "end your reply so the next turn can use the rewrite remedy" — **Assumption:** the turn end is instructed by the refusal, not hard-enforced by the loop.
- **Repetition detector** — `internal/agent/repetition.go` watches the token stream for an n-gram (3–12 words) repeated three consecutive times and fails the turn with a `repetitionError` naming the repeated text; it also runs over the assembled response in the non-streaming `ErrUnsupported` fallback, so a loop is caught on providers that cannot stream. The detector keeps only recent words and a partial-word tail, never an accumulated copy of the reply, so long replies do not grow its memory. A caught loop lands as a `repetition_loop` record, bridged to a `distress` transition (SPEC-055) — token flow is not health, and the stall watchdog stays silent exactly when this brake speaks.
- **Contract-aware output caps** — `outputCapForContract` caps `json:triage` at 2048 tokens (or the duckling's lower declared cap) and leaves `json:decomposition` at the declared cap or `DefaultMaxOutputTokens = 8192`; `applySampling` threads the turn's contract, so contract-repair calls are capped by the same rule as the turn they repair.
- **Per-reply call cap with live lift** — the agent loop (`internal/agent/agent.go`) counts calls per reply against the run's cap: `OnCapNear` announces the last allowed call in advance, and `CapLift` is consulted before every call, so a cap lifted mid-flight takes effect without a restart. Lifting past the cap resolves to `uncappedTurns` (`internal/service/modes.go:231`), the stand-in for "no cap" on the per-reply call loop.

**Assumption:** the "recorded on the run / survive resume" half of REQ-041's lift is provided by `internal/service/modes.go` `capLift`/`onCapNear` wiring and the run's turn-cap resolution (`roleTurnCapsFor`); the lift choice is reflected back into the persisted stage request and re-applied on resume.

## SPEC-043 — Declared fallback ducklings and human-decided reseat

**Implements:** REQ-043
**As-built:** yes

Every duckling may declare `fallback` in config (`internal/config/config.go`), carried on the registered duckling (`internal/duckling/duckling.go`) and surfaced on the fleet view. A run paused on provider weather is reseated only by explicit human call: `RunReseat` in `internal/service/service.go` — exposed as `POST /v1/runs/{id}/reseat` in `internal/engineapi/routes_table.go` and through the desktop/client — swaps every seat role the failed duckling held in the roster to the named replacement, appends a `seat_failover` event (from, to, roles, reason) to the run record, persists the new roster into the stage run's saved request, and resumes with the token budget and ledger intact. There is no automatic router: a run paused at a human gate (question or decision) has nothing to reseat, since availability — not judgment — is the only thing the swap answers.

## SPEC-044 — Per-run overrides at launch

**Implements:** REQ-044
**As-built:** yes
**Covers:** T-048

Any run launch may carry overrides of the configured defaults, recorded on the run and surviving resume:

- **Seat picks** — the run's own duckling line-up overrides the roster for every role; it is persisted on the run record so a resume re-enters with the seats the person chose.
- **Agent turn cap** — `capOverride` in `internal/service/modes.go` resolves one `AgentTurns` value applied to every role for that run (`roleTurnCapsFor` and `internal/service/defaults.go`); negative lifts the cap entirely without touching fleet tuning.
- **Screenshots** — `Images` data URLs ride the run request (`internal/service/stages.go`) and are attached to the architect's (or triager's) own turn; a screenshot aimed at a seat whose declared `vision` cap is false is dropped with a warning event instead of silently vanishing.
- **Mode** — an omitted mode resolves through ONE canonical path (settings phase defaults, then project, then fallback) shared by the desktop launcher, the autopilot and the MCP launchers; the resolution is recorded on the run as `mode_source` (`settings | project | fallback`; an explicit mode records `request`), so the record says not just which mode ran but who chose it.

## SPEC-045 — Triage-recommended verification strategy

**Implements:** REQ-045
**As-built:** yes

The triage contract carries a per-bug verification recommendation: `test_strategy` / `test_reason` in `internal/agent/contract.go` normalize to `test-first` (the bug reproduces as an automated test — behaviour, crash, wrong data) or `build-only` (the honest check is eyes — visual, cosmetic, layout), normalized and persisted on the bug row (store migration `003_test_strategy`). The triager recommends; the person decides. Downstream the recommendation steers the front door: `BuildOnly` on the task view flips the guide (`internal/service/guide.go`) to lead with the build run — "the fix verifies by eyes, not by test; test-first stays one click away" — and `internal/service/stages.go` threads `BuildOnly` into the task lists the clients render, without ever hiding the test-first door.

## SPEC-046 — Subject on taskless runs

**Implements:** REQ-046
**As-built:** yes

A run with no task records a `Subject` naming what it was about (`internal/runlog/runlog.go`): the bug a triage read — `triageSubject` in `internal/service/bugs.go` fills it with the bug id list — occupies the slot where a build would name its task. The subject rides the run record and its listings, so two taskless runs are distinguishable without opening either one.

## SPEC-047 — Retiring an accepted test-first commit

**Implements:** REQ-047
**As-built:** yes

`TestRetire` in `internal/service/testfirst.go` withdraws a committed failing test deterministically — git's own inverse patch, no model. The engine locates the task's latest accepted, unreversed test run and refuses with the verdict first when: any run for the task is open (running/queued/paused), the build already landed (the test is part of the accepted work), no committed test awaits a build, the test run recorded no commit, or the working tree is dirty (sample of offending paths named). On success `git.Revert` produces the revert SHA, written onto the run as `RevertSHA` with an annotated resolution, a `test_retired` event (task, revert SHA, reverted SHA) lands on the record beside the acceptance it does not erase, and the project's queue hold releases (`s.queue.poke`). Routed at `POST /v1/projects/{id}/tasks/{task}/retire-test` in `internal/engineapi/routes_table.go`.

## SPEC-048 — Signed bug audit trail

**Implements:** REQ-048
**As-built:** yes

Every bug status transition is signed and appended to `.ducklab/bugs/audit.jsonl`: `appendBugAudit` in `internal/service/bugaudit.go` writes one `bug.AuditEntry` per move — bug id, who moved it ("human", "mcp:<client>", "autopilot", "engine"), through which door, from and to which states, and a timestamp. Append-only, one JSONL per project, best-effort by construction: a line that cannot be written is dropped without blocking the move it describes — the move is the person's intent, the line is the receipt. `readBugAudit` returns each bug's transitions in chronological order for the clients' history views.

## SPEC-049 — Autopilot

**Implements:** REQ-049
**As-built:** yes
**Covers:** T-038, T-047

An optional per-project autopilot drives the project guide (`internal/service/autopilot.go`): each time a run settles it asks `ProjectNext` and walks the list to the FIRST mechanical step — test-first, build, or triage — launched through the same service methods and queue as the buttons, stamped `origin: "autopilot"` on every run it starts. A human-only step ahead of mechanical work does not head-of-line block it: the gate stays visible as the loop's note ("needs you: …") while the startable task below it launches. Every project with nothing mechanical left is a human gate where the autopilot idles. Stop rails are fixed: a per-activation cap on runs started (default 10, `autopilot_max_tasks`), two consecutive failures switch it off with a recorded reason (default `autopilot_max_fails` = 2), a retry carries the previous failure in hand rather than blind-relaunching, and it never lifts a money cap nor crosses UNVERIFIED. A task whose latest accepted run made no changes is not relaunched automatically — `autopilotNoChangesNeedsHumanNote` parks it until a person adds a note, because a no-changes run is the tree answering the task's question. State is in-memory: an engine restart lands the autopilot off, deliberately.

**Assumption:** decisions the autopilot takes under yolo are attributed to the autopilot on the record rather than `human` — asserted by the accepted fix for B-043 and the audit design (SPEC-048), but the accept-attribution call site was not re-read for this settlement.

## SPEC-050 — Managed application process

**Implements:** REQ-050
**As-built:** yes

The engine runs the project's own app as a first-class managed process (`internal/service/app.go`): `[run]` in `project.toml` (`config.RunApp`) declares the start command, optional URL, preflight environment check, and requirements. Launch runs the preflight first — a failure names what is missing in its own words — then starts the command with stdout/stderr to `.ducklab/app.log`, and tracks pid, start time, exit error, and log tail. Stop kills the whole process group via the cross-platform shell wrapper. Status reports a health field separate from liveness — a process alive and a service answering are different claims — plus the last exit error and log tail for when Launch appears to do nothing.

## SPEC-051 — Project guide: computed next steps

**Implements:** REQ-051
**As-built:** yes
**Covers:** T-036, T-040, T-092

The engine answers "what do I do now?" per project with a computed, ordered list: `ProjectNext` in `internal/service/guide.go` gathers live state (a `projectSnapshot` of documents, tasks, bugs, paused runs) and `nextSteps` produces steps in the loop's own order — work already paid for waiting on one click, then intake → spec → plan, then the bug inbox (triage / promote), then the ONE next buildable task (test-ready, build-only, or test-first), then spec-debt to settle, and in a quiet project the brief / amend / release doors. Each step carries a stable id, the action in outcome language, the reason computed from the state that made it so, and the object (kind + ref) a client links to its own button. The guide is the single brain behind the desktop guide rail, the autopilot, and the MCP operator tools (`internal/mcp/tools.go`); clients render it, none re-derive it. An unparseable `project.toml` outranks every other suggestion, alone.

A fixed bug produces ONE `verify-bug` step — "Verify B-004 — confirm the fix answers the report; reopen it if the problem remains" — with Reopen named inside it as the exception path, never as the day's plan. Reopening accepted TASKS is not a guide step at all: `nextSteps` never returns a `reopen-task` step, in either the single or the grouped case, regardless of accepted-task state — the guide rail devotes no real estate to reopening accepted work. (Reopening a bug remains the bug loop's own step, unaffected.)

## SPEC-052 — Spend reports per mode and per duckling

**Implements:** REQ-052
**As-built:** yes

`internal/report/report.go` aggregates recorded runs into spend reports grouped `by` mode, duckling, role, or task (`Options.By`), over an optional `since` window: runs, passed/unverified/failed verdicts, tokens, cost, and wallclock per row, pure arithmetic over `runlog.Run` — no model consulted. Grouped by duckling, tokens and cost are the duckling's own share from the run's per-duckling spend map, never multiplied across seats; estimated usage is flagged on the row and never silently mixed with measured. Modes are compared against the solo baseline (`BaselineMode`) via pass-rate deltas, with artifact-stage runs and no-change runs kept out of the pass rates. `backfillSpend` in `internal/service/spend.go` reconstructs per-duckling spend from `llm.jsonl` for runs recorded before the rollup existed — once per run, never for runs whose log is gone.

## SPEC-053 — Advisor drafts the answer a paused question waits for

**Implements:** REQ-053
**As-built:** yes
**Covers:** T-011, T-012, T-031

When a run pauses on a question, a second model drafts the answer the human should give: `adviseQuestion` in `internal/service/advisor.go` runs asynchronously from a goroutine so the pause never waits, timed out at three minutes. The advisor seat is first-class: `pickAdvisor` prefers the run's recorded `advisor` roster seat, then the project roster's advisor pin, falling back to the architect only for runs recorded before the seat existed — a configured advisor, never an accident of roster resolution. The advisor's prompt is assembled in `adviseWith`: the task's spec text (`buildTaskPrompt`, clipped at 12 000 chars) under a "work the asking model was doing" heading, then the project's requirements, spec and plan documents (16 000 chars each, clipped) so the prompt's promise to cite the project's own spec is actionable, then the question verbatim and its offered options, all under `advisorSystemPrompt`, which is decisive by contract — one recommendation, citing the project's own spec when it decides the matter, written as the reply itself for verbatim submission.

The reply is governed, not trusted: `advisorViolation` checks the answer against the contract (2–8 sentences, no internal monologue), one repair attempt names the violation, and a reply still invalid after repair fails the consult. Failure is recorded, never silent: an `advice_failed` event (advisor, kind, cause) lands on the record and on the question's pending data, and the run degrades to an ordinary question card. The recommendation lands on the record as an `advice` event and on the question's pending data; the human still decides (accept, edit, or answer from scratch). Under full autopilot autonomy the draft is submitted as the answer through the same `RunAnswer` door, with the decider on the record.

A failed run's decision card carries the same seat's draft of what to do differently: `draftRedoNote` distills the run's own facts into a bounded (12 000 chars), editable `RedoNote` naming its advisor, attached where `redoNoteEligible` admits the run, exposed on the record as `redo_note` and through `run_get` — the lesson arrives with the failure instead of being re-distilled by hand.

## SPEC-054 — Consultation runs (chat)

**Implements:** REQ-054
**As-built:** yes

A conversation with a chosen duckling about a subject is itself a run (`internal/service/chat.go`): stage `"chat"`, mode solo — all the record-keeping, spend tracking, live stream, and transcript for free, with the subject ("chat about bug B-004") noted on the run. `ChatStart` requires a message and a registered duckling, assembles the subject's dossier deterministically into the first prompt, and the person's opening message lands on the record like every turn after it. The consultant's toolbelt is fixed and read-only — `fs_read, fs_search, fs_list, git_log, git_diff, task_read, bug_read, artifact_read, run_list, run_read` — plus one loop-side act, `bug_file`, exercised only on the person's explicit word; it never touches the tree, so a chat may run beside anything. Its closing duty is suggesting actions from the person's own menu, executed with the buttons that already exist. Chats pause as `pending_kind: chat` and continue via `ChatSend`.

## SPEC-055 — Outbound webhook notifications

**Implements:** REQ-055
**As-built:** yes
**Covers:** T-013

The engine announces run-settled moments to one configured webhook: `startNotifier` in `internal/service/notify.go` subscribes to the bus for the persisted `human_needed` and `run_end` records, the derived operator transitions, and autopilot stops, and POSTs a JSON envelope (event, run id, project id, RFC3339 timestamp, data) to the URL from config (`Notify.WebhookURL`); no URL configured means no notifier. The persisted records stay the source of truth, and `publishEvent` in `internal/service/lifecycle.go` derives the operator vocabulary from them as doorbells: `run_paused` (carrying `pending_kind`), `question_asked`, `budget_pause`, and `distress` for a budget pause or a caught repetition loop — so a run that pauses on a question at minute 2 or burns GPU in a generation loop announces itself instead of waiting for the next poll. Payloads are signed GitHub-style — `X-Hub-Signature-256: sha256=<hmac>` against the configured secret — because that is what receivers already verify. Delivery is best-effort by construction: a five-second HTTP timeout, exactly one retry on transport errors or 5xx, failures dropped silently — a dead receiver must never block or slow a run. No credential appears in the record; the secret stays in config.

**Assumption:** the notifier's exact subscribed set was confirmed for `budget_pause` and the transition family (`notify_transitions_test.go` names `run_paused`); the full event list was not re-enumerated line-by-line.

## SPEC-056 — Ranged writes: `fs_write_lines`

**Implements:** REQ-056
**As-built:** yes

`FSWriteLines` in `internal/tools/fs.go` takes `path, start, end, first_line, content`: 1-based inclusive lines matching `fs_read`'s numbering (`numberedWithin`), `first_line` compared to the current content of line `start` — a mismatch refuses with the actual line quoted ("line 302 is …, not …; re-read around line 302"), a range past EOF names the file's real length, a missing file points at `fs_write`. Empty content deletes the range; a file without a trailing newline keeps that shape. The result passes `WriteGuard` like every mutation and reports the new line count with the shifted-numbers warning. Registered in the implementer belt (`internal/tools/roles.go`); the fs_patch brake refusal (`internal/tools/tools.go`) now reads "use fs_read to see current line numbers, then fs_write_lines to replace the exact range". Pinned by `fswritelines_test.go`, including the read-then-edit round trip against `fs_read`'s numbers.

## SPEC-057 — The rubber duck: a positioned advisor turn with three answers

**Implements:** REQ-057
**As-built:** yes
**Covers:** T-059

`internal/strategy/rubberduck.go` measures a finished implementer turn structurally (`measureDistress` counts brake refusals in the tool results, the longest consecutive-failure streak of one tool ≥ 5, `verify_run` reds ≥ 3, plus the deliverables report's undelivered ids) and, only when distressed, `consultAdvisor` runs a `RoleAdvisor` turn (contract `json:advice`, belt read-only) whose prompt carries the seats, the signals as JSON, the tool trace, the reasoning tail, the final text and the deliverables report with notes. `parseAdvice` degrades anything malformed to `none`. In `execute.go` the consult runs after the implementer's `turn_end` is emitted (never nested — T-119 showed the two as parallel), before the reviewer; a `note` appends to the corrective notes and re-runs the implementer turn at once with `retry: n` on its `turn_start`, at most `maxConsultRetries = 2` per round (`advisor_retry` event); `stop` returns `*AdvisorStop`, which the service maps to `Resolution: stopped by advisor <seat>`, an `advisor_stop` event and `failRun` (work in place), and `redoNoteEligible` admits the paused-error run so the redo note is born with the reshuffle. No advisor seat → `advisor_consult {outcome: skipped}`; a failed consult → `outcome: failed`, run continues. `getRolePrompt(RoleAdvisor)` returns the rubber-duck system prompt. Pinned by `pair_test.go` (ordering, no-summon on a rough turn, bounded loop, stop before reviewer, skip without seat).

The distress signal has TWO consumers, and decorrelation (SPEC-004) holds between them: the duck reads the implementer's reasoning, tool trace and self-report — everything the reviewer is forbidden to see — while the reviewer receives only `operationalSummary`, the measured signals marshalled as JSON data, attached to its prompt as telemetry. The reviewer's verdict can tell wounded execution from wrong design without ever reading the implementer's rationalisation of either.

## SPEC-058 — `ask_advisor`: the mid-turn consult

**Implements:** REQ-058
**As-built:** yes

`AskAdvisor` in `internal/tools/exec.go` (non-mutating, implementer belt) calls `ExecContext.OnAskAdvisor(ctx, question)`; the service wires that hook in `runTask` only when `pickAdvisor` yields a seat, to `adviseInline` (`internal/service/advisor.go`) — `adviseWith` under `rubberDuckSystemPrompt` with the task prompt and project documents, three-minute timeout, cost recorded on the run's tracker and `llm.jsonl`, `advice {kind: inline}` / `advice_failed` events. Nil hook → an error result naming the self-help path; provider failure → "proceed with your best judgement". The implementer prompt's method step 5 says when to call it. Pinned by `askadvisor_test.go`.

## SPEC-059 — The deliverables checklist

**Implements:** REQ-059
**As-built:** yes

`internal/strategy/deliverables.go`: `ExtractDeliverables(title, body)` numbers top-level bullets (`-`, `*`, `•`, `N.`), stops at an out-of-scope marker, skips indented sub-bullets and label-only bullets, and falls back to the title. `dispatchMode` sets `ExecuteParams.Deliverables` from the task (`taskDeliverables`, `internal/service/modes.go`); stage, chat and test-first runs carry none. `buildPrompt` appends `deliverablesContract` to the implementer's prompt and `deliverablesForReviewer` (numbered list + `{"reported":[{id,status}],"not_reported":[…]}`, no notes) to the reviewer's. After each implementer turn `ParseDeliverablesReport` (tolerant: last `"deliverables"` object, brace-matched, statuses normalised, unknown ids dropped) emits `deliverables_report {round, retry?, total, deliverables, items, undelivered, unreported}`; undelivered ids enter `distressSignals.Undelivered`; a reviewer `approve` over undelivered ids emits `deliverables_gap`. Desktop: `splitDeliverablesReport` (`frontend/src/lib/runview.ts`) separates the report from the prose and `DeliverablesInline` (`DeliverablesCard.tsx`) renders it in the implementer's turn — marks with titles, never colour alone; `buildTurns` attaches `deliverables_gap` to the round's reviewer block and `VerdictBlock` shows it. Pinned by `deliverables_test.go` (Go) and `deliverables.test.tsx`.

## SPEC-060 — Clean-checkout acceptance: borrowed dependencies, honest failure

**Implements:** REQ-060
**As-built:** yes
**Covers:** T-042, T-054, T-055, T-071

Checkout preparation is declared, not hard-coded: `[verify] link_deps = [...]` in `project.toml` names installed dependency trees (clean relative paths, validated at load) to symlink into the detached worktree — `node_modules`, `frontend/node_modules`, `.venv`, an in-tree pytest runner — and `[verify] setup = "…"` declares a preparation command run in the checkout before the gate. `linkInstalledDeps(root, checkout, cfg.Verify.LinkDeps)` performs the linking; the legacy marker table keeps working projects green. A gate that fails because the commit does not include a path the command references refuses honestly: "the gate references build/, which the commit does not include — declare it in setup or link_deps", naming the missing path. Acceptance also refuses a commit whose build relies on ignored files it therefore omits — the unanchored `build/` pattern that matched `internal/build/` shipped a commit no fresh clone could compile; the accept now says so instead of passing on the strength of untracked files still on disk.

The reproduction itself is on the record: `verifyAcceptedCommit` stores a `GateReproduction` (gate, command, exit code, output, duration, green) on the accepted run as `acceptance_gate` with a `gate_reproduced` event, so an UNVERIFIED run verdict and a passing acceptance gate are two recorded facts, not one overwritten slot. Polarity is stage-appropriate: a build's accept requires the clean-checkout gate green, while a test-first accept requires the committed test's assertion-red — the deliverable of that stage is a failing test, and the two honesty mechanisms no longer fight each other.

In `runTask`'s yolo branch a non-nil `acceptRun` error no longer vanishes: the run pauses `pending_kind: gate` with `PendingData.detail = "auto-accept failed: … — decide it yourself"` and a `human_needed` event, the same shape as reviewer dissent. Pinned by `clean_checkout_deps_test.go` (including the pytest.ini-only layout that stranded T-119), `lifecycle_test.go` (the ignored-`internal/build/` refusal and the recorded reproduction) and `accept_polarity_test.go`.

**Assumption:** the stage-polarity branch and the ignored-file refusal are pinned by their tests; the full accept code path around them was not re-read for this settlement.

## SPEC-026 — Editable roster board

Desktop Settings links to the Roster board instead of editing positional mode seats. Task, TDD, and stage launchers prefill seat chips from the canonical resolver and show `project` or `global` provenance; changing a chip shows `picked now`, is sent only with that run request, and never writes Global or Project roster settings.

**Implements:** REQ-024
**As-built:** yes

The desktop Roster board displays the effective Global or Project roster and
supports equivalent HTML5 drag/drop and keyboard flows. Flock cards carry their
IDs in `dataTransfer`; selecting a seat with Enter or Space exposes accessible
`assign <duckling> to <role>` buttons. Seat cards can be removed, ordered
multi-slot seats append assignments in display order, and project pins can be
un pinned to restore the inherited ghost presentation.

Global edits use only the canonical global mode-seat/role-pin mutation. Project
edits use only the project ordered-list mutation and never alter Global. Every
successful mutation re-reads the roster so displayed provenance remains engine
truth. Engine validation errors and warnings are rendered beside the boards;
the pair board also reports implementer/reviewer overlap from effective seats.

Pinned by `roster_assign.test.tsx` and `roster.test.tsx`.

When a seat is selected, the board reorders eligible Flock cards and labels up to three informational candidates with their evidence reason. Selection never writes an assignment; local roles retain the operator's ordering. The board consumes the same `candidates` ids and `why` text exposed by the MCP roster view.

## SPEC-061 — Git-derived build identity and release awareness

**As-built:** yes
**Covers:** T-006, T-026, T-037, T-043

One package, `internal/build`, carries the binaries' identity: `Version`, `Branch` and `Commit`, stamped by the Makefile's ldflags into all three shipped binaries (`build_test.go` enforces a single stamping site and forbids a hardcoded release string); source builds read `dev`/`unknown`. `Semver()` strips the tag's `v` prefix for the client/server version-skew check; `Provenance()` is `branch@commit`. Every surface reports it: the MCP handshake's `serverInfo` (version, commit, provenance), `engine.json` (Version, Provenance beside pid/port/token), the engine's startup line and `Status.Provenance`. `ducklab-engine --version` (or `version`) prints `ducklab-engine <semver> (<branch>@<commit>)` and EXITS — it never boots an engine — and `daemon.WriteEngineJSON` refuses to overwrite an `engine.json` whose recorded pid is alive, so a stray invocation cannot re-point every client at a corpse.

Release awareness is git truth, not branch-name persistence: `acceptedUnreleased` in `internal/service/service.go` counts, per accepted task, its newest accepted run's commit that is NOT an ancestor of the latest release tag (`git.IsAncestor(commit, tag)`); branch names are provenance only, surviving merge and deletion without defining shipped-ness. The count and branch total ride `ProjectStatus`, the guide's release step ("Cut a release — N accepted task(s) await shipping" appears only while that count is non-zero and nothing is buildable), and the MCP status fields (`accepted_unreleased`, `unreleased_branches`, `unreleased_branch_names`) — accepted-but-unshipped work is visible where decisions are made, and a cut release retires the reminder.

## SPEC-062 — Desktop run surface: cards, stream health, transcripts

**As-built:** yes
**Covers:** T-020, T-021, T-027, T-046, T-056, T-057, T-075, T-076, T-078

The desktop acts at the point where the decision sits:

- A paused run card exposes every legal decision the run's `next` list offers — Resume alongside Abort/Reject — instead of sending the operator up to the task level.
- A retire-test refusal over a dirty tree carries the remedy as actions: Clean and Commit controls sit beside the "commit or clean them" instruction, not a click-distance dead end.
- The store maps the `human` answer event to the run's running status and clears its pending state, so an answered question stops listing as waiting.
- Stream health is a first-class surface: a dead SSE subscription reconnects (an engine restart rotates port and token), the engine binding recovers instead of dying silently against the stale one, and a failed action surfaces an error rather than vanishing.
- Display-only stream state is bounded — streamed deltas and reasoning are trimmed rather than kept per run per turn for the window's life — and summary-only board refreshes are debounced, so hours of heavy runs do not drown the app in its own data.
- Chat transcripts render human turns with a human avatar and duckling turns with the duck, on live chats and on reopened, resynced ones alike.
- A drafted release offers request-changes beside Cut: the operator enters revision text, the view calls `releasePlan(projectId, bump, reviseText)`, and the started revision run is surfaced.

**Assumption:** these are frontend behaviors pinned by their accepted fixes and vitest suites; the React code was not re-read for this settlement, so widget-level detail is described at the behavior level.

## SPEC-063 — Derived task status scoped to the task body

**As-built:** yes
**Covers:** T-023, T-041, T-044

A task's status is derived from its runs, not from the plan document, and the derivation is scoped to the task's CURRENT meaning: every run records `task_body_hash` at launch (`taskBodyHash` over the task body, `internal/service/stages.go`), `runsForCurrentTaskBodies` discounts runs whose hash no longer matches — recycling a task id after rewriting its body makes the old runs historical, never contaminating — and `RecoverRuns` backfills hashes onto runs that predate the field (legacy `2020-` fixture stamps exempted). One derivation feeds every consumer — the board, the launch guard, the MCP task list, the guide, the autopilot — pinned by `task_status_regeneration_test.go` ("shared by all consumers"), so regenerating the plan cannot revert accepted tasks to todo.

An accepted `no_changes` build settles its task: the tree answered the task's question by already containing the work, the derivation counts the acceptance, and the autopilot will not relaunch it without a human note (SPEC-049) — three identical empty runs in four minutes is the bug this closes.

## SPEC-064 — Working-tree snapshot and scoped restore

**Implements:** REQ-008
**As-built:** yes
**Covers:** T-019, T-051

Every run captures `TreeSnapshot` (the working tree as it stood) plus `TreeSnapshotHead` (HEAD at capture) at start. Rejection restores the tree BEFORE touching the run record — a refusal to clean up leaves the gate decision available — and aborting a paused or already-failed run restores it too: such a run has no goroutine left whose cancellation could reach `failRun`, so `RunAbort` calls the restore itself (an active run's restore still waits for its final write, never racing the model's filesystem).

The restore is surgical, not a rewind: `RestoreTreeAtHeadScoped` (`internal/vcs/vcs.go`) restores only when HEAD is still the snapshot's recorded HEAD and only over the paths the run itself wrote, so work committed after the snapshot — landed by hand or by another run — is never silently reverted by an old run's cleanup.

## SPEC-065 — Gate process isolation with preserved build caches

**Implements:** REQ-009
**As-built:** yes
**Covers:** T-045, T-050

`verify_run` executes the gate under `isolatedStateEnvironment` (`internal/verify/verify.go`): a fresh temporary directory carries `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `HOME`, `USERPROFILE`, `AppData` and `LocalAppData` for the whole child process tree, removed when the gate exits — a gate that spawns a `ducklab-engine` (a red test exercising the binary IS a gate command) reads an empty registry, never the live engine's state, and cannot checkpoint the run that hosts it.

Isolation covers engine state, not content-addressed caches: `GOPATH` (an explicit one wins), `GOMODCACHE` (derived from the winning GOPATH's first entry — it is also where `GOTOOLCHAIN` downloads land), `GOCACHE` and `npm_config_cache` default to the real home's locations, so a gate does not redownload the toolchain and every module per run. A scrubbed HOME has no `.gitconfig`, so gate-made commits sign as a fixed, obviously synthetic identity — `ducklab gate <gate@ducklab.invalid>` — never in the person's name.

## SPEC-066 — Test-first accepts assertion-red only

**Implements:** REQ-020
**As-built:** yes
**Covers:** T-024, T-028

The test-first judge (`judgeTestFirst` in `internal/service/testfirst.go`) distinguishes a broken specification from a valid red one. A non-zero gate is not enough: `compileFailure` reads the output STRUCTURALLY — Go's `FAIL … [build failed]` / `[setup failed]` package lines, tsc's `): error TS<n>:` diagnostics, vitest/esbuild's `Transform failed with` / `Failed to parse source` — and a compile-red run FAILS with the compiler's own message, because a test that does not compile breaks the whole package and is not a specification. The check is deliberately not lexical: a test carrying those phrases as fixture data stays assertion-red, and toolchain download chatter triggers nothing.

A green after-run is judged only against files the gate actually reaches: a new test under `frontend/` with a gate command that runs no frontend tests fails with "the new test lives in frontend/, which the gate never runs — widen the gate or move the test", so a correct vitest specification can no longer die silently under a Go-only gate.

## SPEC-067 — Bug settlement on accepted builds and triage sibling resolution

**Implements:** REQ-017
**As-built:** yes
**Covers:** T-009, T-029

A promoted bug moves `in_progress → fixed` only when an accepted BUILD run for its task lands — the auto-accepted test-first half of a TDD chain commits the specification, not the fix, and no longer moves the bug while the build has not started.

Accepting a triage run resolves its paused siblings: a superseded triage over the same bugs — aborted mid-batch, still holding partial proposals at its gate — stops offering accept on classifications the newer run already applied, instead of waiting for a person to notice and order the reject by hand.

**Assumption:** both behaviors are pinned by their accepted fixes; the exact hook call sites were not re-read for this settlement.

## SPEC-068 — Native reference-file picker in the Cycle view

**As-built:** yes
**Covers:** T-079

The desktop's Picker binding (the one native binding; everything else goes over HTTP) gains a `ChooseFile` sibling to `ChooseDirectory` (`cmd/ducklab-desktop/picker.go`): it opens the system file dialog filtered to `.md`/`.txt` (`AllowsOtherFileTypes(false)`), returns the chosen path as an absolute path, and treats cancel as an empty string that is not an error. Its FQN is derived from the package path via reflection (`ChooseFileFQN`) and injected into `window.ducklab.chooseFile` alongside `chooseDirectory` in `cmd/ducklab-desktop/main.go`.

The frontend wraps it in `chooseFile`/`canChooseFile` (`frontend/src/lib/picker.ts`): absent the Wails bridge (browser, CLI, tests) the call returns null and no control renders. In the Cycle view's reference-documents door (`frontend/src/views/Cycle.tsx`), a "Browse…" button sits beside the refs textarea when the chooser is available; a picked path is appended as a new line to `refsText`, and typed paths keep working unchanged.

## SPEC-069 — Accept-phase gate announcements in the transcript

**As-built:** yes
**Covers:** T-080, T-082, T-090, T-094

Acceptance is two visible steps, announced before they run, because staging and committing can take long enough that a passed round gate otherwise reads as an unexplained pause:

- `RunAccept` appends `gate_started {phase: "commit", detail: "committing accepted work before clean-checkout verification"}` BEFORE branch creation, `AddAll`, and `CommitWithTrailer` — so the event is durable even when the commit aborts acceptance. The clean-checkout reproduction is likewise announced with `phase: "accept"` before it runs.
- `buildTurns` (`frontend/src/lib/runview.ts`) opens a live gate turn on `gate_started` regardless of round, labelled by `gatePhaseLabel` from the phase ("committing accepted work", "after commit · clean checkout"), and settles it on `gate`, `round_gate`, or `gate_reproduced` — the accept flow settles on `gate_reproduced`, not only `round_gate`.
- A `gate_started` that supersedes a still-open gate block closes it neutrally (`gate: "done"`, no green check): superseding is a handoff, not a verdict — if the commit aborted, the transcript must not show green on a commit that never completed. The block earns green only when the superseding event carries the committed sha, or when a later `gate_reproduced` confirms the commit landed and reproduced; a failed reproduction closes the accept block red.

## SPEC-070 — Plan-accept warning for rewritten accepted task bodies

**As-built:** yes
**Covers:** T-081

Where `sections_removed` warns what a proposal erases from the document, a plan proposal also warns what accepting resets on the BOARD. Before the proposal gate, the engine compares each proposed task body against the current plan's normalized task-body hashes (`acceptedHistoryRewriteCount`, `internal/service/stages.go`): when a task whose accepted runs currently count has its body rewritten, the run's `Warning` gains "this proposal rewrites N task bodies whose accepted history will stop counting after acceptance" and a `task_history_rewritten {count}` event is appended. The comparison is over normalized bodies, so Implements-only traceability edits do not trigger it. Never blocked — a rewrite can be intended — only never hidden.

## SPEC-071 — Chat image preflight against the declared vision claim

**As-built:** yes
**Covers:** T-085, T-093

`validateChatImages` (`internal/service/chat.go`) refuses images before a text-only provider sees them, in order: the duckling must declare `caps.vision`; then `VerifyVision` probes the claim with one image request — a declared-but-absent projector fails with "model/server has no vision projector (mmproj); start the server with --mmproj or pick a truly seeing duckling" instead of the chat dying as a raw `chat stream: 500`. Only then are count (≤6), per-image and total byte caps, and base64 image-data-URL shape checked. The request context threads from `ChatStart`/`ChatMessage` through `validateChatImages` into the `VerifyVision` probe, so a hung endpoint is cancelled when the caller gives up rather than leaking the probe.

## SPEC-072 — Consultant seat in Roster Common, preselected in the guide chat

**As-built:** yes
**Covers:** T-086

`consultant` is a mode-independent common role: `config.RoleConsultant` is a registered role, and `isCommonRole` (`internal/service/roster.go`) seats it on the Common board beside triager and scribe, so the Common scope pin resolves and validates it like the other common seats. The guide rail's "ask how & why" chat box (`frontend/src/components/GuidePanel.tsx`) takes `preselectedDuckling={consultant}` — the duckling seated as consultant is pre-selected in the guide's duckling selection, so the person asks without re-picking the seat the roster already named.

## SPEC-073 — Run stage in the Recent runs rail

**As-built:** yes
**Covers:** T-088

The Recent runs rail (`RecentRun`, `frontend/src/components/GuidePanel.tsx`) labels each completed entry as `<stage> <identifier>` — "test T-087", "build T-087", "triage B-098" — so a test run is distinguishable from a build run on the same task. Triage entries name the bug (or subject); test and build entries name the task; other stages use task id or subject. Entries without a stage fall back to the run id. Each entry carries a verdict glyph (passed/failed/unverified, with aria-label) beside the label.

## SPEC-074 — Advisor in-flight signal and spend on a paused question

**As-built:** yes
**Implements:** REQ-053
**Covers:** T-091

A question pause names its advisor from the first moment: `pauseForQuestion` (`internal/service/lifecycle.go`) records the advisor seat into `PendingData["advisor"]` and the `human_needed` event BEFORE the asynchronous consultation starts, so the question card can show who is preparing the recommendation immediately. The question card (Board task card and RunView pending block) renders "<advisor> is preparing a recommendation" while the run is paused with no advice yet, then the drafted recommendation with the advisor's name when it lands. The consultation is real spend: its tokens and cost are recorded on the run's budget tracker and appended to `llm.jsonl` with role "advisor", like every other call the run caused.

## SPEC-075 — The guide no longer offers reopening accepted tasks

**As-built:** yes
**Covers:** T-092

`Next()` (`internal/service/guide.go`) never returns a step with ID `reopen-task`, in either the single or the grouped case, regardless of accepted-task state — the guide rail devotes no real estate to reopening accepted work. (Reopening a bug remains the bug loop's own step, unaffected.)

## SPEC-076 — Formatting drift is gate-checked

**As-built:** yes
**Covers:** T-095

The project's verification gate proves formatting as well as behaviour: the configured gate command runs `test -z "$(gofmt -l $(find . -type f -name '*.go'))"` before `go test`, the TypeScript check and the frontend suite, so a run whose Go files are not gofmt-clean fails the gate rather than landing drift. The existing drift this check was added for (misaligned struct literals in duck-authored service files) was settled in the same change.

## SPEC-077 — Desktop app asset bundling

**As-built:** yes
**Implements:** REQ-026

The desktop ships the frontend inside its binary: `make desktop` builds the frontend bundle (`npm run build`), replaces `cmd/ducklab-desktop/frontend/dist` with it, and compiles `cmd/ducklab-desktop` with the version/branch/commit ldflags, so the Wails app serves the assets it was built with. Because the bundled UI and the binary age independently, `make install` warns when `bin/ducklab-desktop` predates `frontend/src` — the remedy (`make desktop`) is named in the warning rather than leaving a stale bundle to silently show the previous build.

**Assumption:** the embed mechanism itself (the `embed.FS` directive in the desktop main package) was not re-read for this settlement; the bundling pipeline is evidenced by the Makefile's `desktop` and `install` targets.

