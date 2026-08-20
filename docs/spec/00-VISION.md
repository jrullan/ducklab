# 00 — Vision, Scope, Glossary

`spec-1.1` — revised: P6 adjusted for the three-binary split; §7 adds the
engine to the mental model. Vision and scope are otherwise unchanged.

## 1. Vision statement (canonical)

Ducklab is a **full-cycle software development harness that is multi-LLM by
default**.

The theme is the rubber duck: the developer thinks out loud, except the duck
answers — and sometimes several ducks disagree with each other in front of you.
The models are called **ducklings**. A conversation between ducklings, or
between ducklings and the user, is available at *every* step of the development
cycle, not only at code-writing time.

Ducklab covers the whole cycle:

1. eliciting and recording **requirements**
2. drafting a **specification**
3. **planning** the development roster and the work breakdown
4. **choosing the models** to use and **assigning roles** to them
5. **orchestrating** the work to a finished product
6. establishing and running a **bug tracking system**
7. **monitoring** reported bugs and assigning them to a duckling for a fix
8. **reviewing** the resulting changes / pull requests
9. **releasing and deploying** new versions

Ducklab runs on **Linux, Windows and macOS**. It supports **local models**
(llama.cpp, vLLM, Ollama, any OpenAI-compatible server) and **API models**
(OpenRouter, OpenAI, Anthropic, …) as equal citizens. It is **extensible**: it
hosts **skills** that ducklings can use, ducklings can **create new skills**, and
it can consume **MCP servers** and other third-party services to accomplish its
mission.

## 2. The bet

A single model has systematic blind spots inherited from its own training.
Several decorrelated models — one that writes, one that criticises, one that
judges — plus **executable verification as the only ground truth** can produce
better results than any one of them alone, at every stage, not just at coding.

Corollary that shapes the whole design: **the orchestrator is deterministic.**
Models only ever produce text. Control flow, git, tests, budgets, and the
decision of whether something passed belong to Ducklab and never to a model.

## 3. In scope

- Multi-provider model access, local and remote, with per-model cost and
  capability tracking.
- A native agentic loop: Ducklab defines the tools, calls the model, executes
  the tool calls itself, and enforces every bound.
- Deterministic, scripted multi-party conversations (the "duck modes").
- Durable, human-readable artifacts for every lifecycle stage.
- A traceability spine: requirement → spec → task → run → commit → release, and
  bug → task.
- A built-in issue/bug store, optionally mirrored to GitHub.
- Skills (on-disk, model-authorable) and MCP client support.
- Measurement: tokens, cost, wall time, pass rate, per duckling and per mode,
  so that "the combination beat the solo model" is a number and not a feeling.

## 4. Out of scope (non-goals)

- **Not** an attempt to match a frontier model. The aim is to raise the ceiling
  of what the models *you have access to* can produce together.
- **Not** a hosted service. Ducklab is a local tool; no account, no telemetry
  leaving the machine.
- **Not** a general chat client. Every conversation is attached to a project
  stage and produces an artifact.
- **Not** an IDE or an editor.
- **Not** a CI system. Ducklab *invokes* verification commands; it does not
  replace CI.
- Ducklab does not train, fine-tune, or serve models.

## 5. Design principles

| # | Principle | Consequence |
|---|-----------|-------------|
| P1 | **Deterministic where it can be** | Turn order, git, merges, gating are code, not prompts. |
| P2 | **Executable verification is ground truth** | A reviewer's approval never overrides a red test. |
| P3 | **Honest verdicts** | If nothing could be executed, the run is labelled `UNVERIFIED`, never dressed up as passing. |
| P4 | **Nothing unbounded** | Every loop has a turn cap, a token cap, a wall-clock cap and a money cap. |
| P5 | **Everything resumable** | Any run can be killed and resumed from disk. |
| P6 | **Light and portable** | The engine and CLI are static Go binaries with pure-Go dependencies, no cgo, no POSIX-only tricks. cgo is quarantined in the desktop shell alone. |
| P7 | **Human-readable state** | Artifacts are Markdown; records are SQLite; logs are JSONL. No opaque formats. |
| P8 | **The human is a participant, not a spectator** | Any conversation turn may be assigned to the human. |
| P9 | **Measurable, or it didn't happen** | Every model call is logged with tokens, latency and cost. |

## 6. Glossary

These terms are used with exactly these meanings throughout the spec.

| Term | Meaning |
|------|---------|
| **Provider** | A configured endpoint that serves models (`openai`-compatible, `anthropic`, …). Holds base URL, auth, dialect. |
| **Duckling** | A named, configured model participant: provider + model id + sampling params + capability record. E.g. `pato-verde`. |
| **Role** | A job a duckling performs in a turn: `architect`, `implementer`, `reviewer`, `judge`, `triager`, `scribe`, `human`. A role fixes the system prompt, the allowed tools and the expected output contract. |
| **Roster** | The mapping of roles → ducklings for a project or a stage. |
| **Toolbelt** | The set of tools exposed to a duckling for a given turn. |
| **Conversation** | A bounded, deterministically scheduled exchange of turns between ducklings and/or the human, producing a transcript. |
| **Turn** | One scheduled unit: a role + a duckling + a rendered prompt + a toolbelt + an output contract. |
| **Mode** (duck mode) | A named conversation script: `solo`, `pair`, `tournament`, `council`, `split`. |
| **Stage** | A phase of the lifecycle: `intake`, `spec`, `plan`, `build`, `review`, `release`, `operate`. |
| **Artifact** | A durable Markdown/JSON output of a stage, stored in `.ducklab/docs/`, with YAML frontmatter carrying provenance. |
| **Task** | A unit of build work with an id, a spec reference, a status and a branch. |
| **Run** | One execution of a mode against a task or a stage. Has a directory, a manifest and a resumable state. |
| **Bug** | A defect record. May be promoted to a Task. |
| **Gate** | The verification tier applied to a run: `tests` > `build` > `lint` > `none`, plus the human approval step. |
| **Skill** | A reusable capability on disk (`SKILL.md` + optional scripts) that a duckling may list, read and execute. |
| **Budget** | The caps (turns / tokens / USD / wall-clock) applied to a run. |
| **Verdict** | The outcome of a run: `PASSED`, `UNVERIFIED`, `FAILED`, `BUDGET_EXCEEDED`, `ABORTED`. |

## 7. The one-paragraph mental model

A Ducklab **project** lives inside a target git repository. Its state is in
`.ducklab/`. A headless **engine** owns every project and executes all work; the
**desktop app** and the **CLI** are interchangeable clients that render its
event stream and call its API. The user moves the project through **stages**; each stage runs a
**conversation** in some **mode** among **ducklings** drawn from the **roster**;
each turn of that conversation is a bounded **agentic loop** in which the
duckling may call **tools** that Ducklab — never the model — executes; the
conversation produces an **artifact** or a diff; a **gate** decides whether it is
green; the human accepts; Ducklab commits. Every step is recorded so the whole
chain from requirement to release can be traced and measured.
