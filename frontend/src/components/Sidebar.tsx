import { useState } from "react";
import type { EngineClient, Project } from "../api/client";
import { StatusChip } from "./StatusChip";
import { AppControl } from "./AppControl";
import { routeHref, type Route } from "../app/routes";
import { AutopilotControl } from "./GuidePanel";

export type SidebarZone = {
  label: string;
  testid: string;
  home: Route;
  members: Route["name"][];
};

type SubnavItem = { label: string; route: Route };

export function Sidebar({
  route,
  zones,
  configMembers,
  subnav,
  project,
  projects,
  projectId,
  onProject,
  client,
  waitingCount,
  connection,
}: {
  route: Route;
  zones: SidebarZone[];
  configMembers: Route["name"][];
  subnav: Record<string, SubnavItem[]>;
  project?: Project;
  projects: Project[];
  projectId: string;
  onProject: (id: string) => void;
  client: EngineClient | null;
  waitingCount: number;
  connection: "open" | "connecting" | "reconnecting" | "closed";
}) {
  const [utilityOpen, setUtilityOpen] = useState(() => localStorage.getItem("ducklab.utility-drawer") !== "off");
  const zone = zones.find((z) => z.members.includes(route.name));
  const config = configMembers.includes(route.name);
  const zoneName = zone?.members.length && zone.members.length > 1 ? zone.label : config ? "Config" : "";
  // Configuration links are the settings area below; repeating them as room
  // navigation would show every settings destination twice.
  const rooms = zoneName === "Config" ? undefined : subnav[zoneName];
  const activeRoom = (r: Route) =>
    r.name === route.name &&
    (r.name !== "board" || ("tab" in r ? r.tab : undefined) === ("tab" in route ? route.tab : undefined));
  return (
    <aside className="relative flex h-full w-64 shrink-0 flex-col border-r border-hairline bg-page px-4 py-5" data-testid="sidebar">
      <div className="text-md">🦆 ducklab</div>
      <div className="mt-5 border-b border-hairline pb-4" data-testid="active-project">
        <div className="text-xs text-ink-muted">project</div>
        <div className="truncate text-sm text-ink">{project?.name || "No project selected"}</div>
        <div className="truncate text-xs text-ink-muted">{project?.branch || "branch unavailable"}</div>
        {projects.length > 0 && (
          <select data-testid="project-select" className="mt-2 w-full rounded border border-hairline bg-page px-2 py-1 text-sm text-ink" value={projectId} onChange={(e) => onProject(e.target.value)}>
            {projects.map((p) => <option key={p.id} value={p.id}>{(p.name || p.id) + (p.missing ? " (missing)" : "")}</option>)}
          </select>
        )}
      </div>
      <nav className="mt-4 flex flex-col gap-1" aria-label="Primary navigation">
        {zones.map((z) => (
          <a key={z.label} href={routeHref(z.home)} data-testid={z.testid} className={`rounded px-2 py-1.5 ${z.members.includes(route.name) ? "bg-surface2 text-ink" : "text-ink-muted"}`}>
            {z.label}{z.label === "Now" && waitingCount > 0 && <span className="ml-1 text-serious" data-testid="nav-badge">● {waitingCount}</span>}
          </a>
        ))}
      </nav>
      {rooms && <nav className="mt-1 flex flex-col gap-1 border-l border-hairline pl-3 text-sm" data-testid="subnav">
        {rooms.map((r) => <a key={r.label} href={routeHref(r.route)} data-testid={`subnav-${r.label.toLowerCase()}`} className={activeRoom(r.route) ? "text-ink" : "text-ink-muted"}>{r.label}</a>)}
      </nav>}
      <nav className="mt-auto flex flex-col gap-1 border-t border-hairline pt-4" aria-label="Settings">
        {(subnav.Config ?? []).map((r) => <a key={r.label} href={routeHref(r.route)} data-testid={`nav-${r.label.toLowerCase()}`} className={`rounded px-2 py-1.5 ${configMembers.includes(route.name) && route.name === r.route.name ? "bg-surface2 text-ink" : "text-ink-muted"}`}>{r.label}</a>)}
      </nav>
      {client && projectId && <>
        <div className="mt-3"><AppControl client={client} projectId={projectId} /></div>
        <div className="mt-3"><AutopilotControl client={client} projectId={projectId} /></div>
        <button
          type="button"
          data-testid="utility-drawer-toggle"
          onClick={() => setUtilityOpen((open) => {
            const next = !open;
            localStorage.setItem("ducklab.utility-drawer", next ? "on" : "off");
            return next;
          })}
          className="mt-3 self-start text-xs text-ink-muted underline"
          aria-expanded={utilityOpen}
        >
          {utilityOpen ? "hide utility drawer" : "show utility drawer"}
        </button>
        {utilityOpen && (
          <div data-testid="utility-drawer" className="absolute left-full top-0 z-20 w-60 border border-hairline bg-page p-3 shadow">
            <p className="text-xs text-ink-muted">Utilities</p>
            <a href={routeHref({ name: "now" })} className="mt-2 block text-sm text-ink underline">next steps on Now</a>
            <a href={routeHref({ name: "runs" })} className="mt-1 block text-sm text-ink underline">recent runs in Records</a>
            <p className="mt-3 border-t border-hairline pt-2 text-xs text-ink-muted">Ask how & why from the command palette.</p>
          </div>
        )}
      </>}
      <footer className="mt-4 flex flex-col gap-1 border-t border-hairline pt-3 text-sm" data-testid="sidebar-footer">
        <StatusChip role={connection === "open" ? "good" : connection === "closed" ? "critical" : "warning"} label={connection === "open" ? "engine" : `engine · stream ${connection}`} />
        {waitingCount > 0 && <StatusChip role="serious" label={`${waitingCount} waiting for you`} />}
      </footer>

    </aside>
  );
}

