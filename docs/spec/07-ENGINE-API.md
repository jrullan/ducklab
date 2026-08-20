# 07 — Engine API

`spec-1.1` — new in this revision.

`ducklab-engine` is the only process that executes work. The CLI and the desktop
app are clients. This document is the contract between them.

---

## 1. Transport, binding and auth

- **HTTP/1.1 + JSON** over TCP on `127.0.0.1` only. Never `0.0.0.0`, never a
  configurable bind address in v1 (I12).
- Port: from `[engine] port` if set, otherwise an ephemeral port chosen at start.
- **Bearer token**, 32 random bytes hex-encoded, regenerated on every engine
  start.
- Both are published in `<state-dir>/ducklab/engine.json`, written atomically
  with mode `0600` (Windows: ACL restricted to the current user):

```json
{
  "pid": 48122,
  "port": 51473,
  "token": "9f2c…",
  "version": "0.2.0",
  "started_at": "2026-07-25T15:30:12Z",
  "state_dir": "/home/jrullan/.local/state/ducklab"
}
```

- Every request carries `Authorization: Bearer <token>` and
  `X-Ducklab-Client: <name>/<semver>`. A missing or wrong token → `401`. A
  client whose **major** version differs from the engine's → `409` with
  `{"error":{"code":"version_skew", …}}`.
- CORS: the engine sets `Access-Control-Allow-Origin` to the Wails app origin
  (`wails://…` / `http://wails.localhost`) and nothing else. There is no
  wildcard.
- Requests time out server-side at `http_timeout_s`; long work never happens
  inside a request (§3).

## 2. Conventions

- Base path `/v1`. Content type `application/json; charset=utf-8`.
- Success: the resource object, bare (no envelope).
- Error: `{"error":{"code":"<slug>","message":"one line","details":{…}}}`.
- Timestamps RFC3339 UTC. Money as a JSON number in USD.
- Every mutating endpoint accepts an optional `Idempotency-Key` header; a repeat
  with the same key within 10 minutes returns the original response.
- Collections are `{"items":[…],"total":N}`. Cursor pagination via `?after=<id>&limit=`.
- All ids are accepted in the loose forms of `03-CLI.md §5` (`3`, `T-3`,
  `t-003`) and returned canonically.

## 3. The asynchrony rule

**No request blocks on model work.** Anything that would take more than a few
seconds returns `202 Accepted` with the created resource, and the caller watches
`/v1/events`. This applies to: starting or resuming a run, probing a duckling,
running a stage, triaging bugs, running a deploy recipe, running a bench suite.

Endpoints that may block (bounded, < 5 s): config reads, DB queries, diff
generation, trace checks.

## 4. Endpoints

### 4.1 Engine

| Method | Path | Body / Query | Returns |
|---|---|---|---|
| GET | `/v1/health` | — | `{"ok":true,"version":"0.2.0","uptime_s":812,"active_runs":1}` — **no auth required** |
| GET | `/v1/engine` | — | engine info, config summary, concurrency slots |
| POST | `/v1/shutdown` | `{"grace_s":30}` | `202`, graceful stop per `01 §8` |
| GET | `/v1/log` | `?tail=200` | plain text engine log |

`/v1/health` is unauthenticated so a client can detect a live engine before
reading the token file. It exposes nothing sensitive.

### 4.2 Projects

| Method | Path | Body / Query | Returns |
|---|---|---|---|
| GET | `/v1/projects` | — | registered projects, most-recent first |
| POST | `/v1/projects` | `{"path":"/abs/path","name":"…","describe":"…","git_init":bool}` | opens or initialises; `201` |
| GET | `/v1/projects/{id}` | — | project config, gate, roster, autonomy |
| PATCH | `/v1/projects/{id}` | `{"autonomy":"guarded","verify":{…},"roster":{…}}` | updated project |
| GET | `/v1/projects/{id}/status` | — | stage progress, counts, budget spent today, active runs |
| DELETE | `/v1/projects/{id}` | — | unregisters (never deletes files) |

`DELETE` removing files is deliberately impossible through the API.

