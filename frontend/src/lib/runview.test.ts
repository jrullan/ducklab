import { describe, it, expect } from "vitest";
import {
  buildTurns, anonymiseTurns, buildTimeline, toolFamily,
  buildGate, buildPending, parseDiff, toolTarget, reviewerDissent, findingsFiled, chainedBuildId, orderDiffFiles, touchesTests, isTestPath,
} from "./runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

describe("buildTurns", () => {
  it("groups events into turns and collapses tool calls into them", () => {
    const turns = buildTurns([
      ev("run_start", 1),
      ev("turn_start", 2, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("tool_call", 3, { tool: "fs_read", ok: true, ms: 3 }),
      ev("tool_call", 4, { tool: "fs_patch", ok: true, ms: 12 }),
      ev("turn_end", 5, {}),
      ev("turn_start", 6, { round: 1, turn: 1, role: "reviewer", duckling: "pato-dos" }),
      ev("turn_end", 7, {}),
    ]);

    expect(turns).toHaveLength(2);
    expect(turns[0]!.role).toBe("implementer");
    expect(turns[0]!.toolCalls.map((t) => t.tool)).toEqual(["fs_read", "fs_patch"]);
    expect(turns[0]!.done).toBe(true);
    expect(turns[1]!.role).toBe("reviewer");
    expect(turns[1]!.toolCalls).toHaveLength(0);
  });

  // A run with forty reads must stay skimmable: forty calls, still one turn.
  it("keeps a turn with many tool calls as a single block", () => {
    const events: DucklabEvent[] = [
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "a" }),
    ];
    for (let i = 0; i < 40; i++) events.push(ev("tool_call", i + 2, { tool: "fs_read", ok: true }));
    const turns = buildTurns(events);
    expect(turns).toHaveLength(1);
    expect(turns[0]!.toolCalls).toHaveLength(40);
  });

  it("marks a policy violation so it can be expanded by default", () => {
    const turns = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "a" }),
      ev("policy_violation", 2, { tool: "fs_write", detail: "path escapes root" }),
    ]);
    const call = turns[0]!.toolCalls[0]!;
    expect(call.violation).toBe(true);
    expect(call.ok).toBe(false);
    expect(call.detail).toContain("escapes root");
  });

  it("leaves an unfinished turn marked not done", () => {
    const turns = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "a" })]);
    expect(turns[0]!.done).toBe(false);
  });
});

describe("anonymiseTurns", () => {
  // I7 as a property of the product: the UI must not hold a mapping it could
  // render by accident.
  it("drops the duckling id entirely and substitutes a stable letter", () => {
    const turns = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("turn_end", 2),
      ev("turn_start", 3, { round: 1, turn: 1, role: "implementer", duckling: "pato-dos" }),
      ev("turn_end", 4),
      ev("turn_start", 5, { round: 2, turn: 0, role: "implementer", duckling: "pato-uno" }),
    ]);
    const anon = anonymiseTurns(turns, true);

    for (const t of anon) expect(t.duckling).toBe("");
    expect(anon[0]!.label).toBe("A");
    expect(anon[1]!.label).toBe("B");
    // The same duckling keeps its letter across rounds.
    expect(anon[2]!.label).toBe("A");

    expect(JSON.stringify(anon)).not.toContain("pato-uno");
    expect(JSON.stringify(anon)).not.toContain("pato-dos");
  });

  it("leaves identities intact when not anonymising", () => {
    const turns = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" })]);
    expect(anonymiseTurns(turns, false)[0]!.duckling).toBe("pato-uno");
  });
});

describe("timeline", () => {
  it("emits one tick per tool call, in order", () => {
    const ticks = buildTimeline([
      ev("turn_start", 1, {}),
      ev("tool_call", 2, { tool: "fs_read" }),
      ev("tool_call", 3, { tool: "verify_run" }),
      ev("policy_violation", 4, { tool: "fs_write" }),
    ]);
    expect(ticks.map((t) => t.tool)).toEqual(["fs_read", "verify_run", "fs_write"]);
    expect(ticks[2]!.ok).toBe(false);
  });

  it("groups tools into families", () => {
    expect(toolFamily("fs_read")).toBe("read");
    expect(toolFamily("fs_search")).toBe("read");
    expect(toolFamily("fs_patch")).toBe("write");
    expect(toolFamily("fs_write")).toBe("write");
    expect(toolFamily("fs_write_lines")).toBe("write");
    expect(toolFamily("shell")).toBe("exec");
    expect(toolFamily("verify_run")).toBe("exec");
    expect(toolFamily("git_diff")).toBe("vcs");
    expect(toolFamily("mcp_call")).toBe("other");
  });
});

