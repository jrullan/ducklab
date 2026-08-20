# 06 — Implementation Phases

`spec-1.1` — revised for the three-binary topology. The engine and the service
layer now come first; the desktop app arrives at v0.3 and becomes primary at
v0.5.

Build in this order. **Do not start a phase until every acceptance criterion of
the previous one passes.** Each phase ends with a tagged commit `vX.Y.0`.

`AC-n` are runnable checks with an observable result. Every package listed under
*Deliverables* needs unit tests; a phase is not done with untested packages.

**A fake provider (`internal/provider/fake`) replaying scripted responses from
JSON is required from v0.1**, and a **fake engine** (recorded event stream
replayer) is required from v0.3, so every criterion below runs in CI with no
live model and no GPU.

---

## v0.1 — Core + engine skeleton

**Goal:** one duckling, one task, real edits, a real gate, a real diff, an accept
that commits — all executed by a headless engine and driven by a thin CLI. No
conversations, no desktop app, no lifecycle stages.

### Deliverables

- `internal/xplat` — shell invocation, config/state/data dirs, atomic write.
- `internal/config` — global + project config, strict TOML decoding, validation,
  env overrides.
- `internal/provider` — `Provider` interface incl. `ChatStream`; `openaicompat`,
  `anthropic`, `fake`; sentinel errors; redaction.
- `internal/duckling`, `internal/budget`.
- `internal/tools` — `Tool`, `Registry`, `ExecContext`, write guard, shell
  policy; `fs_list/read/search/write/patch/delete`, `shell`, `verify_run`,
  `git_status/diff/log`.
- `internal/agent` — the loop, **both dialects**, repair loop, contracts
  `freeform` and `edits`, the `implementer` prompt.
- `internal/vcs`, `internal/verify`, `internal/store` (migration 001),
  `internal/runlog`, `internal/registry`.
- `internal/strategy` — `solo` only.
- `internal/service` — Project*, Duckling*, Run* methods; async `RunStart`.
- `internal/bus` — in-process fan-out.
- `internal/engineapi` — health, projects, ducklings, runs, `/v1/events` SSE,
  auth, OpenAPI document.
- `internal/daemon` — `engine.json`, pidfile, lock, auto-start, graceful stop.
- `internal/engineclt` (generated) and `internal/cli`.
- `cmd/ducklab-engine`, `cmd/ducklab`.

### Acceptance criteria

- **AC-1** `ducklab --version` and `ducklab-engine --version` both print
  `<name> 0.1.0 (<sha>, go1.24+, linux/amd64)`.
- **AC-2** `ducklab project init --yes` in an empty git repo creates
  `.ducklab/project.toml`, `.ducklab/ducklab.db`, extends `.gitignore`, prints
  the detected gate, and registers the project.
- **AC-3** With no engine running, any `ducklab` command auto-starts one within
  10 s; `engine.json` is mode 0600; `ducklab engine status` reports the pid and
  port. With `--no-autostart` and no engine, exit is 9.
- **AC-4** `curl 127.0.0.1:<port>/v1/health` returns 200 **without** a token;
  every other endpoint without the token returns 401. Binding is loopback only —
  connecting from another host on the LAN is refused.
- **AC-5** `ducklab duckling test <id> --prompt "say OK"` prints the reply,
  prompt/completion tokens and `$0.0000` for a local duckling.
- **AC-6** `ducklab run T-001 --dry-run` prints every rendered message and exits
  0 with **no network call** (verify with the provider pointed at
  `http://127.0.0.1:1`).
- **AC-7** Against `fixture-go-red` with a task "make TestAdd pass",
  `ducklab run T-001 --mode solo --yes` produces a diff, runs `go test ./...`,
  reaches `PASSED`, and commits on branch `ducklab/T-001` with the run id in the
  trailer.
- **AC-8** The same run with a `native_tools = false` duckling reaches the same
  result, exercising the `@payload:` protocol on at least one `fs_write`.
- **AC-9** `ducklab run … --no-wait` returns a run id immediately; killing the
  CLI with SIGINT during `--wait` leaves the run **running** and prints the
  detach message. `ducklab run watch <id>` then follows it to completion.
