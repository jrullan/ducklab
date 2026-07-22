# ducklab roadmap

The long game: a tool that evolves with local models and local hardware. Today
those models are modest and the hardware is dear; the design assumes both
improve, and stays worth using at every point along that curve.

Guiding principles:

- **Raise the local ceiling** — never chase a frontier model; make the models
  you can run locally produce more together than any one does alone.
- **Deterministic where it can be** — models produce text; git, tests, and merge
  logic are the harness's job. Integration especially must not depend on a weak
  model regenerating whole files.
- **Measurable, or it didn't happen** — the whole thesis is comparative ("better
  than the model alone"), so the baseline and the numbers come first.
- **Light and cross-platform** — one binary, standard library at the core,
  Linux/macOS/Windows equal citizens.

## v0.1 — foundation ✅

- Core engine: sources, resumable runs, primitives (file blocks, SEARCH/REPLACE,
  portable shell/git), role prompts.
- Strategies: `solo`, `driver`, `tournament`.
- Interactive Charm REPL with the rubber-duck character; scriptable CLI parity.
- Validated end-to-end against real local models.

## v0.2 — measurement & the control arm

The point of `solo` is to be the yardstick. This phase makes the comparison real.

- **Metrics aggregator** — read every `runs/*/state.json` + `llm_log.jsonl` into
  one report: HUMAN_GATE vs ESCALATED rate, resolution mix
  (short_circuit/synthesis/fallback/override), tokens and GPU-time per task.
- **Standard task set** — a versioned suite of local, self-contained coding
  tasks (with tests) to characterize a *model*, a *combination*, or a *role
  assignment* on equal footing. `ducklab bench`.
- **`plan` strategy** — peer planning dialogue → execute → verify.

## v0.3 — cost-aware escalation

- **Escalation ladder** — start on `solo`; on red, escalate to `driver`, then
  `tournament`/`plan`. Pay the multi-model cost only on tasks that exceed a
  single model — and record "solo couldn't, the combination could" as a datum.
- **Network resilience** — retry/backoff on transient endpoint failures; resume
  makes a hard failure cheap, but transient blips shouldn't need a human.
- **Run housekeeping** — `ducklab gc` for stale scratch branches.

## v0.4 — decompose to raise the ceiling

- **`split` strategy** — break a task past one model's context/capacity into
  pieces each model *can* do, solved in isolation (worktrees), and integrated
  **deterministically** (structural composition of separate files/regions), not
  by a model rewriting the whole. This is the mode with the most upside for the
  core bet — and the one that fails if integration is left to a weak model.

## Later

- Streaming token output in the REPL; background runs with notification.
- Pluggable strategies without recompiling.
- Prebuilt release binaries per platform.