describe("buildGate", () => {
  // P3: nothing ran, so it must not render as a neutral tick a user reads as green.
  it("never lets a none gate read as success", () => {
    const gate = buildGate([ev("gate", 1, { gate: "none", exit: 0 })])!;
    expect(gate.unverified).toBe(true);
    expect(gate.role).toBe("warning");
    expect(gate.label).toContain("unverified");
    expect(gate.role).not.toBe("good");
  });

  it("reports green and red from the exit code, not from prose", () => {
    expect(buildGate([ev("gate", 1, { gate: "tests", exit: 0 })])!.role).toBe("good");
    expect(buildGate([ev("gate", 1, { gate: "tests", exit: 1 })])!.role).toBe("critical");
  });

  it("uses the latest gate when a run had several rounds", () => {
    const gate = buildGate([
      ev("gate", 1, { gate: "tests", exit: 1 }),
      ev("gate", 2, { gate: "tests", exit: 0 }),
    ])!;
    expect(gate.role).toBe("good");
  });

  it("returns null before any gate has run", () => {
    expect(buildGate([ev("turn_start", 1)])).toBeNull();
  });
});

describe("buildPending", () => {
  it("surfaces a pending question with its id", () => {
    const p = buildPending([
      ev("human_needed", 1, { kind: "question", question: "Wrap or saturate?", question_id: "q1" }),
    ])!;
    expect(p.kind).toBe("question");
    expect(p.questionId).toBe("q1");
  });

  it("clears once the human acts", () => {
    expect(buildPending([
      ev("human_needed", 1, { kind: "gate" }),
      ev("human", 2, { action: "accept" }),
    ])).toBeNull();
  });

  it("clears when the run ends", () => {
    expect(buildPending([
      ev("human_needed", 1, { kind: "gate" }),
      ev("run_end", 2, { verdict: "PASSED" }),
    ])).toBeNull();
  });

  // Resume appends a checkpoint, not a human event — a lifted-and-resumed
  // budget pause went on saying "waiting for you" over a run that was
  // already working again.
  it("clears when the run resumes", () => {
    expect(buildPending([
      ev("human_needed", 1, { kind: "budget", detail: "token budget exceeded" }),
      ev("checkpoint", 2, { reason: "resume", status: "running" }),
    ])).toBeNull();
  });
});

describe("diff parsing", () => {
  const diff = [
    "--- a/add.go", "+++ b/add.go", "@@ -1,3 +1,3 @@", "-return a - b", "+return a + b",
    "--- a/add_test.go", "+++ b/add_test.go", "@@ -5,3 +5,3 @@", "-want 5", "+want 6",
  ].join("\n");

  it("splits a unified diff into files with hunks", () => {
    const files = parseDiff(diff);
    expect(files.map((f) => f.path)).toEqual(["add.go", "add_test.go"]);
    expect(files[0]!.hunks[0]).toContain("+return a + b");
  });

  // 05 §5.3: a change to the thing that decides pass/fail must be the first
  // thing seen, never buried halfway down.
  it("flags and pins test files to the top", () => {
    const files = orderDiffFiles(parseDiff(diff));
    expect(files[0]!.path).toBe("add_test.go");
    expect(files[0]!.isTest).toBe(true);
    expect(touchesTests(files)).toBe(true);
  });

  it("recognises test paths across languages", () => {
    expect(isTestPath("add_test.go")).toBe(true);
    expect(isTestPath("test_auth.py")).toBe(true);
    expect(isTestPath("src/app.test.tsx")).toBe(true);
    expect(isTestPath("tests/e2e.js")).toBe(true);
    expect(isTestPath("src/add.go")).toBe(false);
    expect(isTestPath("src/latest.go")).toBe(false);
  });

  it("handles an empty diff without throwing", () => {
    expect(parseDiff("")).toEqual([]);
    expect(touchesTests([])).toBe(false);
  });
});

