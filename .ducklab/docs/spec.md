---
kind: spec
version: 2
updated_at: 2026-08-15T00:35:37Z
run_id: r-20260814-235312-32gx
ducklings: [k3, atom-local, glm52, beelink-local]
based_on: 20625e22f741bc19
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

Every run creates `.ducklab/runs/<id>/`:

- `state.json`: metadata, status, verdict, timestamps
- `events.jsonl`: tool calls, deltas (one JSON object per line)
- `llm.jsonl`: full provider request/response payloads
- `transcript.md`: markdown conversation
- `diff.txt`: git diff after run
- `tests.txt`: gate output

Engine rehydrates runs from disk on startup (`internal/runlog/` and `internal/service/service.go` initialization). Runs survive restarts.

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

`ducklab mcp serve` exposes engine over stdio as MCP server:

Tools exposed to MCP clients:
- `read_result`: get run outcome
- `approve_gate`: accept green run with reason
- `answer_question`: respond to ask_human
- `start_work`: initiate run

Attribution: decisions recorded as `mcp:<client-name>`, never `human`. Server implementation: `internal/mcp/mcp.go`, CLI: `internal/cli/mcp.go`.

The engine also exposes `GET /v1/ducklings/scorecards`, returning one configured duckling per row with declared provider/model/cost/capabilities/roles/notes and explicitly sourced measured, latest-per-suite bench, and declared external-index evidence. Missing evidence is null/absent.

The `roster` MCP tool accepts `action` (`get | set | unpin`), `scope` (`global | project`), `project_id`, `mode`, `role`, and ordered `ducklings`. `get` returns every board-addressable seat with `role`, ordered `ducklings`, effective `duckling`, and `source` provenance (`global mode seat`, `project pin`, or `global role fallback`); project pins also return the overridden global `default`. Project `set` replaces the complete ordered pin and `unpin` removes it to restore Global inheritance. Global `set` writes mode seats (or mode-independent triager/scribe role pins) and never changes project files. Invalid action/scope, role, duckling, and mode cardinality return field-named actionable errors with `next` guidance. MCP dispatch delegates to the canonical service roster resolver and records operator actions as `mcp:<client-name>`.

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

Structure: `internal/config/config.go`. CLI overrides via flags (not persisted). Each `[duckling.<id>.index]` may declare `coding_score`, `source`, and `as_of` (YYYY-MM-DD); it is round-tripped with provenance and never fetched or inferred.

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

## SPEC-026 — Desktop asset bundling

**Implements:** REQ-026
**As-built:** yes

Desktop binary embeds React frontend:

- Build: Vite in `frontend/`, output embedded in Go binary
- Communication: Wails v3 injects `window.ducklab` with engine address/token
- No backend logic in desktop binary
- All capability in engine, desktop is thin display layer

Structure: `cmd/ducklab-desktop/main.go` with Wails initialization.

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

## SPEC-031 — Dual budget enforcement

**Implements:** REQ-031
**As-built:** yes

Budget caps enforced in `internal/budget/budget.go`:

1. **USD cost**: cumulative across run, checked after each LLM call
2. **Turn count**: increments per model turn, checked before starting turn

`Exceeded()` returns true when either limit hit. Run stops immediately with `budget_exceeded` verdict.

**Assumption:** Token budget is not enforced by code—only USD and turns are checked.

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

The plan stage carries a light amendment path beside full redrafts: `runExtend` in `internal/stage/extend.go` sends the architect the plan as an outline (ids and titles only) plus the spec as a wiring list, and accepts back only a fragment of one to three new task sections written under the literal id `T-900`. The engine merges by code — `mergeExtension` assigns the next free `T-###` id and places each task under the milestone its `**Milestone:**` field named (or the last one), while a bare `M-` heading in the fragment is read as a placement declaration, resolved to an existing milestone or created with a real id. Amendment turns blank out the whole-document contract on every script turn (`script.Turns[i].Contract = ""`) so the fragment contract in the prompt is the only one that speaks. An amendment whose reply parses to zero work items fails with the architect's own reason (`"the architect added no tasks: …"`); a change altering what the product IS is refused in-prompt toward a requirements brief. Tasks the architect could not wire are merged with no `Implements:`, wearing spec-debt (see SPEC-010 exemption), and the one-click `spec_settle` tool in `internal/mcp/tools.go` plus the guide step in `internal/service/guide.go` ("Teach the spec what was built") close that debt: a specification revision documents delivered tasks as built and wires their `Implements:` upward.

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

