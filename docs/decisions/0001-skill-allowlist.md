# 0001 — `skill_run` does not apply the shell allowlist

**Status:** accepted, 2026-07-29
**Diverges from:** 05 §7 — "`skill_run` executes `entry` through the same shell
policy as the `shell` tool"

## The problem

Read literally, this makes skills unusable.

A project's shell policy in `guarded` mode — the default — allows commands by
prefix: `go `, `npm `, `pytest`. A skill's command is an absolute path to a
script inside `.ducklab/skills/<name>/`. No sane allowlist contains one, so
every `skill_run` in a default project would be refused.

That is not a corner case. It is every skill, in every project, unless the
human first adds a path prefix to `allow_prefixes` — at which point they have
allowed a directory a model can write into, which is worse than where we
started.

## The decision

The denylist applies. `mode = "off"` applies. The allowlist does not.

## Why this is not a weakening

The allowlist exists because a model composes shell commands out of nothing.
There is no prior review of `rm -rf /` — the prefix list is the only thing
standing between the model's imagination and the shell.

A skill inverts that. The script is a file on disk. It arrived through
`fs_write`, under the write guard, and a human read it in a diff and accepted
the run before it could be called at all (05 §7.1). The model's remaining
freedom is: which accepted script to run, and what argument values to pass.
Values are shell-quoted, with a test that an argument containing `;` does not
become a second command.

So the gate on a skill is a human reading it. That is stronger than a prefix
match, not weaker.

## What was rejected

**Applying the allowlist literally.** Correct to the letter and useless in
practice; the feature would ship dead and the first person to try it would
conclude skills are broken.

**Auto-allowing the skills directory as a prefix.** This looks like compliance
and is strictly worse: it permanently allows a path a model can write new files
into, so a future skill needs no human at all.

**Requiring `mode = "free"` for skills.** Pushes the whole project to a weaker
policy in order to use one feature.

## Revisiting

If skills ever become loadable from somewhere a human has not accepted — a
registry, a URL, a package manager — this decision expires with it. The whole
argument rests on "a human read this file".
