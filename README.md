# Ducklab

A full-cycle software development harness that is multi-LLM by default.

You give it a brief. It writes requirements, a spec and a plan; builds tasks
with one model or several arguing; runs your project's real gate; and stops for
you before anything is committed. Every model call is logged. No model decides
a verdict.

It runs against local models. The two used to develop it are a vLLM box on the
LAN and a llama.cpp server on localhost, and both are priced at zero.

This repository implements the specification in `~/dev/ducklab-spec/`. The spec
is normative: where the code differs, that is recorded in
[`docs/decisions/`](docs/decisions/).

## Status

**Version 0.4.0.** Seven stages, five modes, skills, bugs, releases, a CLI and
a desktop app.

The version number is not a phase number. Work did not happen in the spec's
phase order — there are v0.8 features here and two open v0.1 criteria.
[`docs/status.md`](docs/status.md) has all 67 acceptance criteria with what is
built, what is half built and what is missing, and does not round up.

The largest missing piece: **nothing measures anything yet.** `bench`, the
Reports view and the charts are unbuilt, and comparing ducklings is most of why
Ducklab exists.

## Install

Needs Go 1.24+, Node 22+ for the desktop, and git.

```bash
make install          # ducklab, ducklab-engine, and the desktop if it is built
make desktop          # rebuild the desktop's embedded frontend first
```

`make install` installs to `~/.local/bin`. It warns when the desktop binary
predates `frontend/src`, because it will happily install a stale one.

On Ubuntu 24.04+ the desktop also needs an AppArmor profile — see
[decision 0003](docs/decisions/0003-apparmor-userns.md) and
`packaging/apparmor/`.

## Three binaries

| | What it is |
|---|---|
| `ducklab-engine` | The daemon. Owns every run. Binds 127.0.0.1 only, with a bearer token rotated on each start. |
| `ducklab` | The CLI client. Holds no state; it asks the engine. |
| `ducklab-desktop` | The desktop app. Also a client, also holds no state. |

Start the engine yourself for now — auto-start is specified and not built:

```bash
ducklab-engine &
```

## A cycle, end to end

```bash
cd ~/dev/myproject
git init                                    # ducklab needs a git repo
ducklab project init --name MyProject

ducklab intake --from brief.txt             # brief        → requirements
ducklab spec                                # requirements → spec
ducklab plan                                # spec         → milestones and tasks

ducklab run T-001                           # build it
ducklab run accept r-20260729-...           # commit it

ducklab review T-001                        # read the commit
ducklab release plan --bump minor           # what shipped
```

Each stage writes a `.proposed` file first and waits for you. `accept` promotes
it; `reject` leaves the original untouched. Nothing is committed without you.

## The five modes

`ducklab run T-001 --mode <mode>`

| Mode | What it does |
|---|---|
| `solo` | One duckling. The yardstick everything else is measured against. |
| `pair` | Implementer and reviewer, decorrelated — a different model reviews. |
| `tournament` | Contestants build the same task in isolated worktrees; a judge picks, blind to who wrote what. |
| `split` | An architect decomposes; subtasks run in parallel; integration is file copies with no model involved. |
| `council` | Several models on one document, for intake, spec, plan and review. |

`--ducklings a,b` assigns models positionally in `tournament` and `split`.

## What it will not do

These are load-bearing, not preferences.

- **A model never decides a verdict.** A gate is a command's exit code.
- **A green candidate is applied byte-for-byte.** Nothing is re-generated after
  it passed.
- **A reviewer never learns who wrote the code.** Not hidden in the UI —
  absent from the payload.
- **Nothing is unbounded.** Turns, tokens, cost, wallclock, tool output, shell
  commands.
- **Secrets never touch project state.** Keys come from the environment or the
  keyring; no API response returns one.
- **The engine is loopback-only.** There is no remote mode.

## Skills

A skill is a directory with a `SKILL.md` in it, under `.ducklab/skills/`.
The documentation-only form has no script and is the default: a model reads it
and follows it.

```bash
ducklab skill new house-style
ducklab skill list
ducklab skill validate house-style
ducklab skill run changelog-entry --arg summary="..."
```

A duckling can write one during a run, with the ordinary `fs_write` tool,
through the ordinary write guard — so reviewing a new skill is reading a diff.

## Development

```bash
make            # vet, test, build the frontend
go test ./...   # 28 packages
cd frontend && npx vitest run
make cross      # linux/amd64, linux/arm64, darwin/arm64, windows/amd64
```

`docs/openapi.json` and `frontend/src/api/generated.ts` are generated from the
route table by `make api`; do not edit them.

Two tests are architectural and are meant to fail the build:
`TestCLIImportsOnlyClientPackages` keeps the CLI from reaching into the domain,
and `internal/arch_test.go` audits CLI/desktop parity.

## Specification

Read in order:

| # | File | What it fixes |
|---|------|---------------|
| 0 | `00-VISION.md` | Purpose, scope, non-goals, glossary |
| 1 | `01-ARCHITECTURE.md` | Topology, 12 invariants, package layout |
| 2 | `02-DATA-MODEL.md` | Config files, SQLite schema, artifacts |
| 3 | `03-CLI.md` | CLI client: command grammar, flags, exit codes |
| 4 | `04-AGENT-PROTOCOL.md` | Provider interface, toolbelt, role prompts |
| 5 | `05-LIFECYCLE.md` | Stages, conversation engine, duck modes |
| 6 | `06-PHASES.md` | Milestones v0.1 → v1.0, and the 67 criteria |
| 7 | `07-ENGINE-API.md` | HTTP + SSE contract |
| 8 | `08-DESKTOP-UI.md` | Desktop app design |