- **AC-10** `kill -TERM` on the engine mid-run leaves a parseable `state.json`
  with status `paused`; restarting the engine marks it `paused` with reason
  `engine_restart`; `ducklab run resume <id>` completes it.
- **AC-11** Write-guard tests: `fs_write` to `../outside.txt` returns an error
  result and emits `policy_violation`; shrinking a 2 KB file to 100 bytes is
  refused; content containing a fenced `ducklab` marker is refused.
- **AC-12** `--budget-usd 0.001` against a priced provider stops with exit 6 and
  verdict `BUDGET_EXCEEDED` **before** the cap is exceeded.
- **AC-13** `fixture-nogate` yields gate `none` and verdict `UNVERIFIED`, never
  `PASSED`.
- **AC-14** Two SSE subscribers attach at different times to the same run; both
  receive every persisted event exactly once, in `seq` order, with no gap
  (assert by diffing each subscriber's stream against `events.jsonl`).
- **AC-15** `CGO_ENABLED=0 GOOS=windows|darwin|linux GOARCH=amd64|arm64 go build
  ./cmd/ducklab ./cmd/ducklab-engine` succeeds for all four targets.
- **AC-16** `go vet ./...` passes and `internal/cli` imports neither `service`
  nor any domain package (assert with a test that inspects the import graph).

---

## v0.2 — Ducklings that talk

**Goal:** the conversation engine and the multi-model modes. The core bet becomes
testable, still headless.

### Deliverables

- `internal/conv` — `Script`, scheduler, transcript state, `Until` evaluator,
  anonymisation.
- `internal/strategy` — `pair`, `tournament`; concurrent contestants in
  worktrees; resolution recording.
- `internal/agent` — contracts `verdict`, `choice`, `json:*`; `reviewer` and
  `judge` prompts; capability probing; streaming path emitting `token_delta`.
- `internal/tools` — `ask_human` with the paused-run mechanism (`01 §7.1`).
- `internal/report` — `report` with the solo baseline comparison.
- Engine: run queueing, `max_concurrent_runs`, `/answer`, `/approve-tool`,
  `human_needed` events, crash recovery, orphan-worktree reaping.
- CLI: `--mode`, `run watch`, `run answer`, `report`, `roster`, `duckling probe`.

### Acceptance criteria

- **AC-17** `ducklab run T-001 --mode pair` shows two distinct ducklings in
  `transcript.md`; round 2's implementer prompt contains round 1's findings.
- **AC-18** The reviewer's request in `llm.jsonl` contains the diff and **zero**
  occurrences of the implementer's duckling id or transcript (I7).
- **AC-19** `tournament` runs contestants concurrently (overlapping timestamps in
  `events.jsonl`) in separate worktrees; worktrees are removed on completion,
  on `abort`, and by the engine's startup reaper after a kill.
- **AC-20** With exactly one green candidate the run resolves `short_circuit` and
  the applied diff is **byte-identical** to that candidate's patch (I8).
- **AC-21** A judge replying with prose triggers exactly 2 repair prompts and
  then fails the turn with `ErrContract` — never a guessed choice (I6).
- **AC-22** `Until` parses `gate == "green" and verdict == "approve"`;
  `os.Exit(1)` as an expression is a load-time error.
- **AC-23** A duckling calling `ask_human` pauses the run (status `paused`,
  checkpointed, zero goroutines blocked — assert with a goroutine count), emits
  `human_needed`, and resumes on `ducklab run answer`. Left unanswered for
  60 s the run stays `paused` and the engine remains responsive.
- **AC-24** `ducklab report --by mode` prints the table of `03-CLI.md §3.10`
  including the `solo baseline:` line.
- **AC-25** With `max_concurrent_runs = 1`, a second run enters `queued`, is
  visible in `run list`, and starts automatically when the first ends.
- **AC-26** `--stream` produces `token_delta` events on `/v1/events`; a
  subscriber that stops reading is dropped with `event: overflow` and the run
  completes unaffected (I11).

---

## v0.3 — The desktop app appears

**Goal:** `ducklab-desktop` exists and can do the thing it is for — watch a run
live and intervene. Not yet at parity with the CLI.

### Deliverables

- `cmd/ducklab-desktop` (Wails v3), `internal/desktop`, engine bundling and
  resolution (`07 §7.1`).
- `frontend/` scaffold: Vite + React + TS + Tailwind, tokens from
  `08-DESKTOP-UI.md §2.1`, routing, layout shell, theme toggle.
- Generated `frontend/src/api/generated.ts` + `make api`.
- SSE subscriber with backoff, `Last-Event-ID`, resync on overflow.
- Views: **Overview**, **Runs list**, **Run** (all four regions), **Ducklings**,
  **Settings**.
- Components: `StatTile`, `StatusChip`, `DiffView`, `ConversationLane`,
  `ToolTimeline`, `BudgetMeter`, `GateCard`, `CandidateCard`, `DuckAvatar`,
  `EmptyState`, `HumanGateInbox`.
- `internal/xplat` — OS notifications.
- Fake engine binary for frontend tests.

### Acceptance criteria

- **AC-27** `wails3 build` produces a launchable app on Linux, macOS and Windows;
  the bundled engine is used even when a different `ducklab-engine` is on `$PATH`.
- **AC-28** Starting a run from the **CLI** appears live in the desktop Run view
  within 1 s, and vice versa — neither client holds state (I11).
- **AC-29** Closing the desktop window during a run does not stop it; reopening
  shows the run with its full backlog and no gap or duplicate.
- **AC-30** Killing the engine while the app is open turns the status dot
  `critical`, dims the last known state by 40 %, disables engine-requiring
  actions, and recovers automatically when the engine returns — the UI never
  blanks.
- **AC-31** A run reaching a human gate raises an OS notification and a tray
  badge; the Human-gate inbox lists it with the waiting duration; accepting from
  the inbox commits and the CLI's `run show` reflects it.
- **AC-32** In `tournament`, anonymised turns render as `A`/`B` and **the
  mapping is absent from the frontend's data**, not merely hidden (assert by
  inspecting the API payload for candidate authorship: it must not be present).
