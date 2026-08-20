# 03 — Command-Line Client

`spec-1.1` — revised: the CLI is now a **client of the engine**, at full parity
with the desktop app. The Charm TUI of `spec-1.0` is dropped.

## 1. Position

`ducklab` is a thin client. It discovers or auto-starts `ducklab-engine`
(`07-ENGINE-API.md §7.1`), makes one or more API calls, renders, and exits. It
imports `internal/engineclt` and `internal/daemon` and **nothing else**
(`01-ARCHITECTURE.md §4.1`) — that import restriction is what mechanically
guarantees the CLI cannot grow behaviour the desktop app lacks.

Why the CLI stays at full parity when the desktop app is primary:

- Ducklab is being built by Hermes, headlessly.
- Remote and SSH-only machines have no desktop.
- CI and scripts need it.
- The command palette teaches it (`08-DESKTOP-UI.md §3.1`), so the two surfaces
  reinforce each other.

## 2. Grammar

```
ducklab [global-flags] <noun> <verb> [args] [flags]
ducklab                              # no args → status summary, then exit 0
```

Bare `ducklab` prints a compact project + engine status and a hint about
`ducklab --help` and the desktop app. It never enters an interactive UI.

### 2.1 Global flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--repo <path>` | `.` | Project root. Resolved to an absolute path and sent to the engine. |
| `--config <path>` | platform config dir | Alternative `config.toml` (engine-side; forces an engine using that config). |
| `--output text\|json` | `text` | `json` prints one JSON document to stdout and nothing else. |
| `--yes` | false | Assume "yes" for confirmations. Does **not** change autonomy level. |
| `--wait` / `--no-wait` | `--wait` | Whether to follow a `202`-accepted operation to completion. |
| `--quiet` | false | Suppress progress; errors still print. |
| `--debug` | false | Verbose logs to stderr, including engine request/response. |
| `--no-color` | auto | Also honours `NO_COLOR` and non-TTY stdout. |
| `--no-autostart` | false | Fail with exit 9 instead of spawning an engine. |
| `--version` | | `ducklab <semver> (<commit>, go<ver>, <os>/<arch>)`, exit 0. |

With `--output json`, all human text goes to stderr; stdout carries only the JSON
document (§4).

### 2.2 The `--wait` contract

Because the engine is asynchronous (`07 §3`), every long operation returns
immediately. The CLI's default `--wait` restores blocking semantics for humans
and scripts: it subscribes to `/v1/events` and renders progress until the run
reaches a terminal or paused state, then exits with the mapped code.

`--no-wait` prints the run id and exits 0 — the fire-and-forget form, for
launching work you will watch in the desktop app.

A `--wait` CLI that is interrupted with Ctrl-C **detaches**; it does not abort
the run. It prints: `detached; run continues — ducklab run watch <id>`. Aborting
requires `ducklab run abort <id>`. This is the behavioural consequence of the
daemon split and must be implemented exactly.

## 3. Commands

### 3.1 `engine`

```
ducklab engine status                  # pid, port, version, uptime, active runs
ducklab engine start [--foreground]
ducklab engine stop [--grace 30]
ducklab engine restart
ducklab engine log [--tail 200] [--follow]
```

### 3.2 `project`

```
ducklab project init [--name <name>] [--describe <text>] [--git-init]
ducklab project list
ducklab project show
ducklab project describe <text>
ducklab project set <key> <value>          # dotted key into project.toml
ducklab project status
```

`init` creates `.ducklab/`, runs migrations, writes `project.toml`, extends
`.gitignore`, auto-detects the verify gate and prints it, registers the project
with the engine, and offers to infer a description from git history (`--yes`
accepts the inferred text). On an existing project it is a no-op that prints the
project and exits 0. If the repo has no `.git`, it asks before `git init`;
`--git-init` or `--yes` does it silently.

### 3.3 `provider`, `duckling`, `roster`

```
ducklab provider list                      # id, kind, base url, reachable, auth
ducklab provider check [<id>]              # exit 8 if unreachable

ducklab duckling list
ducklab duckling add <id> --provider <p> --model <m> [--role r]... [--temp f]
ducklab duckling probe <id>
ducklab duckling test <id> [--prompt <s>] [--stream]
ducklab duckling remove <id>

ducklab roster show
ducklab roster set <role> <duckling-id>
ducklab roster suggest [--budget <usd>] [--apply]
```

`duckling list` marks unreachable ducklings rather than failing, so it works
offline. `roster suggest` is deterministic — it ranks by recorded success rate
per role, breaks ties by cost, prints the evidence, and **never calls a model**.

### 3.4 Lifecycle stages

```
ducklab intake [--mode council] [--from <file>]
ducklab spec   [--mode council]
ducklab plan   [--mode council]
```

Each runs the stage conversation, writes `docs/<name>.md.proposed`, shows a diff
against the current artifact, and waits at the human gate. On accept the proposal
is promoted, the DB is synced and `trace check` runs. On reject the proposal is
kept and the run closes `UNVERIFIED`.

### 3.5 `task`