### 4.3 Ducklings and providers

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/v1/providers` | — | id, kind, base URL, `reachable`, `auth_ok` |
| POST | `/v1/providers/{id}/check` | — | probe result |
| GET | `/v1/ducklings` | — | full duckling records incl. caps and cost |
| POST | `/v1/ducklings` | duckling object | `201` |
| PATCH | `/v1/ducklings/{id}` | partial | updated |
| DELETE | `/v1/ducklings/{id}` | — | `204` |
| POST | `/v1/ducklings/{id}/probe` | — | `202`; result arrives as an event |
| POST | `/v1/ducklings/{id}/test` | `{"prompt":"say OK","stream":true}` | `202`; deltas stream on `/v1/events` |

### 4.4 Artifacts and the cycle

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/v1/projects/{id}/requirements` | — | parsed REQ sections + DB rows |
| GET | `/v1/projects/{id}/spec` | — | parsed SPEC sections |
| GET | `/v1/projects/{id}/plan` | — | milestones + tasks |
| GET | `/v1/projects/{id}/artifacts/{kind}` | `?proposed=true` | raw markdown + frontmatter |
| PUT | `/v1/projects/{id}/artifacts/{kind}` | `{"body":"…"}` | direct human edit; bumps `version` |
| POST | `/v1/projects/{id}/stages/{stage}` | `{"mode":"council","from":"…","budget":{…}}` | `202` + run object |
| POST | `/v1/projects/{id}/artifacts/{kind}/promote` | `{"accept":true}` | applies the proposal, syncs the DB, runs trace check |
| GET | `/v1/projects/{id}/trace/check` | — | `{"errors":[{"kind":"orphan_requirement","id":"REQ-007"}]}` |
| GET | `/v1/projects/{id}/trace/{anyId}` | — | the spine walked from any id, as nodes + edges |

`{kind}` ∈ `requirements | spec | plan | project`. `{stage}` ∈ `intake | spec |
plan | review | release | operate`.

### 4.5 Tasks and bugs

| Method | Path | Body / Query |
|---|---|---|
| GET | `/v1/projects/{id}/tasks` | `?status=&milestone=&after=&limit=` |
| POST | `/v1/projects/{id}/tasks` | `{"title","body","implements","complexity","depends_on"}` |
| GET/PATCH | `/v1/projects/{id}/tasks/{taskId}` | partial update |
| GET | `/v1/projects/{id}/tasks/next` | first `todo` with satisfied deps |
| GET | `/v1/projects/{id}/bugs` | `?status=&severity=` |
| POST | `/v1/projects/{id}/bugs` | `{"title","body","severity","reporter","source"}` |
| GET/PATCH | `/v1/projects/{id}/bugs/{bugId}` | |
| POST | `/v1/projects/{id}/bugs/triage` | `{"ids":["B-001",…]}` → `202` |
| POST | `/v1/projects/{id}/bugs/{bugId}/promote` | `{"title":"…"}` → created task |

### 4.6 Runs — the core of the API

| Method | Path | Body / Query | Returns |
|---|---|---|---|
| POST | `/v1/projects/{id}/runs` | `RunRequest` (below) | `202` + `Run` |
| GET | `/v1/runs` | `?project=&status=&after=&limit=` | list across all projects |
| GET | `/v1/runs/{runId}` | — | `RunDetail`: run + roster + spend + gate + pending |
| GET | `/v1/runs/{runId}/events` | `?from_seq=0&limit=` | historical events (JSON array) |
| GET | `/v1/runs/{runId}/llm` | `?from_seq=0` | model-call records (redacted) |
| GET | `/v1/runs/{runId}/diff` | `?format=unified\|files` | the working diff |
| GET | `/v1/runs/{runId}/transcript` | — | rendered markdown |
| GET | `/v1/runs/{runId}/candidates` | — | tournament/split candidates, anonymised |
| GET | `/v1/runs/{runId}/verify` | `?tail=500` | gate output |
| POST | `/v1/runs/{runId}/resume` | — | `202` |
| POST | `/v1/runs/{runId}/abort` | — | `202` |
| POST | `/v1/runs/{runId}/accept` | `{"message":"commit msg"}` | `AcceptResult` (commit sha) |
| POST | `/v1/runs/{runId}/reject` | `{"reason":"…"}` | `204` |
| POST | `/v1/runs/{runId}/answer` | `{"question_id":"q1","answer":"…"}` | `204` — satisfies `ask_human` |
| POST | `/v1/runs/{runId}/approve-tool` | `{"call_id":"c7","approve":true}` | `204` — manual autonomy |

