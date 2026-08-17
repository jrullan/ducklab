import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { buildDeliverables } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

const report = (seq: number, extra: Record<string, unknown> = {}) =>
  ev("deliverables_report", seq, {
    round: 1,
    total: 4,
    deliverables: ["Add columns", "Keep rows valid", "Update seed data", "Serialize image_url"],
    items: [
      { id: 1, status: "done" },
      { id: 2, status: "done" },
      { id: 4, status: "blocked", note: "generated serializer, template not found" },
    ],
    undelivered: [4],
    unreported: false,
    ...extra,
  });

describe("buildDeliverables", () => {
  it("returns null with no report", () => {
    expect(buildDeliverables([ev("turn_start", 1)])).toBeNull();
  });

  it("renders every deliverable, unreported ones included, and counts done", () => {
    const d = buildDeliverables([report(5)])!;
    expect(d.total).toBe(4);
    expect(d.done).toBe(2);
    expect(d.lines.map((l) => l.status)).toEqual(["done", "done", "unreported", "blocked"]);
    expect(d.lines[3]!.note).toContain("template not found");
    expect(d.gap).toBe(false);
  });

  it("the latest report wins and a later gap flags it", () => {
    const d = buildDeliverables([
      report(5),
      ev("advisor_retry", 6, { retry: 1 }),
      report(9, { retry: 1, items: [{ id: 1, status: "done" }, { id: 2, status: "done" }, { id: 3, status: "done" }, { id: 4, status: "done" }], undelivered: [] }),
      ev("deliverables_gap", 12, { undelivered: [4] }),
    ])!;
    expect(d.retry).toBe(1);
    expect(d.done).toBe(4);
    expect(d.gap).toBe(true);
  });

  it("a report filed before a retry is superseded and its gap forgotten", () => {
    const d = buildDeliverables([report(5), ev("deliverables_gap", 6, {}), report(9, { retry: 1 })])!;
    expect(d.gap).toBe(false);
  });
});

import { splitDeliverablesReport } from "../lib/runview";
import { ConversationTurn } from "./ConversationLane";
import { buildTurns } from "../lib/runview";

describe("the implementer's closing report in the lane", () => {
  const reply =
    "Implemented favorites and CRUD forms. Ran the gate.\n\n" +
    '{"deliverables":[{"id":1,"status":"done"},{"id":2,"status":"blocked","note":"no fixture"}]}';

  it("splits prose from the report", () => {
    const s = splitDeliverablesReport(reply)!;
    expect(s.prose).toBe("Implemented favorites and CRUD forms. Ran the gate.");
    expect(s.items.map((i) => i.status)).toEqual(["done", "blocked"]);
    expect(splitDeliverablesReport("just prose")).toBeNull();
  });

  it("renders the report as a checklist instead of a JSON blob", () => {
    const events: DucklabEvent[] = [
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "luna" }),
      ev("message", 2, { round: 1, turn: 0, role: "implementer", duckling: "luna", content: reply }),
      ev("turn_end", 3, { round: 1, turn: 0, role: "implementer" }),
    ];
    const block = buildTurns(events)[0]!;
    render(<ConversationTurn block={block} roster={["luna"]} deliverableTexts={["Star toggle", "Add form"]} />);
    const rows = screen.getAllByTestId("deliverable-inline");
    expect(rows).toHaveLength(2);
    expect(rows[1]!.getAttribute("data-status")).toBe("blocked");
    expect(screen.getByText("Add form")).toBeTruthy();
    expect(screen.getByText(/no fixture/)).toBeTruthy();
    expect(screen.getByTestId("turn-text").textContent).not.toContain('"deliverables"');
  });
});

describe("an approve over undelivered items", () => {
  it("is flagged on the reviewer's verdict, not in a rail", () => {
    const events: DucklabEvent[] = [
      ev("turn_start", 1, { round: 1, turn: 1, role: "reviewer", duckling: "glm52" }),
      ev("message", 2, { round: 1, turn: 1, role: "reviewer", duckling: "glm52", content: "{}", verdict: "approve", findings: [] }),
      ev("turn_end", 3, { round: 1, turn: 1, role: "reviewer" }),
      ev("deliverables_gap", 4, { round: 1, undelivered: [4] }),
    ];
    const block = buildTurns(events)[0]!;
    expect(block.deliverablesGap).toEqual([4]);
    render(<ConversationTurn block={block} roster={["glm52"]} />);
    expect(screen.getByTestId("deliverables-gap").textContent).toMatch(/undelivered: 4/);
  });
});