Loops brake at tool level, never by asking a model:

- **Consecutive-gate-fail brake** — `GateFailLimit = 10` in `internal/tools/exec.go`: a run of red `verify_run` results with no green between is refused with "a person (or the reviewer) can redirect the work", and the final three reds carry warnings. A green gate resets `ConsecGateFails` to zero; the count lives on the run's exec context (`internal/tools/tools.go`).
- **Identical-repeat brake** — the same failing tool call tracked via `lastFailSig`/`lastFailCount` is refused the third time with orders to change something.
- **Per-reply call cap with live lift** — the agent loop (`internal/agent/agent.go`) counts calls per reply against the run's cap: `OnCapNear` announces the last allowed call in advance, and `CapLift` is consulted before every call, so a cap lifted mid-flight takes effect without a restart. Lifting past the cap resolves to `uncappedTurns` (`internal/service/modes.go:231`), the stand-in for "no cap" on the per-reply call loop.

**Assumption:** the "recorded on the run / survive resume" half of REQ-041's lift is provided by `internal/service/modes.go` `capLift`/`onCapNear` wiring and the run's turn-cap resolution (`roleTurnCapsFor`); the lift choice is reflected back into the persisted stage request and re-applied on resume.

## SPEC-043 — Declared fallback ducklings and human-decided reseat

**Implements:** REQ-043
**As-built:** yes

Every duckling may declare `fallback` in config (`internal/config/config.go`), carried on the registered duckling (`internal/duckling/duckling.go`) and surfaced on the fleet view. A run paused on provider weather is reseated only by explicit human call: `RunReseat` in `internal/service/service.go` — exposed as `POST /v1/runs/{id}/reseat` in `internal/engineapi/routes_table.go` and through the desktop/client — swaps every seat role the failed duckling held in the roster to the named replacement, appends a `seat_failover` event (from, to, roles, reason) to the run record, persists the new roster into the stage run's saved request, and resumes with the token budget and ledger intact. There is no automatic router: a run paused at a human gate (question or decision) has nothing to reseat, since availability — not judgment — is the only thing the swap answers.

## SPEC-044 — Per-run overrides at launch

**Implements:** REQ-044
**As-built:** yes

Any run launch may carry overrides of the configured defaults, recorded on the run and surviving resume:

- **Seat picks** — the run's own duckling line-up overrides the roster for every role; it is persisted on the run record so a resume re-enters with the seats the person chose.
- **Agent turn cap** — `capOverride` in `internal/service/modes.go` resolves one `AgentTurns` value applied to every role for that run (`roleTurnCapsFor` and `internal/service/defaults.go`); negative lifts the cap entirely without touching fleet tuning.
- **Screenshots** — `Images` data URLs ride the run request (`internal/service/stages.go`) and are attached to the architect's (or triager's) own turn; a screenshot aimed at a seat whose declared `vision` cap is false is dropped with a warning event instead of silently vanishing.

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

An optional per-project autopilot drives the project guide (`internal/service/autopilot.go`): each time a run settles it asks `ProjectNext` and acts only when the first step is mechanical — test-first or build — launched through the same service methods and queue as the buttons, stamped `origin: "autopilot"` on every run it starts. Every other step is a human gate where the autopilot idles. Stop rails are fixed: a per-activation cap on runs started (default 10, `autopilot_max_tasks`), two consecutive failures switch it off with a recorded reason (default `autopilot_max_fails` = 2), a retry carries the previous failure in hand rather than blind-relaunching, and it never lifts a money cap nor crosses UNVERIFIED. State is in-memory: an engine restart lands the autopilot off, deliberately.

## SPEC-050 — Managed application process

**Implements:** REQ-050
**As-built:** yes

