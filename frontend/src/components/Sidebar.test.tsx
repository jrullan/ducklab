import { render, screen } from "@testing-library/react";
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
  it("keeps primary navigation, settings, and footer status reachable", () => {
    render(<Sidebar route={{ name: "now" }} zones={zones} configMembers={["settings", "roster", "skills", "projects"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={2} connection="open" />);

    expect(screen.getByTestId("sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("nav-now")).toHaveTextContent("Now");
    expect(screen.getByTestId("nav-work")).toHaveTextContent("Work");
    expect(screen.getByTestId("nav-records")).toHaveTextContent("Records");
    expect(screen.getByTestId("nav-badge")).toHaveTextContent("2");
    for (const item of config) expect(screen.getByTestId(`nav-${item.label.toLowerCase()}`)).toHaveAttribute("href", routeHref(item.route));
    expect(screen.getByTestId("sidebar-footer")).toHaveTextContent("engine");
  });

  it("does not duplicate settings as config room navigation", () => {
    render(<Sidebar route={{ name: "settings" }} zones={zones} configMembers={["settings", "roster", "skills", "projects"]} subnav={{ Config: config }} projects={[]} projectId="" onProject={() => {}} client={null} waitingCount={0} connection="open" />);
    expect(screen.queryByTestId("subnav")).not.toBeInTheDocument();
    expect(screen.getAllByTestId("nav-settings")).toHaveLength(1);
  });
});
