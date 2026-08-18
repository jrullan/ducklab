import { useEffect, useRef, useState } from "react";
import type { Duckling, EngineClient, Scorecard } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { StatusChip } from "../components/StatusChip";
import { rolesForMode } from "../lib/seats";
import { seriesVar } from "../lib/colors";
import type { ProviderView } from "../api/client";

type Entry = { role: string; duckling?: string; ducklings?: string[]; source?: string; default?: string; global_ducklings?: string[] };
const MODES = ["council", "solo", "pair", "split", "tournament", "common"];
const names = (entry: Entry) => entry.ducklings ?? (entry.duckling ? [entry.duckling] : []);
const pinned = (entry: Entry) => entry.source === "project pin" || entry.source === "project mode seat" || entry.source === "project";
// The columns a board shows: only the roles the mode seats (rolesForMode is
// the same table the run view uses), Common = the mode-independent roles.
// Every board used to paint all seven roles and read as "my whole team".
// Ordered multi-slot seats: everything else holds one duckling.
const multiSlot = (mode: string, role: string): boolean =>
  (mode === "council" && role === "reviewer") || ((mode === "split" || mode === "tournament") && role === "implementer");
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
  const [scorecards, setScorecards] = useState<Scorecard[]>([]);
  const [flockText, setFlockText] = useState("");
  const [flockProvider, setFlockProvider] = useState("");
  const [flockLocality, setFlockLocality] = useState("");
  const [flockVision, setFlockVision] = useState(false);
  const [flockTools, setFlockTools] = useState(false);
  const [flockContext, setFlockContext] = useState("");
  const [flockSort, setFlockSort] = useState("input-cost");
  const [flockDir, setFlockDir] = useState<"asc" | "desc">("asc");
  const [providers, setProviders] = useState<ProviderView[]>([]);
  const [boards, setBoards] = useState<Record<string, Entry[]>>({});
  const [warnings, setWarnings] = useState<Record<string, string | undefined>>({});
  // Errors live with the board that produced them: an error rendered at
  // the top of the view was invisible from Tournament and Common, and two
  // real bugs hid behind that. Key "" is the view itself (a failed re-read).
  const [errors, setErrors] = useState<Record<string, string>>({});
  const setError = (mode: string, message: string) => setErrors((cur) => {
    const next = { ...cur };
    if (message) next[mode] = message; else delete next[mode];
    return next;
  });
  const [chosenSeat, setChosenSeat] = useState<{ mode: string; role: string } | null>(null);
  const dragging = useRef(false);
  const editable = typeof client.RosterSetManyMode === "function" && typeof client.GlobalRosterSet === "function";

  const reload = () => Promise.all(MODES.map(async (mode) => {
    const result = scope === "global" ? await client.globalRosterGet(mode) : await client.rosterGet(projectId, mode);
    return [mode, (result.entries ?? []) as Entry[], result.warning] as const;
  })).then((items) => {
    setBoards(Object.fromEntries(items.map(([mode, entries]) => [mode, entries])));
    setWarnings(Object.fromEntries(items.map(([mode, , warning]) => [mode, warning])));
  }).catch((e: unknown) => {
    // A failed re-read must not leave stale seats standing as if current.
    setError("", `could not re-read the roster: ${e instanceof Error ? e.message : String(e)}`);
  });

  useEffect(() => { client.ducklings().then(setDucks).catch(() => {}); }, [client]);
  useEffect(() => { if (typeof client.Scorecards === "function") client.Scorecards().then(setScorecards).catch(() => {}); }, [client]);
  useEffect(() => {
    if (typeof client.providers !== "function") return;
    client.providers().then(setProviders).catch(() => {});
  }, [client]);
  useEffect(() => { reload(); }, [client, projectId, scope]); // eslint-disable-line react-hooks/exhaustive-deps

  const write = async (mode: string, role: string, ids: string[]) => {
    setError(mode, "");
    try {
      if (scope === "global") await client.GlobalRosterSet(mode, role, ids);
      else await client.RosterSetManyMode(projectId, mode, role, ids);
      await reload();
    } catch (e) { setError(mode, e instanceof Error ? e.message : String(e)); }
  };
  const assign = (mode: string, role: string, id: string) => {
    const entry = (boards[mode] ?? []).find((candidate) => candidate.role === role);
    const ids = entry ? names(entry) : [];
    if (!ids.includes(id)) {
      // The SEAT decides: a single-slot seat (an implementer in solo or
      // pair, an advisor, a judge, an architect) is replaced; a multi-slot
      // seat (council critics, split workers, tournament contestants)
      // appends in displayed order — unless the seat is still inherited on
      // the Project board, where the first assignment starts the pin fresh
      // rather than copying the global list under it. Assigning atom-local
      // to solo's implementer once APPENDED it beside luna: solo then had a
      // two-seat list it cannot use.
      const appendable = multiSlot(mode, role) && (scope === "global" || (entry != null && pinned(entry)));
      void write(mode, role, appendable ? [...ids, id] : [id]);
    }
    setChosenSeat(null);
  };
  const drop = (mode: string, role: string, event: React.DragEvent) => {
    event.preventDefault();
    const id = event.dataTransfer.getData("text/plain") || event.dataTransfer.getData("text");
    if (id) assign(mode, role, id);
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
    <aside className="w-64 shrink-0" data-testid="roster-flock"><h2 className="text-lg font-semibold">Flock</h2>
      <div className="mt-2 space-y-1 text-sm">
        <input data-testid="roster-flock-filter-text" value={flockText} onChange={(e) => setFlockText(e.target.value)} placeholder="id or model" />
        <select data-testid="roster-flock-filter-provider" value={flockProvider} onChange={(e) => setFlockProvider(e.target.value)}><option value="">all providers</option>{[...new Set(scorecards.map(s => s.provider))].map(p => <option key={p}>{p}</option>)}</select>
        <select data-testid="roster-flock-filter-locality" value={flockLocality} onChange={(e) => setFlockLocality(e.target.value)}><option value="">all localities</option>{[...new Set(scorecards.map(s => s.locality).filter(Boolean))].map(p => <option key={p}>{p}</option>)}</select>
        <label><input data-testid="roster-flock-filter-vision" type="checkbox" checked={flockVision} onChange={(e) => setFlockVision(e.target.checked)} /> vision</label>
        <label><input data-testid="roster-flock-filter-native-tools" type="checkbox" checked={flockTools} onChange={(e) => setFlockTools(e.target.checked)} /> native-tools</label>
        <input data-testid="roster-flock-filter-context" type="number" value={flockContext} onChange={(e) => setFlockContext(e.target.value)} placeholder="minimum context" />
        <select data-testid="roster-flock-sort" value={flockSort} onChange={(e) => setFlockSort(e.target.value)}><option value="input-cost">input cost</option><option value="output-cost">output cost</option><option value="pass-rate">pass rate</option><option value="avg-cost">avg cost per run</option><option value="bench:arena">bench score · arena</option><option value="coding-index">coding index</option><option value="context">context</option></select>
        <select data-testid="roster-flock-sort-dir" value={flockDir} onChange={(e) => setFlockDir(e.target.value as "asc" | "desc")}><option value="asc">ascending</option><option value="desc">descending</option></select>
      </div><div className="mt-2 space-y-2">
      {(() => { const cards = ducks.map(d => scorecards.find(s => s.id === d.id) ?? ({ ...d } as Scorecard)); const value = (s: Scorecard): number | undefined => flockSort === "input-cost" ? s.cost?.input_per_mtok : flockSort === "output-cost" ? s.cost?.output_per_mtok : flockSort === "pass-rate" ? (s.measured?.runs ? s.measured.pass_rate : undefined) : flockSort === "avg-cost" ? (s.measured?.runs ? s.measured.avg_cost_usd : undefined) : flockSort === "coding-index" ? s.index?.coding : flockSort === "context" ? s.caps?.context_tokens : s.bench?.[flockSort.slice(6)]?.score; const filtered = cards.filter(s => (!flockText || `${s.id} ${s.model}`.toLowerCase().includes(flockText.toLowerCase())) && (!flockProvider || s.provider === flockProvider) && (!flockLocality || s.locality === flockLocality) && (!flockVision || s.caps?.vision) && (!flockTools || s.caps?.native_tools) && (!flockContext || (s.caps?.context_tokens ?? 0) >= Number(flockContext))).sort((a,b) => { const av=value(a), bv=value(b); if (av === undefined && bv === undefined) return 0; if (av === undefined) return 1; if (bv === undefined) return -1; return (av-bv) * (flockDir === "asc" ? 1 : -1); }); return filtered.map(s => { const d = ducks.find(x => x.id === s.id)!; const local = s.locality ?? ""; const v=value(s); return <div key={s.id} draggable data-testid={`roster-flock-card-${s.id}`} onDragStart={(event) => { dragging.current = true; event.dataTransfer.setData("text/plain", s.id); }} onDragEnd={() => { dragging.current = false; }} className="rounded border border-hairline bg-surface p-2"><DuckAvatar id={s.id} roster={roster} /><span className="ml-2 font-medium">{`${s.id} · ${s.model}`}</span><div className="text-sm text-ink-muted"><StatusChip role="muted" label={local || (isLocal(d, providers) ? "local" : "remote")} /> <span>{s.cost ? `$${s.cost.input_per_mtok} / $${s.cost.output_per_mtok} per Mtok` : "cost unknown"}</span></div><div data-testid={`roster-flock-value-${s.id}`}>{v === undefined ? "—" : v}</div><div className="text-xs">{(!s.measured?.runs) && "no runs yet "}{!s.bench && "no bench "}{!s.index && "no index"}</div></div>; }); })()}
    </div></aside>
    <div className="flex-1 min-w-0">
    {errors[""] && <p role="alert" className="text-sm" style={{ color: "var(--status-critical)" }}>{errors[""]}</p>}
    {overlap && <p role="alert">implementer and reviewer are the same duckling</p>}
    <div className="space-y-5">{MODES.map((mode) => <section key={mode} data-testid={`roster-board-${mode}`} className="border-l-4 pl-3" style={{ borderLeftColor: seriesVar(MODES.indexOf(mode)) }}><h2 className="text-lg font-semibold capitalize">{mode}</h2>{warnings[mode] && <p className="text-sm text-ink-muted" data-testid={`roster-warning-${mode}`}>{warnings[mode]}</p>}{errors[mode] && <p role="alert" className="text-sm" data-testid={`roster-error-${mode}`} style={{ color: "var(--status-critical)" }}>{errors[mode]}</p>}{mode === "common" && scope === "project" && !(boards[mode] ?? []).some((entry) => pinned(entry)) && <p>no pins</p>}<div className="flex gap-3 overflow-x-auto">{(boards[mode] ?? []).filter((entry) => { const cols = columnsFor(mode); return !cols || cols.includes(entry.role); }).sort((a, b) => { const cols = columnsFor(mode) ?? []; return cols.indexOf(a.role) - cols.indexOf(b.role); }).map((entry) => {
      const ids = names(entry); const isPinned = pinned(entry); const ghost = scope === "project" && !isPinned;
      return <div key={entry.role} className="min-w-40" tabIndex={0} data-testid={`roster-column-${mode}-${entry.role}`} onDragOver={(event) => event.preventDefault()} onDrop={(event) => drop(mode, entry.role, event)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setChosenSeat({ mode, role: entry.role }); } }}><h3 className="mb-2">{entry.role}</h3>{editable && !(chosenSeat?.mode === mode && chosenSeat.role === entry.role) && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className={`mb-2 w-full rounded border border-dashed border-hairline px-2 py-2 text-xs text-ink-muted ${ids.length === 0 ? "min-h-12" : ""}`}>{ids.length === 0 ? "drop here · assign" : "+ assign"}</button>}
        {ids.map((id) => <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={isPinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`flex items-center gap-2 rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}><DuckAvatar id={id} roster={roster} /><span className="font-medium">{id}</span>{(ghost || isPinned) && <small className="rounded border border-hairline px-1 text-ink-muted">{ghost ? "global" : "pinned"}</small>}<span className="ml-auto flex gap-2 text-xs">{editable && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`remove ${id} from ${entry.role}`} onClick={() => void write(mode, entry.role, ids.filter((candidate) => candidate !== id))}>remove</button>}{editable && scope === "project" && isPinned && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`unpin ${id} from ${entry.role}`} onClick={() => { setError(mode, ""); void client.RosterUnpin(projectId, mode, entry.role).then(reload).catch((e) => setError(mode, e instanceof Error ? e.message : String(e))); }}>unpin</button>}</span></div>)}
        {editable && chosenSeat?.mode === mode && chosenSeat.role === entry.role && <div className="mb-2 rounded border border-hairline bg-surface2 p-2" role="listbox" aria-label={`choose a duckling for ${entry.role}`}><div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>assign to {entry.role}</span><button type="button" className="underline" onClick={() => setChosenSeat(null)}>cancel</button></div><div className="max-h-56 space-y-1 overflow-y-auto">{ducks.filter((duck) => !ids.includes(duck.id)).map((duck) => <button key={duck.id} type="button" className="block w-full rounded px-2 py-1 text-left text-sm hover:bg-surface" aria-label={`assign ${duck.id} to ${entry.role}`} onClick={() => assign(mode, entry.role, duck.id)}><DuckAvatar id={duck.id} roster={roster} /> <span className="ml-1">{duck.id}</span></button>)}</div></div>}
      </div>;
    })}</div></section>)}</div>
    </div>
    </div>
  </div>;
}