The engine runs the project's own app as a first-class managed process (`internal/service/app.go`): `[run]` in `project.toml` (`config.RunApp`) declares the start command, optional URL, preflight environment check, and requirements. Launch runs the preflight first — a failure names what is missing in its own words — then starts the command with stdout/stderr to `.ducklab/app.log`, and tracks pid, start time, exit error, and log tail. Stop kills the whole process group via the cross-platform shell wrapper. Status reports a health field separate from liveness — a process alive and a service answering are different claims — plus the last exit error and log tail for when Launch appears to do nothing.

## SPEC-051 — Project guide: computed next steps

**Implements:** REQ-051
**As-built:** yes

The engine answers "what do I do now?" per project with a computed, ordered list: `ProjectNext` in `internal/service/guide.go` gathers live state (a `projectSnapshot` of documents, tasks, bugs, paused runs) and `nextSteps` produces steps in the loop's own order — work already paid for waiting on one click, then intake → spec → plan, then the bug inbox (triage / promote), then the ONE next buildable task (test-ready, build-only, or test-first), then spec-debt to settle, and in a quiet project the brief / amend / release doors. Each step carries a stable id, the action in outcome language, the reason computed from the state that made it so, and the object (kind + ref) a client links to its own button. The guide is the single brain behind the desktop guide rail, the autopilot, and the MCP operator tools (`internal/mcp/tools.go`); clients render it, none re-derive it. An unparseable `project.toml` outranks every other suggestion, alone.

## SPEC-052 — Spend reports per mode and per duckling

**Implements:** REQ-052
**As-built:** yes

`internal/report/report.go` aggregates recorded runs into spend reports grouped `by` mode, duckling, role, or task (`Options.By`), over an optional `since` window: runs, passed/unverified/failed verdicts, tokens, cost, and wallclock per row, pure arithmetic over `runlog.Run` — no model consulted. Grouped by duckling, tokens and cost are the duckling's own share from the run's per-duckling spend map, never multiplied across seats; estimated usage is flagged on the row and never silently mixed with measured. Modes are compared against the solo baseline (`BaselineMode`) via pass-rate deltas, with artifact-stage runs and no-change runs kept out of the pass rates. `backfillSpend` in `internal/service/spend.go` reconstructs per-duckling spend from `llm.jsonl` for runs recorded before the rollup existed — once per run, never for runs whose log is gone.

## SPEC-053 — Advisor drafts the answer a paused question waits for

**Implements:** REQ-053
**As-built:** yes

When a run pauses on a question, a second model drafts the answer the human should give: `adviseQuestion` in `internal/service/advisor.go` runs asynchronously from a goroutine so the pause never waits, timed out at three minutes, preferring the run's recorded architect as the advisor (decorrelated from the implementer that asked). The advisor's prompt is assembled inline in `advise`: the task's spec text (`buildTaskPrompt`, clipped at 12 000 chars) under a "work the asking model was doing" heading, then the question verbatim and its offered options (`internal/service/advisor.go:107–121`), all under `advisorSystemPrompt`, which is decisive by contract — one recommendation, citing the project's own spec when it decides the matter, written as the reply itself for verbatim submission. The recommendation lands on the record as an `advice` event and on the question's pending data; the human still decides (accept, edit, or answer from scratch). No advice degrades to an ordinary question card, never a failure. Under full autopilot autonomy the draft is submitted as the answer through the same `RunAnswer` door, with the decider on the record.

## SPEC-054 — Consultation runs (chat)

**Implements:** REQ-054
**As-built:** yes

A conversation with a chosen duckling about a subject is itself a run (`internal/service/chat.go`): stage `"chat"`, mode solo — all the record-keeping, spend tracking, live stream, and transcript for free, with the subject ("chat about bug B-004") noted on the run. `ChatStart` requires a message and a registered duckling, assembles the subject's dossier deterministically into the first prompt, and the person's opening message lands on the record like every turn after it. The consultant's toolbelt is fixed and read-only — `fs_read, fs_search, fs_list, git_log, git_diff, task_read, bug_read, artifact_read, run_list, run_read` — plus one loop-side act, `bug_file`, exercised only on the person's explicit word; it never touches the tree, so a chat may run beside anything. Its closing duty is suggesting actions from the person's own menu, executed with the buttons that already exist. Chats pause as `pending_kind: chat` and continue via `ChatSend`.

