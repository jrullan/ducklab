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
  { label: "Roster", route: { name: "roster" as const } },
  { label: "Skills", route: { name: "skills" as const } },
  { label: "Projects", route: { name: "projects" as const } },
];

describe("desktop sidebar rail", () => {
  it("keeps primary navigation, one settings entry, and footer status reachable", () => {
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={["settings", "roster", "skills", "projects", "ducklings"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={2} connection="open" />);

    expect(screen.getByTestId("sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("nav-now")).toHaveTextContent("Now");
    expect(screen.getByTestId("nav-work")).toHaveTextContent("Work");
    expect(screen.getByTestId("nav-records")).toHaveTextContent("Records");
    expect(screen.getByTestId("nav-badge")).toHaveTextContent("2");
    expect(screen.getByTestId("nav-settings")).toHaveAttribute("href", routeHref({ name: "settings" }));
    expect(screen.queryByTestId("nav-ducklings")).not.toBeInTheDocument();
    expect(screen.queryByTestId("nav-roster")).not.toBeInTheDocument();
    expect(screen.queryByTestId("nav-skills")).not.toBeInTheDocument();
    expect(screen.queryByTestId("nav-projects")).not.toBeInTheDocument();
    expect(screen.getByTestId("sidebar-footer")).toHaveTextContent("engine");
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

  it("does not duplicate settings as config room navigation", () => {
    render(<Sidebar route={{ name: "settings" }} zones={zones} configMembers={["settings", "roster", "skills", "projects", "ducklings"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={0} connection="open" />);
    expect(screen.queryByTestId("subnav")).not.toBeInTheDocument();
    expect(screen.getAllByTestId("nav-settings")).toHaveLength(1);
  });
});
