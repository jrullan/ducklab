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
- Clients present `DUCKLAB_TOKEN` when it is set — the MCP server and the CLI
  alike, and the CLI even at an interactive terminal — and a reconnect keeps
  it rather than falling back to `engine.json`.

**Transition.** The shared token keeps working, at full access everywhere,
until the person turns it off. Turning it on by default keeps every existing
integration alive on the day this lands. With it off, the engine's own
renderer receives an in-memory read-only token for the project it renders,
revoked when the render ends.

## What it does not do

It orders, limits and audits agents that use the engine's API; it does not
sandbox them. An agent running as the person's OS user can read
`human.token`, another agent's MCP configuration, or the repository itself.
Closing that needs agents under a different OS identity, where the existing
file modes become a real boundary. That is a separate decision.
