import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Scorecard } from "../api/client";
import { DucklingPickerDrawer, MAP_METRICS, mapPoints } from "./DucklingPickerDrawer";

const scorecards: Scorecard[] = [
  { id: "capable", provider: "cloud", model: "a", measured: { runs: 100, pass_rate: 90, avg_cost_usd: 0.2 }, index: { coding_score: 90 } },
  { id: "cheap", provider: "cloud", model: "b", measured: { runs: 25, pass_rate: 70, avg_cost_usd: 0.05 }, index: { coding_score: 70 } },
  { id: "dominated", provider: "cloud", model: "c", measured: { runs: 2, pass_rate: 60, avg_cost_usd: 0.3 }, index: { coding_score: 60 } },
];
const ducklings = scorecards.map(({ id, provider, model }) => ({ id, provider, model }));

describe("duckling map", () => {
  it("orients both axes toward desirability and marks the Pareto frontier", () => {
    const cost = MAP_METRICS.find((metric) => metric.key === "cost-per-run")!;
    const coding = MAP_METRICS.find((metric) => metric.key === "coding-index")!;
    const points = mapPoints(scorecards, "implementer", cost, coding);
    expect(points.find((point) => point.scorecard.id === "cheap")!.x).toBeGreaterThan(points.find((point) => point.scorecard.id === "capable")!.x);
    expect(points.find((point) => point.scorecard.id === "capable")!.y).toBeGreaterThan(points.find((point) => point.scorecard.id === "cheap")!.y);
    expect(points.filter((point) => point.pareto).map((point) => point.scorecard.id).sort()).toEqual(["capable", "cheap"]);
  });

  it("uses relative rank spacing by default so an outlier cannot bunch the useful field", () => {
    const cost = MAP_METRICS.find((metric) => metric.key === "cost-per-run")!;
    const coding = MAP_METRICS.find((metric) => metric.key === "coding-index")!;
    const clustered = [1, 2, 3, 100].map((value) => ({ id: `d${value}`, provider: "cloud", model: "m", measured: { runs: 10, avg_cost_usd: value }, index: { coding_score: value } })) as Scorecard[];
    const relative = mapPoints(clustered, "implementer", cost, coding);
    const absolute = mapPoints(clustered, "implementer", cost, coding, "absolute");
    const gap = (points: typeof relative) => Math.abs(points.find((point) => point.scorecard.id === "d2")!.x - points.find((point) => point.scorecard.id === "d3")!.x);
    expect(gap(relative)).toBeGreaterThan(0.2);
    expect(gap(absolute)).toBeLessThan(0.02);
  });

  it("lets the operator switch between relative spacing and absolute values", () => {
    render(<DucklingPickerDrawer mode="solo" role="implementer" ducklings={ducklings} scorecards={scorecards} current={["capable"]} multiple={false} scope="global" onClose={vi.fn()} onApply={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Relative spacing" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(/evenly spaced by rank/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Absolute values" }));
    expect(screen.getByRole("button", { name: "Absolute values" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(/robust 5–95% value scale/i)).toBeInTheDocument();
  });

  it("stages a multi-selection and persists only when Apply is pressed", async () => {
    const apply = vi.fn().mockResolvedValue(undefined);
    render(<DucklingPickerDrawer mode="tournament" role="implementer" ducklings={ducklings} scorecards={scorecards} current={["capable"]} multiple scope="project" onClose={vi.fn()} onApply={apply} />);
    fireEvent.click(screen.getByRole("button", { name: "cheap" }));
    expect(apply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /apply 2 ducklings/i }));
    await waitFor(() => expect(apply).toHaveBeenCalledWith(["capable", "cheap"]));
  });

  it("keeps the drawer open and explains a failed write", async () => {
    render(<DucklingPickerDrawer mode="solo" role="implementer" ducklings={ducklings} scorecards={scorecards} current={["capable"]} multiple={false} scope="global" onClose={vi.fn()} onApply={vi.fn().mockRejectedValue(new Error("cannot save roster"))} />);
    fireEvent.click(screen.getByRole("button", { name: /^apply$/i }));
    expect(await screen.findByRole("alert")).toHaveTextContent("cannot save roster");
    expect(screen.getByTestId("duckling-picker-drawer")).toBeInTheDocument();
  });
});
