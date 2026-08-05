---
kind: spec
version: 1
updated_at: 2026-08-05T15:13:33Z
run_id: r-20260805-145618-yd5l
ducklings: [dsv4flash, deepseekv4pro, pato-sonnet, luna]
based_on: e3b0c44298fc1c14
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

**Implements:** REQ-024
**As-built:** yes

Configuration sources (precedence order):
1. Per-project: `.ducklab/project.toml`
2. Global: `~/.config/ducklab/config.toml`

Strict TOML parsing rejects unknown keys. Schema version in frontmatter. Hand edits preserved for known keys on write-back.

Structure: `internal/config/config.go`. CLI overrides via flags (not persisted).

## SPEC-025 — Runtime concurrent run queue

**Implements:** REQ-025
**As-built:** yes

`max_concurrent_runs` configuration enforces in-memory queue:

- Runs exceeding limit enter `queued` state
- Queue drains as running slots free
- On shutdown, queued runs are marked `paused`

Implementation: `internal/service/queue.go` with semaphore. Status exposed via `GET /api/queue`.

**Assumption:** Queue does not persist order or maintain FIFO guarantee across engine restarts. Queued runs are drained and paused on shutdown.

## SPEC-026 — Desktop asset bundling

**Implements:** REQ-026
**As-built:** yes

Desktop binary embeds React frontend:

- Build: Vite in `frontend/`, output embedded in Go binary
- Communication: Wails v3 injects `window.ducklab` with engine address/token
- No backend logic in desktop binary
- All capability in engine, desktop is thin display layer

Structure: `cmd/ducklab-desktop/main.go` with Wails initialization.

## SPEC-027 — CLI and engine cross-platform builds

**Implements:** REQ-027

`make cross` target builds CLI and engine for linux/amd64, linux/arm64, darwin/arm64, windows/amd64 with `CGO_ENABLED=0` for static binaries.

Desktop (`ducklab-desktop`) requires separate build with `make desktop` (cgo for Wails). The `cross` target validates that CLI and engine compile for all platforms but does not produce desktop binaries.

**Assumption:** Desktop cross-compilation is out of scope for the `make cross` target. Requirements document specifies all three binaries across the matrix; code implements only CLI and engine in cross target.

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

