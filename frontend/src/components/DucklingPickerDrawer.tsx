import { useEffect, useMemo, useRef, useState } from "react";
import type { Duckling, Scorecard } from "../api/client";
import { DuckAvatar } from "./DuckAvatar";

type Measurement = { runs?: number; pass_rate?: number; avg_cost_usd?: number; avg_wallclock_s?: number };
export type MapMetric = {
  key: string;
  label: string;
  direction: "asc" | "desc";
  value: (scorecard: Scorecard, role: string) => number | undefined;
  format: (value: number) => string;
};

const roleMeasurement = (scorecard: Scorecard, role: string): Measurement | undefined =>
  scorecard.measured_by_role?.[role] ?? scorecard.measured;

export const MAP_METRICS: MapMetric[] = [
  { key: "coding-index", label: "Coding index", direction: "desc", value: (s) => s.index?.coding_score, format: (v) => v.toFixed(1) },
  { key: "pass-rate", label: "Pass rate", direction: "desc", value: (s, role) => roleMeasurement(s, role)?.pass_rate, format: (v) => `${Math.round(v)}%` },
  { key: "cost-per-run", label: "Cost per run", direction: "asc", value: (s, role) => roleMeasurement(s, role)?.avg_cost_usd, format: (v) => `$${v.toFixed(2)}` },
  { key: "cost-per-accept", label: "Cost per accept", direction: "asc", value: (s, role) => { const m = roleMeasurement(s, role); return m?.avg_cost_usd !== undefined && m.pass_rate ? m.avg_cost_usd / (m.pass_rate > 1 ? m.pass_rate / 100 : m.pass_rate) : undefined; }, format: (v) => `$${v.toFixed(2)}` },
  { key: "wallclock", label: "Wall-clock time", direction: "asc", value: (s, role) => roleMeasurement(s, role)?.avg_wallclock_s, format: (v) => `${Math.round(v)}s` },
  { key: "context", label: "Context window", direction: "desc", value: (s) => s.caps?.context_tokens, format: (v) => `${Math.round(v / 1000)}k` },
  { key: "input-cost", label: "Input price / Mtok", direction: "asc", value: (s) => s.cost?.input_per_mtok, format: (v) => `$${v.toFixed(2)}` },
  { key: "output-cost", label: "Output price / Mtok", direction: "asc", value: (s) => s.cost?.output_per_mtok, format: (v) => `$${v.toFixed(2)}` },
];

type Point = { scorecard: Scorecard; x: number; y: number; xv: number; yv: number; runs: number; pareto: boolean };

function extent(values: number[]): [number, number] {
  const sorted = values.slice().sort((a, b) => a - b);
  if (sorted.length < 5) return [sorted[0] ?? 0, sorted.at(-1) ?? 1];
  return [sorted[Math.floor((sorted.length - 1) * 0.05)]!, sorted[Math.ceil((sorted.length - 1) * 0.95)]!];
}

export function mapPoints(scorecards: Scorecard[], role: string, xMetric: MapMetric, yMetric: MapMetric): Point[] {
  const raw = scorecards.flatMap((scorecard) => {
    const xv = xMetric.value(scorecard, role), yv = yMetric.value(scorecard, role);
    return xv === undefined || yv === undefined ? [] : [{ scorecard, xv, yv }];
  });
  const [xmin, xmax] = extent(raw.map((p) => p.xv));
  const [ymin, ymax] = extent(raw.map((p) => p.yv));
  const normalize = (value: number, min: number, max: number, direction: "asc" | "desc") => {
    const n = max === min ? 0.5 : Math.max(0, Math.min(1, (value - min) / (max - min)));
    return direction === "asc" ? 1 - n : n;
  };
  const points = raw.map((p) => ({ ...p, x: normalize(p.xv, xmin, xmax, xMetric.direction), y: normalize(p.yv, ymin, ymax, yMetric.direction), runs: roleMeasurement(p.scorecard, role)?.runs ?? 0, pareto: false }));
  return points.map((point) => ({ ...point, pareto: !points.some((other) => other !== point && other.x >= point.x && other.y >= point.y && (other.x > point.x || other.y > point.y)) }));
}

