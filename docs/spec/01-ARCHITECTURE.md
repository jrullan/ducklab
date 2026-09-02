# 01 — Architecture

`spec-1.1` — revised: three-binary split, headless engine, desktop app as the
primary client. Supersedes the CLI/TUI single-binary design of `spec-1.0`.

## 1. Topology

Ducklab is **three artifacts around one core library.**

```
                    ┌──────────────────────────────────────┐
                    │  ducklab-desktop   (Wails v3, cgo)   │  ← primary face
                    │  React + TS + Vite in a system webview│
                    └──────────────┬───────────────────────┘
                                   │  HTTP + SSE on 127.0.0.1
                    ┌──────────────┴───────────────────────┐
   ducklab (CLI) ───┤   ducklab-engine   (headless, Go)    │
   pure Go, cgo-free│   owns runs, git, gate, budgets, DB   │
                    └──────────────┬───────────────────────┘
                                   │
                              internal/*  (the core library)
                                   │
                    providers · tools · agent · conv · stage · store · vcs
```

| Artifact | Build | cgo | Role |
|----------|-------|-----|------|
| `ducklab-engine` | `go build ./cmd/ducklab-engine` | **off** | Long-lived headless daemon. Executes every run. Single source of truth at runtime. |
| `ducklab` | `go build ./cmd/ducklab` | **off** | Full-parity CLI **client** of the engine. Scripting, CI, SSH boxes, Hermes automation. |
| `ducklab-desktop` | `wails3 build` | **on** | Desktop app. Attaches to the engine. Primary UX and the monitoring surface. |

### 1.1 Why the split

- **The desktop app must not own the work.** Closing a window must never kill a
  six-minute tournament run. The engine outlives every client (P5 → now literal).
- **cgo is quarantined.** Wails needs cgo and per-platform native builds. Keeping
  it in `cmd/ducklab-desktop` + `frontend/` only means the engine and CLI still
  cross-compile to four targets from one machine with `CGO_ENABLED=0`.
- **Clients are interchangeable.** A run started from the CLI is visible live in
  the desktop app and vice versa, because neither of them holds run state.
- **Hermes can still drive everything headlessly**, which is how Ducklab itself
  is being built.

### 1.2 Rule: no client-exclusive behaviour

Every capability lives in `internal/service` and is exposed through the engine
API. The desktop app and the CLI are both **thin** — they render and they call.
Any logic that appears in `frontend/` or in `internal/cli` and nowhere else is a
spec violation. The single test for this: *deleting the desktop app must not
remove any capability.*

The Charm TUI of `spec-1.0` is **dropped**. `ducklab run watch` remains as a
plain streaming log for terminal use; it is not an interactive UI.

## 2. Technology decisions (fixed — do not revisit)

| Decision | Value | Rationale |
|----------|-------|-----------|
| Language (core, engine, CLI) | **Go ≥ 1.24**, `CGO_ENABLED=0` | Cross-compiles to 4 targets, one binary each. |
| SQLite driver | **`modernc.org/sqlite`** (pure Go) | No cgo. Never `mattn/go-sqlite3`. |
| Desktop shell | **Wails v3** | Go-native, system webview, no bundled Chromium. |
| Frontend | **React 18 + TypeScript + Vite** | Largest ecosystem; most training data for the implementing model. |
| Styling | **Tailwind CSS** + CSS variables for theming | Deterministic, no runtime CSS-in-JS. |
| Frontend state | **Zustand** + TanStack Query | Small, explicit; no Redux boilerplate. |
| Diff rendering | **`diff2html`** side-by-side; **Monaco** only in the editor pane | Monaco is heavy; do not use it for read-only diffs. |
| Charts | **Recharts** | Sufficient for the report views. |
| Engine transport | **HTTP/1.1 + JSON on `127.0.0.1`**, events over **SSE** | Trivially debuggable with `curl`; works in a webview with no extra permissions. |
| Config format | **TOML** (`github.com/BurntSushi/toml`) | Human-editable. |
| MCP | `github.com/modelcontextprotocol/go-sdk` (client only) | |
| Module path | `github.com/jrullan/ducklab` | |
| Min OS | Linux (glibc 2.31+, WebKitGTK 4.1), macOS 13+, Windows 10 + WebView2 | |

