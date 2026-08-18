import { useEffect, useRef, useState } from "react";
import type { Duckling, EngineClient } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { StatusChip } from "../components/StatusChip";
import { rolesForMode } from "../lib/seats";
import { seriesVar } from "../lib/colors";
import type { ProviderView } from "../api/client";

type Entry = { role: string; duckling?: string; ducklings?: string[]; source?: string; default?: string; global_ducklings?: string[] };
const MODES = ["council", "solo", "pair", "split", "tournament", "common"];
const names = (entry: Entry) => entry.ducklings ?? (entry.duckling ? [entry.duckling] : []);
const pinned = (entry: Entry) => entry.source === "project pin" || entry.source === "project";
// The columns a board shows: only the roles the mode seats (rolesForMode is
// the same table the run view uses), Common = the mode-independent roles.
// Every board used to paint all seven roles and read as "my whole team".
const columnsFor = (mode: string): string[] | null =>
  mode === "common" ? ["triager", "scribe"] : rolesForMode(mode);
// A duckling is local when its provider answers on this machine or the LAN;
// the provider id says nothing about that ("beelink" is local, "openrouter"
// is not), so the endpoint decides. A provider literally named "local" counts.
const isLocal = (d: Duckling, providers: ProviderView[]): boolean => {
  if (d.provider === "local") return true;
  const p = providers.find((x) => x.id === d.provider);
  if (!p) return false;
  return /^(https?:\/\/)?(localhost|127\.|0\.0\.0\.0|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|\[::1\]|[^/]*\.local\b)/i.test(p.base_url ?? "");
};

