# 0003 — The desktop ships an AppArmor profile rather than lowering a sysctl

**Status:** accepted, 2026-07
**Relates to:** 08 — desktop packaging. The spec is silent on this.

## The problem

On Ubuntu 24.04 and later, unprivileged user namespaces are restricted by
`kernel.apparmor_restrict_unprivileged_userns=1`. WebKitGTK's sandbox needs
them. Without something, the desktop will not start.

## The decision

Ship an AppArmor profile at `packaging/apparmor/ducklab-desktop` that grants
`userns` to the installed binary path, and nothing else.

## What was rejected, and why it matters that it was

**`sysctl kernel.apparmor_restrict_unprivileged_userns=0`.** This turns the
restriction off for every program on the machine to make one program work. It
is the answer that appears first in every search result and it is a
machine-wide weakening in exchange for a single-application need.

**`WEBKIT_FORCE_SANDBOX=0`.** Turns off WebKit's own sandbox — in the process
that renders the frontend and holds the engine's bearer token.

Both were rejected for the same reason: the first response to a security
control blocking your program should be to find the sanctioned way through it,
not to remove it. A profile scoped to one binary path is the sanctioned way.

## Consequence

Installing the desktop is not just copying a file on these distributions. The
profile has to be installed too, and the profile matches on an absolute path —
which is why `make install` puts the binary at a fixed location rather than
leaving it in `bin/`.