export function DucklingPickerDrawer({ mode, role, ducklings, scorecards, candidates = [], current, multiple, scope, onClose, onApply }: {
  mode: string; role: string; ducklings: Duckling[]; scorecards: Scorecard[]; candidates?: { id: string; why: string; arithmetic?: string }[]; current: string[]; multiple: boolean; scope: "global" | "project"; onClose: () => void; onApply: (ids: string[]) => Promise<void> | void;
}) {
  const [xKey, setXKey] = useState("cost-per-accept");
  const [yKey, setYKey] = useState("coding-index");
  const [selected, setSelected] = useState<string[]>(current);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const closeRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useRef<HTMLElement>(null);
  const xMetric = MAP_METRICS.find((m) => m.key === xKey)!;
  const yMetric = MAP_METRICS.find((m) => m.key === yKey)!;
  const complete = useMemo(() => ducklings.map((duck) => scorecards.find((s) => s.id === duck.id) ?? ({ ...duck } as Scorecard)), [ducklings, scorecards]);
  const points = useMemo(() => mapPoints(complete, role, xMetric, yMetric), [complete, role, xMetric, yMetric]);
  const plotted = new Set(points.map((p) => p.scorecard.id));
  const missing = complete.filter((s) => !plotted.has(s.id));
  useEffect(() => { closeRef.current?.focus(); }, []);
  useEffect(() => { const key = (event: KeyboardEvent) => {
    if (event.key === "Escape") onClose();
    if (event.key !== "Tab" || !drawerRef.current) return;
    const focusable = [...drawerRef.current.querySelectorAll<HTMLElement>('button:not([disabled]), select:not([disabled]), [tabindex="0"]')];
    if (!focusable.length) return;
    const first = focusable[0]!, last = focusable.at(-1)!;
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }; addEventListener("keydown", key); return () => removeEventListener("keydown", key); }, [onClose]);
  const toggle = (id: string) => setSelected((ids) => multiple ? (ids.includes(id) ? ids.filter((candidate) => candidate !== id) : [...ids, id]) : [id]);
  const apply = async () => { setSaving(true); setSaveError(""); try { await onApply(selected); onClose(); } catch (error) { setSaveError(error instanceof Error ? error.message : String(error)); } finally { setSaving(false); } };
  const roster = ducklings.map((d) => d.id);
  return <div className="fixed inset-0 z-40 flex justify-end bg-black/30" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section ref={drawerRef} role="dialog" aria-modal="true" aria-labelledby="duckling-map-title" data-testid="duckling-picker-drawer" className="flex h-full w-full flex-col border-l border-hairline bg-page shadow-2xl lg:w-[46%] xl:w-[42%]">
      <header className="flex items-start gap-3 border-b border-hairline px-5 py-4">
        <div><p className="text-xs uppercase tracking-[0.14em] text-ink-muted">{scope} default · {mode}</p><h2 id="duckling-map-title" className="text-lg font-semibold capitalize">Choose {role}</h2><p className="mt-1 text-xs text-ink-muted">{multiple ? "Select one or more ducklings." : "Select one duckling."} Individual runs may override this default.</p></div>
        <button ref={closeRef} type="button" onClick={onClose} aria-label="Close duckling map" className="ml-auto rounded border border-hairline px-2 py-1 text-ink-muted">×</button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="grid grid-cols-2 gap-3">
          <label className="text-xs text-ink-muted">Horizontal · better →<select aria-label="Horizontal axis" value={xKey} onChange={(e) => setXKey(e.target.value)} className="mt-1 block w-full rounded border border-hairline bg-surface1 px-2 py-1.5 text-sm text-ink">{MAP_METRICS.filter((m) => m.key !== yKey).map((m) => <option key={m.key} value={m.key}>{m.label}</option>)}</select></label>
          <label className="text-xs text-ink-muted">Vertical · better ↑<select aria-label="Vertical axis" value={yKey} onChange={(e) => setYKey(e.target.value)} className="mt-1 block w-full rounded border border-hairline bg-surface1 px-2 py-1.5 text-sm text-ink">{MAP_METRICS.filter((m) => m.key !== xKey).map((m) => <option key={m.key} value={m.key}>{m.label}</option>)}</select></label>
        </div>
        <div className="mt-4 rounded-card border border-hairline bg-surface1 p-3" data-testid="duckling-map">
          <div className="mb-2 flex items-center justify-between text-xs text-ink-muted"><span>relative to this flock · robust 5–95% scale</span><span><i className="mr-1 inline-block h-2 w-2 rounded-full bg-good" />Pareto frontier</span></div>
          <svg viewBox="0 0 640 430" className="h-auto w-full" role="img" aria-label={`${yMetric.label} by ${xMetric.label} duckling map`}>
            <rect x="55" y="20" width="550" height="350" rx="8" fill="var(--surface-2)" />
            <line x1="330" y1="20" x2="330" y2="370" stroke="var(--border)" /><line x1="55" y1="195" x2="605" y2="195" stroke="var(--border)" />
            <text x="70" y="44" fill="var(--text-muted)" fontSize="11">stronger {yMetric.label.toLowerCase()}</text><text x="470" y="44" fill="var(--status-good)" fontSize="11">best trade-offs</text>
            <text x="330" y="410" textAnchor="middle" fill="var(--text-muted)" fontSize="12">{xMetric.label} · more desirable →</text>
            <text x="16" y="195" textAnchor="middle" transform="rotate(-90 16 195)" fill="var(--text-muted)" fontSize="12">{yMetric.label} · more desirable →</text>
            {points.map((point) => { const cx = 70 + point.x * 520, cy = 355 - point.y * 320; const chosen = selected.includes(point.scorecard.id); const radius = 7 + Math.min(8, Math.sqrt(point.runs)); return <g key={point.scorecard.id} role="button" tabIndex={0} aria-label={`${point.scorecard.id}: ${xMetric.format(point.xv)} ${xMetric.label}, ${yMetric.format(point.yv)} ${yMetric.label}${chosen ? ", selected" : ""}`} onClick={() => toggle(point.scorecard.id)} onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") toggle(point.scorecard.id); }} className="cursor-pointer">
              <circle cx={cx} cy={cy} r={radius} fill={point.pareto ? "var(--status-good)" : "var(--text-muted)"} fillOpacity={point.runs < 5 ? .35 : .75} stroke={chosen ? "var(--text-primary)" : current.includes(point.scorecard.id) ? "var(--status-warning)" : "var(--surface-1)"} strokeWidth={chosen ? 4 : 2}><title>{point.scorecard.id} · {xMetric.format(point.xv)} · {yMetric.format(point.yv)} · {point.runs} runs</title></circle>
              <text x={cx} y={cy + radius + 14} textAnchor="middle" fill="var(--text-primary)" fontSize="11">{point.scorecard.id}</text>
            </g>; })}
          </svg>
        </div>
        {missing.length > 0 && <details className="mt-3 text-xs text-ink-muted"><summary>Not plotted · {missing.length} missing one or both metrics</summary><p className="mt-1">{missing.map((s) => s.id).join(", ")}</p></details>}
        <div className="mt-5"><h3 className="text-xs font-semibold uppercase tracking-wide text-ink-muted">All candidates</h3><div className="mt-2 grid grid-cols-1 gap-1 sm:grid-cols-2">{complete.slice().sort((a, b) => { const ai = candidates.findIndex((candidate) => candidate.id === a.id), bi = candidates.findIndex((candidate) => candidate.id === b.id); return (ai < 0 ? 999 : ai) - (bi < 0 ? 999 : bi); }).map((scorecard) => { const candidate = candidates.find((item) => item.id === scorecard.id); const chosen = selected.includes(scorecard.id); return <button key={scorecard.id} type="button" aria-pressed={chosen} onClick={() => toggle(scorecard.id)} data-testid={candidate ? `roster-pick-suggested-${scorecard.id}` : undefined} className={`rounded border px-2 py-2 text-left ${chosen ? "border-ink bg-surface2" : "border-hairline bg-surface1"}`}><span className="text-sm font-medium"><DuckAvatar id={scorecard.id} roster={roster} /> <span className="ml-1">{scorecard.id}</span></span>{candidate && <span className="mt-1 block text-xs text-ink-muted">{candidate.why}</span>}{candidate?.arithmetic && <span data-testid={`roster-pick-suggested-arithmetic-${scorecard.id}`} className="mt-1 block text-xs text-ink-muted">{candidate.arithmetic}</span>}</button>; })}</div></div>
        <div className="mt-5"><h3 className="text-xs font-semibold uppercase tracking-wide text-ink-muted">Selection · {selected.length}</h3><div className="mt-2 flex flex-wrap gap-2">{selected.length === 0 ? <span className="text-sm text-ink-muted">No duckling selected.</span> : selected.map((id, index) => <button key={id} type="button" onClick={() => toggle(id)} className="rounded-full border border-hairline bg-surface1 px-2 py-1 text-sm"><DuckAvatar id={id} roster={roster} /> <span className="ml-1">{multiple ? `${index + 1}. ` : ""}{id} ×</span></button>)}</div></div>
        <div className="mt-5 space-y-1 border-t border-hairline pt-4 text-xs text-ink-muted"><p>Circle size represents the amount of run evidence.</p><p>Faded circles have fewer than five measured runs. Axes always point toward the more desirable outcome.</p></div>
      </div>
      {saveError && <p role="alert" className="border-t border-critical px-5 py-2 text-xs text-critical">{saveError}</p>}
      <footer className="flex items-center gap-3 border-t border-hairline px-5 py-3"><span className="text-xs text-ink-muted">{current.length ? `Current: ${current.join(", ")}` : "Currently unseated"}</span><button type="button" onClick={onClose} className="ml-auto rounded border border-hairline px-3 py-1.5 text-sm">Cancel</button><button type="button" disabled={saving || selected.length === 0} onClick={() => void apply()} className="rounded bg-ink px-3 py-1.5 text-sm text-page disabled:opacity-40">{saving ? "Applying…" : `Apply${multiple ? ` ${selected.length} ducklings` : ""}`}</button></footer>
    </section>
  </div>;
}
