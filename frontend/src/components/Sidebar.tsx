import { useEffect, useState } from "react";
import type { Duckling, EngineClient, Project } from "../api/client";
import { StatusChip } from "./StatusChip";
import { AppControl } from "./AppControl";
import { routeHref, type Route } from "../app/routes";
import { AutopilotControl } from "./GuidePanel";
import { ChatAbout } from "./ChatAbout";

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
  const baseBranch = project?.base_branch ?? (typeof project?.config?.base_branch === "string" ? project.config.base_branch : "main");
  const [ducklings, setDucklings] = useState<Duckling[]>([]);
  useEffect(() => {
    if (!client || !projectId) return;
    client.ducklings().then(setDucklings).catch(() => setDucklings([]));
  }, [client, projectId]);
  // Settings is one door; its rooms are deliberately owned by the category
  // menu inside the Settings view, not by a sidebar subnav.
  const hasSettings = configMembers.includes("settings");
  const activeRoom = (r: Route) =>
    r.name === route.name &&
    (r.name !== "board" || ("tab" in r ? r.tab : undefined) === ("tab" in route ? route.tab : undefined));
  return (
    <aside className="relative flex h-full w-64 shrink-0 flex-col border-r border-hairline bg-page px-4 py-5" data-testid="sidebar">
      <div className="mt-1 border-b border-hairline pb-4" data-testid="active-project">
        {projects.length > 0 && (
          <select aria-label="Project" data-testid="project-select" className="w-full rounded border border-hairline bg-page px-2 py-1 text-sm text-ink" value={projectId} onChange={(e) => onProject(e.target.value)}>
            {projects.map((p) => <option key={p.id} value={p.id}>{(p.name || p.id) + (p.missing ? " (missing)" : "")}</option>)}
          </select>
        )}
        {project?.branch && project.branch !== baseBranch && (
          <div className="mt-2 truncate text-xs text-ink-muted">on branch {project.branch} — not the base</div>
        )}
      </div>
      <nav className="mt-4 flex flex-col gap-1" aria-label="Primary navigation">
        {zones.map((z) => {
          const rooms = z.label === "Config" ? undefined : subnav[z.label];
          return <div key={z.label}>
            <a href={routeHref(z.home)} data-testid={z.testid} className={`block rounded px-2 py-1.5 ${z.members.includes(route.name) ? "bg-surface2 text-ink" : "text-ink-muted"}`}>
              {z.label}{z.label === "Now" && waitingCount > 0 && <span className="ml-1 text-serious" data-testid="nav-badge">● {waitingCount}</span>}
            </a>
            {rooms && <nav className="mt-1 flex flex-col gap-1 border-l border-hairline pl-3 text-sm" data-testid={`subnav-${z.label.toLowerCase()}`}>
              {rooms.map((r) => <a key={r.label} href={routeHref(r.route)} data-testid={`subnav-${r.label.toLowerCase()}`} className={activeRoom(r.route) ? "text-ink" : "text-ink-muted"}>{r.label}</a>)}
            </nav>}
          </div>;
        })}
      </nav>
      <nav className="mt-auto flex flex-col gap-1 border-t border-hairline pt-4" aria-label="Settings">
        {hasSettings && (
          <a href={routeHref({ name: "settings" })} data-testid="nav-settings" className={`rounded px-2 py-1.5 ${route.name === "settings" ? "bg-surface2 text-ink" : "text-ink-muted"}`}>Settings</a>
        )}
      </nav>
      {client && projectId && <>
        <div className="mt-3"><AppControl client={client} projectId={projectId} /></div>
        <div className="mt-3"><AutopilotControl client={client} projectId={projectId} /></div>
      </>}
      <footer className="mt-4 flex flex-col gap-1 border-t border-hairline pt-3 text-sm" data-testid="sidebar-footer">
        {client && projectId && ducklings.length > 0 && (
          <ChatAbout
            client={client}
            projectId={projectId}
            aboutKind="ducklab"
            aboutId={projectId}
            ducklings={ducklings}
            label="Ask how & why — chat about the project"
          />
        )}
        <StatusChip role={connection === "open" ? "good" : connection === "closed" ? "critical" : "warning"} label={connection === "open" ? "engine" : `engine · stream ${connection}`} />
        {waitingCount > 0 && <StatusChip role="serious" label={`${waitingCount} waiting for you`} />}
      </footer>

    </aside>
  );
}

