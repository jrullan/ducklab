import { useEffect, useState } from "react";
import type { Duckling, EngineClient } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { StatusChip } from "../components/StatusChip";

type Entry = { role: string; duckling?: string; ducklings?: string[]; source?: string; default?: string; global_ducklings?: string[] };
const MODES = ["council", "solo", "pair", "split", "tournament", "common"];

function names(entry: Entry) { return entry.ducklings ?? (entry.duckling ? [entry.duckling] : []); }

export function Roster({ client, projectId, projectName }: { client: EngineClient; projectId: string; projectName?: string }) {
  const [scope, setScope] = useState<"global" | "project">("global");
  const [ducks, setDucks] = useState<Duckling[]>([]);
  const [boards, setBoards] = useState<Record<string, Entry[]>>({});
  useEffect(() => { client.ducklings().then(setDucks).catch(() => {}); }, [client]);
  useEffect(() => {
    Promise.all(MODES.map(async (mode) => {
      const result = scope === "global" ? await client.globalRosterGet(mode) : await client.rosterGet(projectId, mode);
      return [mode, (result.entries ?? []) as Entry[]] as const;
    })).then((items) => setBoards(Object.fromEntries(items))).catch(() => {});
  }, [client, projectId, scope]);
  const roster = ducks.map((d) => d.id);
  return <div className="space-y-6" data-testid="roster-view">
    <div className="flex items-center gap-2" data-testid="roster-scope">
      <button type="button" className={scope === "global" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("global")}>Global</button>
      <span>|</span>
      <button type="button" className={scope === "project" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("project")}>Project · {projectName ?? projectId}</button>
    </div>
    <section data-testid="roster-flock"><h2 className="text-lg font-semibold">Flock</h2><div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
      {ducks.map((d) => <div key={d.id} className="rounded border border-hairline bg-surface p-2"><DuckAvatar id={d.id} roster={roster} /><span className="ml-2">{d.id} · duckling</span><div className="text-sm text-ink-muted"><StatusChip role="muted" label={d.provider === "local" ? "local" : "remote"} /> <span>{d.cost ? `${d.cost.input_per_mtok}/${d.cost.output_per_mtok}` : "cost unknown"}</span></div></div>)}
    </div></section>
    <div className="space-y-5">{MODES.map((mode) => <section key={mode} data-testid={`roster-board-${mode}`}><h2 className="text-lg font-semibold capitalize">{mode}</h2>{scope === "project" && !(boards[mode] ?? []).some((e) => e.source === "project pin" || e.source === "project") && <p className="text-ink-muted">no pins</p>}<div className="flex gap-3 overflow-x-auto">{(boards[mode] ?? []).map((entry) => { const ids = names(entry); return <div key={entry.role} className="min-w-40" data-testid={`roster-column-${mode}-${entry.role}`}><h3 className="mb-2">{({ architect: "council", implementer: "solo", advisor: "solo", reviewer: "pair", judge: "tournament", triager: "common", scribe: "common" } as Record<string, string>)[entry.role] === mode ? entry.role : `${entry.role} · ${mode}`}</h3>{ids.map((id) => { const pinned = entry.source === "project pin" || entry.source === "project"; const ghost = scope === "project" && !pinned; return <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={pinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}><DuckAvatar id={id} roster={roster} /> <span className="ml-1">{id} · seat</span> <small className="ml-1">{ghost ? "global" : pinned ? "pinned" : ""}</small></div>; })}</div>})}</div></section>)}</div>
  </div>;
}

