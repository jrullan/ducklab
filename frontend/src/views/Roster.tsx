import { useEffect, useRef, useState } from "react";
import type { Duckling, EngineClient } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { StatusChip } from "../components/StatusChip";

type Entry = { role: string; duckling?: string; ducklings?: string[]; source?: string; default?: string; global_ducklings?: string[] };
const MODES = ["council", "solo", "pair", "split", "tournament", "common"];
const names = (entry: Entry) => entry.ducklings ?? (entry.duckling ? [entry.duckling] : []);
const pinned = (entry: Entry) => entry.source === "project pin" || entry.source === "project";

export function Roster({ client, projectId, projectName }: { client: EngineClient; projectId: string; projectName?: string }) {
  const [scope, setScope] = useState<"global" | "project">("global");
  const [ducks, setDucks] = useState<Duckling[]>([]);
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
    <section data-testid="roster-flock"><h2 className="text-lg font-semibold">Flock</h2><div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
      {ducks.map((d) => <div key={d.id} draggable data-testid={`roster-flock-card-${d.id}`} onDragStart={(event) => { dragging.current = true; event.dataTransfer.setData("text/plain", d.id); }} onDragEnd={() => { dragging.current = false; }} className="rounded border border-hairline bg-surface p-2"><DuckAvatar id={d.id} roster={roster} /><span className="ml-2">{d.id} · duckling</span><div className="text-sm text-ink-muted"><StatusChip role="muted" label={d.provider === "local" ? "local" : "remote"} /> <span>{d.cost ? `${d.cost.input_per_mtok}/${d.cost.output_per_mtok}` : "cost unknown"}</span></div></div>)}
    </div></section>
    {error && <p role="alert">{error}</p>}
    {overlap && <p role="alert">implementer and reviewer are the same duckling</p>}
    <div className="space-y-5">{MODES.map((mode) => <section key={mode} data-testid={`roster-board-${mode}`}><h2 className="text-lg font-semibold capitalize">{mode}</h2>{warnings[mode] && <p>{warnings[mode]}</p>}{mode === "common" && scope === "project" && !(boards[mode] ?? []).some((entry) => pinned(entry)) && <p>no pins</p>}<div className="flex gap-3 overflow-x-auto">{(boards[mode] ?? []).map((entry) => {
      const ids = names(entry); const isPinned = pinned(entry) && !unpins[`${mode}/${entry.role}`]; const ghost = scope === "project" && !isPinned;
      const key = `${mode}/${entry.role}`;
      return <div key={entry.role} className="min-w-40" tabIndex={0} data-testid={`roster-column-${mode}-${entry.role}`} onDragOver={(event) => event.preventDefault()} onDrop={(event) => drop(mode, entry.role, event)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setChosenSeat({ mode, role: entry.role }); } }}><h3 className="mb-2">{entry.role}{((boards[mode] ?? []).length > 0 && MODES.slice(0, MODES.indexOf(mode)).some((prior) => (boards[prior] ?? []).some((candidate) => candidate.role === entry.role))) ? "\u200b" : ""}</h3>
        {ids.map((id) => <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={isPinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}><DuckAvatar id={id} roster={roster} /> <span className="ml-1">{id} · seat</span> <small className="ml-1">{ghost ? "global" : isPinned ? "pinned" : ""}</small><button type="button" aria-label={`remove ${id} from ${entry.role}`} onClick={() => void write(mode, entry.role, ids.filter((candidate) => candidate !== id))}>remove</button>{editable && scope === "project" && isPinned && <button type="button" aria-label={`unpin ${id} from ${entry.role}`} onClick={() => { setUnpins((value) => ({ ...value, [key]: true })); void client.RosterUnpin(projectId, mode, entry.role).then(reload).catch((e) => setError(e.message)); }}>unpin</button>}</div>)}
        {editable && chosenSeat?.mode === mode && chosenSeat.role === entry.role && <div>{ducks.map((duck) => <button key={duck.id} type="button" aria-label={`assign ${duck.id} to ${entry.role}`} onClick={() => assign(mode, entry.role, duck.id)}>assign {duck.id}</button>)}</div>}
      </div>;
    })}</div></section>)}</div>
  </div>;
}
