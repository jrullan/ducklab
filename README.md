# Ducklab

Ducklab is a full-cycle software development harness that is multi-LLM by default.

This repository is the implementation of the specification in `~/dev/ducklab-spec/`.

## Status

**v0.1 in progress** — Core + engine skeleton.

## Build

```bash
go build ./cmd/ducklab
go build ./cmd/ducklab-engine
```

## Specification

Read the spec in order:

| # | File | What it fixes |
|---|------|---------------|
| 0 | `00-VISION.md` | Purpose, scope, non-goals, glossary |
| 1 | `01-ARCHITECTURE.md` | Topology, 12 invariants, package layout |
| 2 | `02-DATA-MODEL.md` | Config files, SQLite schema, artifacts |
| 3 | `03-CLI.md` | CLI client: command grammar, flags, exit codes |
| 4 | `04-AGENT-PROTOCOL.md` | Provider interface, toolbelt, role prompts |
| 5 | `05-LIFECYCLE.md` | Stages, conversation engine, duck modes |
| 6 | `06-PHASES.md` | Milestones v0.1 → v1.0 |
| 7 | `07-ENGINE-API.md` | HTTP + SSE contract |
| 8 | `08-DESKTOP-UI.md` | Desktop app design |
