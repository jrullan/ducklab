import { describe, it, expect } from "vitest";
import { buildGate, buildTurns, readGateRecord } from "./runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

// B-510: the final gate keeps the command's real exit code in exit_code and
// the gate's outcome in effective_exit_code, naming in red_by each check that
// made it red. r-20261005-110414-sumi: npm test passed 174/174, the visual
// check failed, and the desktop showed a failing command.
const sumi = {
  gate: "red", command_gate: "tests", command: "npm test --silent", exit_code: 0, effective_exit_code: 1,
  red_by: [{ check: "visual", summary: "visual check failed: calculator.png differs from REF-IMG-6c63e390 in 31.8% of pixels (allowed 20.0%)" }],
};

const finalGate = (data: Record<string, unknown>) => [ev("gate_started", 1, { phase: "final" }), ev("gate", 2, data)];

describe("B-510 final gate record", () => {
  it("says tests passed and the visual check failed, and stays red", () => {
    const gate = buildGate(finalGate(sumi))!;
    expect(gate.role).toBe("critical");
    expect(gate.exitCode).toBe(0);
    expect(gate.label).toBe("tests passed; visual check failed: calculator.png differs from REF-IMG-6c63e390 in 31.8% of pixels (allowed 20.0%)");
    const lane = buildTurns(finalGate(sumi))[0]!;
    expect(lane).toMatchObject({ gate: "red", gateExitCode: 0, gateSummary: gate.label });
  });

  // The matrix: each red source alone over a passing command reads red on
  // both surfaces, with its own words; none takes exit_code 0 for a pass.
  for (const check of ["task_verification", "acceptance_probes", "app_smoke", "visual", "capability_coverage"]) {
    it(`keeps a ${check} red gate red`, () => {
      const data = { ...sumi, red_by: [{ check, summary: `${check} failed: why` }] };
      expect(readGateRecord(data).green).toBe(false);
      const gate = buildGate(finalGate(data))!;
      expect(gate.role).toBe("critical");
      expect(gate.label).toBe(`tests passed; ${check} failed: why`);
      expect(buildTurns(finalGate(data))[0]!.gate).toBe("red");
    });
  }

  it("keeps the command's own failure worded as before", () => {
    const data = { gate: "red", command_gate: "tests", exit_code: 1, effective_exit_code: 1, red_by: [{ check: "command", summary: "npm test exited 1" }] };
    const gate = buildGate(finalGate(data))!;
    expect(gate.role).toBe("critical");
    expect(gate.label).toBe("tests failed");
    expect(buildTurns(finalGate(data))[0]).toMatchObject({ gate: "red", gateExitCode: 1, gateSummary: undefined });
  });

  it("names a failing command beside another red check", () => {
    const data = { ...sumi, exit_code: 2, red_by: [{ check: "command", summary: "npm test exited 2" }, sumi.red_by[0]] };
    expect(buildGate(finalGate(data))!.label).toBe(`tests failed; ${sumi.red_by[0]!.summary}`);
  });

  it("reads records written before B-510 as it always did", () => {
    // exit_code was the effective code; gate "red" is the overridden word.
    expect(buildGate(finalGate({ gate: "red", exit_code: 1 }))!.role).toBe("critical");
    expect(buildGate(finalGate({ gate: "tests", exit_code: 0 }))!.role).toBe("good");
    expect(buildTurns(finalGate({ gate: "red", exit_code: 1 }))[0]!.gate).toBe("red");
    // test-first's baseline writes exit.
    expect(buildGate([ev("gate", 1, { gate: "tests", exit: 1, phase: "before" })])!.label).toBe("baseline tests failed");
  });

  it("never reads a red word or a cause as green, whatever the codes say", () => {
    expect(readGateRecord({ gate: "red", exit_code: 0 }).green).toBe(false);
    expect(readGateRecord({ gate: "tests", exit_code: 0, red_by: [{ check: "visual", summary: "x" }] }).green).toBe(false);
    expect(readGateRecord({ gate: "tests", exit_code: 0, effective_exit_code: 1 }).green).toBe(false);
    expect(readGateRecord({ gate: "tests", exit_code: 0, effective_exit_code: 0 }).green).toBe(true);
  });

  it("does not invent a passing command when there was none", () => {
    const data = { gate: "red", command_gate: "none", exit_code: 0, effective_exit_code: 1, red_by: [{ check: "task_verification", summary: "task verification: x exited 1" }] };
    expect(buildGate(finalGate(data))!.label).toBe("task verification: x exited 1");
  });
});
