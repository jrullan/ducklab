import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { EngineClient } from "../api/client";
import { describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import { routeHref } from "../app/routes";

const zones = [
  { label: "Now", testid: "nav-now", home: { name: "now" as const }, members: ["now" as const] },
  { label: "Work", testid: "nav-work", home: { name: "board" as const }, members: ["board" as const, "cycle" as const] },
  { label: "Records", testid: "nav-records", home: { name: "runs" as const }, members: ["runs" as const] },
];
const config = [
  { label: "Settings", route: { name: "settings" as const } },
  { label: "Ducklings", route: { name: "ducklings" as const } },
  { label: "Roster", route: { name: "roster" as const } },
  { label: "Skills", route: { name: "skills" as const } },
  { label: "Projects", route: { name: "projects" as const } },
];

describe("desktop sidebar rail", () => {
  it("keeps primary navigation, settings rooms, and footer status reachable", () => {
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={["settings", "roster", "skills", "projects", "ducklings"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={2} connection="open" />);

    expect(screen.getByTestId("sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("nav-now")).toHaveTextContent("Now");
    expect(screen.getByTestId("nav-work")).toHaveTextContent("Work");
    expect(screen.getByTestId("nav-records")).toHaveTextContent("Records");
    expect(screen.getByTestId("nav-badge")).toHaveTextContent("2");
    expect(screen.getByTestId("nav-settings")).toHaveAttribute("href", routeHref({ name: "settings" }));
    expect(screen.queryByTestId("subnav-settings")).not.toBeInTheDocument();
    expect(screen.queryByTestId("subnav-ducklings")).not.toBeInTheDocument();
    expect(screen.queryByTestId("subnav-roster")).not.toBeInTheDocument();
    expect(screen.queryByTestId("subnav-skills")).not.toBeInTheDocument();
    expect(screen.queryByTestId("subnav-projects")).not.toBeInTheDocument();
    expect(screen.getByTestId("sidebar-footer")).toHaveTextContent("engine");
    expect(screen.queryByTestId("utility-drawer")).not.toBeInTheDocument();
    expect(screen.queryByTestId("utilities-drawer")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /hide utility drawer/i })).not.toBeInTheDocument();
  });

  it("renders the project chat door and opens the chat", async () => {
    const client = new EngineClient({
      baseUrl: "http://engine",
      token: "t",
      fetchFn: (async () => new Response('[{"id":"luna","provider":"test","model":"test"}]', { status: 200, headers: { "Content-Type": "application/json" } })) as never,
    });
    vi.spyOn(client, "roster").mockResolvedValue({ entries: [] });
    vi.spyOn(client, "ducklings").mockResolvedValue([{ id: "luna", provider: "test", model: "test" }]);
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={[]} subnav={{}} projects={[{ id: "p", name: "project", path: "." }]} projectId="p" onProject={() => {}} client={client} waitingCount={0} connection="open" />);

    await waitFor(() => expect(within(screen.getByTestId("sidebar-footer")).getByTestId("chat-about")).toBeInTheDocument());
    fireEvent.click(screen.getByTestId("chat-about"));
    expect(screen.getByTestId("chat-about-form")).toBeInTheDocument();
  });

  it("removes app identity and keeps project selection as the only project control", () => {
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={[]} subnav={{}} project={{ id: "p", name: "ducklab", path: ".", branch: "main" }} projects={[{ id: "p", name: "ducklab", path: ".", branch: "main" }]} projectId="p" onProject={() => {}} client={null} waitingCount={0} connection="open" />);
    expect(screen.queryByText("ducklab", { selector: ".text-md" })).not.toBeInTheDocument();
    expect(screen.getByTestId("project-select")).toBeInTheDocument();
    expect(screen.queryByText("on branch")).not.toBeInTheDocument();
  });

  it("words only a non-base branch", () => {
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={[]} subnav={{}} project={{ id: "p", name: "ducklab", path: ".", branch: "chore/release-0.3.48", base_branch: "main" }} projects={[{ id: "p", name: "ducklab", path: ".", branch: "chore/release-0.3.48", base_branch: "main" }]} projectId="p" onProject={() => {}} client={null} waitingCount={0} connection="open" />);
    expect(screen.getByText("on branch chore/release-0.3.48 — not the base")).toBeInTheDocument();
  });

  it("attaches each subnav to its own parent", () => {
    render(<Sidebar route={{ name: "cycle" }} zones={zones} configMembers={[]} subnav={{ Work: [{ label: "Documents", route: { name: "cycle" } }] }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={0} connection="open" />);
    const work = screen.getByTestId("nav-work");
    expect(within(work.parentElement as HTMLElement).getByTestId("subnav-documents")).toBeInTheDocument();
  });

  it("keeps every routed view reachable, with settings rooms behind its internal menu", () => {
    const routedViews = ["now", "board", "cycle", "runs", "reports", "review", "release", "bench", "settings", "ducklings", "roster", "skills", "projects"];
    const sidebarTree = [...zones.flatMap((zone) => [zone.home.name, ...zone.members]), "reports", "review", "release", "bench", "settings"];
    const settingsMenu = ["ducklings", "roster", "skills", "projects"];
    const tree = [...sidebarTree, ...settingsMenu];
    expect(routedViews.every((name) => tree.includes(name as (typeof tree)[number]))).toBe(true);
  });

  it("attaches every configured route to a sidebar door", () => {
    render(<Sidebar route={{ name: "settings" }} zones={zones} configMembers={["settings", "roster", "skills", "projects", "ducklings"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={0} connection="open" />);

    expect(screen.getByTestId("nav-settings")).toHaveAttribute("href", routeHref({ name: "settings" }));
    expect(screen.queryByTestId("subnav-settings")).not.toBeInTheDocument();
    for (const item of config.slice(1)) {
      expect(screen.queryByText(item.label)).not.toBeInTheDocument();
    }
  });
});
