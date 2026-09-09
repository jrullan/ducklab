import { describe, it, expect } from "vitest";
import { evidenceFromDiff, proofsFromDiff } from "./VerificationLedger";
import { parseDiff } from "../lib/runview";

// B-216: the proof of an internal fix is the test its accepted diff added,
// named with the exact command that runs it. Nothing a model wrote counts.
describe("proofs from an accepted diff", () => {
  const diff = [
    "diff --git a/internal/service/recovery.go b/internal/service/recovery.go",
    "--- a/internal/service/recovery.go",
    "+++ b/internal/service/recovery.go",
    "@@ -1,2 +1,3 @@",
    "+func recover() {}",
    "diff --git a/internal/service/recovery_test.go b/internal/service/recovery_test.go",
    "--- a/internal/service/recovery_test.go",
    "+++ b/internal/service/recovery_test.go",
    "@@ -10,0 +11,4 @@",
    "+func TestProjectRecoveryDoors(t *testing.T) {",
    "+}",
    "+func TestProjectRecoveryKeepsId(t *testing.T) {",
    " func TestUnchanged(t *testing.T) {",
    "diff --git a/frontend/src/views/now.test.tsx b/frontend/src/views/now.test.tsx",
    "--- a/frontend/src/views/now.test.tsx",
    "+++ b/frontend/src/views/now.test.tsx",
    "@@ -5,0 +6,2 @@",
    '+  it("folds every fixed report into one ledger card", async () => {',
    "+  });",
    "diff --git a/tests/test_probe.py b/tests/test_probe.py",
    "--- a/tests/test_probe.py",
    "+++ b/tests/test_probe.py",
    "@@ -0,0 +1,2 @@",
    "+def test_probe_reports_reason():",
    "+    pass",
  ].join("\n");

  it("names each added test with the command that runs it, per language", () => {
    const proofs = proofsFromDiff(parseDiff(diff));
    expect(proofs.map((p) => p.cmd)).toEqual([
      "go test ./internal/service -run '^TestProjectRecoveryDoors$'",
      "go test ./internal/service -run '^TestProjectRecoveryKeepsId$'",
      'cd frontend && npx vitest run src/views/now.test.tsx -t "folds every fixed report into one ledger card"',
      "pytest tests/test_probe.py -k test_probe_reports_reason",
    ]);
    // A pre-existing test in the same file is not a proof of this fix.
    expect(proofs.map((p) => p.name)).not.toContain("TestUnchanged");
  });

  // Fledge is Rust: a #[test] is a proof whether it lives under tests/ or
  // inside src/, and the command is cargo's.
  it("names Rust tests by their attribute, wherever they live", () => {
    const rust = [
      "diff --git a/tests/conformance.rs b/tests/conformance.rs",
      "--- /dev/null",
      "+++ b/tests/conformance.rs",
      "@@ -0,0 +1,6 @@",
      "+#[test]",
      "+fn fixture_isolation() {",
      "+}",
      "+#[tokio::test]",
      "+async fn dispatch_rejects_unknown_schema() {",
      "+}",
      "diff --git a/src/lib.rs b/src/lib.rs",
      "--- a/src/lib.rs",
      "+++ b/src/lib.rs",
      "@@ -40,0 +41,4 @@",
      "+    #[test]",
      "+    fn selection_defaults() {",
      "+    }",
      "+pub fn not_a_test() {}",
    ].join("\n");
    const ev = evidenceFromDiff(rust);
    expect(ev.proofs.map((p) => p.cmd)).toEqual([
      "cargo test fixture_isolation",
      "cargo test dispatch_rejects_unknown_schema",
      "cargo test selection_defaults",
    ]);
    expect(ev.unknownTests).toEqual([]);
  });

  // A stack Ducklab cannot derive a command for is not "no test": the row
  // must say the tests exist and cannot yet be run for the person, never
  // that the only proof is their eyes.
  it("separates tests it can see but not name from a diff with no test at all", () => {
    const c = [
      "diff --git a/tests/check_probe.c b/tests/check_probe.c",
      "--- /dev/null",
      "+++ b/tests/check_probe.c",
      "@@ -0,0 +1,3 @@",
      "+static void test_probe_reports_reason(void) {",
      "+}",
      "diff --git a/src/probe.c b/src/probe.c",
      "--- a/src/probe.c",
      "+++ b/src/probe.c",
      "@@ -1 +1 @@",
      "+int probe(void) { return 0; }",
    ].join("\n");
    const ev = evidenceFromDiff(c);
    expect(ev.proofs).toEqual([]);
    expect(ev.unknownTests).toEqual(["tests/check_probe.c"]);
    // A test file added with no test in it, in a known stack, is not evidence either way.
    const emptyGo = "diff --git a/x_test.go b/x_test.go\n--- a/x_test.go\n+++ b/x_test.go\n@@ -1 +1 @@\n+package x\n";
    expect(evidenceFromDiff(emptyGo).unknownTests).toEqual([]);
  });

  it("lists the changed files and finds no proof in a diff without tests", () => {
    const ev = evidenceFromDiff("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n+package x\n");
    expect(ev.files).toEqual(["x.go"]);
    expect(ev.proofs).toEqual([]);
    expect(ev.unknownTests).toEqual([]);
  });
});
