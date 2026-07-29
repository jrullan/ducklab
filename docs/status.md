# Status against the specification

What is built, measured against the 67 acceptance criteria in
`ducklab-spec/06-PHASES.md`. Written 2026-07-29 at commit `ac007f1`.

This exists because the phase order in the spec is not the order the work
happened. Ducklab has v0.8 features and unmet v0.1 criteria, and a version
number alone hides that.

Three states, and the difference between them matters:

| | Meaning |
|---|---|
| **done** | Built, and either a test asserts the criterion or it was run against real ducklings. |
| **partial** | Built, but the criterion asks for something the code does not do. What is missing is named. |
| **no** | Not built. |

"done" is not a claim that the feature is good. It is a claim that the
criterion, as written, is met.

---

## v0.1 — Core + engine skeleton

| AC | State | Note |
|----|-------|------|
| AC-1 | partial | Prints `ducklab 0.4.0 (dev, go1.24+, linux/amd64)`. The spec wants the git sha where `dev` is. |
| AC-2 | done | `project init` creates `project.toml`, the DB, extends `.gitignore`, prints the gate, registers. |
| AC-3 | **partial** | `engine.json` is 0600 and `--no-autostart` exits 9, but **auto-start is not implemented** — the CLI prints "run ducklab-engine directly". Every session so far has started the engine by hand. |
| AC-4 | done | `/v1/health` is open, everything else 401s, bind is loopback only (I12). |
| AC-5 | done | Verified against `pato-local` and `pato-atom`; both print `$0.0000`. |
| AC-6 | done | `--dry-run` renders every message with no network call. |
| AC-7 | done | Run many times this cycle; commits on `ducklab/T-00N` with the run id in the trailer. |
| AC-8 | done | `pato-local` is a `native_tools = false` duckling and exercises `@payload:` on every run. |
| AC-9 | done | Closed in commit `0f3ed48`. |
| AC-10 | done | Closed in commit `0f3ed48`. |
| AC-11 | done | Write-guard tests cover the escape, the shrink and the fenced marker. |
| AC-12 | partial | The budget stops the run, but every duckling here is local and priced 0, so the **priced** path in the criterion has never actually run. Token and wallclock enforcement were both broken until this cycle. |
| AC-13 | done | `gate: none` yields `UNVERIFIED`, never `PASSED`. |
| AC-14 | done | Closed in commit `0f3ed48`. |
| AC-15 | done | `make cross` builds linux/amd64, linux/arm64, darwin/arm64, windows/amd64. |
| AC-16 | done | `TestCLIImportsOnlyClientPackages`. It caught a real violation this cycle when the CLI reached for `verify`. |

## v0.2 — Ducklings that talk

| AC | State | Note |
|----|-------|------|
| AC-17 | done | `pair` shows two ducklings; round 2 carries round 1's findings. |
| AC-18 | done | I7: the reviewer's request has the diff and no author identity. |
| AC-19 | partial | Contestants run concurrently in separate worktrees, removed on completion and abort. The **startup reaper after a kill** is not implemented. |
| AC-20 | done | Single green candidate short-circuits and applies byte-identically (I8). |
| AC-21 | done | Two repairs then `ErrContract`; never a guessed choice (I6). |
| AC-22 | done | `Until` parses the expression; `os.Exit(1)` is a load-time error. |
| AC-23 | done | `ask_human` checkpoints and resumes without blocking a goroutine. |
| AC-24 | partial | `ducklab report` exists. Not checked against the exact table in 03 §3.10, including the `solo baseline:` line. |
| AC-25 | done | `max_concurrent_runs` queues; `internal/service/queue_test.go`. |
| AC-26 | done | `token_delta` streams; a slow subscriber gets `overflow` and the run is unaffected. |

## v0.3 — The desktop app appears

| AC | State | Note |
|----|-------|------|
| AC-27 | partial | Builds and launches on Linux. macOS and Windows have never been built. Bundled-engine precedence is untested. |
| AC-28 | done | CLI-started runs appear live in the desktop and vice versa. |
| AC-29 | done | Closing the window does not stop the run; reopening replays the backlog. |
| AC-30 | done | Degraded state dims rather than blanks. |
| AC-31 | **no** | `internal/xplat/notify.go` exists and **nothing calls it**. No OS notification, no tray badge. The in-app "waiting for you" count is there; the criterion is not met. |
| AC-32 | done | Authorship is absent from the payload, not hidden in the UI. |
| AC-33 | partial | `VirtualList` exists and deltas are batched per frame. The 5000-event fps assertion has never been run. |
| AC-34 | done | Accept shows pending until the engine confirms; failure shows an error, never a phantom commit. |
| AC-35 | partial | Playwright runs against the fake engine. Coverage of all five listed flows is unconfirmed. |
| AC-36 | partial | `lib/contrast.ts` and its test exist. Whether every status chip in both themes passes has not been asserted end to end. |

## v0.4 — The front of the cycle