## SPEC-055 — Outbound webhook notifications

**Implements:** REQ-055
**As-built:** yes

The engine announces run-settled moments to one configured webhook: `startNotifier` in `internal/service/notify.go` subscribes to the bus for `human_needed`, `run_end`, and autopilot stops, and POSTs a JSON envelope (event, run id, project id, RFC3339 timestamp, data) to the URL from config (`Notify.WebhookURL`); no URL configured means no notifier. Payloads are signed GitHub-style — `X-Hub-Signature-256: sha256=<hmac>` against the configured secret — because that is what receivers already verify. Delivery is best-effort by construction: a five-second HTTP timeout, exactly one retry on transport errors or 5xx, failures dropped silently — a dead receiver must never block or slow a run, since the run record on disk is the source of truth and the webhook is a doorbell. No credential appears in the record; the secret stays in config.

## SPEC-056 — Ranged writes: `fs_write_lines`

**Implements:** REQ-056
**As-built:** yes

`FSWriteLines` in `internal/tools/fs.go` takes `path, start, end, first_line, content`: 1-based inclusive lines matching `fs_read`'s numbering (`numberedWithin`), `first_line` compared to the current content of line `start` — a mismatch refuses with the actual line quoted ("line 302 is …, not …; re-read around line 302"), a range past EOF names the file's real length, a missing file points at `fs_write`. Empty content deletes the range; a file without a trailing newline keeps that shape. The result passes `WriteGuard` like every mutation and reports the new line count with the shifted-numbers warning. Registered in the implementer belt (`internal/tools/roles.go`); the fs_patch brake refusal (`internal/tools/tools.go`) now reads "use fs_read to see current line numbers, then fs_write_lines to replace the exact range". Pinned by `fswritelines_test.go`, including the read-then-edit round trip against `fs_read`'s numbers.

## SPEC-057 — The rubber duck: a positioned advisor turn with three answers

**Implements:** REQ-057
**As-built:** yes

`internal/strategy/rubberduck.go` measures a finished implementer turn structurally (`measureDistress`: `REFUSED:`-prefixed tool results, the longest consecutive-failure streak of one tool ≥ 5, `verify_run` reds ≥ 3, plus the deliverables report's undelivered ids) and, only when distressed, `consultAdvisor` runs a `RoleAdvisor` turn (contract `json:advice`, belt read-only) whose prompt carries the seats, the signals as JSON, the tool trace, the reasoning tail, the final text and the deliverables report with notes. `parseAdvice` degrades anything malformed to `none`. In `execute.go` the consult runs after the implementer's `turn_end` is emitted (never nested — T-119 showed the two as parallel), before the reviewer; a `note` appends to the corrective notes and re-runs the implementer turn at once with `retry: n` on its `turn_start`, at most `maxConsultRetries = 2` per round (`advisor_retry` event); `stop` returns `*AdvisorStop`, which the service maps to `Resolution: stopped by advisor <seat>`, an `advisor_stop` event and `failRun` (work in place), and `redoNoteEligible` admits the paused-error run so the redo note is born with the reshuffle. The reviewer prompt receives `operationalSummary` — the signals JSON — as data. No advisor seat → `advisor_consult {outcome: skipped}`; a failed consult → `outcome: failed`, run continues. `getRolePrompt(RoleAdvisor)` returns the rubber-duck system prompt. Pinned by `pair_test.go` (ordering, no-summon on a rough turn, bounded loop, stop before reviewer, skip without seat).

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

`linkInstalledDeps(root, checkout)` in `internal/service/service.go` is a table of `{rel, markers}`: `node_modules` and `frontend/node_modules` justified by `package.json`; `.venv` justified by any of `pyproject.toml, requirements.txt, setup.py, setup.cfg, pytest.ini, tox.ini, Pipfile` — symlinked into the detached worktree before `verify.Run`. In `runTask`'s yolo branch a non-nil `acceptRun` error no longer vanishes: the run pauses `pending_kind: gate` with `PendingData.detail = "auto-accept failed: … — decide it yourself"` and a `human_needed` event, the same shape as reviewer dissent. Pinned by `clean_checkout_deps_test.go` (including the pytest.ini-only layout that stranded T-119).

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
