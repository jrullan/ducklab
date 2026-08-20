# 02 — Data Model

`spec-1.2` — revised: seat assignment becomes canonical `mode_seats` (§2.3),
replacing positional mode line-ups; project `[roster]` pins are documented as
the project scope of the same model. `spec-1.1` added the engine's runtime
state and the global project registry.

Everything Ducklab knows lives in five places. There is no hidden state.

| Location | Contents | Committed to git? |
|----------|----------|-------------------|
| `<config-dir>/ducklab/config.toml` | Providers, ducklings, global defaults | n/a (user's machine) |
| `<data-dir>/ducklab/` | Capability cache, global skills, price table, project registry, bench results | n/a |
| `<state-dir>/ducklab/` | Engine runtime state: `engine.json`, lock, logs, window state | n/a — ephemeral |
| `<repo>/.ducklab/` | Project config, SQLite db, artifacts, runs | **partially** — see §1.3 |
| Environment | API keys only (I10) | never |

Directory resolution is `internal/xplat`'s job; never write a literal path.

| Symbol | Linux | macOS | Windows |
|---|---|---|---|
| `<config-dir>` | `$XDG_CONFIG_HOME` or `~/.config` | `~/Library/Application Support` | `%AppData%` |
| `<data-dir>` | `$XDG_DATA_HOME` or `~/.local/share` | `~/Library/Application Support` | `%LocalAppData%` |
| `<state-dir>` | `$XDG_STATE_HOME` or `~/.local/state` | `~/Library/Application Support` | `%LocalAppData%` |

---

## 1. Filesystem layout

### 1.1 Global

```
<config-dir>/ducklab/
  config.toml            # providers, ducklings, defaults
<data-dir>/ducklab/
  caps.json              # probed capabilities cache, keyed by "provider:model"
  prices.json            # optional model price overrides
  projects.json          # global project registry (§9)
  skills/                # global skills (see 05 §7)
  bench/<suite>/*.json   # bench results
<state-dir>/ducklab/
  engine.json            # pid, port, token, version — mode 0600 (07 §1)
  engine.lock            # exclusive-create guard against concurrent auto-starts
  engine.log             # rolling engine log, 5 MB × 3
  window.json            # desktop window geometry, per machine
```

### 1.2 Per project

```
<repo>/.ducklab/
  project.toml           # project identity, verify commands, roster, autonomy
  ducklab.db             # SQLite
  lock                   # advisory run lock (pid + run id)
  docs/
    requirements.md      # artifact: REQ-*
    spec.md              # artifact: SPEC-*
    plan.md              # artifact: milestones + tasks
    project.md           # rolling project memory (see §5)
    decisions/ADR-001-*.md
    reviews/<task>.md
    releases/<version>.md
  modes/                 # optional user-defined conversation scripts (v0.7)
  skills/                # project-local skills
  runs/<run-id>/
    state.json
    events.jsonl
    llm.jsonl
    verify.log
    diff.patch
    candidates/A.patch   # tournament/split only
    transcript.md        # human-readable rendering of the conversation
```

### 1.3 Git tracking

`ducklab project init` writes/extends `<repo>/.gitignore` with:

```
.ducklab/runs/
.ducklab/ducklab.db
.ducklab/ducklab.db-wal
.ducklab/ducklab.db-shm
.ducklab/lock
```

`.ducklab/docs/`, `project.toml`, `modes/` and `skills/` **are** intended to be
committed — they are the project's specification and are as valuable as the code.
Do not gitignore them.

---

## 2. Global config — `config.toml`

Complete example with every key. Keys not shown do not exist.

```toml
schema = 1

[defaults]
autonomy            = "guarded"   # manual | guarded | auto | yolo
mode                = "solo"
repair_attempts     = 2
tool_result_max_bytes = 32768
agent_max_turns     = 24          # per conversation turn (agent loop cap)
http_timeout_s      = 300
transient_retries   = 3

[defaults.budget]
max_usd       = 2.00
max_tokens    = 400000
max_wallclock_s = 1800
max_turns     = 24

# ---------- engine ----------

[engine]
autostart           = true    # clients may spawn the engine
port                = 0       # 0 = ephemeral, published in engine.json
path                = ""      # explicit ducklab-engine path; "" = auto-resolve
max_concurrent_runs = 2
shutdown_grace_s    = 30
project_memory_max_bytes = 8192

# ---------- providers ----------

[provider.beelink]
kind     = "openai"                    # openai | anthropic
base_url = "http://localhost:8081/v1"
api_key_env = "DUCKLAB_BEELINK_API_KEY" # optional; omit for keyless local
headers  = { }                          # optional extra headers

[provider.aitopatom]
kind     = "openai"
base_url = "http://10.0.0.5:8000/v1"

[provider.openrouter]
kind        = "openai"
base_url    = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"
headers     = { "HTTP-Referer" = "https://github.com/jrullan/ducklab", "X-Title" = "ducklab" }

[provider.anthropic]
kind        = "anthropic"
base_url    = "https://api.anthropic.com"
api_key_env = "ANTHROPIC_API_KEY"

# ---------- ducklings ----------

[duckling.pato-local]
provider = "beelink"
model    = "gemma-4-26b-a4b"
roles    = []                  # empty = eligible for any role
notes    = "fast local generalist"

[duckling.pato-local.params]
temperature = 0.2
top_p       = 0.95
max_tokens  = 8192
disable_thinking = true
stop        = []

[duckling.pato-local.caps]
native_tools   = false         # omit to auto-probe
context_tokens = 65536

[duckling.pato-local.cost]
input_per_mtok  = 0.0          # local models are free in USD
output_per_mtok = 0.0

[duckling.pato-nube]
provider = "openrouter"
model    = "qwen/qwen3.6-35b-a3b"
roles    = ["implementer", "reviewer", "judge"]

[duckling.pato-nube.params]
temperature = 0.3
max_tokens  = 16384

[duckling.pato-nube.cost]
input_per_mtok  = 0.20
output_per_mtok = 0.60

# ---------- mcp (v0.5) ----------

[mcp.piplc]
command = "piplc-mcp"
args    = ["--stdio"]
env     = { }
enabled = true
```

### 2.1 Validation rules

On load, `internal/config` must reject with exit code 3 and a one-line message:

- unknown top-level table or key (typo protection — **strict decoding required**)
- `schema` ≠ 1
- a `duckling.*` referencing a `provider` that is not defined
- an id not matching `[a-z0-9][a-z0-9-]{0,31}`
- `kind` not in {`openai`, `anthropic`}
- `base_url` that does not parse as an absolute URL
- `autonomy` not in {manual, guarded, auto, yolo}
- negative or zero budget values
- a `roles` entry not in the Role enum

Missing `api_key_env` is **not** an error; it means keyless. An `api_key_env`
naming an unset variable is an error only at first use, reported as exit 8.

### 2.2 Environment overrides

For scripting and CI, every provider field may be overridden without editing the
file:

```
DUCKLAB_PROVIDER_<NAME>_BASE_URL
DUCKLAB_PROVIDER_<NAME>_API_KEY      # value, not the variable name
DUCKLAB_DUCKLING_<NAME>_MODEL
DUCKLAB_CONFIG                       # path to an alternative config.toml
```

`<NAME>` is the id uppercased with `-` → `_`. Environment wins over file.

---

### 2.3 Seats — `[defaults.mode_seats]` and `[roster]` (spec-1.2)

Who sits where is one model with two scopes, so it can have one visible home
(the desktop's Roster board and the MCP roster tool) and no invisible
government.

```toml
[defaults.mode_seats]            # global: mode → real role name → ORDERED duckling ids
  [defaults.mode_seats.solo]
  implementer = ["terra"]
  advisor     = ["k3"]
  [defaults.mode_seats.pair]
  implementer = ["luna"]
  advisor     = ["k3"]
  reviewer    = ["glm52"]
  [defaults.mode_seats.council]
  architect   = ["terra"]
  reviewer    = ["glm52", "pato-sonnet"]     # N critics, in order
  [defaults.mode_seats.split]
  architect   = ["terra"]                    # the orchestrator
  implementer = ["luna", "qwen38-max"]       # N workers, in order
  reviewer    = ["glm52"]
  [defaults.mode_seats.tournament]
  implementer = ["luna", "qwen38-max"]       # N contestants, in order
  judge       = ["glm52"]

[roster]                          # global, mode-independent "common" roles and fallbacks
triager = "terra"
scribe  = "beelink-local"
```

Rules:

- **Roles, not positions.** Every seat is addressed by mode and real role name
  (`architect`, `implementer`, `advisor`, `reviewer`, `judge`); the old
  positional `mode_ducklings` (first = implementer, second = reviewer, …) is
  retired. A configuration that still carries `mode_ducklings` is migrated
  **one-way and idempotently** into `mode_seats` on load — nothing in the wild
  breaks, and re-running the migration on a canonical file changes nothing.
- **Multi-slot roles** are ordered lists: council critics, split workers,
  tournament contestants. Order is meaning (contestant 1, worker 2).
- **The advisor is per mode**, so a cheap local duck can serve `solo` and a
  strong one `pair`. Mode-independent roles (`triager`, `scribe`) live in
  `[roster]`; a `[roster]` role also serves as the fallback for a mode that
  seats that role but names nobody.
- **Project scope.** A project's `[mode_seats.<mode>]` are per-mode **pins**
  (the same shape as Global); its `[roster]` / `roster_seats` are **role
  pins**, mode-independent — the form for triager and scribe, and the
  project's own fallback for a mode that pins nobody for that role. A pin
  replaces the whole ordered list for that seat; roles not pinned inherit
  Global. Precedence per seat: request → project mode seat → project role
  pin → global mode seat → global role pin. A per-run pick (launcher chips,
  `ducklings` on an MCP launch) outranks all of them, for that run only.
- **No invented seats.** A seat nobody filled stays empty: optional roles
  (the advisor) are never staffed by the engine, and a launch with an empty
  required seat is refused naming the seat. Only a blank installation — no
  seat configured anywhere — lets the engine pick, recorded as `engine picked
  (no seats configured)`, and even then never an advisor.
- **One resolver.** `RosterGet(project, mode)` returns, per seat, the effective
  ordered list and its **provenance** — `project pin | global mode seat |
  global role fallback | request` — and the value a pin overrides. Runs,
  launcher prefill, the desktop board and the MCP roster tool all read this
  one path; a per-run pick is recorded on the run with `roster_sources`.
- **Cardinality is validated at write and at launch**: council needs ≥ 1
  critic, split ≥ 2 workers, tournament ≥ 2 contestants; pair warns when one
  duckling both implements and reviews, council when the architect critiques
  its own draft. Errors name the seat and the rule.

## 3. Project config — `.ducklab/project.toml`

```toml
schema = 1
id     = "miempresa"                # slug, unique per machine, from dir name
name   = "MiEmpresa"
created = "2026-07-25T15:30:12Z"

autonomy = "guarded"

[verify]
mode  = "auto"                      # auto | tests | build | lint | none | custom
tests = "go test ./..."             # used when mode = tests or auto resolves here
build = "go build ./..."
lint  = ""
custom = ""                         # used when mode = "custom"
timeout_s = 900

[roster]
architect   = "pato-nube"
implementer = "pato-local"
reviewer    = "pato-nube"
judge       = "pato-nube"
triager     = "pato-local"
scribe      = "pato-local"

[modes]
build  = "pair"                     # default mode per stage
intake = "council"
spec   = "council"
plan   = "council"
review = "solo"

[budget]                            # overrides global defaults for this project
max_usd = 5.00

[git]
branch_prefix = "ducklab/"          # scratch branches
base_branch   = "main"
commit_trailer = true               # append Duckling/Run trailers to commits

[github]                            # optional, v0.4
enabled = false
repo    = ""                        # "owner/name"; blank = infer from origin
mirror_bugs = false
```

### 3.1 Verify auto-detection

When `verify.mode = "auto"`, `internal/verify` picks the **first** rung that
applies, in this order, and records which one in the run state:

| Rung | Detection | Command |
|------|-----------|---------|
| tests | `go.mod` present and `go test ./... -run XXX -count=1` exits 0 fast | `go test ./...` |
| tests | `pytest.ini`/`pyproject.toml` with `[tool.pytest*]`/`tests/` dir | `pytest -q` |
| tests | `package.json` with a `test` script that is not the npm default stub | `npm test --silent` |
| tests | `Cargo.toml` | `cargo test` |
| build | `go.mod` | `go build ./...` |
| build | `tsconfig.json` | `npx tsc --noEmit` |
| build | any `*.py` | `python -m compileall -q .` |
| lint | `.golangci.yml` | `golangci-lint run` |
| none | otherwise | — |

Detection must not execute the candidate command more than once, and must treat
a missing binary (`exec.ErrNotFound`) as "rung does not apply", falling through.
The chosen gate is displayed to the user before the first run and stored in
`project.toml` as an explicit value so detection does not silently change later.

---

## 4. SQLite schema — `.ducklab/ducklab.db`

Migrations are numbered Go files in `internal/store/migrations`, applied in order
inside one transaction, tracked in `schema_migrations(version INTEGER PRIMARY KEY,
applied_at TEXT)`. Never edit an applied migration; add a new one.

```sql
-- 001_init.sql

CREATE TABLE project (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

CREATE TABLE requirement (
  id          TEXT PRIMARY KEY,          -- REQ-001
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  acceptance  TEXT NOT NULL DEFAULT '',
  priority    TEXT NOT NULL DEFAULT 'should', -- must|should|could|wont
  status      TEXT NOT NULL DEFAULT 'draft',  -- draft|approved|dropped
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE spec_section (
  id          TEXT PRIMARY KEY,          -- SPEC-001
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  status      TEXT NOT NULL DEFAULT 'draft',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE traceability (
  from_kind   TEXT NOT NULL,   -- requirement|spec_section|task|bug|run|release
  from_id     TEXT NOT NULL,
  to_kind     TEXT NOT NULL,
  to_id       TEXT NOT NULL,
  PRIMARY KEY (from_kind, from_id, to_kind, to_id)
);

CREATE TABLE milestone (
  id          TEXT PRIMARY KEY,          -- M-01
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  position    INTEGER NOT NULL,
  status      TEXT NOT NULL DEFAULT 'open' -- open|done
);

CREATE TABLE task (
  id           TEXT PRIMARY KEY,         -- T-001
  title        TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  milestone_id TEXT REFERENCES milestone(id),
  status       TEXT NOT NULL DEFAULT 'todo',
      -- todo|in_progress|blocked|review|accepted|abandoned
  complexity   TEXT NOT NULL DEFAULT 'medium', -- trivial|small|medium|large
  role_hint    TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  depends_on   TEXT NOT NULL DEFAULT '',  -- comma-separated task ids
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE bug (
  id           TEXT PRIMARY KEY,         -- B-001
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  severity     TEXT NOT NULL DEFAULT 'normal', -- critical|high|normal|low
  status       TEXT NOT NULL DEFAULT 'open',
      -- open|triaged|duplicate|wontfix|in_progress|fixed|verified|closed
  duplicate_of TEXT REFERENCES bug(id),
  task_id      TEXT REFERENCES task(id),
  source       TEXT NOT NULL DEFAULT 'manual', -- manual|import|github|run
  reporter     TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE run (
  id           TEXT PRIMARY KEY,         -- r-20260725-153012-k7q2
  stage        TEXT NOT NULL,
  mode         TEXT NOT NULL,
  task_id      TEXT REFERENCES task(id),
  roster_json  TEXT NOT NULL,
  gate         TEXT NOT NULL,            -- tests|build|lint|none|custom
  status       TEXT NOT NULL,            -- running|paused|done|failed
  verdict      TEXT NOT NULL DEFAULT '',
  accepted     INTEGER NOT NULL DEFAULT 0,
  commit_sha   TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL,
  ended_at     TEXT NOT NULL DEFAULT '',
  wallclock_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE llm_call (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id        TEXT NOT NULL REFERENCES run(id),
  seq           INTEGER NOT NULL,
  duckling      TEXT NOT NULL,
  role          TEXT NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  finish_reason TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE release (
  version     TEXT PRIMARY KEY,          -- semver, e.g. 0.3.1
  notes       TEXT NOT NULL DEFAULT '',
  commit_sha  TEXT NOT NULL DEFAULT '',
  deployed_at TEXT NOT NULL DEFAULT '',
  channel     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_bug_status  ON bug(status);
CREATE INDEX idx_run_task    ON run(task_id);
CREATE INDEX idx_llm_run     ON llm_call(run_id, seq);
CREATE INDEX idx_trace_to    ON traceability(to_kind, to_id);
```

All timestamps are RFC3339 UTC strings (`2026-07-25T15:30:12Z`). Booleans are
`INTEGER` 0/1.

### 4.1 The traceability spine

`traceability` is the backbone that makes "full cycle" checkable rather than
aspirational. Required edges, created automatically:

```
requirement → spec_section    (spec stage)
spec_section → task           (plan stage)
task        → run             (build stage)
run         → release         (release stage)
bug         → task            (triage)
```

`ducklab trace check` (see `03-CLI.md`) is a **deterministic** verification, not
a model call. It reports:

- requirements with no spec section (`orphan requirement`)
- spec sections with no task (`unimplemented spec`)
- tasks with no requirement ancestor (`unjustified task`)
- accepted tasks whose requirement is still `draft`

---

## 5. Artifacts

Artifacts are Markdown with YAML frontmatter. Frontmatter is the machine's view;
the body is the human's and the model's.

### 5.1 Frontmatter (all artifacts)

```yaml
---
kind: requirements | spec | plan | adr | review | release | project
project: miempresa
version: 3                 # bumped on every write
updated_at: 2026-07-25T15:30:12Z
run_id: r-20260725-153012-k7q2   # run that produced this version
ducklings: [pato-nube, pato-local]
based_on: 1f4a9c2d8e0b6a31   # hash of the approved doc a proposal was drafted
                             # against; promote refuses on drift (05 §1.1)
origin: adopted              # only when the document was surveyed from the
                             # tree rather than decided by a person
approved_by: human         # or "" while unapproved
---
```

### 5.2 `requirements.md`

Every requirement is an H2 whose text starts with its id. Parsing is by this
rule and nothing else; a section that does not match is preserved verbatim but
not indexed.

```markdown
## REQ-001 — Users can log in with email and password

**Priority:** must
**Status:** approved

Body prose here.

**Acceptance:**
- Given a registered user, when they submit correct credentials, they receive a session.
- Given a wrong password, the response is 401 and no session is created.
```

### 5.3 `spec.md`

```markdown
## SPEC-004 — Session tokens

**Implements:** REQ-001, REQ-007
**Status:** approved

Design prose, interfaces, schemas.
```

`Implements:` is the machine-readable edge into `traceability`. A spec section
without an `Implements:` line is a trace error.

### 5.4 `plan.md`

```markdown
## M-01 — Authentication

### T-003 — Implement session token issuance
**Implements:** SPEC-004
**Complexity:** medium
**Depends on:** T-001
**Role hint:** implementer

What done looks like, in one or two sentences.
```

### 5.5 `project.md` — rolling project memory

A short, always-injected context file. Written by Ducklab, editable by the human,
appended to on every `run accept`.

```markdown
---
kind: project
---
# What we are building
One paragraph, set with `ducklab project describe "…"`. If unset, Ducklab infers
it once from the repo's git history and file tree, shows it, and asks the human
to confirm or edit.

# Conventions
Free-form notes the human wants every duckling to see.

# Accepted work
- 2026-07-25 T-003 Implement session token issuance (r-20260725-153012-k7q2)
```

`project.md` is capped at `project_memory_max_bytes` (8192). When the "Accepted
work" list would exceed it, the oldest entries are folded into a single
`- … and N earlier tasks` line. It is injected into the system prompt of every
turn.

---

## 6. Run directory

### 6.1 `state.json`

The `runlog.Run` struct from `01-ARCHITECTURE.md §4.6`, serialised. Written
atomically: write `state.json.tmp`, `fsync`, `rename`. Never partially written.

### 6.2 `events.jsonl`

One JSON object per line, appended, never rewritten.

```json
{"ts":"2026-07-25T15:30:14.221Z","seq":7,"type":"turn_start","round":1,"turn":0,"role":"implementer","duckling":"pato-local"}
{"ts":"...","seq":8,"type":"tool_call","tool":"fs_read","args_digest":"sha256:ab12…","ok":true,"bytes":1840,"ms":3}
{"ts":"...","seq":9,"type":"policy_violation","tool":"fs_write","detail":"path escapes root: ../../etc/passwd"}
{"ts":"...","seq":10,"type":"turn_end","round":1,"turn":0,"outcome":"ok","tokens_in":5120,"tokens_out":870,"cost_usd":0.0}
{"ts":"...","seq":11,"type":"gate","gate":"tests","cmd":"go test ./...","exit":0,"ms":8210}
{"ts":"...","seq":12,"type":"verdict","verdict":"PASSED"}
{"ts":"...","seq":13,"type":"human","action":"accept","note":""}
```

Event `type` values: `run_start`, `run_end`, `round_start`, `turn_start`,
`turn_end`, `llm_call`, `tool_call`, `policy_violation`, `gate`, `verdict`,
`human`, `checkpoint`, `error`, `budget`.

`seq` is monotonic within a run and is what resume uses to detect a torn tail:
on resume, a final line that does not parse as JSON is truncated.

### 6.3 `llm.jsonl`

```json
{"ts":"...","seq":3,"duckling":"pato-local","provider":"beelink","model":"gemma-4-26b-a4b",
 "request":{"messages":[…],"tools":[…]},
 "response":{"content":"…","tool_calls":[…],"finish_reason":"tool_calls"},
 "usage":{"prompt_tokens":5120,"completion_tokens":870},
 "cost_usd":0.0,"latency_ms":4310,"attempt":1}
```

The `request` object is stored verbatim **except** that any header or field whose
name matches `(?i)(api[-_]?key|authorization|token|secret)` is replaced with
`"[redacted]"` (I10).

### 6.4 `transcript.md`

Human-readable rendering of the conversation produced at run end (and on demand
by `ducklab run show <id> --transcript`). Ducklings appear with their id and
role; tool calls appear as collapsed one-liners; the final artifact or diff is
appended. This file is what the user reads when they want to know *why*.

---

## 7. Capability cache — `caps.json`

```json
{
  "beelink:gemma-4-26b-a4b": {
    "native_tools": false, "json_mode": true, "context_tokens": 65536,
    "vision": true, "probed_at": "2026-07-25T15:00:00Z"
  }
}
```

Entries older than 30 days are re-probed. `ducklab duckling probe <id>` forces
a re-probe. The probe procedure is defined in `04-AGENT-PROTOCOL.md §5`.

---

## 8. Engine runtime state — `engine.json`

Written by `ducklab-engine` at start, deleted on graceful stop. Schema and
semantics in `07-ENGINE-API.md §1`. Mode `0600`; on Windows the ACL is
restricted to the current user. It contains a live bearer token, so it is
**state, not config** — never in a config directory, never in a repo, never
logged.

A stale `engine.json` whose pid is dead is deleted by the next client that
notices, which then proceeds with auto-start.

## 9. Project registry — `projects.json`

The engine serves multiple projects, so it needs to know they exist.

```json
{
  "schema": 1,
  "projects": [
    {"id":"miempresa","path":"/home/jrullan/dev/MiTimesheet","name":"MiEmpresa",
     "last_opened":"2026-07-25T15:30:12Z"}
  ]
}
```

Rules: `id` is the directory basename slugified, deduplicated with a `-2` suffix
on collision. A registry entry whose `path` no longer contains `.ducklab/` is
marked `missing` in listings but never auto-removed — the user may have the drive
unmounted. Unregistering never deletes files (`07 §4.2`).
