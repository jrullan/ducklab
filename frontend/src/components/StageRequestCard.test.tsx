import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StageRequestCard } from "./StageRequestCard";

describe("StageRequestCard", () => {
  it("shows the exact persisted amendment request", () => {
    render(<StageRequestCard request={{ stage: "plan", extend: "add provider isolation", rounds: 2 }} />);
    expect(screen.getByTestId("stage-request-card").textContent).toContain("add provider isolation");
    expect(screen.getByText("rounds").nextSibling?.textContent).toBe("2");
  });

  // B-520: a scope reference is persisted as {path, role}; show the role.
  it("names a reference's scope role", () => {
    render(<StageRequestCard request={{ stage: "intake", refs: ["/m/notes.md", { path: "/m/guide.md", role: "scope" }] }} />);
    expect(screen.getByText("refs").nextSibling?.textContent).toBe("/m/notes.md, /m/guide.md (scope)");
  });

  it("stays absent for ordinary runs", () => {
    const { container } = render(<StageRequestCard />);
    expect(container).toBeEmptyDOMElement();
  });
});