- **AC-33** A run with 5000 events scrolls at ≥ 55 fps in the conversation lane
  (virtualised); `token_delta` updates are batched per animation frame, verified
  by a render-count assertion.
- **AC-34** Accepting a run shows a pending state until the engine confirms; a
  forced engine failure during accept leaves the UI showing an error, never a
  phantom commit.
- **AC-35** Playwright suite against the fake engine covers: live streaming,
  reconnect with `Last-Event-ID`, overflow resync, human-gate answering, and
  abort. No test requires a model.
- **AC-36** Light and dark themes both pass an automated contrast check on text
  and on every status chip; every status chip renders icon + label + color.

---

## v0.4 — The front of the cycle

**Goal:** requirements, spec, plan, tasks, traceability, project memory — in both
clients.

### Deliverables

- `internal/artifact` — frontmatter, REQ/SPEC/M section parsing, proposal +
  diff + promote flow.
- `internal/store` — migration 002: `requirement`, `spec_section`, `milestone`,
  `task`, `traceability`; transactional sequence allocation.
- `internal/stage` — `intake`, `spec`, `plan`; the `council` script; `architect`
  and `scribe` prompts; conditional `human` turn.
- `internal/tools` — `artifact_read`, `task_read`.
- Project memory `docs/project.md`: inference from git history, 8 KB folding,
  injection into every turn. Failed-attempt memory (`04 §1.5`).
- Engine endpoints for artifacts, stages, tasks, trace.
- Desktop: **Cycle** view (three tabs, traceability rail, document diff, live
  council drawer), **Board** view (tasks).
- CLI: `intake`, `spec`, `plan`, `task *`, `trace check/show`,
  `project describe/status`.

### Acceptance criteria

- **AC-37** `ducklab intake --from brief.txt --yes` produces
  `docs/requirements.md` with `## REQ-001 — …` sections, frontmatter carrying the
  run id, and matching `requirement` rows.
- **AC-38** A stage writes `docs/spec.md.proposed` first; rejecting leaves
  `docs/spec.md` untouched and the proposal on disk; accepting promotes it,
  syncs the DB and runs `trace check`.
- **AC-39** `ducklab plan --yes` creates milestones and tasks whose ids match the
  `plan.md` headings exactly.
- **AC-40** `trace check` exits non-zero naming `orphan_requirement REQ-00N` when
  a `must` requirement has no spec section; exits 0 on a fully linked project.
  The desktop Cycle rail shows the same orphan with a `⚠` and routes to it.