Forbidden anywhere in Go code: cgo outside `cmd/ducklab-desktop`, POSIX-only
syscalls, hardcoded `/` separators (use `path/filepath`), invoking `bash` by
name. Forbidden in the frontend: any network call to a host other than the
engine's loopback origin; any business logic; any direct filesystem access.

## 3. Invariants (normative — an implementation that violates one is wrong)

- **I1 — Models never mutate version control.** `git commit/branch/merge/checkout/push/worktree`
  happen only in `internal/vcs`, called only by the orchestrator. No such tool is
  ever in a duckling's toolbelt. Read-only git tools may be exposed.
- **I2 — Models never decide the verdict.** The verdict comes from
  `internal/verify` executing the gate and reading its exit code. A reviewer's
  opinion is advice; it can block a merge, never turn a red gate green.
- **I3 — Every loop is bounded.** Turns, tokens, wall-clock, USD. Hitting a bound
  terminates with `BUDGET_EXCEEDED`; never a silent continue or truncate.
- **I4 — Every filesystem write is path-jailed** to the project root, with
  symlink resolution. Escaping paths are refused, never clamped.
- **I5 — Every model call is logged** (redacted) before its result is used.
- **I6 — Unparseable model output is repaired or failed, never guessed.**
- **I7 — Review is anonymous.** Judges and reviewers see `A`/`B`, never the
  author's identity or reasoning transcript.
- **I8 — No regeneration of a green solution.** A passing candidate is applied
  verbatim; a judge chooses or refuses, it never rewrites.
- **I9 — Every run is resumable** from `runs/<id>/state.json`, checkpointed after
  every turn and every tool call.
- **I10 — Secrets never touch disk in project state.** Keys come from the
  environment or the OS keyring; never in configs, state, or logs.
- **I11 — Clients hold no authoritative state.** The desktop app and the CLI may
  cache for rendering, but the engine's DB and run directories are the only truth.
  Killing every client must not change the outcome of a run.
- **I12 — The engine binds to loopback only** and requires a bearer token from a
  0600 file. It never listens on `0.0.0.0`, and it has no remote mode in v1.

## 4. Package layout

```
cmd/
  ducklab-engine/main.go     # daemon entry point                (cgo off)
  ducklab/main.go            # CLI client entry point            (cgo off)
  ducklab-desktop/main.go    # Wails v3 app entry point          (cgo ON)

frontend/                    # React+TS+Vite app for the desktop shell
  src/
    app/         routing, layout, theme
    api/         generated engine client + SSE subscriber
    views/       Overview Ducklings Cycle Board Run Review Release Reports Settings
    components/  DiffView, ConversationLane, ToolTimeline, CostMeter, GateBadge, …
    store/       zustand slices (session, project, runs, notifications)

internal/
  service/    THE CAPABILITY LAYER. Every operation Ducklab can perform, as a
              plain Go method on Service. Both the engine handlers and the
              in-process desktop fallback call only this. No HTTP here.
  engineapi/  HTTP handlers, routing, SSE hub, auth, request/response DTOs
  engineclt/  Go client for the engine API (used by cmd/ducklab)
  bus/        In-process event bus; fan-out of run events to SSE subscribers
  daemon/     Process lifecycle: pidfile, engine.json, auto-start, graceful stop
  registry/   Global project registry (id ↔ path), recent projects

  cli/        Command dispatch and rendering. Calls engineclt only.
  desktop/    Wails bindings; a thin adapter over engineclt + bus

  config/     Global + project config: load, merge, validate, defaults
  provider/   Provider interface, openaicompat, anthropic, fake
  duckling/   Registry, health probe, capability detection, cost table
  budget/     Budget accounting and enforcement

  agent/      Agentic loop, dialects, repair, contracts, embedded prompts
  tools/      Tool interface, registry, policy, write guard, all tools
  conv/       Conversation script engine, scheduler, transcript, Until expr
  strategy/   solo, pair, tournament, council, split
  stage/      intake, spec, plan, build, review, release, operate

  store/      SQLite schema, migrations, typed queries
  artifact/   Markdown artifacts, frontmatter, traceability checks
  vcs/        Git operations, worktrees, patch apply, file tree
  verify/     Gate detection and execution
  capability/ Stack-specific adapters and their composable check registry
  skill/      Skill discovery, SKILL.md parsing, invocation
  mcpc/       MCP client manager
  report/     Metrics aggregation
  runlog/     Run directory, JSONL writers, atomic checkpointing
  xplat/      OS abstraction: shell, paths, keyring, notifications, open-in-editor
```