// turn_start and turn_end bracketed a turn whose content was never rendered:
// nothing filled `text`, so every lane showed a participant header above an
// empty bubble even once the engine started recording what was said.
describe("buildTurns and what the model said", () => {
  it("puts a message's content in its turn", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" } },
      { seq: 2, type: "message", run_id: "r", data: { round: 1, turn: 0, role: "implementer", content: "I fixed add.go." } },
      { seq: 3, type: "tool_call", run_id: "r", data: { tool: "fs_patch", ok: true } },
      { seq: 4, type: "turn_end", run_id: "r", data: { round: 1, turn: 0 } },
    ] as never);

    expect(turns).toHaveLength(1);
    expect(turns[0]!.text).toBe("I fixed add.go.");
    expect(turns[0]!.toolCalls.map((c) => c.tool)).toEqual(["fs_patch"]);
    expect(turns[0]!.done).toBe(true);
  });

  it("keeps a message that arrives outside a turn instead of dropping it", () => {
    const turns = buildTurns([
      { seq: 1, type: "message", run_id: "r", data: { round: 1, turn: 2, role: "reviewer", duckling: "pato-dos", content: "Looks right." } },
    ] as never);
    expect(turns).toHaveLength(1);
    expect(turns[0]!.text).toBe("Looks right.");
    expect(turns[0]!.role).toBe("reviewer");
  });
});

// A tournament runs its contestants in parallel, so their events interleave.
// buildTurns advanced a single `current` pointer, so the second turn_start
// replaced the first: everything after it landed in the wrong lane, and the
// first contestant was left thinking forever above an orphaned block holding
// its own words. Jose's screenshot showed three lanes for two contestants.
describe("buildTurns with turns that overlap", () => {
  const interleaved = [
    { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" } },
    { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "implementer", duckling: "pato-dos" } },
    { seq: 3, type: "tool_call", run_id: "r", data: { round: 1, turn: 1, tool: "fs_read", ok: true } },
    { seq: 4, type: "tool_call", run_id: "r", data: { round: 1, turn: 0, tool: "fs_patch", ok: true } },
    { seq: 5, type: "message", run_id: "r", data: { round: 1, turn: 1, content: "B did it" } },
    { seq: 6, type: "turn_end", run_id: "r", data: { round: 1, turn: 1 } },
    { seq: 7, type: "message", run_id: "r", data: { round: 1, turn: 0, content: "A did it" } },
    { seq: 8, type: "turn_end", run_id: "r", data: { round: 1, turn: 0 } },
  ] as never;

  it("makes one lane per turn, not one per interleaving", () => {
    const turns = buildTurns(interleaved);
    expect(turns).toHaveLength(2);
  });

  it("gives each turn its own words and its own tools", () => {
    const turns = buildTurns(interleaved);
    const a = turns.find((t) => t.turn === 0)!;
    const b = turns.find((t) => t.turn === 1)!;
    expect(a.text).toBe("A did it");
    expect(b.text).toBe("B did it");
    expect(a.toolCalls.map((c) => c.tool)).toEqual(["fs_patch"]);
    expect(b.toolCalls.map((c) => c.tool)).toEqual(["fs_read"]);
  });

  it("ends both turns, so neither is left thinking", () => {
    const turns = buildTurns(interleaved);
    expect(turns.every((t) => t.done)).toBe(true);
  });

  // Older events carry no round or turn. A sequential run must still work.
  it("falls back to the open turn for events that do not say which they are", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer" } },
      { seq: 2, type: "tool_call", run_id: "r", data: { tool: "fs_read", ok: true } },
      { seq: 3, type: "turn_end", run_id: "r", data: {} },
    ] as never);
    expect(turns).toHaveLength(1);
    expect(turns[0]!.toolCalls).toHaveLength(1);
    expect(turns[0]!.done).toBe(true);
  });
});

// A split runs its subtasks concurrently on the same duckling, so its lanes
// read "pato-atom implementer" twice with nothing to tell them apart — which
// is the question the screen should answer without anyone opening the run log.
describe("what a turn was working on", () => {
  it("carries a split's subtask into the lane", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-atom", subtask: "Add mathutil.go" } },
      { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "implementer", duckling: "pato-atom", subtask: "Add strutil.go" } },
    ] as never);
    expect(turns.map((t) => t.subject)).toEqual(["Add mathutil.go", "Add strutil.go"]);
  });

  it("names a tournament's contestant slot", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer", contestant: 0 } },
      { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "implementer", contestant: 1 } },
    ] as never);
    expect(turns.map((t) => t.subject)).toEqual(["candidate 1", "candidate 2"]);
  });

  it("leaves an ordinary turn with no subject rather than inventing one", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "reviewer" } },
    ] as never);
    expect(turns[0]!.subject).toBeUndefined();
  });
});

