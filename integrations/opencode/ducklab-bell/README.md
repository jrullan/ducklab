# ducklab-bell — an OpenCode coordinator for Ducklab

An [OpenCode](https://opencode.ai) 2.x server plugin that makes one OpenCode
session the coordinator of a Ducklab project.

Ducklab announces what an operator acts on — a run asks a question, pauses at
its gate, fails, ends — on the engine's event stream (`GET /v1/events`). The
plugin listens to that stream inside the OpenCode server and wakes the
configured session with a short `[Ducklab]` message. The agent there checks
with its `ducklab` MCP tools and continues; nobody polls.

## Modes

- **`ask`** (default) — each wake asks the agent to look, summarise and
  propose the next action. It changes nothing without the person.
- **`act`** — the agent runs errands end to end ("fix the bugs", "settle the
  spec-debt", "add this feature") under [`policy.md`](policy.md), which the
  plugin adds to that session's system prompt. It accepts work only on a green
  gate with the reviewers' approval and escalates everything else. In this
  mode the plugin also refuses, in that session, `sleep` and reads of
  `.ducklab/` files: a busy turn holds the wake, and the policy alone did not
  stop a local model from polling.

The engine keeps its own line whatever the mode: an agent's credential cannot
widen a lane, verify or close a bug, or lift a budget cap; those go through
`approval_request` to the person (`docs/decisions/0010-actor-from-credential.md`).

## Install

Requirements: OpenCode 2.x with the `ducklab` MCP server configured
(`ducklab mcp serve`), and a running Ducklab engine.

Register the plugin by path in the project's OpenCode config
(`.opencode/config.json` or `opencode.json`), with its options:

```jsonc
{
  "plugins": [
    {
      "package": "/path/to/ducklab/integrations/opencode/ducklab-bell",
      "options": {
        "session": "ses_…",           // the OpenCode session to wake (required)
        "project": "my-project",      // the Ducklab project id (required)
        "mode": "act",                // ask | act (default ask)
        "language": "Spanish",        // reply language; the wake text is English
        "maxPerHour": 30              // wake cap; a hold is logged (default 30)
      }
    }
  ]
}
```

Alternatively, copy the directory into the project's `.opencode/plugins/`,
where OpenCode loads it automatically, and put the options in a `bell.json`
next to `server.ts`. Plugin options win over `bell.json`.

Find the session id with `opencode session list` in the project directory.

The OpenCode server loads the plugin as soon as it sees it, and reloads it
when `server.ts` changes; no restart is needed.

## Behaviour

- **One wake at a time.** OpenCode queues a prompt sent to a busy session and
  gives it its own turn; the plugin also waits for each wake's turn to end
  before sending the next batch.
- **Bursts coalesce.** Events arriving within 5 s go out as one wake.
- **Capped.** At most `maxPerHour` wakes per hour; further events are held and
  go out together when the window frees, and the hold is logged.
- **Follows the engine.** `engine.json` is re-read on every reconnect, so an
  engine restart is followed.
- **Catches up.** On every (re)connect, runs that have waited for a decision
  since the last wake are announced, so a wake lost to a reload, a restart or a
  hold is not lost for good.
- **One owner per session.** A lock file in the Ducklab state directory keeps
  two OpenCode processes from waking the same session twice. Every instance,
  owner or not, carries the policy and the refusals.
- **Catch-up starts at install.** The first connect for a session records the
  mark instead of announcing every gate the project ever left open; a failed
  wake does not move the mark and is retried.

Log: `bell.log` in the Ducklab state directory (`~/.local/state/ducklab/` on
Linux).

## Limits

- The policy is the model's instructions, not a barrier; the engine's
  credential rules are the barrier. The refusal of waiting and of reads of
  Ducklab's state is a guard rail against habit: it errs toward refusing
  (a command that merely mentions `sleep` is refused too), and a determined
  model can still find a way around it.
- The policy and the refusals apply to the configured session only, not to
  subagent sessions it spawns.
- The plugin wakes the session only while the OpenCode server runs; OpenCode
  starts its server with the TUI. Without it, events wait in the catch-up.
- Wakes caused by the coordinator's own decisions (the `run_end` after it
  accepts) still ring; Ducklab events do not yet carry the deciding actor for
  the plugin to filter on.
