# Working in this repository as an agent

This repo is unusual: it is developed **by agents, through the product it
contains**. Most of the last hundred tasks were built by LLM seats inside
ducklab's own loop, gated by a human. If you are an agent, you have two ways
to work here — and the second one is the point of the project.

## Path 1: conventional (edit the tree directly)

Build and test:

```bash
make                                  # vet, go tests, frontend build
go test ./...                         # 38 packages, -count=1 for arch tests
cd frontend && npx tsc --noEmit && npx vitest run
make desktop && make install          # binaries to ~/.local/bin
```

Rules the tests enforce (they will fail your change, not just advise):

- CLI, desktop and MCP server are **clients** — they import only
  `engineclt`, `daemon`, `xplat` (`internal/arch_test.go`).
- Every engine event must be registered in `frontend/src/api/events.ts`.
- Every engine route needs a desktop client method or an excused entry
  (`internal/engineapi/desktop_coverage_test.go`).
- `docs/openapi.json` and `frontend/src/api/generated.ts` are generated
  (`make api`); never edit them by hand.
- Flipping a behavior means **rewriting** the test that pinned the old one
  with the new reasoning — never deleting it.

House style: comments state the incident or the constraint, never the next
line. Commit messages: first line what, body why, usually with the incident.
Frontend changes are invisible until `make desktop && make install`.

The normative spec is `docs/spec/` (what it must be); the as-built truth is
`.ducklab/docs/` (requirements, spec, plan — loop-maintained, human-gated);
deliberate divergences live in `docs/decisions/`.

## Path 2: native (operate the loop — how this repo is actually developed)

```bash
ducklab mcp serve      # stdio MCP server exposing the whole loop
```

An MCP-connected agent can file bugs (`bug_report`), triage, start stage and
build runs (`stage_start`, with `refs` for reference documents), read
transcripts, answer questions, and decide gates — every decision recorded
and attributed (`approved_by: mcp:<client>`, never "human"). The engine's
`next` lists are the law: an agent cannot take an action a person could not.

The HTTP contract is `docs/openapi.json`, served live at `/v1/openapi.json`;
the engine binds loopback only with a bearer token from
`~/.local/state/ducklab/engine.json`.

Found a defect while working here? File it on the bug board rather than
fixing it silently — triage → promote → task → build → accept is the paper
trail this project runs on, and your report's quality is the next run's
prompt.
