import { useEffect, useState } from "react";
import type { CandidateCriteriaView, Duckling, EngineClient, Scorecard } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { ErrorCard } from "../components/ErrorCard";
import { rolesForMode } from "../lib/seats";
import { DucklingPickerDrawer } from "../components/DucklingPickerDrawer";

type Entry = { role: string; duckling?: string; ducklings?: string[]; source?: string; default?: string; global_ducklings?: string[]; candidates?: { id: string; why: string }[] };
// Common first: triager and scribe serve every mode, and at the bottom of a
// long page they read as an afterthought — or a sixth mode.
const MODES = ["common", "council", "solo", "pair", "split", "tournament"];
// One line each, in the spec's own words (05 §4): what the mode is FOR, so a
// person seating it knows what they are staffing.
const MODE_BLURB: Record<string, string> = {
  common: "shared by every mode — the triager classifies bug reports, the consultant advises, the scribe writes release notes",
  council: "the artifact modes — intake, spec and plan: an architect drafts, reviewers critique, no code is written",
  solo: "the yardstick — one implementer builds until the gate passes; the advisor is a rubber duck it may consult",
  pair: "driver and navigator — an implementer builds, a reviewer reads each round; the advisor steps in on distress",
  split: "decompose to raise the ceiling — an architect splits the task, several implementers take a piece each",
  tournament: "independent attempts, arbitrated — contestants build unseen by each other; a judge picks one",
};
const names = (entry: Entry) => entry.ducklings ?? (entry.duckling ? [entry.duckling] : []);
const pinned = (entry: Entry) => entry.source === "project pin" || entry.source === "project mode seat" || entry.source === "project";
// The columns a board shows: only the roles the mode seats (rolesForMode is
// the same table the run view uses), Common = the mode-independent roles.
// Every board used to paint all seven roles and read as "my whole team".
// Ordered multi-slot seats: everything else holds one duckling.
const multiSlot = (mode: string, role: string): boolean =>
  (mode === "council" && role === "reviewer") || ((mode === "split" || mode === "tournament") && role === "implementer");
const columnsFor = (mode: string): string[] | null =>
  mode === "common" ? ["triager", "consultant", "scribe"] : rolesForMode(mode);
