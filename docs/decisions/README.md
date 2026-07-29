# Decisions

Where the implementation departs from `ducklab-spec/`, and why.

The spec is normative. Everything here is a place the code does something
else, and each one is a question someone will eventually ask — including
whoever wrote it, months later, having forgotten. A decision that lives only
in a code comment is findable by someone already reading that file. These are
for someone who is not.

Ordinary implementation choices do not belong here. Only divergences, and the
security-relevant judgements that a reader would otherwise have to reverse
engineer.

| # | Decision |
|---|---|
| [0001](0001-skill-allowlist.md) | `skill_run` does not apply the shell allowlist |
| [0002](0002-tampering-spine-ids.md) | The test-tampering guard ignores traceability IDs |
| [0003](0003-apparmor-userns.md) | The desktop ships an AppArmor profile rather than lowering a sysctl |
| [0004](0004-verify-run-shares-the-gate.md) | `verify_run` calls the gate's own code path |
| [0005](0005-canonical-tasks-do-not-discriminate.md) | Canonical problems do not discriminate between models |
| [0006](0006-test-first.md) | The test is written first, by a different model, and read by a person |
| [0007](0007-artifact-modes.md) | An artifact stage may run solo, and says which ducklings it will use |
