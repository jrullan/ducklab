# Desktop blueprint — the coherent target after the 2026-08 field audit

This is the binding design reference for the desktop rework. Task briefs
cite sections of this file; where a brief and this file disagree, this
file wins. It consolidates nine field observations from the operator
(Jose) plus the UI benchmark synthesis (docs/ux-audit-visual-2026-08.md
is the earlier audit; the benchmark evaluations live outside the repo).

## 1. Shell: two zones, nothing else

The window has exactly two permanent zones: the **sidebar** and the
**content pane**. No middle utility column (its jobs move to the
surfaces that own them: next steps are decisions → Now; recent runs →
Records; autopilot control → sidebar, near engine status). No standing
right-hand rail on any view (see §3 for where its content went).

Sidebar, top to bottom:
- **No app-name text** — the window title bar already says it. At most
  the glyph.
- **One project control**: the selector shows AND switches the project.
  No separate informational project card.
- **Branch is exception-state**: hidden when the project sits on its
  base branch; when it deviates, a worded line ("on branch X — not the
  base"), because that is when it changes decisions.
- Primary nav: Now (waiting badge) · Work · Records · Settings — four
  words, four questions. Subnav renders visually attached to its own
  parent, never adjacent to a sibling heading.
- Footer: the system-truth zone — engine status and the waiting
  summary, plain words. **Update availability lives here, as
  exception-state**: silent when current; one worded line when a newer
  desktop exists ("update ready — 0.9.1 · restart when idle") whose
  action uses the engine's checkpointed restart so nothing in flight is
  lost. A blocking version mismatch (engine older than the app,
  features failing) remains a banner — broken-now interrupts,
  available-later does not.

The Settings consolidation (four entries → one, organized by the
user's three questions) is specified in its own proposal and lands as
its own arc; this blueprint only fixes the target: ONE settings entry.

## 2. Documents: index + detail, never a scroll-wall

The documents surface replaces linear scrolling with a **pinned frame**:

- **Stage control**: one control owns Requirements/Spec/Plan switching.
  The teaching lines ("you write this; nobody codes from it", etc.)
  are captions on that control — nothing inert may look pressable.
- **Index pane** (pinned): every section of the active stage as
  `id — title` rows with a type-ahead filter that narrows as you type.
  Rows carry state markers inline (break, no task yet, proposal
  pending). Selecting a row scrolls/loads the detail pane to that
  section. Answering: "how do I find one section with minimum
  interaction and time?"
- **Detail pane**: the person's words first — "what you asked for"
  open at top, never collapsed while machine text sprawls. Then the
  selected section(s) as **summary-first cards**: one plain sentence,
  everything else behind a disclosure. Markers like "As-built" are
  explained at point of use or dropped.
- **Claims are verified before rendered**: a section claiming
  `implements REQ-008` renders that claim only if the target exists;
  otherwise "claims REQ-008 — no such requirement exists", styled as
  the break it is. The card and the trace check can never disagree on
  the same screen.
- **The redraft machinery lives behind an affordance**, below or beside
  the document — never before it. Its default face is one plain line
  ("k3 drafts, glm52 critiques until it approves — about $1"); seats,
  rounds, caps appear on disclosure. Helper copy speaks human, not
  ids/diff/gate.

## 3. Gaps: in place, at decision time, on demand

The traceability rail is retired. Its information lives in three homes:
- **In place**: the section's own marker (index row + card), e.g.
  "no task born yet", "claims a requirement that does not exist".
- **At decision time**: coverage as evidence on approval cards
  ("N of M criteria covered · K sections without a task") — phase-2
  plan approval consumes this.
- **On demand — the ledger**: one view listing every break with its
  two ways out (create the missing piece / mark non-normative / amend
  the document). Table shape: what the document says / what exists /
  since when / settle it. Reached from a single permanent health line
  ("16 breaks in the spine") which is the ONLY ambient trace of the
  old rail.

## 4. Cross-cutting rules (apply to every surface, every future task)

1. Nothing inert may look pressable; every control that looks pressable
   does something.
2. Machinery (seats, tokens, rounds, raw ids) renders behind
   disclosure; the default face is one plain sentence.
3. Status is exception-based: quiet when normal, worded when deviating.
4. A page is owned by zones; every zone answers "what job does this do
   for the user?" — a zone without an answer is removed.
5. Landing a surface change requires a real-render pass against the
   live engine with real project data, not only a green gate.
6. **Every routed view keeps a door**: any view reachable by route must
   be reachable from the sidebar tree. Hiding an entry without re-homing
   its destination is a regression; a vitest reachability test walks the
   sidebar tree and asserts every registered route appears.