### 4.1 Dependency rules

- `internal/service` is the hub. It may import every domain package.
- `internal/engineapi` imports `service` and `bus`. **It contains no logic** —
  each handler is: decode DTO → call one `service` method → encode DTO.
- `internal/cli` imports `engineclt` and `daemon` **only**. It must not import
  `service`, `strategy`, `agent`, `store`, or `vcs`. This is what forces the CLI
  to stay a client.
- `internal/desktop` imports `engineclt`, `daemon`, `registry` only.
- `frontend/` imports nothing from Go; it speaks HTTP.
- `internal/tools` must not import `agent`, `conv`, `strategy`, `stage`, `service`.
- No import cycles; enforce with a `TestNoCycles`.

**Consequence to respect:** if a new feature can't be expressed as a `service`
method, the design is wrong. Do not add a shortcut path from a client into a
domain package.

### 4.2 Stack capabilities are adapters, not core branches

The core owns workflow, accepted contracts, isolation, evidence, and gate
polarity. Knowledge of a language, framework, build system, package manager,
runtime, project, task, or source file does not belong in that core.

`internal/capability` is a microkernel-style registry of independent providers.
Each provider recognizes one reusable stack capability and contributes checks
through the same interface. Providers compose: a project may resolve to
`c-native`, `meson`, `pkg-config`, and `desktop-linux` simultaneously; Ducklab
must not force it into one project-type label.

The task's accepted `Verification` and the project's configured gate remain
authoritative. Capability checks are diagnostics by default. Project policy may
disable one or promote it to `required`; an adapter may never silently weaken
or strengthen an accepted contract. Provider implementations must not contain
project names, task IDs, benchmark-run IDs, or project-specific paths.

Adding support for another stack means implementing and registering another
provider, not adding a language conditional to `tools`, `service`, or `verify`.
The first provider, `c-native`, turns a simple C/C++ syntax command into an
additional warning-strict diagnostic without changing the original command.

Providers implement only the ports they need. A `Detector` contributes
evidence and prioritized project-gate candidates; a `Checker` contributes
task diagnostics. Project discovery may probe a collector or toolchain, so it
runs separately from per-task check resolution and must never be repeated by
every `verify_run`. Gate priority is candidate data resolved by the registry,
not provider registration order or a language switch in `verify`.

## 5. Core types

Types from `spec-1.0` stand unchanged except where noted here.

### 5.1 The service layer

```go
// internal/service
type Service struct { /* unexported deps */ }

func New(cfg config.Global, opts Options) (*Service, error)

// Projects
func (s *Service) ProjectOpen(ctx context.Context, path string) (Project, error)
func (s *Service) ProjectInit(ctx context.Context, req InitRequest) (Project, error)
func (s *Service) ProjectList(ctx context.Context) ([]Project, error)
func (s *Service) ProjectStatus(ctx context.Context, id string) (Status, error)

// Ducklings
func (s *Service) DucklingList(ctx context.Context) ([]duckling.Duckling, error)
func (s *Service) DucklingProbe(ctx context.Context, id string) (duckling.Capabilities, error)
func (s *Service) DucklingTest(ctx context.Context, id, prompt string) (TestResult, error)

// Runs
func (s *Service) RunStart(ctx context.Context, req RunRequest) (runlog.Run, error)
func (s *Service) RunResume(ctx context.Context, id string) (runlog.Run, error)
func (s *Service) RunAbort(ctx context.Context, id string) error
func (s *Service) RunGet(ctx context.Context, id string) (RunDetail, error)
func (s *Service) RunList(ctx context.Context, f RunFilter) ([]runlog.Run, error)
func (s *Service) RunAccept(ctx context.Context, id string, msg string) (AcceptResult, error)
func (s *Service) RunReject(ctx context.Context, id, reason string) error
func (s *Service) RunAnswer(ctx context.Context, id, questionID, answer string) error

// Stages, tasks, bugs, reviews, releases, skills, mcp, reports … one method each.
```

`RunStart` **returns immediately** with the run in `running` status. It never
blocks until completion. Progress is observed only through the event stream.
This is the single most important shape change from `spec-1.0`, where `run` was
a blocking call.

### 5.2 Provider — streaming added