// One grammar for evidence everywhere: "84% · 198 runs · $0.21/run ·
// coding 68.8". Positive when there is some, one quiet word when there is
// none — not three negatives in a row. Few runs are said to be few: a 0%
// on one run is noise, and it must not read like a measurement. Local
// models have no price to compare, so no "$0.00/run".
const runsWord = (n: number) => `${n} ${n === 1 ? "run" : "runs"}${n < 5 ? " · few" : ""}`;
const evidenceParts = (s: Scorecard, m: { runs?: number; pass_rate?: number; avg_cost_usd?: number } | undefined, prefix = ""): string[] => {
  const parts: string[] = [];
  if (m?.runs) {
    parts.push(`${Math.round(m.pass_rate ?? 0)}%${prefix} · ${runsWord(m.runs)}`);
    if (s.locality !== "local" && m.avg_cost_usd !== undefined) parts.push(`$${m.avg_cost_usd.toFixed(2)}/run`);
  }
  const bench = s.bench?.arena?.score;
  if (bench !== undefined) parts.push(`bench ${Math.round(bench * 100)}`);
  if (s.index?.coding_score !== undefined) parts.push(`coding ${s.index.coding_score}`);
  return parts;
};
type Measurement = { runs?: number; pass_rate?: number; accepted_rate?: number; avg_cost_usd?: number; avg_cost_per_accept_usd?: number; cost_per_accept_usd?: number; cost_per_accepted_run_usd?: number; accepted?: number; accepted_runs?: number; send_back_rate?: number; sent_back?: number; send_backs?: number };
// Scorecards serve pass rates as percentages. Keep the compatibility path for
// legacy fractional fixtures, but 1 is a real one-percent value, not 100%.
const rate = (value: number | undefined): number | undefined => value === undefined ? undefined : (value < 1 ? value * 100 : value);
const money = (value: number | undefined): string | undefined => value === undefined ? undefined : `$${value.toFixed(2)}`;
const suggestionArithmetic = (id: string, role: string | undefined, candidates: { id: string; why: string }[], scorecards: Scorecard[]): string | undefined => {
  const s = scorecards.find((c) => c.id === id);
  if (!s) return undefined;
  const m = (s.measured_by_role?.[role ?? ""] ?? s.measured) as Measurement | undefined;
  if (!m?.runs) return undefined;
  const acceptance = rate(m.pass_rate ?? m.accepted_rate ?? (m.accepted_runs !== undefined || m.accepted !== undefined ? ((m.accepted_runs ?? m.accepted)! / m.runs) : undefined));
  const cost = m.cost_per_accept_usd ?? m.avg_cost_per_accept_usd ?? (m.avg_cost_usd !== undefined && acceptance ? m.avg_cost_usd / (acceptance / 100) : undefined);
  const peers = candidates.map((c) => scorecards.find((x) => x.id === c.id)).filter(Boolean) as Scorecard[];
  const peerValues = peers.map((x) => { const p = (x.measured_by_role?.[role ?? ""] ?? x.measured) as Measurement | undefined; const a = rate(p?.pass_rate); return p?.cost_per_accept_usd ?? p?.avg_cost_per_accept_usd ?? (p?.avg_cost_usd !== undefined && a ? p.avg_cost_usd / (a / 100) : undefined); }).filter((v): v is number => v !== undefined);
  const comparison = peerValues.filter((value) => value !== cost).sort((a, b) => b - a)[0];
  if (cost !== undefined && comparison !== undefined && cost <= Math.min(...peerValues)) return `${id}: lowest cost per accepted run (${money(cost)} vs ${money(comparison)})`;
  if (acceptance !== undefined) return `${id} produced findings in ${Math.round(acceptance)}% of reviews`;
  return undefined;
};
// The criteria a seat's suggestions are ordered by, per role, editable in
// place: the engine ships a default and one developer wants cost first while
// another wants the coding index. Every list is visible — a rule the engine
// honours and the UI hides is a rule nobody can trust (B-052).
function CriteriaPanel({ client, onSaved }: { client: EngineClient; onSaved: () => void }) {
  const [view, setView] = useState<CandidateCriteriaView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const load = () => { if (typeof client.candidateCriteria !== "function") return; client.candidateCriteria().then(setView).catch(setError); };
  useEffect(load, [client]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!view) return error ? <ErrorCard error={error} testId="roster-criteria-error" /> : null;
  const label = (key: string) => view.catalog.find((c) => c.key === key)?.label ?? key;
  const dir = (key: string) => view.catalog.find((c) => c.key === key)?.direction === "asc" ? "↑ lowest first" : "↓ highest first";
  const roles = Object.keys(view.defaults).sort();
  const save = (next: Record<string, string[]>) => { setError(null); client.candidateCriteriaSet(next).then((v) => { setView(v); onSaved(); }).catch(setError); };
  const configured = () => { const out: Record<string, string[]> = {}; for (const r of view.configured) out[r] = view.criteria[r] ?? []; return out; };
  const setRole = (role: string, list: string[]) => save({ ...configured(), [role]: list });
  const reset = (role: string) => { const next = configured(); delete next[role]; save(next); };
  return <div className="rounded border border-hairline bg-surface p-3 text-sm" data-testid="roster-criteria">
    <div className="mb-2 text-xs text-ink-muted">Suggestions rank the flock for a seat by these criteria, in order; a duckling with no value for a criterion sorts after those with one. Ducklings whose declared roles exclude the seat are never suggested. Local models are not compared on price.</div>
    {error !== null && <ErrorCard error={error} testId="roster-criteria-error" />}
    <div className="space-y-2">{roles.map((role) => { const list = view.criteria[role] ?? []; const isCustom = view.configured.includes(role); const available = view.catalog.filter((c) => !list.includes(c.key)); return <div key={role} className="flex flex-wrap items-center gap-1" data-testid={`roster-criteria-${role}`}>
      <span className="w-24 font-medium">{role}</span>
      {list.length === 0 && <span className="text-xs text-ink-muted">suggestions off</span>}
      {list.map((key, i) => <span key={key} className="inline-flex items-center gap-1 rounded-full border border-hairline px-2 py-0.5 text-xs" data-testid={`roster-criteria-${role}-${key}`} title={`${label(key)} — ${dir(key)}`}>
        <span className="text-ink-muted">{i + 1}.</span>{label(key)}
        <button type="button" aria-label={`move ${label(key)} earlier for ${role}`} disabled={i === 0} className="disabled:opacity-30" onClick={() => { const n = list.slice(); [n[i - 1], n[i]] = [n[i]!, n[i - 1]!]; setRole(role, n); }}>‹</button>
        <button type="button" aria-label={`move ${label(key)} later for ${role}`} disabled={i === list.length - 1} className="disabled:opacity-30" onClick={() => { const n = list.slice(); [n[i], n[i + 1]] = [n[i + 1]!, n[i]!]; setRole(role, n); }}>›</button>
        <button type="button" aria-label={`remove ${label(key)} from ${role}`} onClick={() => setRole(role, list.filter((k) => k !== key))}>×</button>
      </span>)}
      {available.length > 0 && <select aria-label={`add a criterion for ${role}`} data-testid={`roster-criteria-add-${role}`} value="" onChange={(e) => { if (e.target.value) setRole(role, [...list, e.target.value]); }} className="rounded border border-hairline bg-surface px-1 py-0.5 text-xs"><option value="">+ add…</option>{available.map((c) => <option key={c.key} value={c.key} title={c.source}>{c.label} ({c.direction === "asc" ? "lowest first" : "highest first"})</option>)}</select>}
      {isCustom ? <button type="button" className="text-xs underline text-ink-muted" data-testid={`roster-criteria-reset-${role}`} onClick={() => reset(role)}>reset to default</button> : <span className="text-xs text-ink-muted">default</span>}
    </div>; })}</div>
  </div>;
}

