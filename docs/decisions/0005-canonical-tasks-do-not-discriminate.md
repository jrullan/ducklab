# 0005 — Canonical problems do not discriminate between models

**Status:** finding, 2026-07-29
**Relates to:** 03 §3.10 (`bench`), AC-60

## What happened

`std` v1 was five small Go tasks. Two very different ducklings ran it:

- `pato-atom` — Qwen3.6 FP8 on a GB10, vLLM, native tool calling
- `pato-local` — Qwen3.6 Q4 on a Strix Halo, llama.cpp, text tool protocol

Both scored 5/5. That is not a tie between equals; it is a suite measuring
below both their ceilings.

So `std` v2 added four harder tasks: an LRU cache, merging intervals, a
race-checked worker pool that must preserve order, and a signature change
across three call sites.

Both scored 9/9.

## Why

The four "harder" tasks are canonical. LRU and merge-intervals are among the
most reproduced programming exercises in existence, and a bounded worker pool
is in every Go tutorial. A model has not reasoned its way to those answers; it
has seen them. Difficulty for a person and difficulty for a language model are
different axes, and the suite was built along the wrong one.

## What the bench did measure

Effort, and clearly. On B-004 — a rename across two files — `pato-atom` spent
34,765 tokens and `pato-local` spent 72,293, better than double, and both
passed. Across the suite: 211,149 against 238,238.

That is a real difference between two models that a pass rate reports as
identical, and it is what the Bench view now shows when correctness ties.

## What would discriminate

Untested. Stated as hypotheses, not as a plan:

- Tasks whose answer cannot be recalled: a project-specific invariant, an
  unusual convention, a domain the model must read rather than remember.
- Tasks that need a lot of existing code read before the first edit, where a
  smaller context window and a weaker retrieval habit cost real accuracy.
- Tasks with a plausible wrong answer that a test catches — not an edge case
  of a known algorithm, but a wrong *approach*.
- Longer chains, where one early mistake compounds instead of being caught by
  the next test.

## The lesson worth keeping

When picking bench tasks, "would this be hard for a junior engineer" is the
wrong question. The right one is "could a model have memorised the answer" —
and for anything with a name, it could.