The desktop app shows tokens as they arrive, so `Provider` gains a streaming
method. Non-streaming `Chat` remains and is what the agent loop uses by default;
streaming is opt-in per run.

```go
type Provider interface {
    ID() config.ProviderID
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    // ChatStream emits deltas on ch and returns the assembled final response.
    // Implementations that cannot stream MUST return ErrUnsupported without
    // sending on ch; the caller then falls back to Chat.
    ChatStream(ctx context.Context, req ChatRequest, ch chan<- Delta) (ChatResponse, error)
    Models(ctx context.Context) ([]string, error)
}

type Delta struct {
    Text      string `json:"text,omitempty"`
    ToolName  string `json:"tool_name,omitempty"`  // set when a tool call begins
    Done      bool   `json:"done,omitempty"`
}
```

Rules: streaming is a **display** concern. Contract parsing, tool dispatch, and
logging always operate on the assembled final response, never on deltas. A
dropped SSE subscriber must never affect the run.

### 5.3 Events

`internal/bus` fans out the same events that are appended to `events.jsonl`
(`02-DATA-MODEL.md §6.2`), plus two client-only types that are **not** persisted:

| Type | Payload | Persisted? |
|------|---------|------------|
| `token_delta` | `{run_id, turn, duckling, text}` | no — display only |
| `heartbeat` | `{ts}` every 15 s | no — keeps SSE alive through proxies |

Ordering guarantee: persisted events are delivered to subscribers in `seq`
order, after the append to `events.jsonl` has been flushed. A subscriber that
attaches mid-run receives the full backlog from `events.jsonl` first, then live
events, with no gap and no duplicate — implemented by taking the bus lock while
reading the tail of the file.

## 6. Identifiers

Unchanged from `spec-1.0`: `r-<YYYYMMDD>-<HHMMSS>-<4 base32>`, `T-nnn`,
`B-nnn`, `REQ-nnn`, `SPEC-nnn`, `ADR-nnn`, `M-nn`. Sequences are allocated in a
SQLite transaction, never by counting files.

Additionally: **project id** is the directory basename slugified, deduplicated
in the global registry with a `-2` suffix on collision.

## 7. Control flow — the four nested loops

```
┌─ ENGINE  (internal/service + daemon) ───────────────────────────────────┐
│  Supervises N concurrent runs across M projects. Outlives all clients.  │
│                                                                         │
│  ┌─ STAGE  (internal/stage) ────────────────────────────────────────┐   │
│  │  Produces one artifact or one accepted diff. Ends at a human gate│   │
│  │                                                                  │   │
│  │  ┌─ CONVERSATION  (internal/conv) ────────────────────────────┐  │   │
│  │  │  Executes Script.Turns in order, ≤ MaxRounds, until Until. │  │   │
│  │  │  Turn order is DATA, never chosen by a model.              │  │   │
│  │  │                                                            │  │   │
│  │  │  ┌─ AGENT LOOP  (internal/agent) ───────────────────────┐  │  │   │
│  │  │  │  ≤ Turn.MaxTurns iterations of:                      │  │  │   │
│  │  │  │    provider.Chat/ChatStream(messages, toolbelt)      │  │  │   │
│  │  │  │      ├─ tool_calls → tools.Execute → append → loop   │  │  │   │
│  │  │  │      └─ final text → parse contract → return         │  │  │   │
│  │  │  └─────────────────────────────────────────────────────┘  │  │   │
│  │  └──────────────────────────────────────────────────────────┘  │   │
│  │  then: verify.Run(gate) → Verdict → HUMAN GATE → vcs.Commit    │   │
│  └──────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```

The turn-execution pseudocode of `spec-1.0 §6.1` is unchanged, with one
addition: after `runlog.AppendLLM`, the event is published to `bus`.

### 7.1 The human gate under a daemon

This is the piece the desktop app exists for. A run that reaches a human gate —
or a duckling that calls `ask_human` — does **not** block a goroutine waiting on
a terminal. It:

1. checkpoints `state.json` with `status = "paused"` and a `pending_question`
   or `pending_gate` block,
2. emits a `human_needed` event on the bus,
3. returns control to the supervisor; the run occupies no resources.

Any client may then answer via `POST /v1/runs/{id}/answer` or
`/accept` / `/reject`. The engine resumes the run from the checkpoint. If no
client ever answers, the run stays paused indefinitely — that is correct
behaviour, not a hang. `run.pending_since` drives the desktop's inbox and the OS
notification.

