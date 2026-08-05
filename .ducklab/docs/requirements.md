---
kind: requirements
version: 1
updated_at: 2026-08-05T13:15:31Z
run_id: r-20260805-130519-7cyn
ducklings: [pato-sonnet, deepseekv4pro, luna, dsv4flash]
based_on: e3b0c44298fc1c14
origin: adopted
approved_by: human
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

