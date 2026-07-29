# 0004 — `verify_run` calls the gate's own code path

**Status:** accepted, 2026-07-29
**Relates to:** 04 §2.3 — the `verify_run` tool; 05 §5 — the gate

## The problem

`verify_run` ran `go test ./...`, hardcoded, with a comment calling itself a
placeholder. The gate that decides a run's verdict reads the project's
`[verify]` config, which may be `go build`, `npm test`, or a custom command.

Two different commands answering the same question is not a rough edge. It is
a harness that lies to the model it is running.

On a project whose gate is `go build`, a duckling called `verify_run`, was
told exit 0, and stopped. The real gate then failed on work the model had just
been told was fine. Three rounds of a model insisting it was done, against a
gate it had never executed once.

## The decision

`verify_run` calls `verify.Run` with the project's own config, carried on the
`ExecContext`. The tool and the gate are the same function.

## Why not "pass the command string through"

A command string would have worked and would have drifted. Gate selection has
logic in it — auto-detection, mode-to-command mapping, the timeout, how a
missing gate becomes `none` rather than a failure. Duplicating any of that
creates a second place to fix a bug, and the whole failure here was two things
answering one question differently.

The rule this generalises to: a tool that reports on a decision must run the
decision's code, not a lookalike.