## 8. Engine lifecycle

- **One engine per OS user.** Discovery and locking via
  `<state-dir>/ducklab/engine.json` (`02-DATA-MODEL.md §8`).
- **Auto-start**: both the CLI and the desktop app spawn `ducklab-engine` if
  none is running and wait for the health endpoint, then proceed. Disable
  with `--no-autostart` / `[engine] autostart = false`.
- **Readiness is honest** (B-298, 2026-08-28): the engine binds its port
  once, recovers runs, and only then writes `engine.json` and serves on that
  same socket — a caller that finds `engine.json` can trust `/v1/health`. The
  CLI waits while the spawned pid lives (cap 5 min) rather than a fixed
  number of seconds. Recovery reads only the tail of each run's event log
  to restore its sequence; a state dir with ~1,400 runs recovers in ~6 s.
- **Graceful stop**: `SIGTERM` (or `/v1/shutdown`) stops accepting new runs,
  checkpoints every active run at its next safe point, sets them `paused`, and
  exits within `shutdown_grace_s` (default 30). A killed engine is equivalent:
  `I9` means runs resume from disk. Windows uses a stop event; never rely on
  signal semantics beyond `SIGTERM`/`SIGINT`.
- **Version skew**: every client sends `X-Ducklab-Client: <semver>`. The engine
  refuses a client with a different **major** version (HTTP 409) and the desktop
  app offers to restart the engine.
- **Crash recovery**: on start the engine scans every registered project for
  runs in status `running` whose pid is gone, marks them `paused` with reason
  `engine_restart`, and emits a notification.

## 9. Concurrency model

- The engine runs **up to `max_concurrent_runs`** (default 2) across all
  projects, and **at most one run per project** unless `--parallel`.
- Additional runs queue in status `queued`, visible to clients.
- Within a run, independent turns of a round (tournament contestants, split
  subtasks) execute concurrently in their own git worktrees, created and removed
  by `internal/vcs`, including on abort and on engine restart (orphan worktrees
  are reaped at startup).
- Advisory project lock at `.ducklab/lock` (pid + run id); a stale lock whose pid
  is dead is broken with a warning.
- `internal/store`: one connection per project, WAL, `busy_timeout=5000`, all
  writes serialised.

## 10. Error handling and exit codes

CLI exit codes are unchanged from `spec-1.0 §8` (0 success, 2 usage, 3 config,
4 lock, 5 gate failed, 6 budget, 7 aborted/gate-required, 8 provider), with two
additions:

| Code | Meaning |
|------|---------|
| 9 | Engine unreachable and auto-start failed. |
| 10 | Engine/client version mismatch. |

The engine maps the same conditions to HTTP status codes; see `07-ENGINE-API.md §5`.

Rules unchanged: one-line user-facing errors, no stack traces, name the file and
key for config causes, `--debug` adds detail.

## 11. Logging and observability

Per run, in `.ducklab/runs/<run-id>/`: `events.jsonl`, `llm.jsonl`,
`verify.log`, `state.json`, `diff.patch`, `transcript.md` — unchanged.

Additionally the engine writes `<state-dir>/ducklab/engine.log` (rolling, 5 MB ×
3): startup, client connections, run supervision, panics. A panic in a run
goroutine is recovered, logged, recorded as an `error` event, and marks the run
`failed` — **it never takes down the engine.**

Every client renders from events. Nothing parses another process's stdout.

## 12. Extension points

| Extension | Mechanism | Phase |
|-----------|-----------|-------|
| New provider | Implement `provider.Provider`, register in `provider.Registry` | v0.1 |
| New tool | Implement `tools.Tool`, register in `tools.Registry` | v0.1 |
| New service capability | Add a `Service` method + an `engineapi` handler + a client method. **All three, always.** | ongoing |
| New mode | Ship a `conv.Script`; user-loadable from `.ducklab/modes/*.toml` | v0.2 / v0.7 |
| New role | Prompt template + contract under `internal/agent/prompts/` | v0.2 |
| Skills | Directories under `.ducklab/skills/` and the global skills dir | v0.5 |
| MCP servers | `[mcp.<name>]` blocks in config | v0.5 |
| Deploy recipes | `[deploy.<name>]` blocks with ordered steps and gates | v0.6 |
