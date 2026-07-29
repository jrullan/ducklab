# 0007 — An artifact stage may run solo, and says which ducklings it will use

**Status:** accepted, 2026-07-29
**Diverges from:** 05 §4.4 — "`council` — the artifact modes. Used by `intake`,
`spec`, `plan`."

## The problem

The Cycle view offered one button, `Draft it`, and nothing else. Behind it: a
council, always, with the roster picking who drafts and who critiques. The
`mode` field on the stage request existed and was never read.

Two complaints, from the first person to use it:

- It does not say which ducklings will take part.
- It does not let me choose.

The first is not arguable. A button that spends minutes and tokens without
saying whose minutes and tokens is hiding the only two facts worth knowing
first.

## The decision

The stage reports what it will do — "pato-sonnet drafts, pato-local critiques"
— and offers `council` or `solo`. Council stays the default.

Solo is one architect and no reviewer. That is a real deviation from §4.4,
which names council as *the* artifact mode.

## Why the deviation is worth it

Council's value is a second model critiquing the draft. That is worth its cost
on a first draft of requirements and frequently not worth it on a one-line
revision — where the reviewer's turn doubles the wait to approve a change the
person already specified precisely.

Offering the choice is also more honest than the alternative people actually
use, which is to accept a council result they did not need and move on. A mode
nobody can see is a mode nobody can reason about.

## What it must not do

**Silently change what a run means.** The run record stores the mode that ran,
not the constant "council" it stored before. A report claiming every stage was
a council when half were solo cannot answer the question reports exist for.

**Hide a degenerate roster.** When the architect and the reviewer resolve to
the same duckling, the panel says so — that measures self-consistency, not
review (05 §3.2), and the place to say it is while the choice is still open.

**Change what gets written.** Both scripts carry the same contract, so the mode
decides who writes the document and never what shape it has.