export function Roster({ client, projectId, projectName }: { client: EngineClient; projectId: string; projectName?: string }) {
  const [scope, setScope] = useState<"global" | "project">("global");
  const [ducks, setDucks] = useState<Duckling[]>([]);
  const [providers, setProviders] = useState<ProviderView[]>([]);
  const [boards, setBoards] = useState<Record<string, Entry[]>>({});
  const [warnings, setWarnings] = useState<Record<string, string | undefined>>({});
  const [error, setError] = useState("");
  const [chosenSeat, setChosenSeat] = useState<{ mode: string; role: string } | null>(null);
  const [unpins, setUnpins] = useState<Record<string, boolean>>({});
  const dragging = useRef(false);
  const editable = typeof client.RosterSetManyMode === "function" && typeof client.GlobalRosterSet === "function";

  const reload = () => Promise.all(MODES.map(async (mode) => {
    const result = scope === "global" ? await client.globalRosterGet(mode) : await client.rosterGet(projectId, mode);
    return [mode, (result.entries ?? []) as Entry[], result.warning] as const;
  })).then((items) => {
    setBoards(Object.fromEntries(items.map(([mode, entries]) => [mode, entries])));
    setWarnings(Object.fromEntries(items.map(([mode, , warning]) => [mode, warning])));
  }).catch(() => {});

  useEffect(() => { client.ducklings().then(setDucks).catch(() => {}); }, [client]);
  useEffect(() => {
    if (typeof client.providers !== "function") return;
    client.providers().then(setProviders).catch(() => {});
  }, [client]);
  useEffect(() => { reload(); }, [client, projectId, scope]); // eslint-disable-line react-hooks/exhaustive-deps

  const write = async (mode: string, role: string, ids: string[]) => {
    setError("");
    try {
      if (scope === "global") await client.GlobalRosterSet(mode, role, ids);
      else await client.RosterSetManyMode(projectId, mode, role, ids);
      await reload();
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  };
  const assign = (mode: string, role: string, id: string, append = false) => {
    const entry = (boards[mode] ?? []).find((candidate) => candidate.role === role);
    const ids = entry ? names(entry) : [];
    if (!ids.includes(id)) {
      // A real drag onto an inherited project seat replaces that seat. The
      // ordinary click flow (and drops without a drag source) appends, which
      // preserves ordered multi-slot seats.
      const replaceInherited = scope === "project" && entry != null && !pinned(entry) && !append;
      void write(mode, role, replaceInherited ? [id] : [...ids, id]);
    }
    setChosenSeat(null);
  };
  const drop = (mode: string, role: string, event: React.DragEvent) => {
    event.preventDefault();
    const id = event.dataTransfer.getData("text/plain") || event.dataTransfer.getData("text");
    if (id) assign(mode, role, id, !dragging.current);
  };

  const roster = ducks.map((d) => d.id);
  const pair = (boards.pair ?? []);
  const implementers = new Set((pair.find((e) => e.role === "implementer") ? names(pair.find((e) => e.role === "implementer")!) : []));
  const reviewers = new Set((pair.find((e) => e.role === "reviewer") ? names(pair.find((e) => e.role === "reviewer")!) : []));
  const overlap = [...implementers].some((id) => reviewers.has(id));
  return <div className="space-y-6" data-testid="roster-view">
    <div className="flex items-center gap-2" data-testid="roster-scope">
      <button type="button" className={scope === "global" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("global")}>Global</button>
      <span>|</span>
      <button type="button" className={scope === "project" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("project")}>Project · {projectName ?? projectId}</button>
    </div>
    <div className="flex gap-6 items-start">
    <aside className="w-64 shrink-0" data-testid="roster-flock"><h2 className="text-lg font-semibold">Flock</h2><div className="mt-2 space-y-2">
      {ducks.map((d) => { const local = isLocal(d, providers); return <div key={d.id} draggable data-testid={`roster-flock-card-${d.id}`} onDragStart={(event) => { dragging.current = true; event.dataTransfer.setData("text/plain", d.id); }} onDragEnd={() => { dragging.current = false; }} className="rounded border border-hairline bg-surface p-2"><DuckAvatar id={d.id} roster={roster} /><span className="ml-2 font-medium">{`${d.id}${d.model ? ` · ${d.model}` : ""}`}</span><div className="text-sm text-ink-muted"><StatusChip role="muted" label={local ? "local" : "remote"} /> <span title="cost per million tokens, in / out">{d.cost ? `$${d.cost.input_per_mtok} / $${d.cost.output_per_mtok} per Mtok` : "cost unknown"}</span></div></div>; })}
    </div></aside>
    <div className="flex-1 min-w-0">
    {error && <p role="alert">{error}</p>}
    {overlap && <p role="alert">implementer and reviewer are the same duckling</p>}
    <div className="space-y-5">{MODES.map((mode) => <section key={mode} data-testid={`roster-board-${mode}`} className="border-l-4 pl-3" style={{ borderLeftColor: seriesVar(MODES.indexOf(mode)) }}><h2 className="text-lg font-semibold capitalize">{mode}</h2>{warnings[mode] && <p>{warnings[mode]}</p>}{mode === "common" && scope === "project" && !(boards[mode] ?? []).some((entry) => pinned(entry)) && <p>no pins</p>}<div className="flex gap-3 overflow-x-auto">{(boards[mode] ?? []).filter((entry) => { const cols = columnsFor(mode); return !cols || cols.includes(entry.role); }).sort((a, b) => { const cols = columnsFor(mode) ?? []; return cols.indexOf(a.role) - cols.indexOf(b.role); }).map((entry) => {
      const ids = names(entry); const isPinned = pinned(entry) && !unpins[`${mode}/${entry.role}`]; const ghost = scope === "project" && !isPinned;
      const key = `${mode}/${entry.role}`;
      return <div key={entry.role} className="min-w-40" tabIndex={0} data-testid={`roster-column-${mode}-${entry.role}`} onDragOver={(event) => event.preventDefault()} onDrop={(event) => drop(mode, entry.role, event)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setChosenSeat({ mode, role: entry.role }); } }}><h3 className="mb-2">{entry.role}</h3>{editable && !(chosenSeat?.mode === mode && chosenSeat.role === entry.role) && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className={`mb-2 w-full rounded border border-dashed border-hairline px-2 py-2 text-xs text-ink-muted ${ids.length === 0 ? "min-h-12" : ""}`}>{ids.length === 0 ? "drop here · assign" : "+ assign"}</button>}
        {ids.map((id) => <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={isPinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`flex items-center gap-2 rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}><DuckAvatar id={id} roster={roster} /><span className="font-medium">{id}</span>{(ghost || isPinned) && <small className="rounded border border-hairline px-1 text-ink-muted">{ghost ? "global" : "pinned"}</small>}<span className="ml-auto flex gap-2 text-xs">{editable && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`remove ${id} from ${entry.role}`} onClick={() => void write(mode, entry.role, ids.filter((candidate) => candidate !== id))}>remove</button>}{editable && scope === "project" && isPinned && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`unpin ${id} from ${entry.role}`} onClick={() => { setUnpins((value) => ({ ...value, [key]: true })); void client.RosterUnpin(projectId, mode, entry.role).then(reload).catch((e) => setError(e.message)); }}>unpin</button>}</span></div>)}
        {editable && chosenSeat?.mode === mode && chosenSeat.role === entry.role && <div>{ducks.map((duck) => <button key={duck.id} type="button" aria-label={`assign ${duck.id} to ${entry.role}`} onClick={() => assign(mode, entry.role, duck.id)}>assign {duck.id}</button>)}</div>}
      </div>;
    })}</div></section>)}</div>
    </div>
    </div>
  </div>;
}
