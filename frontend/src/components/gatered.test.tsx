import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ConversationTurn } from "./ConversationLane";
import { GateCard } from "./GateCard";
import { buildGate, buildTurns } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

// B-510, r-20261005-110414-sumi: "# pass 174 # fail 0", then a required
// visual check at 31.8% > 20%. The rail and the lane said the command failed.
const events = [
  ev("gate_started", 1, { phase: "final", detail: "running the full gate" }),
  ev("gate", 2, {
    gate: "red", command_gate: "tests", command: "npm test --silent", exit_code: 0, effective_exit_code: 1,
    output: "# pass 174\n# fail 0", duration_s: 3,
    red_by: [{ check: "visual", summary: "visual check failed: calculator.png differs from REF-IMG-6c63e390 in 31.8% of pixels (allowed 20.0%)" }],
  }),
];

describe("B-510 a red gate over passing tests", () => {
  it("the rail says tests passed and the visual check failed", () => {
    render(<GateCard gate={buildGate(events)} />);
    const card = screen.getByTestId("gate-card");
    expect(card.textContent).toContain("tests passed; visual check failed: calculator.png differs from REF-IMG-6c63e390 in 31.8% of pixels (allowed 20.0%)");
    expect(card.textContent).toContain("command exit code 0 · 3s");
    expect(card.textContent).not.toContain("tests failed");
  });

  it("the lane says the same under the red gate", () => {
    const [block] = buildTurns(events);
    render(<ConversationTurn block={block!} roster={[]} />);
    expect(block!.gate).toBe("red");
    expect(screen.getByTestId("gate-red-by").textContent).toBe("tests passed; visual check failed: calculator.png differs from REF-IMG-6c63e390 in 31.8% of pixels (allowed 20.0%)");
    expect(screen.getByTestId("gate-result").textContent).toContain("command exit code 0 · 3s");
  });

  it("a failing command alone keeps its plain exit code", () => {
    const red = [events[0]!, ev("gate", 2, { gate: "red", command_gate: "tests", command: "npm test", exit_code: 1, effective_exit_code: 1, red_by: [{ check: "command", summary: "npm test exited 1" }] })];
    render(<GateCard gate={buildGate(red)} />);
    const card = screen.getByTestId("gate-card");
    expect(card.textContent).toContain("tests failed");
    expect(card.textContent).toContain("exit code 1");
    expect(card.textContent).not.toContain("command exit code");
  });
});
