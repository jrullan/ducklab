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

## Where to start

- [`docs/status.md`](docs/status.md) — every acceptance criterion, honestly
  graded. The "half built" rows are scoped, known work.
- The bug board of this repo's own `.ducklab/` — real, triaged, sized.
- macOS: the desktop build has never been verified on a Mac. The first
  person with a Mac and an hour owns that milestone.
- Packaging: `make cross` compile-checks four targets; nothing ships
  artifacts yet.

## License

Not chosen yet; the repository is private today and an OSI license will be
added before it goes public. By contributing before that, you're trusting
the maintainer to pick a real one — raise it in an issue if that matters to
you now.
