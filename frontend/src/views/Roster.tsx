import { useEffect, useRef, useState } from "react";
import type { CandidateCriteriaView, Duckling, EngineClient, Scorecard } from "../api/client";
import { DuckAvatar } from "../components/DuckAvatar";
import { StatusChip } from "../components/StatusChip";
import { ErrorCard } from "../components/ErrorCard";
import { rolesForMode } from "../lib/seats";
import type { ProviderView } from "../api/client";

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
// A duckling is local when its provider answers on this machine or the LAN;
// the provider id says nothing about that ("beelink" is local, "openrouter"
// is not), so the endpoint decides. A provider literally named "local" counts.
const isLocal = (d: Duckling, providers: ProviderView[]): boolean => {
  if (d.provider === "local") return true;
  const p = providers.find((x) => x.id === d.provider);
  if (!p) return false;
  return /^(https?:\/\/)?(localhost|127\.|0\.0\.0\.0|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|\[::1\]|[^/]*\.local\b)/i.test(p.base_url ?? "");
};

const CONTEXT_TIERS = [{ label: "128k", tokens: 128_000 }, { label: "256k", tokens: 256_000 }, { label: "1M", tokens: 1_000_000 }];
const SORTS: { key: string; label: string; value: (s: Scorecard) => number | undefined; format: (v: number) => string }[] = [
  { key: "pass-rate", label: "pass rate", value: (s) => (s.measured?.runs ? s.measured.pass_rate : undefined), format: (v) => `${Math.round(v)}%` },
  { key: "avg-cost", label: "cost per run", value: (s) => (s.measured?.runs ? s.measured.avg_cost_usd : undefined), format: (v) => `$${v.toFixed(2)}/run` },
  { key: "input-cost", label: "input cost", value: (s) => s.cost?.input_per_mtok, format: (v) => `$${v}/Mtok in` },
  { key: "output-cost", label: "output cost", value: (s) => s.cost?.output_per_mtok, format: (v) => `$${v}/Mtok out` },
  { key: "bench:arena", label: "bench · arena", value: (s) => s.bench?.arena?.score, format: (v) => `bench ${Math.round(v * 100)}` },
  { key: "coding-index", label: "coding index", value: (s) => s.index?.coding_score, format: (v) => `coding ${v}` },
  { key: "context", label: "context", value: (s) => s.caps?.context_tokens, format: (v) => (v >= 1_000_000 ? `${(v / 1_000_000).toFixed(v % 1_000_000 ? 1 : 0)}M ctx` : `${Math.round(v / 1000)}k ctx`) },
];
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
const evidenceLine = (s: Scorecard): string => { const parts = evidenceParts(s, s.measured); return parts.length ? parts.join(" · ") : "no evidence yet"; };

