# Visual usability audit — 2026-08-25

Auditor: the operator model, with eyes on real Playwright captures
(1440×900, chromium) of the Vite-served frontend against the fake engine.
Doctrine applied: the user is NOT the ducklab expert; the UI is a
first-order surface; inexpert-first, no-forensics, jargon explains itself
at point of use; intuitive, pleasant, simple, effective.

Scope honesty: 15 surfaces captured, 6 audited in depth (Now, Tasks,
Runs, Roster, Settings, Skills). Several views could not render data
because the fake engine has drifted behind the app's API (finding I-2) —
re-run this audit after parity is restored. Visual aesthetics were
auditable; interaction feel (latency, motion) was not.

## I. Findings about the audit path itself (dev infrastructure)

- **I-1** The documented browser dev flow cannot use a REAL engine: the
  engine serves no CORS headers (it never needed them for the desktop),
  so `?engine=&token=` against a real engine yields silent request
  failures and a misleading "session died" banner. Either document
  "fake engine only" or add opt-in CORS for loopback dev.
- **I-2** The fake engine drifted behind the app: `tasks?summary=true`,
  `bugs?summary=true`, `/v1/providers`, `/v1/defaults/budget`,
  `roster?mode=`, `/skills` are unknown to it, so Tasks, Bugs, Settings
  panels, Roster council row and Skills all error in the documented dev
  flow. Nothing pins fake-engine parity with the routes table.
- **I-3** README's example port 8787 collided with an unrelated local
  service, producing confusing 401s before "address already in use" was
  noticed. Cosmetic, but the flow's first-run experience depends on it.

## II. Systemic: error presentation

- **II-1** User-facing errors mix a perfect plain sentence ("it is older
  than this app. Restart the engine.") with developer debris: raw
  `GET /v1/...` paths and an `ApiError:` prefix ("could not read the
  settings: ApiError: ..."). Pattern fix, one component: plain sentence
  first; method/path/status behind a "details" disclosure.
- **II-2** Contradictory simultaneous states: Skills shows an error line
  AND "Loading…" together. An errored fetch should terminate its loading
  state everywhere.

## III. The inexpert user's first screen (Now)

- **III-1** The one card a novice must understand reads
  "build · T-001  pair  ⚠ passed  waiting 0s" — four unexplained terms.
  The amber ⚠ beside a green-worded "passed" is the worst offender:
  passed-with-warning? unverified? Nothing at point of use says. The card
  should carry one plain sentence ("This task finished and passed its
  tests — it's waiting for your decision") with the chips as detail, and
  the ⚠ needs a tooltip or a word.
- **III-2** "today $0.0000 · all time $0.0000" — four decimals of zero
  reads like a rendering glitch; $0.00 (or "nothing spent yet") is human.

## IV. Where the doctrine already shines (keep these; they are the house style)

- Roster mode subtitles are exemplary plain-language teaching: "driver
  and navigator — an implementer builds, a reviewer reads each round";
  "decompose to raise the ceiling". This register is the target for
  every other surface.
- Settings taxonomy is human ("my ducklings", "budgets & limits",
  "appearance & alerts") and "Add a provider first — a duckling is a
  model reached through one" is perfect point-of-use jargon teaching.
- The Now view's radical focus (one thing waiting, cost line, plain
  footer "✓ engine · 1 waiting for you") is the right shape.
- Runs table: plain headers, honest counts, the landed filter present.

## V. Smaller frictions

- **V-1** Roster's filter chip row (beelink · openrouter · local ·
  remote · vision · tools · ≥128k…) has no label; a novice sees mystery
  pills. A three-word caption ("filter the flock:") fixes it.
- **V-2** Skills' header paragraph is an implementation dump (paths,
  shadowing, name collisions) where a purpose sentence belongs ("a
  recipe your models can read — or run — when a task calls for it"),
  with the internals behind a "how it works" disclosure.

## Verdict

The bones are excellent: information architecture is human, the plain-
language register exists and is house style where it exists. The gaps
are consistency (error surfaces and the Now card never got the register)
and the dev-infrastructure drift that blocks auditing — and blocks any
contributor's frontend work — until fake-engine parity is pinned.
