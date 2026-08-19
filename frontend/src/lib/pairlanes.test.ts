import { describe, it, expect } from "vitest";
import { buildTurns } from "./runview";

/** Copied from a real pair run's events.jsonl, keys and all. The reviewer was
 * reported as never appearing in the transcript, and the engine's log shows it
 * ran three full rounds — so the fault is on this side. */
const PAIR_EVENTS = [
  { seq: 2, type: "turn_start", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-atom" } },
  { seq: 3, type: "message", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-atom", content: "The verification passes." } },
  { seq: 4, type: "tool_call", run_id: "r", data: { round: 1, turn: 0, role: "implementer", duckling: "pato-atom", tool: "fs_read", ok: true } },
  { seq: 15, type: "turn_end", run_id: "r", data: { round: 1, turn: 0, role: "implementer" } },
  { seq: 16, type: "turn_start", run_id: "r", data: { round: 1, turn: 1, role: "reviewer", duckling: "pato-sonnet" } },
  { seq: 17, type: "message", run_id: "r", data: { round: 1, turn: 1, role: "reviewer", duckling: "pato-sonnet", content: '```json\n{"verdict":"request-changes"}\n```' } },
  { seq: 18, type: "tool_call", run_id: "r", data: { round: 1, turn: 1, role: "reviewer", duckling: "pato-sonnet", tool: "git_diff", ok: true } },
  { seq: 26, type: "turn_end", run_id: "r", data: { round: 1, turn: 1, role: "reviewer" } },
  { seq: 28, type: "turn_start", run_id: "r", data: { round: 2, turn: 0, role: "implementer", duckling: "pato-atom" } },
  { seq: 29, type: "message", run_id: "r", data: { round: 2, turn: 0, role: "implementer", duckling: "pato-atom", content: "Still passes." } },
  { seq: 40, type: "turn_end", run_id: "r", data: { round: 2, turn: 0, role: "implementer" } },
] as never;

describe("a pair run's transcript", () => {
  it("shows the reviewer as its own turn", () => {
    const turns = buildTurns(PAIR_EVENTS);
    const roles = turns.map((t) => t.role);
    // The round divider between rounds is the feature, not noise: two
    // adjacent same-actor turns read as a duplicate until the loop is named.
    expect(roles).toEqual(["implementer", "reviewer", "round", "implementer"]);
  });

  it("names the duckling that took each turn", () => {
    const turns = buildTurns(PAIR_EVENTS).filter((t) => t.role !== "round");
    expect(turns.map((t) => t.duckling)).toEqual(["pato-atom", "pato-sonnet", "pato-atom"]);
  });

  // The whole point of pair is that a second model said something. An empty
  // bubble under its name is the same as it not being there.
  it("carries what the reviewer actually said", () => {
    const turns = buildTurns(PAIR_EVENTS);
    const reviewer = turns.find((t) => t.role === "reviewer");
    expect(reviewer?.text).toContain("request-changes");
  });

  it("carries the reviewer's tool calls", () => {
    const turns = buildTurns(PAIR_EVENTS);
    const reviewer = turns.find((t) => t.role === "reviewer");
    expect(reviewer?.toolCalls.map((c) => c.tool)).toEqual(["git_diff"]);
  });
});