- **AC-41** Re-running a task that failed injects the first attempt's summary
  into the second run's implementer prompt (grep `llm.jsonl`).
- **AC-42** `project.md` after 200 accepted tasks is ≤ 8192 bytes and contains
  the folding line.
- **AC-43** In the desktop Cycle view, a `council` run's `human` turn appears as
  an inline input; answering it advances the conversation in place.

---

## v0.5 — Parity, bugs, review, PRs

**Goal:** the desktop app becomes the primary face — every CLI capability has a
UI. Plus the operate and review stages.

### Deliverables

- `internal/store` — migration 003: `bug`, `release`.
- `internal/stage` — `operate` (triage) and `review`.
- `internal/agent` — `triager` prompt + `json:triage` contract; review rendering.
- `internal/tools` — `bug_read`.
- GitHub integration **through the `gh` binary only** (no API client, no token
  handling): `bug import --github`, `review pr`, optional issue mirroring.
- Test-tampering guard (`05 §5.3`).
- Desktop: **Board** bugs, **Review** view, command palette, keyboard map, all
  four states per view, pop-out windows.
- CLI: `bug *`, `review`, `run gc`.

### Acceptance criteria

- **AC-44** Parity audit: a generated table maps every `service.Service` method
  to its CLI command and its desktop entry point; **no method may be missing
  either** (this test is the enforcement of `01 §1.2` and must fail the build).
- **AC-45** `ducklab bug add … --severity high` creates `B-001`; `bug triage
  B-001` proposes severity, suspected files and a task title **without** mutating
  status under `guarded`.
- **AC-46** Triage of a duplicate proposes `duplicate_of`; accepting sets status
  and link, rejecting leaves both open.
- **AC-47** `bug promote B-001` creates a task with a `bug → task` edge;
  accepting that task's run sets `B-001` to `fixed`; `bug verify` moves it to
  `verified`.
- **AC-48** `review T-003` writes `docs/reviews/T-003.md` with `file:line`
  findings; a red gate makes `approve` unavailable and the artifact records the
  constraint. The desktop Review view shows the same and can convert a finding to
  a bug in one action.
- **AC-49** A run whose diff touches `*_test.go` for a task that never mentions
  tests is flagged `tests_modified`; the desktop Diff tab pins those hunks to the
  top under the warning banner.
- **AC-50** With `gh` absent, every GitHub command fails with one line and exit
  3; no other command is affected.
- **AC-51** Pop-out: `⌘⇧O` opens a second window on `/runs/:id` that streams
  independently; closing it does not affect the run or the main window.

---

## v0.6 — Extensibility: skills and MCP

### Deliverables

- `internal/skill` — discovery (project shadows global), `SKILL.md` parsing,
  validation, invocation with env + args, timeout.
- `internal/mcpc` — stdio client manager, lazy start, tool listing, call
  proxying, failure isolation.
- `internal/tools` — `skill_list`, `skill_read`, `skill_run`, `mcp_call`.
- Desktop **Skills** view; MCP servers in Settings.
- CLI `skill *`, `mcp *`.

### Acceptance criteria

- **AC-52** A documentation-only skill is discovered, listed with its
  description, and returned by `skill_read`.
- **AC-53** `skill validate` rejects a skill whose description does not say when
  to use it, and one with a missing `entry` file.
- **AC-54** An executable skill runs under the shell policy, receives
  `DUCKLAB_ARG_PATH`, and times out at `timeout_s` with partial output captured.
- **AC-55** A project skill shadows a global skill of the same name.
- **AC-56** A failing MCP server is reported once and the run completes; a
  working server's tools are callable via `mcp_call`; an MCP tool named
  `fs_write` does **not** shadow the native tool.
- **AC-57** A duckling writes `.ducklab/skills/foo/SKILL.md` during a run; the
  skill shows as `pending acceptance` in the Skills view, is unusable until the
  run is accepted, and `skill validate` runs on the diff automatically.

---

## v0.7 — Release, deploy, measurement

### Deliverables

- `internal/stage` — `release`; changelog from accepted runs; `scribe` turn.
- Deploy recipe runner: ordered steps, expected exit codes, rollback, per-step
  events.
- `ducklab bench` + a versioned standard suite (`bench/std/`, ≥ 10 self-contained
  tasks with tests).
