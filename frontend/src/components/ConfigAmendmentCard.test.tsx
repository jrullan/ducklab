import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigAmendmentCard } from "./ConfigAmendmentCard";

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
});
