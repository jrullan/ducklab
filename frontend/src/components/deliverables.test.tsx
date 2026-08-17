import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DeliverablesCard } from "./DeliverablesCard";
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

describe("DeliverablesCard", () => {
  it("renders nothing without a report", () => {
    const { container } = render(<DeliverablesCard report={null} />);
    expect(container.firstChild).toBeNull();
  });

  it("shows the checklist with count, marks and the note", () => {
    render(<DeliverablesCard report={buildDeliverables([report(5)])} />);
    expect(screen.getByTestId("deliverables-count").textContent).toContain("2/4");
    const rows = screen.getAllByTestId("deliverable");
    expect(rows).toHaveLength(4);
    expect(rows[3]!.getAttribute("data-status")).toBe("blocked");
    expect(rows[2]!.getAttribute("data-status")).toBe("unreported");
    expect(screen.getByText(/template not found/)).toBeTruthy();
    // Status is never colour alone: each mark carries its title.
    expect(screen.getByTitle("blocked")).toBeTruthy();
    expect(screen.queryByTestId("deliverables-gap")).toBeNull();
  });

  it("flags a reviewer approve over undelivered items", () => {
    render(<DeliverablesCard report={buildDeliverables([report(5), ev("deliverables_gap", 7, {})])} />);
    expect(screen.getByTestId("deliverables-gap").textContent).toMatch(/approved over undelivered/);
  });
});