- Desktop **Release** view (live step monitor) and **Reports** view with the
  chart set of `08-DESKTOP-UI.md §4.7`.
- `ducklab cost`; report segmentation by duckling, role, mode.

### Acceptance criteria

- **AC-58** `release plan --bump minor` produces `docs/releases/0.2.0.md` listing
  only user-visible accepted work, and exactly `No user-visible changes.` when
  there is none.
- **AC-59** `deploy staging --dry-run` prints steps without running them; a
  failing step with `on_fail = "rollback"` runs the rollback and exits non-zero.
  The desktop Release view shows each step transitioning live and has **no**
  conversation lane (no model is involved).
- **AC-60** `bench --suite std --ducklings a --modes solo,pair` runs every suite
  task in a temp worktree and writes a results JSON; re-running is structurally
  reproducible.
- **AC-61** `report --by duckling` separates measured from estimated token counts
  and never sums them without the `~` marker; the desktop table shows the same.
- **AC-62** Charts obey the palette rules: outcome mix uses the **status**
  palette with icon+label legends; mode comparison uses categorical slots in
  fixed order; no chart has two y-axes; every chart has a working Table toggle;
  filtering a report does not repaint the surviving series.

---

## v0.8 — Split, and opening the harness

### Deliverables

- `split` mode: decomposition contract, deterministic disjoint-file validation,
  concurrent worktrees, copy-based integration, seam-fixing rounds.
- User-defined modes from `.ducklab/modes/*.toml`, validated against the
  toolbelt-narrowing and `Until` grammar rules.
- Escalation ladder `--escalate`: `solo` → `pair` → `tournament`, recording
  "solo could not, the combination could".
- Network resilience polish; `run gc`.
- Signed/notarised desktop builds and prebuilt CLI+engine binaries for
  linux/amd64, linux/arm64, darwin/arm64, windows/amd64.

### Acceptance criteria

- **AC-63** A decomposition where two subtasks claim the same file is rejected
  deterministically, retried once with the conflict named, then aborted — no
  model is ever asked to merge overlapping files.
- **AC-64** A successful `split` integration performs only file copies: assert
  from `llm.jsonl` that **no model call occurs** between the last subtask's
  completion and the gate run.
- **AC-65** A user mode in `.ducklab/modes/x.toml` that widens a role's toolbelt
  fails to load with a named error.
- **AC-66** `run T-001 --escalate` where solo fails and pair passes records both
  runs, linked, and `report` shows the escalation as a distinct row.
- **AC-67** The desktop Run view renders a `split` run as parallel subtask lanes
  with a distinct integration step marked `deterministic — no model involved`.

---

## v1.0 — Done means

- Everything above, on Linux, macOS and Windows.
- Documentation: `README.md`, `docs/getting-started.md`, `docs/modes.md`,
  `docs/skills.md`, `docs/engine-api.md`, a worked example repo.
- `go test ./...`, `vitest run` and the Playwright suite green in CI on all three
  platforms, with no live model required.
- The parity audit (AC-44) green.
- A published `bench/std` result table for ≥ 3 duckling configurations, showing
  the delta between `solo` and the multi-model modes — the number the whole
  project exists to produce.
- Zero known violations of the invariants in `01-ARCHITECTURE.md §3`.

---

## Appendix A — fixtures

| Fixture | Contents | Used by |
|---------|----------|---------|
| `fixture-go-red` | Go module, buggy `add.go`, failing `add_test.go` | AC-7, AC-17, AC-20 |
| `fixture-go-green` | same, passing | baseline / already-red gate tests |
| `fixture-nogate` | markdown only | AC-13 |
| `fixture-py` | pytest project, one red test | gate detection |
| `fixture-big` | 60 files across 4 packages | `split`, file-tree capping |

## Appendix B — test doubles

| Double | Replaces | Required from |
|--------|----------|---------------|
| `internal/provider/fake` | a model endpoint; replays scripted responses, can assert on requests | v0.1 |
| `cmd/fake-engine` | `ducklab-engine`; serves a recorded event stream and canned API responses over the real HTTP contract | v0.3 |

Neither double may be reachable in a release build: both live behind a build tag
or in `cmd/` paths excluded from the release matrix.
