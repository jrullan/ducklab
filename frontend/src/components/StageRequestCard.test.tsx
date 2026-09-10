import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StageRequestCard } from "./StageRequestCard";

describe("StageRequestCard", () => {
  it("shows the exact persisted amendment request", () => {
    render(<StageRequestCard request={{ stage: "plan", extend: "add provider isolation", rounds: 2 }} />);
    expect(screen.getByTestId("stage-request-card").textContent).toContain("add provider isolation");
    expect(screen.getByText("rounds").nextSibling?.textContent).toBe("2");
  });

  it("stays absent for ordinary runs", () => {
    const { container } = render(<StageRequestCard />);
    expect(container).toBeEmptyDOMElement();
  });
});