// Lanes are stacked, so concurrency reads as sequence: a reviewer of a split
// cannot tell whether two pieces ran together or one after the other, and
// those are different claims about what the models were given.
describe("turns that overlapped", () => {
  it("marks both sides of an overlap", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer" } },
      { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "implementer" } },
      { seq: 3, type: "turn_end", run_id: "r", data: { round: 1, turn: 1 } },
      { seq: 4, type: "turn_end", run_id: "r", data: { round: 1, turn: 0 } },
    ] as never);
    expect(turns.map((t) => t.concurrent)).toEqual([true, true]);
  });

  it("leaves turns that took their turns alone", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer" } },
      { seq: 2, type: "turn_end", run_id: "r", data: { round: 1, turn: 0 } },
      { seq: 3, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "reviewer" } },
      { seq: 4, type: "turn_end", run_id: "r", data: { round: 1, turn: 1 } },
    ] as never);
    expect(turns.some((t) => t.concurrent)).toBe(false);
  });

  // A turn still running when another starts is an overlap, even though
  // neither has ended.
  it("marks an overlap that has not finished yet", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer" } },
      { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "implementer" } },
    ] as never);
    expect(turns.every((t) => t.concurrent)).toBe(true);
  });
});

// A proposal's diff has no file headers.
//
// artifact.LineDiff compares two documents, not two trees, so it emits @@ and
// +/- lines and nothing else. parseDiff required a `+++ ` line before it would
// create a file, so every proposal parsed to zero files and the Cycle view
// said "No changes yet." over a 78-line draft.
describe("a diff with no file header", () => {
  it("still parses", () => {
    const files = parseDiff("@@ -1,1 +1,3 @@\n-\n+## REQ-001 — A thing\n+\n+**Priority:** must\n");
    expect(files).toHaveLength(1);
    expect(files[0]!.hunks.join("\n")).toContain("REQ-001");
  });

  it("does not disturb a diff that has headers", () => {
    const files = parseDiff(
      "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n" +
        "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n",
    );
    expect(files.map((f) => f.path)).toEqual(["a.go", "b.go"]);
  });

  it("is empty for an empty diff", () => {
    expect(parseDiff("")).toHaveLength(0);
  });
});

// "fs_read fs_read fs_read" says a model is busy without saying with what.
// The args were on every event the whole time; the lane just never looked.
describe("toolTarget", () => {
  it("names the path, with the window when one was read", () => {
    expect(toolTarget({ args: '{"path":"profile_api.py","start":760,"end":1200}' }))
      .toBe("profile_api.py:760–1200");
    expect(toolTarget({ args: '{"path":"app.py"}' })).toBe("app.py");
  });

  it("falls back to the pattern, command, or id", () => {
    expect(toolTarget({ args: '{"pattern":"CREATE TABLE challenges"}' })).toBe("CREATE TABLE challenges");
    expect(toolTarget({ args: '{"cmd":"pytest -q"}' })).toBe("pytest -q");
    expect(toolTarget({ args: '{"id":"T-036","kind":"plan"}' })).toBe("T-036");
  });

  it("keeps a long path's tail and a long command's head", () => {
    const path = "a/".repeat(50) + "file.py";
    expect(toolTarget({ args: JSON.stringify({ path }) })!.startsWith("…")).toBe(true);
    expect(toolTarget({ args: JSON.stringify({ path }) })!.endsWith("file.py")).toBe(true);
    const cmd = "x".repeat(100);
    expect(toolTarget({ args: JSON.stringify({ cmd }) })!.endsWith("…")).toBe(true);
  });

  it("answers nothing for unparseable or empty args", () => {
    expect(toolTarget({ args: "not json" })).toBeUndefined();
    expect(toolTarget({})).toBeUndefined();
  });
});

// A failed call's result says WHY — recorded all along, shown never: the ✕
// expanded to nothing.
describe("a failed tool call's reason", () => {
  it("lands in the turn block's detail", () => {
    const turns = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "luna" }),
      ev("tool_call", 2, {
        round: 1, turn: 0, tool: "fs_patch", ok: false,
        args: '{"path":"app.py"}', result: "search text not found in app.py",
      }),
    ]);
    const call = turns[0]!.toolCalls[0]!;
    expect(call.target).toBe("app.py");
    expect(call.detail).toBe("search text not found in app.py");
  });
});

