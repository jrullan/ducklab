import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
    expect(screen.getByTestId("subnav-ducklings")).toHaveAttribute("href", routeHref({ name: "ducklings" }));
    expect(screen.getByTestId("subnav-roster")).toHaveAttribute("href", routeHref({ name: "roster" }));
    expect(screen.getByTestId("subnav-skills")).toHaveAttribute("href", routeHref({ name: "skills" }));
    expect(screen.getByTestId("subnav-projects")).toHaveAttribute("href", routeHref({ name: "projects" }));
    expect(screen.getByTestId("sidebar-footer")).toHaveTextContent("engine");
    expect(screen.queryByTestId("utility-drawer")).not.toBeInTheDocument();
    expect(screen.queryByTestId("utilities-drawer")).not.toBeInTheDocument();
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

  it("keeps every routed view reachable from the sidebar tree", () => {
    const routedViews = ["now", "board", "cycle", "runs", "reports", "review", "release", "bench", "settings", "ducklings", "roster", "skills", "projects"];
    const tree = [...zones.flatMap((zone) => [zone.home.name, ...zone.members]), "reports", "review", "release", "bench", ...config.map((item) => item.route.name)];
    expect(routedViews.every((name) => tree.includes(name as (typeof tree)[number]))).toBe(true);
  });

  it("attaches every configured route to a sidebar door", () => {
    render(<Sidebar route={{ name: "settings" }} zones={zones} configMembers={["settings", "roster", "skills", "projects", "ducklings"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={0} connection="open" />);

    for (const item of config) {
      const testid = item.label === "Settings" ? "nav-settings" : `subnav-${item.label.toLowerCase()}`;
      expect(screen.getByTestId(testid)).toHaveAttribute("href", routeHref(item.route));
    }
  });
});
