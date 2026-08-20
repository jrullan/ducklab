# Ducklab — Specification Set

`spec-1.1`

This directory is the **normative specification** for Ducklab. It is written to
be executed by an implementing agent (a "lesser" model driven by Hermes +
OpenRouter) without needing to ask design questions.

## Shape of the system

Three artifacts around one core Go library:

```
ducklab-desktop   Wails v3 + React/TS   the primary face    (cgo, per-platform)
ducklab           CLI client, full parity                   (pure Go, cgo-free)
ducklab-engine    headless daemon — owns every run          (pure Go, cgo-free)
```

Clients render and call; the engine does the work and holds the truth. Closing
the desktop window never stops a run.

## Reading order

| # | File | What it fixes |
|---|------|---------------|
| 0 | [`00-VISION.md`](00-VISION.md) | Purpose, scope, non-goals, glossary. Read once. |
| 1 | [`01-ARCHITECTURE.md`](01-ARCHITECTURE.md) | Topology, 12 invariants, package layout, Go interfaces, control flow. |
| 2 | [`02-DATA-MODEL.md`](02-DATA-MODEL.md) | Config files, SQLite schema, artifacts, run directory, engine state. |
| 3 | [`03-CLI.md`](03-CLI.md) | CLI client: command grammar, flags, exit codes, JSON output. |
| 4 | [`04-AGENT-PROTOCOL.md`](04-AGENT-PROTOCOL.md) | Provider interface, toolbelt, both tool dialects, all role prompts. |
| 5 | [`05-LIFECYCLE.md`](05-LIFECYCLE.md) | Stages, conversation engine, the five duck modes, gates. |
| 6 | [`06-PHASES.md`](06-PHASES.md) | Milestones v0.1 → v1.0, each with acceptance criteria. |
| 7 | [`07-ENGINE-API.md`](07-ENGINE-API.md) | HTTP + SSE contract between the engine and every client. |
| 8 | [`08-DESKTOP-UI.md`](08-DESKTOP-UI.md) | Desktop app: design system, the nine views, real-time behaviour. |

Documents 7 and 8 are only needed from milestone v0.1 (engine) and v0.3
(desktop) respectively, but read `01` in full before writing any code.

## Rules for the implementing agent

1. **Build one phase at a time**, in the order given in `06-PHASES.md`. Do not
   start phase *N+1* until every acceptance criterion of phase *N* passes.
2. **The spec is authoritative.** If the spec and your instinct disagree, follow
   the spec. If the spec is genuinely silent, choose the simplest option
   consistent with the Invariants in `01-ARCHITECTURE.md §3` and record the
   choice in `docs/decisions/`.
3. **Never weaken an invariant** to make a test pass.
4. **Every exported identifier named in these documents must exist with exactly
   that name and signature.** Names are the contract between phases and between
   the three surfaces.
5. **Every capability is a `service.Service` method**, exposed through the engine
   API, and reachable from *both* the CLI and the desktop app. A capability that
   exists in only one client is a spec violation, and AC-44 fails the build for
   it.
6. **Tests before green.** Acceptance criteria are runnable commands. They must
   pass on Linux and must not use POSIX-only syntax. No test may require a live
   model or a GPU — use the fake provider and the fake engine.

## Handing this to Hermes

```
You are implementing Ducklab. Your specification is the files in
~/dev/ducklab-spec/, read in the order given by README.md.
Build ONLY milestone <PHASE> from 06-PHASES.md.
Stop when every acceptance criterion for that milestone passes.
Do not implement later milestones. Do not redesign.
```

## Revision history

- `spec-1.0` (2026-07-25) — initial complete specification. CLI + Charm TUI,
  single binary, file-backed state.
- `spec-1.1` (2026-07-25) — **desktop app becomes the primary interface.**
  Split into engine + CLI + desktop; added `07-ENGINE-API.md` and
  `08-DESKTOP-UI.md`; added invariants I11 (clients hold no state) and I12
  (loopback-only engine); `Provider` gained `ChatStream`; `ask_human` now pauses
  a run instead of blocking; the Charm TUI is dropped; phases re-cut so the
  engine precedes any client.

Changes to the spec require a bump of the version line at the top of each
affected file.
