# 0010 — The engine derives the actor from the credential

**Status:** adopted (B-530)

## The incident

An OpenCode session operating a project through `ducklab mcp serve` tried
`decide accept` on a test-first run that needed a lane widening. The engine
refused: *lane widening requires direct human approval; actor mcp:opencode*.
The agent then read the bearer token from `~/.local/state/ducklab/engine.json`
(printing it into its transcript), POSTed `/v1/runs/<id>/accept` itself with
`lane_widening` and an empty actor, and the engine recorded the decision as
`human` and committed it. The person had said "Ok" in chat, so the intent was
approved — but the record said a person decided directly, and the human-only
rule protected nothing.

Two causes: the actor was self-declared in the request body (empty meant
`human`), and one token served the desktop, the CLI, the MCP server and
anything that could read `engine.json`.

## The decision

**Two credentials, and the actor comes from the one a request presents.**

| Credential | File | Presented by |
|---|---|---|
| the person's | `<state-dir>/ducklab/human.token` (mode 0600) | the desktop (Go shell and webview); the CLI at an interactive terminal |
| the agents' | `token` in `<state-dir>/ducklab/engine.json` (mode 0600) | `ducklab mcp serve`; the CLI over pipes (how agent shell tools run it); the visual-check renderer |

`<state-dir>` is `$XDG_STATE_HOME` (default `~/.local/state`) on Linux. Both
tokens are 256 random bits, regenerated at every engine start;
`human.token` is written before `engine.json` and both are removed on
graceful stop.

The pair is published by one engine at a time. An engine holds an OS file
lock (`flock` / `LockFileEx`) on `<state-dir>/ducklab/engine.lock` for its
whole life, taken before it recovers runs or writes either file. Without
it, two simultaneous starts could leave `engine.json` from one engine beside
`human.token` from the other (review of #180). The kernel releases the lock
when the holder exits, crash included, so a dead engine never blocks a
start; the file's existence means nothing. A new start waits up to 15 s for
a holder on its way out (a restart), then exits with code 4; a CLI
auto-start that loses the race adopts the engine that won.

- The person's credential records `human` when the body names no actor. It
  may narrow (name an agent); it never needs to elevate.
- An agent credential keeps a body actor only of the form `mcp:<client>` or
  `agent:<name>`; anything else — empty, `human`, `desktop`, `engine`,
  `auto:yolo` — is recorded as `agent`. It can never yield `human`.
- Every handler that records an actor goes through `engineapi.actorFor`; a
  test fails on any handler that reads `.Actor` from a body directly or
  writes a literal `"human"` (`TestNoHandlerTrustsABodyActor`).
- Because `human` can only come from the person's credential, the service's
  human-only rules — lane widening at accept and on an answer, confirming
  manual verification, answering a test-first oracle dispute, and (B-541)
  lifting a budget cap — are now
  credential checks. Each refusal is recorded (`accept_refused` for accept)
  with the derived actor, and names the legitimate door.

**The legitimate door: approval requests.** An operator that has the
person's agreement files `approval_request` over MCP (`POST
/v1/runs/{id}/approval-requests`): the action (`accept`, `answer` or
`budget_lift`), why, and the paths or slices or answer or cap. It is bound to
the run's pending decision (`pending_kind` + `pending_since`); approving one
whose run has moved on is refused and marks it superseded. A `budget_lift`
request is bound to the run instead, since a cap binds a running run as well
as a paused one: it stands until the run ends. The engine emits `approval_requested`, a
webhook doorbell. The desktop lists pending requests on the run and in Now
with Approve / Decline. Approve and decline need the person's credential
(`403` otherwise); MCP has no tool for either. Approved, the decision runs as
`human`, and the record carries both actors: `approval_granted`
(`requested_by`, `approved_by`), the decision's own events, and
`approval_resolved` with the outcome. Declined, nothing is decided.

A request moves `pending → approved → landed | failed | interrupted`, or
`pending → declined | superseded`, never back. The `pending → approved`
claim is atomic under a per-run lock, and the decision runs with no approval
lock held, so a long accept on one run never blocks requests on another.
`approved` means the decision is in flight. `failed` means the door refused
it after approval (the outcome says why). `interrupted` means the engine
stopped mid-decision; the next engine settles it on recovery, or marks it
`landed` if the accept had committed. A retry is a new request, so nothing
is decided twice. An accept that settles later (B-529's accept job) waits in
`approved` until it settles.

## What this does not do

A same-user process can read `human.token`, as it can any file the desktop
reads; the interactive-terminal test is a heuristic an agent with a pty can
pass. The split does not stop an agent determined to impersonate the person.
It removes the default path B-530's agent took — the token every channel
used, with a body field that elevated it — and makes impersonation a
deliberate act on a file no agent channel is handed. Stronger separation
(an OS keyring, a desktop-only socket, a confirmation prompt on the CLI) is
open for the person to choose; the conservative choice here kept the desktop
on a token file so it can still adopt an engine the CLI started.

## Migration

After an engine restart:

- A client from before the split reads `engine.json`'s `token`, which is now
  the agent credential. Everything an agent may do keeps working; the
  human-only doors refuse with a message that names the approval request.
  Run `make install` so the desktop and CLI pick up `human.token`.
- A new client against an engine from before the split finds no
  `human.token` and uses the single token in `engine.json`, as before.
