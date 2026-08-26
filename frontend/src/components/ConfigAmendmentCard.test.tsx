import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigAmendmentCard, describeConfigValue, shouldShowConfigAmendment } from "./ConfigAmendmentCard";

const finding = { key: "verify.link_deps", proposed: "frontend/node_modules", reason: "acceptance needs dependencies" };

describe("ConfigAmendmentCard", () => {
  it("applies only on a person click through the attributed config API", async () => {
    const projectUpdate = vi.fn().mockResolvedValue({});
    render(<ConfigAmendmentCard client={{ projectUpdate } as never} projectId="p-1" finding={finding} old="" why="Scribe: keep verification reproducible." />);
    expect(projectUpdate).not.toHaveBeenCalled();
    expect(screen.getByTestId("config-amendment-card")).toHaveTextContent("Scribe: keep verification reproducible.");
    fireEvent.click(screen.getByTestId("config-amendment-apply"));
    await waitFor(() => expect(projectUpdate).toHaveBeenCalledWith("p-1", { "verify.link_deps": "frontend/node_modules" }, "consultant_proposal"));
  });

  it("renders configuration values as words and keeps raw values behind disclosure", () => {
    expect(describeConfigValue("github.config", "false", "old")).toBe("github integration is configured but nothing uses it");
    expect(describeConfigValue("github.config", "false", "new")).toBe("github integration is turned off");
    render(<ConfigAmendmentCard client={{ projectUpdate: vi.fn() } as never} projectId="p-1" finding={{ key: "github.config", proposed: "false", reason: "unused" }} old="false" />);
    expect(screen.getByTestId("config-amendment-card")).toHaveTextContent("github integration is configured but nothing uses it");
    expect(screen.getByTestId("config-amendment-raw")).toBeInTheDocument();
  });

  it("does not show a standing finding in an unrelated chat", () => {
    expect(shouldShowConfigAmendment({ touchesConfiguration: false, isNew: false, dismissed: false })).toBe(false);
  });
});
