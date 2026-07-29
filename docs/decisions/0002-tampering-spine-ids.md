# 0002 — The test-tampering guard ignores traceability IDs

**Status:** accepted, 2026-07-29
**Refines:** 05 §5.3 — "if the diff modifies tests **and** the task did not
mention tests"

## The problem

"Did the task mention tests" is a judgement, and the spec does not say how to
make it. The obvious implementation is a word list: `test`, `tests`, `spec`,
`coverage`, `assertion`.

That implementation is wrong here, and it was wrong in a way that switched the
guard off exactly where it mattered most.

Every properly traced task carries `**Implements:** SPEC-001` in its body. The
spine (02 §3) uses `PREFIX-<digits>` ids, and `SPEC` is one of them. So the
word list matched, `Asked` came back true, and the guard stayed quiet — on the
best-documented tasks in the project.

It was found by running it. `pato-atom` was given "make `Add` multiply
instead" on a repo whose test asserts `Add(2,3) == 5`. It changed both files,
the gate went green, and nothing was flagged.

## The decision

`MentionsTests` strips `[A-Za-z]+-\d+` before matching. A traceability
reference is not a request for test work.

`spec` stays in the word list: `*.spec.ts` is how a large part of the world
names its tests, and "update the spec" is a real ask.

## Why this shape

The alternative — dropping `spec` from the word list — would have made
"add specs for the parser" flag as tampering, which trains a reader to dismiss
the warning. A warning that fires on ordinary work is worse than no warning,
because it teaches people to click past the one that matters.

Stripping IDs is narrow and it is about a thing this project actually owns: the
spine's id format is ours, defined in the spec, and unlikely to drift.

## Lesson worth keeping

This class of bug — a heuristic that is silently inverted by another feature's
output — does not surface in unit tests, because the unit test author writes
the input by hand and writes it without the frontmatter. It surfaced on the
first real task. That is an argument for running the thing, not a suggestion
to write more careful unit tests.