```jsonc
// RunRequest
{
  "task_id": "T-003",          // or "stage" for artifact stages
  "mode": "pair",              // solo|pair|tournament|council|split
  "ducklings": ["a","b"],      // optional positional override
  "rounds": 3,
  "verify": "auto",            // auto|none|"<cmd>"
  "budget": {"max_usd":2.0,"max_tokens":400000,"max_wallclock_s":1800,"max_turns":24},
  "autonomy": "guarded",       // optional per-run override
  "stream": true,              // emit token_delta events
  "dry_run": false,
  "parallel": false,
  "unsafe_writes": false
}
```

`dry_run: true` is synchronous (it makes no model calls) and returns
`{"prompts":[{"turn":0,"role":"implementer","messages":[…]}]}`.

### 4.7 Review, release, deploy

| Method | Path | Body |
|---|---|---|
| POST | `/v1/projects/{id}/reviews` | `{"target":"T-003","mode":"solo"}` → `202` |
| GET | `/v1/projects/{id}/reviews/{target}` | rendered review artifact + findings |
| POST | `/v1/projects/{id}/releases/plan` | `{"bump":"minor"}` → `202` |
| POST | `/v1/projects/{id}/releases/cut` | `{"version":"0.2.0"}` |
| GET | `/v1/projects/{id}/deploys` | recipes from `project.toml` |
| POST | `/v1/projects/{id}/deploys/{recipe}` | `{"dry_run":false}` → `202`; each step emits events |

### 4.8 Skills, MCP, reports

| Method | Path |
|---|---|
| GET | `/v1/projects/{id}/skills?scope=project\|global\|all` |
| GET | `/v1/skills/{name}` |
| POST | `/v1/projects/{id}/skills/{name}/validate` |
| GET | `/v1/mcp/servers` · `/v1/mcp/servers/{name}/tools` |
| POST | `/v1/mcp/servers/{name}/call` |
| GET | `/v1/projects/{id}/report?since=30d&by=mode\|duckling\|role\|task` |
| GET | `/v1/projects/{id}/cost?since=30d` |
| POST | `/v1/bench` → `202` |

### 4.9 Config

| Method | Path | Notes |
|---|---|---|
| GET | `/v1/config` | global config **with every secret value replaced by `"[set]"` or `"[unset]"`** |
| PATCH | `/v1/config` | dotted-key patch; validated with the same strict rules as file load; rejects any attempt to write a key ending in `api_key` |
| GET | `/v1/config/raw` | the TOML text, secrets still redacted |

The API can never read back a secret. Setting one is done by naming an
environment variable (`api_key_env`), not by sending the value.

## 5. HTTP status mapping

| Status | Condition | CLI exit |
|---|---|---|
| 200 / 201 / 202 / 204 | success | 0 |
| 400 | malformed request | 2 |
| 401 | bad or missing token | 9 |
| 404 | unknown project/run/task | 2 |
| 409 | version skew, lock contention, run already running | 4 / 10 |
| 412 | human gate required but `--yes` not given | 7 |
| 422 | config invalid | 3 |
| 424 | provider unreachable or unauthenticated | 8 |
| 429 | engine at `max_concurrent_runs` (run was queued, not rejected) | 0 |
| 500 | internal error — always logged with a stack to `engine.log` | 1 |
| 507 | budget exceeded | 6 |

## 6. The event stream

```
GET /v1/events?project=<id>&run=<runId>&from_seq=<n>
Accept: text/event-stream
```

Server-Sent Events. One SSE `event:` per Ducklab event type; `data:` is the
event object from `02-DATA-MODEL.md §6.2`. `id:` is `"<runId>:<seq>"` so a
reconnecting client sends `Last-Event-ID` and resumes with no gap.

