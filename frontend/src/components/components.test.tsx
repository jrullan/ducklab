import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StatusChip } from "./StatusChip";
import { BudgetMeter } from "./BudgetMeter";
import { DuckAvatar } from "./DuckAvatar";
import { EmptyState } from "./EmptyState";
import { money } from "../lib/format";

describe("StatusChip", () => {
  // AC-36: status is never colour alone. Two of the four steps are below 3:1
  // on the light surface by design, so the icon and label must always render.
  it("renders icon, label and colour together", () => {
    render(<StatusChip role="warning" label="unverified" />);
    const chip = screen.getByTestId("status-chip");
    expect(chip.getAttribute("data-role")).toBe("warning");
    expect(chip.textContent).toContain("unverified");
    expect(chip.textContent!.replace("unverified", "").trim()).not.toBe("");
    expect(chip.getAttribute("style")).toContain("var(--status-warning)");
  });

  it("never renders an empty label", () => {
    render(<StatusChip role="good" label="passed" />);
    expect(screen.getByTestId("status-chip").textContent).toContain("passed");
  });
});

describe("BudgetMeter", () => {
  it("marks itself warning at 80% and critical at 100%", () => {
    const { rerender } = render(
      <BudgetMeter label="cost" used={0.5} limit={2} format={money} />,
    );
    expect(screen.getByTestId("budget-meter").getAttribute("data-role")).toBe("good");

    rerender(<BudgetMeter label="cost" used={1.7} limit={2} format={money} />);
    expect(screen.getByTestId("budget-meter").getAttribute("data-role")).toBe("warning");

    rerender(<BudgetMeter label="cost" used={2} limit={2} format={money} />);
    expect(screen.getByTestId("budget-meter").getAttribute("data-role")).toBe("critical");
  });

  it("does not overflow its track past the limit", () => {
    render(<BudgetMeter label="cost" used={99} limit={2} format={money} />);
    const track = screen.getByTestId("budget-meter").querySelectorAll("div");
    const fill = track[track.length - 1] as HTMLElement;
    expect(fill.style.width).toBe("100%");
  });
});

describe("DuckAvatar", () => {
  it("tints by the duckling's stable slot, not its position in the view", () => {
    const roster = ["pato-uno", "pato-dos"];
    render(<DuckAvatar id="pato-dos" roster={roster} />);
    expect(screen.getByTestId("duck-avatar").getAttribute("style")).toContain("var(--series-2)");
  });
});

describe("EmptyState", () => {
  it("says what would fill the view", () => {
    render(<EmptyState message="No runs yet. Pick a task and press Run." />);
    expect(screen.getByTestId("empty-state").textContent).toContain("No runs yet");
  });
});

describe("BudgetMeter lift", () => {
  // A run close to a ceiling gets headroom IN PLACE: the checkbox removes
  // that one cap — one-way, recorded by the engine — instead of the run dying
  // at the limit and its work being rolled back.
  it("offers lifting a live cap", () => {
    const onLift = vi.fn();
    render(<BudgetMeter label="tokens" used={90} limit={100} format={String} lift={{ onLift }} />);
    fireEvent.click(screen.getByTestId("lift-tokens"));
    expect(onLift).toHaveBeenCalledOnce();
  });

  // There is no un-lifting: a lifted cap renders checked and frozen, and the
  // numbers say "no cap" rather than a zero that reads as an empty budget.
  it("freezes a lifted cap and says no cap", () => {
    const onLift = vi.fn();
    render(<BudgetMeter label="cost" used={3} limit={0} format={money} lift={{ onLift }} />);
    const box = screen.getByTestId("lift-cost") as HTMLInputElement;
    expect(box.checked).toBe(true);
    expect(box.disabled).toBe(true);
    expect(screen.getAllByTestId("budget-meter")[0]!.textContent).toContain("no cap");
  });

  // Without the prop — a finished run's record — the meter stays read-only.
  it("offers nothing on a finished run", () => {
    render(<BudgetMeter label="turns" used={3} limit={24} format={String} />);
    expect(screen.queryByTestId("lift-turns")).toBeNull();
  });
});
