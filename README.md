```
   __
 <(o )___   ducklab
  ( ._> /   a multi-model harness for local LLMs
   `---'
```

# ducklab

The solo developer keeps a rubber duck on the desk to think out loud. **ducklab
gives the duck a voice — and sometimes two ducks that argue.** It is a light,
cross-platform terminal harness for developing with the models you can run
*locally*, on the bet that combining several modest models with the right
strategy beats any one of them alone.

ducklab is **not** trying to match a frontier model. Its aim is to raise the
ceiling of what your *local* hardware can produce today — and to keep doing so
as local models get better and local hardware gets cheaper.

## Why

A single local model has systematic blind spots from its own training. Two
decorrelated models — one that writes, one that reviews — plus **executable
tests as the only ground truth** can push past what either reaches solo. The
orchestrator is deterministic; models only ever produce text. Git, tests, and
control flow belong to ducklab, never to the model.

## Install

Single static binary, no runtime, three platforms:

```bash
go build -o ducklab ./cmd/ducklab      # or: go install github.com/jrullan/ducklab/cmd/ducklab@latest
./ducklab version
```

## Use

```bash
ducklab chat                            # interactive REPL (recommended)
ducklab sources                         # list configured models + reachability
ducklab run <id> <req.txt> --repo . --tests "go test ./..." --mode driver
ducklab resume <id> --repo .            # resume from state.json
```

Every action in the REPL is also a scriptable subcommand. Per-run artifacts —
solutions, diffs, test output, judge verdict, and a resumable `state.json` —
land in `<repo>/runs/<id>/` (git-ignore that directory in your target repo).

## Strategies (recipes)

Each strategy is a deterministic state machine wiring model roles over shared
primitives. Add `--mode`:

| Mode | What it does | Status |
|------|--------------|--------|
| `solo` | one model solves; tests decide. **The baseline every other mode is measured against.** | ✅ |
| `driver` | one model drives surgical SEARCH/REPLACE edits, a second observes and approves or corrects (up to 3 rounds) | ✅ |
| `tournament` | two models solve independently; an independent judge picks a green winner (short-circuit), overrides a cheating winner, or synthesizes as a last resort | ✅ |
| `plan` | peer dialogue: A drafts a plan, B gives observations, A decides, then execute + verify | ⏳ roadmap |
| `split` | decompose a task beyond one model's reach, solve pieces, integrate **deterministically** | ⏳ roadmap |

A hard-won rule baked into `tournament`: on modest local models, free
regeneration corrupts. The judge's value is **evaluation**, not rewriting — a
green solution is applied verbatim, never regenerated.

## Verification is a spectrum

ducklab's thesis is *executable verification is the ground truth, not the other
model's opinion*. But ground truth comes in tiers, and ducklab always tells you
which rung a run stood on — it never fakes a verdict:

| Gate | Example | Outcome when it passes |
|------|---------|------------------------|
| **tests** | `go test ./...`, `pytest -q`, `npm test` | `HUMAN_GATE` (green) |
| **build** | `tsc --noEmit`, `python -m compileall` | `HUMAN_GATE` (compiles) |
| **none** | docs, config, a new repo with no harness | `UNVERIFIED` — reviewer + your eyes are the gate |

The gate is **auto-detected** (tests → build → none) and shown in `/config` and
`/show`. Override with `--verify "<cmd>"` (`ducklab run`) or `/verify <cmd|auto|none>`
(REPL). An `UNVERIFIED` run still produces a diff and still reaches your
`/accept` — it's just labeled honestly, never dressed up as tested-green (or
faked-red).

## Sources

Defaults: `beelink` (`localhost:8081`, model auto-detected) and `aitopatom`
(`10.0.0.5:8000`). Override with env vars
`DUCKLAB_<NAME>_BASE_URL | _MODEL | _API_KEY`, or a JSON file at
`~/.config/ducklab/config.json` (`base_url`/`model` only — **API keys live only
in the environment**, never in the file).

Any OpenAI-compatible endpoint works (llama.cpp, vLLM, …). `disable_thinking` is
on by default: some local models burn the token budget on hidden reasoning and
return empty completions on strict-format tasks.

## Design

- **Light & portable** — one Go binary, standard library at the core, Charm for
  the terminal UI. No POSIX-only tricks; Linux, macOS, Windows.
- **Tests are ground truth** — the judge evaluates, the tests verify.
- **Isolated review** — the judge sees anonymized A/B diffs and the test
  verdicts, never the author's identity or reasoning.
- **Nothing is unbounded** — every loop has a round cap; every run resumes from
  disk.

## Status

`v0.1` — core engine, `solo`/`driver`/`tournament`, interactive REPL, resumable
runs, validated end-to-end against real local models. See [ROADMAP](ROADMAP.md).

MIT licensed.