// Portraits are deliberately prose: the compact evidence line remains useful
// for comparison, while this sentence tells a person what the record means.
type Measurement = { runs?: number; pass_rate?: number; accepted_rate?: number; avg_cost_usd?: number; avg_cost_per_accept_usd?: number; cost_per_accept_usd?: number; cost_per_accepted_run_usd?: number; accepted?: number; accepted_runs?: number; send_back_rate?: number; sent_back?: number; send_backs?: number };
// Scorecards serve pass rates as percentages. Keep the compatibility path for
// legacy fractional fixtures, but 1 is a real one-percent value, not 100%.
const rate = (value: number | undefined): number | undefined => value === undefined ? undefined : (value < 1 ? value * 100 : value);
const money = (value: number | undefined): string | undefined => value === undefined ? undefined : `$${value.toFixed(2)}`;
const portrait = (s: Scorecard): string => {
  const m = s.measured as (Measurement | undefined);
  if (!m?.runs) return "No measured runs yet.";
  const early = m.runs < 15 ? "Early numbers: " : "";
  const acceptedRate = rate(m.pass_rate ?? m.accepted_rate ?? (m.accepted_runs !== undefined || m.accepted !== undefined ? ((m.accepted_runs ?? m.accepted)! / m.runs) : undefined));
  const sentBackRate = rate(m.send_back_rate ?? ((m.sent_back ?? m.send_backs) !== undefined ? ((m.sent_back ?? m.send_backs)! / m.runs) : undefined));
  const cost = m.cost_per_accept_usd ?? m.cost_per_accepted_run_usd ?? m.avg_cost_per_accept_usd ?? (m.avg_cost_usd !== undefined && acceptedRate ? m.avg_cost_usd / (acceptedRate / 100) : undefined);
  const parts = [acceptedRate === undefined ? undefined : `${Math.round(acceptedRate)}% accepted`, sentBackRate === undefined ? undefined : `${Math.round(sentBackRate)}% sent back`, cost === undefined || s.locality === "local" ? undefined : `${money(cost)} per accept`].filter(Boolean);
  return `${early}${parts.length ? parts.join("; ") : `${m.runs} ${m.runs === 1 ? "run" : "runs"} measured`}.`;
};

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
function FilterChip({ testId, label, on, onClick }: { testId: string; label: string; on: boolean; onClick: () => void }) {
  return <button type="button" data-testid={testId} aria-pressed={on} onClick={onClick} className={`rounded-full border px-2 py-0.5 ${on ? "border-ink bg-surface2 text-ink" : "border-hairline text-ink-muted hover:text-ink"}`}>{label}</button>;
}

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
  const [ducks, setDucks] = useState<Duckling[]>([]);
  const [scorecards, setScorecards] = useState<Scorecard[]>([]);
  const [flockText, setFlockText] = useState("");
  const [flockProvider, setFlockProvider] = useState("");
  const [flockLocality, setFlockLocality] = useState("");
  const [flockVision, setFlockVision] = useState(false);
  const [flockTools, setFlockTools] = useState(false);
  const [flockContext, setFlockContext] = useState(0);
  // Evidence first: what a duckling has done here outranks what its provider
  // charges. Highest first, because that is the question ("who is best?").
  const [flockSort, setFlockSort] = useState("pass-rate");
  const [flockDir, setFlockDir] = useState<"asc" | "desc">("desc");
  const clearFlockFilters = () => { setFlockText(""); setFlockProvider(""); setFlockLocality(""); setFlockVision(false); setFlockTools(false); setFlockContext(0); };
  const [providers, setProviders] = useState<ProviderView[]>([]);
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
  const dragging = useRef(false);
  const [dragOver, setDragOver] = useState<string | null>(null);
  // The drag image. Left to the browser, WebKitGTK renders the whole card
  // as the ghost, anchored far from the pointer — it looked like the drop
  // was landing on the next column. A small chip with the duckling's name,
  // held at the cursor, says exactly what is in hand and where it is.
  const startDrag = (id: string, event: React.DragEvent) => {
    dragging.current = true;
    event.dataTransfer.setData("text/plain", id);
    event.dataTransfer.effectAllowed = "copy";
    if (typeof event.dataTransfer.setDragImage !== "function") return;
    const ghost = document.createElement("div");
    ghost.textContent = id;
    ghost.setAttribute("data-testid", "roster-drag-ghost");
    ghost.style.cssText = "position:fixed;top:-100px;left:-100px;padding:4px 10px;border-radius:9999px;font:600 13px system-ui,sans-serif;background:var(--surface,#222);color:var(--text,#eee);border:1px solid var(--border,#666);box-shadow:0 2px 8px rgba(0,0,0,.35);white-space:nowrap;pointer-events:none;";
    document.body.appendChild(ghost);
    // Anchor the chip so the pointer sits at its left-middle: the name
    // trails the cursor and the drop point is the cursor, not the card.
    event.dataTransfer.setDragImage(ghost, 12, ghost.offsetHeight / 2 || 12);
    setTimeout(() => ghost.remove(), 0);
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
  // Evidence failing to load is said, not swallowed: a Flock that shows "no
  // runs yet" for a duckling with 264 runs is a lie the operator will act on.
  useEffect(() => { if (typeof client.Scorecards === "function") client.Scorecards().then(setScorecards).catch((e) => setError("", e)); }, [client]);
  const flock = (() => {
    const all = ducks.map((d) => scorecards.find((s) => s.id === d.id) ?? ({ ...d } as Scorecard));
    const sort = SORTS.find((o) => o.key === flockSort) ?? SORTS[0]!;
    const filtering = Boolean(flockText || flockProvider || flockLocality || flockVision || flockTools || flockContext);
    const filtered = all.filter((s) => (!flockText || `${s.id} ${s.model}`.toLowerCase().includes(flockText.toLowerCase())) && (!flockProvider || s.provider === flockProvider) && (!flockLocality || (s.locality ?? (isLocal(ducks.find((d) => d.id === s.id)!, providers) ? "local" : "remote")) === flockLocality) && (!flockVision || s.caps?.vision) && (!flockTools || s.caps?.native_tools) && (!flockContext || (s.caps?.context_tokens ?? 0) >= flockContext))
      .sort((a, b) => { const av = sort.value(a), bv = sort.value(b); if (av === undefined && bv === undefined) return 0; if (av === undefined) return 1; if (bv === undefined) return -1; return (av - bv) * (flockDir === "asc" ? 1 : -1); });
    const active = chosenSeat ? (boards[chosenSeat.mode] ?? []).find((e) => e.role === chosenSeat.role) : undefined;
    const candidates = active?.candidates ?? [];
    const rank = new Map(candidates.map((c, i) => [c.id, i]));
    const shown = chosenSeat && !["triager", "consultant", "scribe"].includes(chosenSeat.role) ? filtered.slice().sort((a, b) => (rank.has(a.id) ? rank.get(a.id)! : 999) - (rank.has(b.id) ? rank.get(b.id)! : 999)) : filtered;
    return { all, shown, filtering, providers: [...new Set(all.map((s) => s.provider).filter(Boolean))] as string[], value: sort.value, format: sort.format, sortLabel: sort.label, candidates, forRole: active?.role };
  })();
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
    } catch (e) { setError(mode, e); }
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
  // The Flock and the boards scroll on their own: the flock is as long as the
  // roster of ducklings, the boards as long as the modes, and scrolling one
  // to reach its tail must not hide the other's head (Council, Solo).
  // Evidence for a seated duckling IN THAT SEAT, and the seat's suggestion,
  // so the board is a decision surface and not a map of names.
  // The seat line is 14rem wide: rate, runs and coding index fit; the cost
  // per run rides the tooltip (the Flock card shows it in full).
  const seatEvidence = (id: string, role: string): { line: string; full: string } => {
    const s = scorecards.find((c) => c.id === id);
    if (!s) return { line: "", full: "" };
    const inRole = s.measured_by_role?.[role];
    const parts = inRole?.runs ? evidenceParts(s, inRole) : evidenceParts(s, s.measured, " overall");
    return { line: parts.filter((p) => !p.endsWith("/run")).join(" · "), full: parts.join(" · ") };
  };
  return <div className="flex h-full min-h-0 flex-col gap-4" data-testid="roster-view">
    <div className="flex items-center gap-2" data-testid="roster-scope">
      <button type="button" className={scope === "global" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("global")}>Global</button>
      <span>|</span>
      <button type="button" className={scope === "project" ? "text-ink font-semibold" : "text-ink-muted"} onClick={() => setScope("project")}>Project · {projectName ?? projectId}</button>
    </div>
    {criteriaOpen && <CriteriaPanel client={client} onSaved={() => { void reload(); }} />}
    <div className="flex min-h-0 flex-1 gap-6 items-stretch">
    <aside className="flex w-72 shrink-0 flex-col min-h-0" data-testid="roster-flock">
      <div className="flex items-baseline gap-2"><h2 className="text-lg font-semibold">Flock</h2>
        <span data-testid="roster-flock-count" className={`text-xs ${flock.filtering ? "font-medium text-ink" : "text-ink-muted"}`}>{flock.shown.length === flock.all.length ? `${flock.all.length} ducklings` : `${flock.shown.length} of ${flock.all.length}`}{flock.filtering && <> · <button type="button" className="underline" data-testid="roster-flock-clear" onClick={clearFlockFilters}>clear</button></>}</span>
        <button type="button" className="ml-auto text-xs text-ink-muted underline hover:text-ink" data-testid="roster-criteria-toggle" aria-expanded={criteriaOpen} title="how seats are suggested" onClick={() => setCriteriaOpen((v) => !v)}>{criteriaOpen ? "hide criteria" : "criteria"}</button></div>
      <input data-testid="roster-flock-filter-text" value={flockText} onChange={(e) => setFlockText(e.target.value)} placeholder="search id or model" aria-label="search the flock" className="mt-2 w-full rounded border border-hairline bg-surface px-2 py-1 text-sm" />
      <div className="mt-2 flex flex-wrap items-center gap-1 text-xs" aria-label="flock filters">
        <span className="mr-1 text-ink-muted" data-testid="roster-flock-filter-caption">filter the flock:</span>
        {flock.providers.map((p) => <FilterChip key={p} testId={`roster-flock-filter-provider-${p}`} label={p} on={flockProvider === p} onClick={() => setFlockProvider(flockProvider === p ? "" : p)} />)}
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-hairline" />
        {(["local", "remote"] as const).map((l) => <FilterChip key={l} testId={`roster-flock-filter-locality-${l}`} label={l} on={flockLocality === l} onClick={() => setFlockLocality(flockLocality === l ? "" : l)} />)}
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-hairline" />
        <FilterChip testId="roster-flock-filter-vision" label="vision" on={flockVision} onClick={() => setFlockVision(!flockVision)} />
        <FilterChip testId="roster-flock-filter-native-tools" label="tools" on={flockTools} onClick={() => setFlockTools(!flockTools)} />
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-hairline" />
        {CONTEXT_TIERS.map((t) => <FilterChip key={t.label} testId={`roster-flock-filter-context-${t.label}`} label={`≥${t.label}`} on={flockContext === t.tokens} onClick={() => setFlockContext(flockContext === t.tokens ? 0 : t.tokens)} />)}
      </div>
      <div className="mt-2 flex items-center gap-1 text-xs text-ink-muted"><span>sort by</span>
        <select data-testid="roster-flock-sort" aria-label="sort the flock by" value={flockSort} onChange={(e) => setFlockSort(e.target.value)} className="rounded border border-hairline bg-surface px-1 py-0.5 text-xs text-ink">{SORTS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}</select>
        <button type="button" data-testid="roster-flock-sort-dir" aria-label={flockDir === "desc" ? "highest first — switch to lowest first" : "lowest first — switch to highest first"} title={flockDir === "desc" ? "highest first" : "lowest first"} onClick={() => setFlockDir(flockDir === "desc" ? "asc" : "desc")} className="rounded border border-hairline px-1.5 py-0.5">{flockDir === "desc" ? "↓" : "↑"}</button>
      </div>
      <div className="mt-2 min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1" data-testid="roster-flock-list">
      {flock.shown.length === 0 && <p className="text-sm text-ink-muted" data-testid="roster-flock-empty">no duckling matches these filters.</p>}
      {/* Compact: two lines per duckling so the whole flock fits the height
          it is dragged across. Name, locality and the sort value on the
          first; the evidence line on the second. Model and list price ride
          the tooltip. */}
      {flock.shown.map((s) => { const d = ducks.find((x) => x.id === s.id)!; const candidate = flock.candidates.find((c) => c.id === s.id); const local = s.locality ?? ""; const v = flock.value(s); const price = s.cost ? `$${s.cost.input_per_mtok} / $${s.cost.output_per_mtok} per Mtok` : "price unknown"; return <div key={s.id} draggable data-testid={`roster-flock-card-${s.id}`} title={`${s.model} · ${price}${s.index?.source ? `\ncoding index: ${s.index.source} · as of ${s.index.as_of ?? "?"}` : ""}`} onDragStart={(event) => startDrag(s.id, event)} onDragEnd={() => { dragging.current = false; setDragOver(null); }} className={`rounded border px-2 py-1.5 bg-surface ${candidate ? "border-ink-muted" : "border-hairline"}`}>
        <div className="flex items-center gap-2"><DuckAvatar id={s.id} roster={roster} /><span className="font-medium truncate">{s.id}</span><span className="text-xs text-ink-muted"><StatusChip role="muted" label={local || (isLocal(d, providers) ? "local" : "remote")} /></span><span data-testid={`roster-flock-value-${s.id}`} className="ml-auto text-sm tabular-nums" title={flock.sortLabel}>{v === undefined ? "—" : flock.format(v)}</span></div>
        <div className="mt-0.5 truncate text-xs text-ink-muted" data-testid={`roster-flock-evidence-${s.id}`}>{evidenceLine(s)}</div>
        <div className="mt-0.5 truncate text-xs text-ink-muted" data-testid={`roster-flock-portrait-${s.id}`}>{portrait(s)}</div>
        {candidate && <div className="mt-0.5 text-xs"><span data-testid={`roster-suggested-${s.id}`} className="font-medium">suggested for {flock.forRole}</span> <span data-testid={`roster-suggested-why-${s.id}`} className="text-ink-muted">· {candidate.why}</span></div>}
      </div>; })}
    </div></aside>
    <div className="flex-1 min-w-0 min-h-0 overflow-y-auto pr-1" data-testid="roster-boards">
    {errors[""] !== undefined && <ErrorCard error={errors[""]} testId="roster-error" />}
    {overlap && <p role="alert">implementer and reviewer are the same duckling</p>}
    <div className="space-y-6">{MODES.map((mode) => { const common = mode === "common"; const notRunnable = Boolean(warnings[mode]); return <section key={mode} data-testid={`roster-board-${mode}`} data-runnable={common ? undefined : String(!notRunnable)}>
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
      return <div key={entry.role} className={`w-56 shrink-0 rounded transition-colors ${dragOver === `${mode}/${entry.role}` ? "bg-surface2 outline outline-1 outline-ink-muted" : ""}`} tabIndex={0} data-testid={`roster-column-${mode}-${entry.role}`} onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = "copy"; if (dragOver !== `${mode}/${entry.role}`) setDragOver(`${mode}/${entry.role}`); }} onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver((cur) => (cur === `${mode}/${entry.role}` ? null : cur)); }} onDrop={(event) => { setDragOver(null); drop(mode, entry.role, event); }} data-dragover={dragOver === `${mode}/${entry.role}` ? "true" : undefined} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setChosenSeat({ mode, role: entry.role }); } }}>
        {/* Column header: the role, and — when the seat is occupied — one
            small "+" to add or change. The dashed drop zone is for empty
            seats only; an occupied seat with a big "+ assign" over it was
            two frames for one idea. */}
        <div className="mb-1.5 flex items-center gap-1"><h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{entry.role}</h3>{editable && ids.length > 0 && !open && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} title={`assign to ${entry.role}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className="ml-auto rounded border border-hairline px-1.5 text-xs text-ink-muted hover:text-ink">+</button>}</div>
        {editable && ids.length === 0 && !open && <button type="button" data-testid={`roster-drop-${mode}-${entry.role}`} aria-label={`assign to ${entry.role} in ${mode}`} onClick={() => setChosenSeat({ mode, role: entry.role })} className="mb-2 min-h-12 w-full rounded border border-dashed border-hairline px-2 py-2 text-xs text-ink-muted">drop here · assign</button>}
        {ids.length === 0 && <p className="mb-2 text-xs text-ink-muted" data-testid={`roster-seat-empty-${mode}-${entry.role}`}>{entry.candidates?.length ? `No duckling is seated for ${entry.role} yet; suggestions are waiting for a choice.` : `No duckling is seated for ${entry.role} yet; there is no measured suggestion for this seat.`}</p>}
        {ids.map((id) => { const evidence = seatEvidence(id, entry.role); const isTop = top?.id === id; return <div key={id} data-testid={`roster-card-${mode}-${entry.role}-${id}`} data-ghost={ghost ? "true" : undefined} title={isPinned && (entry.global_ducklings ?? entry.default) ? `Global: ${Array.isArray(entry.global_ducklings) ? entry.global_ducklings.join(", ") : entry.default}` : undefined} className={`group rounded border p-2 mb-2 ${ghost ? "border-dashed text-ink-muted" : "border-hairline"}`}>
          <div className="flex items-center gap-2"><DuckAvatar id={id} roster={roster} /><span className="font-medium truncate">{id}</span>{(ghost || isPinned) && <small className="rounded border border-hairline px-1 text-ink-muted">{ghost ? "global" : "pinned"}</small>}{isTop && <small className="text-xs" data-testid={`roster-seat-top-${mode}-${entry.role}-${id}`} title={top?.why} style={{ color: "var(--status-good)" }}>✓ suggested</small>}
            <span className="ml-auto flex gap-2 text-xs opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">{editable && scope === "project" && isPinned && <button type="button" className="text-ink-muted hover:text-ink underline" aria-label={`unpin ${id} from ${entry.role}`} onClick={() => { setError(mode, ""); void client.RosterUnpin(projectId, mode, entry.role).then(reload).catch((e) => setError(mode, e)); }}>unpin</button>}{editable && <button type="button" className="text-ink-muted hover:text-ink" aria-label={`remove ${id} from ${entry.role}`} title="remove" onClick={() => void write(mode, entry.role, ids.filter((candidate) => candidate !== id))}>×</button>}</span></div>
          {evidence.line && <div className="mt-0.5 truncate text-xs text-ink-muted" data-testid={`roster-seat-evidence-${mode}-${entry.role}-${id}`} title={evidence.full}>{evidence.line}</div>}
        </div>; })}
        {suggestion && !open && <div className="mb-2 text-[11px] text-ink-muted" data-testid={`roster-seat-suggestion-${mode}-${entry.role}`} title={suggestion.why}>suggested: <button type="button" className="underline hover:text-ink" aria-label={`assign suggested ${suggestion.id} to ${entry.role} in ${mode}`} onClick={() => assign(mode, entry.role, suggestion.id)}>{suggestion.id}</button>{suggestionArithmetic(suggestion.id, entry.role, entry.candidates ?? [], scorecards) && <span className="ml-1" data-testid={`roster-seat-suggestion-arithmetic-${mode}-${entry.role}`}> · {suggestionArithmetic(suggestion.id, entry.role, entry.candidates ?? [], scorecards)}</span>}</div>}
        {editable && open && <div className="mb-2 rounded border border-hairline bg-surface2 p-2" role="listbox" aria-label={`choose a duckling for ${entry.role}`}><div className="mb-1 flex items-center justify-between text-xs text-ink-muted"><span>assign to {entry.role}</span><button type="button" className="underline" onClick={() => setChosenSeat(null)}>cancel</button></div><div className="max-h-56 space-y-1 overflow-y-auto">{(() => { const candidates = entry.candidates ?? []; const rank = new Map(candidates.map((c, i) => [c.id, i])); const ordered = ducks.filter((duck) => !ids.includes(duck.id)).slice().sort((a, b) => (rank.has(a.id) ? rank.get(a.id)! : 999) - (rank.has(b.id) ? rank.get(b.id)! : 999)); return ordered.map((duck) => { const candidate = candidates.find((c) => c.id === duck.id); return <button key={duck.id} type="button" className="block w-full rounded px-2 py-1 text-left text-sm hover:bg-surface" aria-label={`assign ${duck.id} to ${entry.role}`} onClick={() => assign(mode, entry.role, duck.id)} data-testid={candidate ? `roster-pick-suggested-${duck.id}` : undefined}><DuckAvatar id={duck.id} roster={roster} /> <span className="ml-1">{duck.id}</span>{candidate && <><span className="ml-2 text-xs">suggested for {entry.role}</span><span data-testid={`roster-pick-suggested-why-${duck.id}`} className="ml-2 text-xs text-ink-muted">{candidate.why}</span>{suggestionArithmetic(duck.id, entry.role, candidates, scorecards) && <span className="ml-2 text-xs text-ink-muted" data-testid={`roster-pick-suggested-arithmetic-${duck.id}`}>{suggestionArithmetic(duck.id, entry.role, candidates, scorecards)}</span>}</>}</button>; }); })()}</div></div>}
      </div>;
    })}</div></section>; })}</div>
    </div>
    </div>
  </div>;
}