export function Roster({ client, projectId, projectName }: { client: EngineClient; projectId: string; projectName?: string }) {
  const [scope, setScope] = useState<"global" | "project">("global");
  const [activeMode, setActiveMode] = useState("pair");
  const [ducks, setDucks] = useState<Duckling[]>([]);
  const [scorecards, setScorecards] = useState<Scorecard[]>([]);
  const [boards, setBoards] = useState<Record<string, Entry[]>>({});
  const [criteriaOpen, setCriteriaOpen] = useState(false);
  const [warnings, setWarnings] = useState<Record<string, string | undefined>>({});
  // Errors live with the board that produced them: an error rendered at
  // the top of the view was invisible from Tournament and Common, and two
  // real bugs hid behind that. Key "" is the view itself (a failed re-read).
  const [errors, setErrors] = useState<Record<string, unknown>>({});
  const setError = (mode: string, message: unknown) => setErrors((cur) => {
    const next = { ...cur };
    if (message) next[mode] = message; else delete next[mode];
    return next;
  });
  const [chosenSeat, setChosenSeat] = useState<{ mode: string; role: string } | null>(null);
  const closePicker = () => {
    const seat = chosenSeat;
    setChosenSeat(null);
    if (seat) queueMicrotask(() => (document.querySelector(`[data-testid="roster-column-${seat.mode}-${seat.role}"]`) as HTMLElement | null)?.focus());
  };
  const editable = typeof client.RosterSetManyMode === "function" && typeof client.GlobalRosterSet === "function";

  const reload = () => Promise.all(MODES.map(async (mode) => {
    const result = scope === "global" ? await client.globalRosterGet(mode) : await client.rosterGet(projectId, mode);
    return [mode, (result.entries ?? []) as Entry[], result.warning] as const;
  })).then((items) => {
    setBoards(Object.fromEntries(items.map(([mode, entries]) => [mode, entries])));
    setWarnings(Object.fromEntries(items.map(([mode, , warning]) => [mode, warning])));
  }).catch((e: unknown) => {
    // A failed re-read must not leave stale seats standing as if current.
    setError("", e);
  });

  useEffect(() => { client.ducklings().then(setDucks).catch(() => {}); }, [client]);
  // Evidence powers the seat summaries and the comparison drawer.
  useEffect(() => { if (typeof client.Scorecards === "function") client.Scorecards().then(setScorecards).catch((e) => setError("", e)); }, [client]);
  useEffect(() => { reload(); }, [client, projectId, scope]); // eslint-disable-line react-hooks/exhaustive-deps

  const write = async (mode: string, role: string, ids: string[], propagate = false) => {
    setError(mode, "");
    try {
      if (scope === "global") await client.GlobalRosterSet(mode, role, ids);
      else await client.RosterSetManyMode(projectId, mode, role, ids);
      await reload();
    } catch (e) { setError(mode, e); if (propagate) throw e; }
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
  const applySelection = async (mode: string, role: string, ids: string[]) => {
    await write(mode, role, ids, true);
  };
  const roster = ducks.map((d) => d.id);
  const pair = (boards.pair ?? []);
  const implementers = new Set((pair.find((e) => e.role === "implementer") ? names(pair.find((e) => e.role === "implementer")!) : []));
  const reviewers = new Set((pair.find((e) => e.role === "reviewer") ? names(pair.find((e) => e.role === "reviewer")!) : []));
  const overlap = [...implementers].some((id) => reviewers.has(id));
  // Evidence for a seated duckling IN THAT SEAT keeps the board a decision
  // surface instead of an inventory. The inventory now belongs to the drawer.
  const seatEvidence = (id: string, role: string): { line: string; full: string } => {
    const s = scorecards.find((c) => c.id === id);
    if (!s) return { line: "", full: "" };
    const inRole = s.measured_by_role?.[role];
    const parts = inRole?.runs ? evidenceParts(s, inRole) : evidenceParts(s, s.measured, " overall");
    return { line: parts.filter((p) => !p.endsWith("/run")).join(" · "), full: parts.join(" · ") };
  };
  return <div className="flex h-full min-h-0 flex-col gap-4" data-testid="roster-view">
    <header className="flex flex-wrap items-start gap-4 border-b border-hairline pb-4">
      <div className="min-w-0 flex-1"><p className="text-xs uppercase tracking-[0.14em] text-ink-muted">Team defaults</p><h1 className="mt-1 text-xl font-semibold">Flock</h1><p className="mt-1 max-w-2xl text-sm text-ink-muted">Choose the default ducklings for each operating mode. Task launches and relaunches can override these seats for an individual run.</p></div>
      <div className="flex rounded border border-hairline bg-surface1 p-1" data-testid="roster-scope" aria-label="Flock scope">
        <button type="button" className={`rounded px-3 py-1.5 text-sm ${scope === "project" ? "bg-surface2 font-semibold text-ink" : "text-ink-muted"}`} onClick={() => setScope("project")}>Project · {projectName ?? projectId}</button>
        <button type="button" className={`rounded px-3 py-1.5 text-sm ${scope === "global" ? "bg-surface2 font-semibold text-ink" : "text-ink-muted"}`} onClick={() => setScope("global")}>Global defaults</button>
      </div>
      <button type="button" className="rounded border border-hairline px-3 py-1.5 text-sm text-ink-muted hover:text-ink" data-testid="roster-criteria-toggle" aria-expanded={criteriaOpen} title="how seats are suggested" onClick={() => setCriteriaOpen((v) => !v)}>{criteriaOpen ? "Hide criteria" : "Suggestion criteria"}</button>
    </header>
    {criteriaOpen && <CriteriaPanel client={client} onSaved={() => { void reload(); }} />}
    <nav className="flex shrink-0 gap-1 overflow-x-auto border-b border-hairline" aria-label="Flock modes" data-testid="roster-mode-tabs">
      {MODES.map((mode) => { const warning = Boolean(warnings[mode]); const entries = boards[mode] ?? []; const projectPins = entries.filter(pinned).length; return <button key={mode} type="button" data-testid={`roster-tab-${mode}`} aria-selected={activeMode === mode} onClick={() => setActiveMode(mode)} className={`relative min-w-fit px-3 py-2 text-sm capitalize ${activeMode === mode ? "text-ink" : "text-ink-muted"}`}><span>{mode}</span>{scope === "project" && projectPins > 0 && <span className="ml-1 text-[10px]">{projectPins} override{projectPins === 1 ? "" : "s"}</span>}{warning && <span className="ml-1 text-warning">●</span>}{activeMode === mode && <span className="absolute inset-x-2 bottom-0 h-0.5 bg-ink" />}</button>; })}
    </nav>
    <div className="min-h-0 flex-1 overflow-y-auto pr-1" data-testid="roster-boards">
    {errors[""] !== undefined && <ErrorCard error={errors[""]} testId="roster-error" />}
    {overlap && <p role="alert">implementer and reviewer are the same duckling</p>}
    <div>{MODES.map((mode) => { const common = mode === "common"; const notRunnable = Boolean(warnings[mode]); return <section key={mode} className={activeMode === mode ? "" : "hidden"} data-testid={`roster-board-${mode}`} data-runnable={common ? undefined : String(!notRunnable)}>
      {/* The header is a title, one line saying what the mode is for, and a
          rule beneath. The rule's colour MEANS something: hairline when the
          mode runs, amber when it says "not runnable yet" — the exception
          is the one that should be seen from across the room. Common is not
          a mode; it sits quieter. */}
      <div className="flex items-baseline gap-3 border-b pb-1" style={{ borderBottomColor: notRunnable ? "var(--status-warning)" : "var(--border)" }}>
        <h2 className={common ? "text-base font-medium capitalize text-ink-muted" : "text-lg font-semibold capitalize"}>{mode}</h2>
        <span className="min-w-0 text-xs text-ink-muted" data-testid={`roster-blurb-${mode}`} title={MODE_BLURB[mode]}>{MODE_BLURB[mode]}</span>
        {warnings[mode] && <span className="ml-auto shrink-0 text-xs" data-testid={`roster-warning-${mode}`} style={{ color: "var(--status-warning)" }}>⚠ {warnings[mode]}</span>}
      </div>
      {errors[mode] !== undefined && <ErrorCard error={errors[mode]} testId={`roster-error-${mode}`} />}
      {mode === "common" && scope === "project" && !(boards[mode] ?? []).some((entry) => pinned(entry)) && <p className="text-xs text-ink-muted">no pins</p>}
      <div className="mt-2 flex flex-wrap gap-3">{(boards[mode] ?? []).filter((entry) => { const cols = columnsFor(mode); return !cols || cols.includes(entry.role); }).sort((a, b) => { const cols = columnsFor(mode) ?? []; return cols.indexOf(a.role) - cols.indexOf(b.role); }).map((entry) => {
      const ids = names(entry); const isPinned = pinned(entry); const ghost = scope === "project" && !isPinned; const open = chosenSeat?.mode === mode && chosenSeat.role === entry.role; const top = entry.candidates?.[0]; const suggestion = top && !ids.includes(top.id) ? top : undefined;
      return <div key={entry.role} className="min-w-64 flex-1 rounded" tabIndex={0} data-testid={`roster-column-${mode}-${entry.role}`} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setChosenSeat({ mode, role: entry.role }); } }}>
        {/* Column header: the role, and — when the seat is occupied — one
            small "+" to add or change. The dashed drop zone is for empty
            seats only; an occupied seat with a big "+ assign" over it was
            two frames for one idea. */}
        <div className="mb-1.5 flex items-center gap-1"><h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{entry.role}</h3>{editable && ids.length > 0 && !open && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} title={`assign to ${entry.role}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className="ml-auto rounded border border-hairline px-1.5 text-xs text-ink-muted hover:text-ink">+</button>}</div>
        {editable && ids.length === 0 && !open && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className="mb-2 min-h-12 w-full rounded border border-dashed border-hairline px-2 py-2 text-xs text-ink-muted">drop here · assign</button>}
        {ids.length === 0 && <p className="mb-2 text-xs text-ink-muted" data-testid={`roster-seat-empty-${mode}-${entry.role}`}>{entry.candidates?.length ? `No duckling is seated for ${entry.role} yet; suggestions are waiting for a choice.` : `No duckling is seated for ${entry.role} yet; there is no measured suggestion for this seat.`}</p>}
        {ids.map((id) => { const evidence = seatEvidence(id, entry.role); const isTop = top?.id === id; return <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={isPinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`group rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}>
          <div className="flex items-center gap-2"><DuckAvatar id={id} roster={roster} /><span className="font-medium truncate">{id}</span>{(ghost || isPinned) && <small className="rounded border border-hairline px-1 text-ink-muted">{ghost ? "global default" : "project default"}</small>}{isTop && <small className="text-xs" data-testid={`roster-seat-top-${mode}-${entry.role}-${id}`} title={top?.why} style={{ color: "var(--status-good)" }}>✓ suggested</small>}
            <span className="ml-auto flex gap-2 text-xs opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">{editable && scope === "project" && isPinned && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`unpin ${id} from ${entry.role}`} onClick={() => { setError(mode, ""); void client.RosterUnpin(projectId, mode, entry.role).then(reload).catch((e) => setError(mode, e)); }}>unpin</button>}{editable && <button type="button" className="text-ink-muted hover:text-ink" aria-label={`remove ${id} from ${entry.role}`} title="remove" onClick={() => void write(mode, entry.role, ids.filter((candidate) => candidate !== id))}>×</button>}</span></div>
          {evidence.line && <div className="mt-0.5 truncate text-xs text-ink-muted" data-testid={`roster-seat-evidence-${mode}-${entry.role}-${id}`} title={evidence.full}>{evidence.line}</div>}
        </div>; })}
        {suggestion && !open && <div className="mb-2 text-[11px] text-ink-muted" data-testid={`roster-seat-suggestion-${mode}-${entry.role}`} title={suggestion.why}>suggested: <button type="button" className="underline hover:text-ink" aria-label={`assign suggested ${suggestion.id} to ${entry.role} in ${mode}`} onClick={() => assign(mode, entry.role, suggestion.id)}>{suggestion.id}</button>{suggestionArithmetic(suggestion.id, entry.role, entry.candidates ?? [], scorecards) && <span className="ml-1" data-testid={`roster-seat-suggestion-arithmetic-${mode}-${entry.role}`}> · {suggestionArithmetic(suggestion.id, entry.role, entry.candidates ?? [], scorecards)}</span>}</div>}
      </div>;
    })}</div></section>; })}</div>
    </div>
    {chosenSeat && (() => { const entry = (boards[chosenSeat.mode] ?? []).find((candidate) => candidate.role === chosenSeat.role); const candidates = (entry?.candidates ?? []).map((candidate) => ({ ...candidate, arithmetic: suggestionArithmetic(candidate.id, chosenSeat.role, entry?.candidates ?? [], scorecards) })); return <DucklingPickerDrawer mode={chosenSeat.mode} role={chosenSeat.role} ducklings={ducks} scorecards={scorecards} candidates={candidates} current={entry ? names(entry) : []} multiple={multiSlot(chosenSeat.mode, chosenSeat.role)} scope={scope} onClose={closePicker} onApply={(ids) => applySelection(chosenSeat.mode, chosenSeat.role, ids)} />; })()}
  </div>;
}
