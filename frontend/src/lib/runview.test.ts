import { describe, it, expect } from "vitest";
import {
  buildTurns, anonymiseTurns, buildTimeline, toolFamily,
  buildGate, buildPending, parseDiff, orderDiffFiles, touchesTests, isTestPath,
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
