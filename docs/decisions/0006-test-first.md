# 0006 — The test is written first, by a different model, and read by a person

**Status:** accepted, 2026-07-29
**Extends:** 05 §5 (the gate), 05 §5.3 (the tampering guard). The spec is
silent on who writes tests.

## The problem

`verify.mode = none` yields `UNVERIFIED`, which is honest and useless: nothing
was checked. The obvious repair is to make "write tests" part of the task.

That repair is circular. The same model writes the test and the code in one
run, and the gate then measures whether a model agrees with itself — the same
objection 05 §3.2 raises against using one duckling as both implementer and
reviewer.

It is worse than circular, because it disables the one defence already in
place. The tampering guard stays quiet when the task mentions tests (§5.3, and
deliberately: a warning that fires on requested work is a warning people learn
to dismiss). So a task saying "add tests" is precisely the shape in which a
model can write a test fitted to whatever the code happens to do, and nothing
will say a word.

## The decision

`ducklab test <task> [--duckling ID]` writes the test, on its own, before the
implementation exists. A human accepts it. Then `ducklab run <task>` builds
against a gate it did not author.

Two facts are established without asking a model:

- **Only test files were written.** Enforced in the write guard, not requested
  in the prompt. A prompt is a request; this is a rule.
- **The gate went red.** A test that passes against absent code has asserted
  nothing. If the suite was already red, the verdict is `UNVERIFIED` rather
  than `PASSED`: "it is red now" proves nothing on its own (§5.2).

The tampering guard is repaired as a side effect. The build task no longer
mentions tests, so touching one is flagged.

## What this does not fix, and cannot

**A wrong test becomes the specification.** This is not a residual risk, it is
the mechanism working as designed: the point is that the test decides. If the
test is wrong, correct code fails.

Both times this ran against a real model, the test was wrong:

- The first wrote a table asserting a total of 100 cents from an empty slice,
  and a case spreading a remainder the opposite way from both the task and its
  own earlier case. A correct implementation was judged `FAILED` by it.
- The second demanded `[142858 ×6, 142854]` for 1000000 among 7 — which sums
  to 1000002.

Neither is detectable by any check the harness can run. A test that is
internally inconsistent still compiles, still fails, and still looks like a
specification.

So the human gate is not a formality here; it is where the specification is
actually decided. Which is why the first version of this was wrong: it printed
"read it, then accept" and showed nothing. The author of that message then
accepted a test without reading it and spent a run discovering why. The gate
now prints the test.

## Consequence

The value of test-first is not that a model writes better tests. It is that
the test exists as a separate artifact, read and approved on its own, before
anything is built to satisfy it — and that whatever passes afterwards passed
something a person agreed to.