// A pair can end green with the reviewer still requesting changes — rounds
// exhausted, gate the only voter (I2). Legitimate, and it must not be silent:
// T-028 said "tests passed" over three straight request-changes verdicts,
// discovered only by reading the whole transcript.
describe("reviewerDissent", () => {
  const turn = (n: number, verdict?: string, findings: unknown[] = []) => [
    ev("turn_start", n, { round: n, turn: 1, role: "reviewer", duckling: "luna" }),
    ev("message", n + 1, { round: n, turn: 1, content: "…", verdict, findings }),
  ];

  it("surfaces a final request-changes with its findings, ready to ride a note", () => {
    const turns = buildTurns([...turn(1, "request-changes", [{ issue: "old" }]), ...turn(3, "request-changes", [
      { severity: "major", file: "app.py", line: 12, issue: "wrong week boundary", fix: "use ISO weeks" },
      { issue: "missing null check" },
    ])]);
    const d = reviewerDissent(turns)!;
    expect(d.verdict).toBe("request-changes");
    expect(d.findings).toBe(2);
    expect(d.notes[0]).toBe("wrong week boundary (app.py:12) — fix: use ISO weeks");
    expect(d.notes[1]).toBe("missing null check");
  });

  it("stays silent when the last word was approval", () => {
    const turns = buildTurns([...turn(1, "request-changes", [{}]), ...turn(3, "approve")]);
    expect(reviewerDissent(turns)).toBeNull();
  });

  it("stays silent when nothing carried a verdict", () => {
    const turns = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "d" }),
      ev("message", 2, { round: 1, turn: 0, content: "done" }),
    ]);
    expect(reviewerDissent(turns)).toBeNull();
  });
});

// "waiting for you — error" without the error: the reason was on the
// human_needed event the whole time, and the person went to the record to
// learn what the banner already knew.
describe("a pause's reason travels with it", () => {
  it("carries the detail of an error pause", () => {
    const p = buildPending([
      ev("human_needed", 1, { kind: "error", detail: "provider chat: 404: model does not exist" }),
    ])!;
    expect(p.kind).toBe("error");
    expect(p.detail).toContain("404");
  });
});

// The record, not the mount, remembers a filing: a filed run re-visited
// offered to file again, saved only by the engine's refusal.
describe("findingsFiled", () => {
  it("reads the filed bug ids from the record", () => {
    expect(findingsFiled([
      ev("findings_filed", 9, { bugs: ["B-003"], count: 1 }),
    ])).toEqual(["B-003"]);
  });
  it("answers null when nothing was filed", () => {
    expect(findingsFiled([ev("run_end", 1, {})])).toBeNull();
  });
});

// The advisor's recommendation rides the pending question — matched by id,
// cleared with the wait, so a stale draft never dresses a new question.
describe("advice on a pending question", () => {
  it("attaches the advisor's draft to its question", () => {
    const p = buildPending([
      ev("human_needed", 1, { kind: "question", question: "Which contract?", question_id: "q1" }),
      ev("advice", 2, { question_id: "q1", advisor: "pato-sonnet", answer: "python app.py, poll /health" }),
    ])!;
    expect(p.advice).toContain("app.py");
    expect(p.advisor).toBe("pato-sonnet");
  });

  it("drops advice that belongs to an earlier question", () => {
    const p = buildPending([
      ev("human_needed", 1, { kind: "question", question: "old?", question_id: "q1" }),
      ev("advice", 2, { question_id: "q1", advisor: "a", answer: "old advice" }),
      ev("human", 3, { action: "answer" }),
      ev("human_needed", 4, { kind: "question", question: "new?", question_id: "q2" }),
    ])!;
    expect(p.questionId).toBe("q2");
    expect(p.advice).toBeUndefined();
  });
});

// The chain's hand-off is on the record: the test run's log names the build
// that took over, and the view follows it.
describe("chainedBuildId", () => {
  it("finds the build the chain started", () => {
    expect(chainedBuildId([
      ev("human", 1, { action: "accept" }),
      ev("tdd_build_started", 2, { run: "r-build-9" }),
    ])).toBe("r-build-9");
  });
  it("answers nothing when no chain fired", () => {
    expect(chainedBuildId([ev("run_end", 1, {})])).toBe("");
  });
});
