# Contributing to Ducklab

Thanks for looking. This document is the map: how to build, what the tests
protect, how work actually flows here, and where a first contribution lands
well.

## Build and test

```bash
make            # vet, go tests, frontend build
go test ./...   # 38 packages; -count=1 for the architecture tests
cd frontend && npx tsc --noEmit && npx vitest run
make desktop && make install   # binaries into ~/.local/bin
```

Frontend changes are invisible until `make desktop && make install` — the
desktop embeds its assets at build time. The guide rail inside ducklab will
tell you when the repo has outrun the installed binaries.

### Working without a model, a GPU or a key

`cmd/fake-engine` serves the real engine HTTP contract with scripted
scenarios (`pair`, `tournament`, `question`, `flood`, `idle`) — it exists so
the frontend can be exercised against streaming, reconnection and the human
gate with zero models. The playwright e2e suite (`make e2e`) runs the
desktop flows against it. Known gap: `npm run dev` in a plain browser
cannot reach it yet, because the engine address is injected by the desktop
shell — that is B-108 on the bug board, and a good first contribution.

### Fast lanes

- `cd frontend && npx vitest --watch` for red-green frontend loops.
- `go test ./internal/<pkg>/ -run TestName -count=1` — most Go packages are
  fast; the service package's full suite (~20s) is the slow one.
- `make api` regenerates `docs/openapi.json` + `generated.ts` from the route
  table; `make api-check` fails on drift. Never edit generated files.

`docs/openapi.json` and `frontend/src/api/generated.ts` are **generated**
from the route table by `make api`; never edit them by hand. `make api-check`
fails CI-style on drift.

## The rules the tests enforce

Some tests are architectural and are meant to fail your build:

- `internal/arch_test.go` — the CLI, desktop and MCP server are **clients**:
  they may import only `engineclt`, `daemon` and `xplat`. Tools are a leaf
  and may not re-enter orchestration. These tests read the source files
  themselves (not `go list`) so the test cache cannot hide a violation.
- `TestEveryEmittedEventIsKnownToTheDesktop` — a new engine event must be
  registered in `frontend/src/api/events.ts`, even if nothing renders it yet.
- `TestEveryEngineCapabilityIsReachableFromTheDesktop` — a new route needs a
  desktop client method, or an explicit entry saying why not.

The normative spec lives at [`docs/spec/`](docs/spec/) (see decision 0009).
House invariants worth internalizing before a PR (the spec's I-numbers):
no model decides a verdict; green candidates apply byte-for-byte; reviewers
never learn authorship; secrets never touch project state; everything is
bounded; the engine binds loopback only.

## How work flows (we dogfood)

Ducklab is developed **inside ducklab**. Bugs go on the bug board
(`Work → Bugs`, or `bug_report` over MCP) and travel triage → promote →
task → build → accept; features enter as plan amendments. Direct commits
happen — with care: runs share the working tree, so commit pathspec-scoped
and promptly, and never `git add -A` while a run is active.

Comment style: a comment states what the code cannot — the incident, the
constraint, the reversed decision — not what the next line does. Most files
carry short war stories; keep them true and keep writing them. Commit
messages follow the same rule: the first line says what changed, the body
says why it needed changing, usually with the incident that proved it.

Tests are reversal-proof by intention: when you flip a behavior, find the
test that pinned the old one and rewrite it with the new reasoning, don't
delete it.

## What the system believes it is

Three files in `.ducklab/docs/` are the living, loop-maintained truth:
`requirements.md` (what it does), `spec.md` (how, with **Implements:**
traceability), `plan.md` (every task that built it, milestone by milestone).
Reading them is the fastest induction there is — they were written by the
same process you are about to contribute through, and the human gate signed
every version.

## Where to start

- **The bug board of this repo's own `.ducklab/`** — real, triaged, sized.
  `low`-severity bugs are deliberately good first issues: small, verified,
  with the failing behaviour described by whoever hit it.
- [`docs/status.md`](docs/status.md) — every acceptance criterion, honestly
  graded. The "half built" rows are scoped, known work.
- **B-108** — let `npm run dev` reach `fake-engine` in a plain browser: one
  dev-only fallback, and every future frontend contributor gets a live loop
  without building the desktop.
- macOS: the desktop build has never been verified on a Mac. The first
  person with a Mac and an hour owns that milestone.
- Packaging: `make cross` compile-checks four targets; nothing ships
  artifacts yet.

## PR checklist

- `make` green (vet + Go tests + frontend build); frontend touched →
  `npx tsc --noEmit && npx vitest run` too; routes touched → `make api`.
- Behaviour flipped → the test that pinned the old behaviour is rewritten
  with the new reasoning, not deleted.
- New engine event → registered in `events.ts`; new route → desktop client
  method or an excused entry. (Two architecture tests enforce these; this
  line is so their failure does not surprise you.)
- Comments tell the incident or the constraint, never the next line.

## License

Apache-2.0 (see [LICENSE](LICENSE)). Your contributions are licensed under
the same terms automatically — §5 of the license, so there is no CLA to
sign. The trademark carve-out (§6) means the Ducklab name and mark stay
with the maintainer.
