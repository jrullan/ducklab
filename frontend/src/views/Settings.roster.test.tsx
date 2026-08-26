import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { Settings } from "./Settings";

describe("framed roster room", () => {
  it("keeps seated cards visible inside the Settings frame", async () => {
    const client = {
      ducklings: vi.fn().mockResolvedValue([
        { id: "implementer", provider: "local", model: "qwen" },
        { id: "duckling", provider: "local", model: "qwen" },
      ]),
      globalRosterGet: vi.fn((mode: string) => Promise.resolve({
        entries: mode === "solo" ? [
          { role: "implementer", ducklings: ["implementer"], source: "global mode seat" },
          { role: "advisor", ducklings: ["duckling"], source: "global mode seat" },
        ] : [],
      })),
      rosterGet: vi.fn().mockResolvedValue({ entries: [] }),
    };

    render(
      <Settings
        theme="light"
        onTheme={() => {}}
        engineVersion=""
        connection="open"
        client={client as never}
        projectId="project-1"
        room="roster"
      />,
    );

    const content = screen.getByTestId("settings-content");
    expect(content.className).not.toContain("max-w-3xl");
    expect(await within(content).findByTestId("roster-card-solo-implementer-implementer")).toBeVisible();
  });
});
