import { useEffect, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { parseRoute, routeHref, type Route } from "../app/routes";
import { Settings } from "./Settings";

const client = {
  ducklings: vi.fn().mockResolvedValue([]),
  providers: vi.fn().mockResolvedValue([]),
  budgetDefaults: vi.fn().mockResolvedValue({ max_usd: 1, max_tokens: 1, max_turns: 1, max_wallclock_s: 1 }),
  modeDefaults: vi.fn().mockResolvedValue({ rounds: {}, script_rounds: {}, role_turns: {}, script_role_turns: {}, agent_max_turns: 1 }),
  engineDefaults: vi.fn().mockResolvedValue({ max_concurrent_runs: 1, cpu_ceiling: 1 }),
  autopilotDefaults: vi.fn().mockResolvedValue({ max_tasks: 1, max_fails: 1, autonomy: "" }),
  runs: vi.fn().mockResolvedValue([]),
  projectGate: vi.fn().mockResolvedValue({}),
  projectAutonomy: vi.fn().mockResolvedValue({ autonomy: "" }),
  globalRosterGet: vi.fn().mockResolvedValue({ entries: [] }),
  rosterGet: vi.fn().mockResolvedValue({ entries: [] }),
  scorecards: vi.fn().mockResolvedValue([]),
  skills: vi.fn().mockResolvedValue({ items: [] }),
  projects: vi.fn().mockResolvedValue([]),
  configDoctor: vi.fn().mockResolvedValue([]),
} as never;

function HashSettings() {
  const [route, setRoute] = useState<Route>(() => parseRoute(location.hash));
  useEffect(() => {
    const changed = () => setRoute(parseRoute(location.hash));
    addEventListener("hashchange", changed);
    return () => removeEventListener("hashchange", changed);
  }, []);
  const room = route.name === "roster" || route.name === "skills" || route.name === "projects" ? route.name : undefined;
  return <Settings theme="light" onTheme={() => {}} engineVersion="" connection="open" client={client} projectId="p" room={room} section={route.name === "settings" ? route.section ?? "ducklings" : undefined} />;
}

const entries: { id: string; route: Route; content: string }[] = [
  { id: "ducklings", route: { name: "settings", section: "ducklings" }, content: "settings-section-ducklings" },
  { id: "fleet", route: { name: "settings", section: "fleet" }, content: "settings-section-fleet" },
  { id: "budgets", route: { name: "settings", section: "budgets" }, content: "settings-section-budgets" },
  { id: "autopilot", route: { name: "settings", section: "autopilot" }, content: "settings-section-autopilot" },
  { id: "remote", route: { name: "settings", section: "remote" }, content: "settings-section-remote" },
  { id: "appearance", route: { name: "settings", section: "appearance" }, content: "settings-section-appearance" },
  { id: "engine", route: { name: "settings", section: "engine" }, content: "settings-section-engine" },
  { id: "roster", route: { name: "roster" }, content: "settings-room-roster" },
  { id: "skills", route: { name: "skills" }, content: "settings-room-skills" },
  { id: "projects", route: { name: "projects" }, content: "settings-room-projects" },
];

describe("Settings hash navigation", () => {
  it("navigates from a room to providers and renders its section", async () => {
    location.hash = "#/roster";
    render(<HashSettings />);
    await screen.findByTestId("settings-room-roster");

    fireEvent.click(screen.getByTestId("settings-nav-fleet"));
    await waitFor(() => expect(location.hash).toBe("#/settings/fleet"));
    expect(screen.getByTestId("settings-section-fleet")).toBeInTheDocument();
    expect(screen.getByTestId("ducklings-view")).toHaveTextContent("Providers");
  });

  it("random-walks every category-menu entry from every starting entry", async () => {
    location.hash = routeHref(entries[0]!.route);
    render(<HashSettings />);
    for (const start of entries) {
      location.hash = routeHref(start.route);
      dispatchEvent(new HashChangeEvent("hashchange"));
      await screen.findByTestId(start.content);
      for (const destination of entries) {
        fireEvent.click(screen.getByTestId(`settings-nav-${destination.id}`));
        await waitFor(() => expect(location.hash).toBe(routeHref(destination.route)));
        expect(screen.getByTestId(destination.content)).toBeInTheDocument();
      }
    }
  });
});