| AC | State | Note |
|----|-------|------|
| AC-37 | done | `intake` writes `requirements.md` with `REQ-00N` sections and matching rows. |
| AC-38 | done | `.proposed` first; reject leaves the original untouched; accept promotes, syncs and runs `trace check`. |
| AC-39 | done | Plan ids match the headings exactly. |
| AC-40 | done | `trace check` names orphans and exits non-zero; the Cycle rail shows them. |
| AC-41 | done | A retried task carries the first attempt's summary. |
| AC-42 | partial | `artifact/memory.go` folds. The 200-task, 8192-byte bound has not been measured. |
| AC-43 | done | A council's `human` turn is an inline input that advances in place. |

## v0.5 — Parity, bugs, review, PRs

| AC | State | Note |
|----|-------|------|
| AC-44 | partial | `internal/arch_test.go` audits parity. Whether **every** service method maps to both a CLI command and a desktop entry point is not currently enforced — several methods added this cycle have no desktop surface. |
| AC-45 | done | `bug add`/`bug triage`; triage proposes without mutating status under `guarded`. |
| AC-46 | done | Duplicate proposal, accept links, reject leaves both open. |
| AC-47 | done | `bug promote` creates the task and the edge; accept sets `fixed`; `bug verify` moves to `verified`. |
| AC-48 | partial | `review` writes the artifact with `file:line` findings and records the red-gate constraint. **Converting a finding to a bug from the desktop is not built.** |
| AC-49 | done | Built this cycle and verified against `pato-atom`, which edited the test that would have caught its change. Hunks pin to the top of the Diff tab. |
| AC-50 | **no** | No GitHub commands exist at all. |
| AC-51 | **no** | Routes support opening on `/runs/:id`, but there is no `⌘⇧O` binding and no second window. |

## v0.6 — Extensibility: skills and MCP

| AC | State | Note |
|----|-------|------|
| AC-52 | done | Documentation-only skill discovered, listed and read. Verified: `pato-atom` read `house-style` and followed it. |
| AC-53 | done | Rejects a description that does not say when, and a missing `entry` file. |
| AC-54 | partial | Runs under the policy and receives `DUCKLAB_ARG_*`. It times out — that was fixed this cycle — but **partial output on timeout has not been asserted**. |
| AC-55 | done | Project shadows global, whole. |
| AC-56 | **no** | MCP is a config type and nothing else. No client, no `mcp_call`. |
| AC-57 | **partial, and the gap is a real one** | `skill validate` runs automatically on the run's diff. But a skill written during a run is **usable immediately** — skills are read from the working tree, so the "unusable until the run is accepted" half of the criterion does not hold. There is also no Skills view in the desktop. |

## v0.7 — Release, deploy, measurement

| AC | State | Note |
|----|-------|------|
| AC-58 | done | `release plan --bump` produces the document; the empty case prints exactly `No user-visible changes.` The wording was wrong until writing this file caught it. |
| AC-59 | **no** | Deploy recipes are a column in the database. No runner, no CLI, no view. |
| AC-60 | **no** | No `bench` command. |
| AC-61 | partial | `report` exists. Measured-vs-estimated separation and the `~` marker are unverified, and there is no desktop table. |
| AC-62 | **no** | No Reports view, so no charts. |

## v0.8 — Split, and opening the harness

| AC | State | Note |
|----|-------|------|
| AC-63 | done | Overlapping-file decompositions are rejected deterministically, retried once with the conflict named, then aborted. |
| AC-64 | done | Integration is file copies only; no model call between the last subtask and the gate. |
| AC-65 | **no** | User modes in `.ducklab/modes/` are not loaded at all, so the "widening a toolbelt fails to load" guarantee is untested — and unneeded, for now. |
| AC-66 | **no** | No `--escalate`. |
| AC-67 | partial | Split runs render as lanes with concurrent turns marked `∥ in parallel`. The integration step is **not** shown as a distinct step marked deterministic. |

## v1.0 — Done means done

Not assessed. Its criteria are about the whole thing being finished.

---

## Totals

| | Count |
|---|---|
| done | 42 |
| partial | 16 |
| no | 9 |

Counted from the tables above, not by hand: the first draft of this file said
34/22/11 because I estimated instead of counting.

## What the gaps say

Read together rather than one by one, three things stand out.

**Nothing measures anything yet.** `bench`, the Reports view, the charts and
the measured-vs-estimated split are all missing, and `report` has never been
checked against its own spec. P9 in the vision is "measurable, or it didn't
happen" — and the harness for comparing ducklings, which is most of why
Ducklab exists, is the least built part of it.

**Two v0.1 criteria are still open**, and both are ergonomic: the engine does
not auto-start, and the version has no sha. The first has been worked around
by hand in every session, which is exactly how a gap survives eight phases.

**AC-57 is the only gap that is a correctness problem.** A skill a duckling
writes is usable the moment it is written, before anyone has accepted the run
that created it. The spec puts the human accept between the two on purpose.
