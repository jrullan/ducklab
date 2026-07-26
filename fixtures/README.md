# Fixtures

Self-contained repositories used by the acceptance tests.

**Never run ducklab against these directories directly.** A run mutates the
working tree, and a mutated fixture silently stops proving anything: `add.go`
in `fixture-go-red` was committed in its *fixed* state at 8e0a39b, which made
AC-7 pass without the model doing any work.

Copy the fixture to a temp directory first. `e2e/ac_test.sh` and
`fixtures/fixtures_test.go` both do this; follow the same pattern.

| Fixture | Contents | Guards |
|---------|----------|--------|
| `fixture-go-red` | Go module with a deliberate bug in `add.go` and a failing `add_test.go` | AC-7, AC-8 |
| `fixture-nogate` | Markdown only — nothing executable | AC-13 (`UNVERIFIED`, never `PASSED`) |
