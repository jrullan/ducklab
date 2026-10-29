# 0011 — Per-agent tokens with project grants

**Status:** adopted (B-550)

## The problem

0010 split the engine's one token into the person's and the agents'. The
agents' was still one token for every agent: whatever read `engine.json`
presented the same credential, so the engine could not tell OpenCode from
Claude Code from Hermes — the name in the record (`mcp:opencode`) was the
agent's own claim — and every agent could reach every project. An agent
working in one project, or one that obeyed injected text, could start stages,
land tasks and open PRs in any other.

## The decision

**The person issues each agent a token and grants it projects.**

- A token has a name, a hint (its first characters), creation and last-use
  times, and grants: `{project, level}` with level `read-only` or `full`. No
  grant is no access.
- The secret is shown once, in the answer to `POST /v1/agent-tokens`. The
  engine stores its SHA-256 in `<config-dir>/agent-tokens.json` (mode 0600),
  so tokens survive restarts and the file holds nothing that authenticates.
- Issuing, changing grants, revoking and the shared-token switch are the
  person's credential alone. An agent that could widen its own grants would
  make them decoration.
- The engine records a token's requests as `agent:<slug of its name>`; the
  body cannot change it. Two live tokens may not share a slug.
- Where a route stands is derived once from the route table
  (`engineapi/agent_scope.go`): project routes answer to the project's grant,
  run routes to their run's project, `GET` needs `read-only` and anything
  else `full`. Outside projects a token reads configuration only, from a
  pinned list; a new route outside projects is refused until someone adds it
  there (`TestTheRoutesOutsideProjectsAnAgentMayReachArePinned`).
- **Full does the work; the person sets the rules.** Review found that a
  full grant reaching `PATCH /v1/projects/{id}` could set `render.command`,
  which the engine runs as the person — so every grant would have been host
  access, `human.token` included. A project's config, autonomy, gate,
  visual gate, skills, registration and orphan recovery are refused to every
  per-agent token. Likewise, files a token names (`from`, `refs`, a
  reference import) must resolve inside the project: `{"from":
  "~/.local/state/ducklab/human.token"}` otherwise read the person's
  credential into a brief the agent could fetch.
- Review's second pass added: turning autopilot on or off, the roster (which
  models — local or hosted — see the project) and a task's lane are rules
  too; files are vetted after a revision inherits its parent's references,
  and every file in a named directory is checked through its links; a chat
  cannot reach the harness project.
- A grant ends when it should: an event stream re-checks the token before
  each write, and forgetting a project drops its grants, since project ids
  are reused.
- Clients present `DUCKLAB_TOKEN` when it is set — the MCP server and the CLI
  alike, and the CLI even at an interactive terminal — and a reconnect keeps
  it rather than falling back to `engine.json`. The engine never inherits
  it: an agent's CLI that auto-starts the engine would otherwise make every
  child the engine spawns act as that agent. (So `DUCKLAB_TOKEN` is no longer
  a render-worker override; `DUCKLAB_ENGINE` still is.)

**Transition.** The shared token keeps working, at full access everywhere,
until the person turns it off. Turning it on by default keeps every existing
integration alive on the day this lands. With it off, the engine's own
renderer receives an in-memory read-only token for the project it renders,
revoked when the render ends.

## What it does not do

**`full` still runs code as the person.** A full grant launches builds,
runs the project's gate and tests, starts its app and its install chain —
all of which execute code that ducklings wrote under the agent's direction,
with the person's OS rights. Locking the project's rules stops an agent from
choosing *which* command runs; it cannot stop the project's own code from
doing what code does. So `full` is real trust in that agent for that
project, and `read-only` is the level that contains it.

It orders, limits and audits agents that use the engine's API; it does not
sandbox them. An agent running as the person's OS user can read
`human.token`, another agent's MCP configuration, or the repository itself.
Closing that needs agents under a different OS identity, where the existing
file modes become a real boundary. That is a separate decision.