```
event: turn_start
id: r-20260725-153012-k7q2:7
data: {"ts":"…","seq":7,"type":"turn_start","run_id":"r-…","round":1,"turn":0,"role":"implementer","duckling":"pato-local"}

event: token_delta
data: {"run_id":"r-…","turn":0,"duckling":"pato-local","text":"func "}

event: human_needed
id: r-…:31
data: {"seq":31,"type":"human_needed","run_id":"r-…","kind":"gate","verdict":"PASSED","question_id":null}

event: heartbeat
data: {"ts":"…"}
```

Rules:

- **Backlog then live, no gap and no duplicate.** On subscribe, the engine takes
  the run's bus lock, replays `events.jsonl` from `from_seq`, then attaches.
- `token_delta` and `heartbeat` are never persisted and never have a `seq`.
- Per-subscriber send buffer of 256 events. On overflow the subscriber is
  **dropped** with a final `event: overflow`; it must reconnect with
  `Last-Event-ID`. A slow client must never stall a run (I11).
- Omitting `run` subscribes to all runs of a project; omitting both subscribes
  to everything, including engine-level events (`run_queued`, `run_started`,
  `engine_shutdown`).
- Heartbeat every 15 s.
- Events added in spec-1.2 (all persisted, all with `seq`): `advisor_consult`
  (`{round, advisor, outcome: none|note|stop|skipped|failed, signals, reason?,
  reshuffle?}`), `advisor_retry` (`{round, retry, of}` — the implementer runs
  again with the note), `advisor_stop` (`{advisor, reason, reshuffle}`),
  `deliverables_report` (`{round, retry?, total, deliverables: [text],
  items: [{id, status, note?}], undelivered: [id], unreported}` —
  self-contained so a client renders the checklist without the task),
  `deliverables_gap` (`{round, undelivered: [id]}` — a reviewer approved over
  items the implementer reported undelivered), and `advice` with
  `kind: "inline"` for an `ask_advisor` consult. A `turn_start` for a
  duck-sent implementer retry carries `retry: n`.

## 7. Client behaviour

### 7.1 Discovery and auto-start (`internal/daemon`)

```
1. read <state-dir>/ducklab/engine.json; if absent → step 4
2. GET http://127.0.0.1:<port>/v1/health (500 ms timeout)
3. ok and major version matches → use it
4. spawn `ducklab-engine --detach`; poll /v1/health every 100 ms up to 10 s
5. still failing → exit 9 with: "engine not running and could not be started: <cause>"
```

The engine binary is located, in order: next to the running executable; then
`$PATH`; then `[engine] path` in config. The desktop app bundles it and always
resolves the bundled copy first, so a stale `$PATH` engine is never used.

Concurrent auto-start is made safe by an exclusive create of
`<state-dir>/ducklab/engine.lock`; a loser waits for the winner's health check.

### 7.2 Reconnection

Clients reconnect the SSE stream with exponential backoff (0.5 s → 8 s, jitter),
always sending `Last-Event-ID`. The desktop app shows a `Reconnecting…` chip in
the status bar but keeps rendering the last known state; it must not blank the
UI on a dropped stream.

### 7.3 Generated client

`internal/engineapi` emits an OpenAPI 3.1 document at `/v1/openapi.json`. The
build generates:

- `internal/engineclt` — the Go client used by `cmd/ducklab`
- `frontend/src/api/generated.ts` — the TypeScript client and types

Both are generated artifacts, committed to the repo, and regenerated by
`make api`. Hand-editing either is a spec violation: the DTOs must not drift
between the three surfaces.

## 8. Security notes

- Loopback only, token-authenticated, token rotated per start, file mode 0600.
- The API never returns a secret value and never accepts one (§4.9).
- `shell` policy, the write guard, and the path jail are enforced in
  `internal/tools` — that is, **inside the engine**. A malicious client gains
  nothing the local user does not already have, but a compromised *model* still
  cannot escape the jail, which is the threat that actually matters here.
- `POST /v1/projects` accepts an absolute path and will open any directory the
  user can read. This is intentional and equivalent to the CLI's `--repo`.
- No remote access in v1. If it is ever added, it is a separate spec with mTLS
  and a per-project authorisation model, not a bind-address flag.