```
ducklab task list [--status s] [--milestone m]
ducklab task add <title> [--implements SPEC-004] [--complexity medium] [--depends T-001]
ducklab task show <id>
ducklab task set <id> <key> <value>
ducklab task next
```

### 3.6 `run`

```
ducklab run <task-id> [--mode solo|pair|tournament|council|split]
                      [--ducklings a,b,c] [--rounds n]
                      [--verify "<cmd>"|auto|none]
                      [--budget-usd f] [--max-tokens n] [--timeout s]
                      [--autonomy manual|guarded|auto|yolo]
                      [--stream] [--dry-run] [--parallel] [--unsafe-writes]
ducklab run resume <run-id>
ducklab run list [--status s] [--project p]
ducklab run show <run-id> [--transcript] [--diff] [--llm] [--candidates]
ducklab run watch <run-id>                 # attach to the event stream and render
ducklab run accept <run-id> [--message <commit msg>]
ducklab run reject <run-id> [--reason <text>]
ducklab run answer <run-id> --question <qid> --answer <text>
ducklab run abort <run-id>
ducklab run gc [--older-than 30d] [--keep-accepted]
```

`--dry-run` renders every prompt that would be sent, prints them, and exits 0
without any model call. It is the primary debugging tool and ships in v0.1.

`run watch` is the terminal equivalent of the desktop Run view: it renders turn
boundaries, collapsed tool calls, budget meters as text, and the gate result. It
is read-only except that it surfaces a pending human gate and tells you which
command answers it. Multiple `watch` clients on one run are fine.

Under `guarded` autonomy `run` blocks at the human gate; with `--yes` it
auto-accepts a `PASSED` verdict but **never** `UNVERIFIED` or `FAILED`.

### 3.7 `bug`

```
ducklab bug add <title> [--body <text>|--body-file <f>] [--severity high] [--reporter <who>]
ducklab bug list [--status s] [--severity s]
ducklab bug show <id>
ducklab bug triage [<id>...]
ducklab bug promote <id> [--title <t>]
ducklab bug set <id> <key> <value>
ducklab bug verify <id>
ducklab bug import --github
```

### 3.8 `review`, `release`, `deploy`

```
ducklab review <task-id|run-id> [--mode solo|council]
ducklab review pr <number>

ducklab release plan [--bump major|minor|patch]
ducklab release cut <version>

ducklab deploy list
ducklab deploy <recipe> [--dry-run]
```

### 3.9 `skill`, `mcp`

```
ducklab skill list [--scope project|global|all]
ducklab skill show <name>
ducklab skill new <name> [--from-run <run-id>]
ducklab skill run <name> [--arg k=v]...
ducklab skill validate <name>

ducklab mcp list
ducklab mcp tools <server>
ducklab mcp call <server> <tool> --arg k=v
```

### 3.10 Reporting

```
ducklab report [--since 30d] [--by duckling|mode|role|task]
ducklab bench [--suite std] [--ducklings a,b] [--modes solo,pair]
ducklab trace check
ducklab trace show <id>
ducklab cost [--since 30d]
```

`ducklab report` must print at least:

```
mode        runs  passed  unverified  failed  avg_tokens  avg_usd  total_usd  avg_wall
solo          42      27           4      11      18_400    0.000      0.000     1m12s
pair          19      15           1       3      52_100    0.000      0.000     4m03s
tournament     8       7           0       1      96_700    0.014      0.112     6m41s

solo baseline: 64.3% passed
pair:          78.9% passed  (+14.6 pts, n=19)
```

`ducklab bench` runs a versioned suite of self-contained tasks against the given
ducklings and modes, in temporary worktrees, writing results to
`<data-dir>/ducklab/bench/<suite>/<ts>.json`.

## 4. JSON output

One document on stdout, in this envelope:

```json
{"ok": true, "command": "run", "data": { … }}
{"ok": false, "command": "run", "error": {"code": 5, "message": "gate failed: go test ./... exited 1"}}
```

| Command | `data` |
|---------|--------|
| `run` | the `Run` object (`01 §4.6` / `07 §4.6`) plus `"diff_path"` |
| `task list` | `{"tasks":[…]}` |
| `bug list` | `{"bugs":[…]}` |
| `report` | `{"rows":[…],"baseline":"solo"}` |
| `trace check` | `{"errors":[{"kind":"orphan_requirement","id":"REQ-007"}]}` |
| `duckling list` | `{"ducklings":[…]}` |
| `engine status` | `{"pid":…,"port":…,"version":"…","active_runs":1}` |

`--output json` implies `--quiet` and never prompts. A command that would need a
human gate returns `ok:false` with code 7 and `"human gate required"` unless
`--yes` was given.

## 5. Conventions

- Idempotent where possible: `project init` on an existing project, `task add`
  with an existing title (warns, returns the existing id under `--yes`).
- Anything that writes shows what it will write first, unless `--yes`.
- Times printed in local time, stored in UTC.
- Money: 4 decimals under $1, 2 above.
- Ids accepted case-insensitively, with or without prefix: `3`, `T-3`, `t-003`
  all resolve to `T-003`.
- Exit codes per `01-ARCHITECTURE.md §10`, including the new `9` (engine
  unreachable) and `10` (version skew).
