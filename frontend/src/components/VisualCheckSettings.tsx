/**
 * Setting up the visual gate from the desktop (B-460, part 2).
 *
 * The gate compares screenshots of the product with reference images. Its
 * inputs are things the project already knows — the images attached to the
 * requirements and the captures of the last run — so the editor offers them
 * as choices instead of asking for ids and file names. An image chosen from
 * disk is imported as a reference first, so it gets the same citable id the
 * requirements use.
 */

import { useEffect, useState } from "react";
import type { EngineClient, RenderCompare, VisualCheckView } from "../api/client";
import { canChooseFile, chooseFile } from "../lib/picker";

// threshold is carried, not edited (review of #124): rebuilding rows from
// the visible fields reset a configured per-pixel threshold to the default,
// which could turn a required failure into a pass.
type Row = { capture: string; reference: string; tolerancePct: string; threshold?: number };

const toRows = (compare: RenderCompare[]): Row[] =>
  compare.map((c) => ({ capture: c.capture, reference: c.reference, tolerancePct: String(Math.round((c.tolerance ?? 0.02) * 1000) / 10), threshold: c.threshold }));

export function VisualCheckSettings({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [view, setView] = useState<VisualCheckView | null>(null);
  const [editing, setEditing] = useState(false);
  const [command, setCommand] = useState("");
  const [enforcement, setEnforcement] = useState<"diagnostic" | "required">("diagnostic");
  const [rows, setRows] = useState<Row[]>([]);
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (typeof client.visualCheck !== "function") return;
    let live = true;
    client.visualCheck(projectId).then((v) => { if (live) setView(v); }).catch(() => {});
    return () => { live = false; };
  }, [client, projectId]);

  // Thumbnails of the reference images, loaded once each.
  useEffect(() => {
    if (!view || typeof client.referenceImageUrl !== "function") return;
    let live = true;
    for (const ref of view.references) {
      if (thumbs[ref.id]) continue;
      void client.referenceImageUrl(projectId, ref.id).then((url) => {
        if (!live) { URL.revokeObjectURL?.(url); return; }
        setThumbs((cur) => ({ ...cur, [ref.id]: url }));
      }).catch(() => {});
    }
    return () => { live = false; };
  }, [view, client, projectId]);

  if (!view) return null;

  const open = () => {
    setCommand(view.command);
    setEnforcement(view.enforcement);
    setRows(view.compare.length ? toRows(view.compare) : [{ capture: view.recent_captures?.[0] ?? "", reference: view.references[0]?.id ?? "", tolerancePct: "2" }]);
    setFailure(null);
    setEditing(true);
  };
  const setRow = (i: number, patch: Partial<Row>) => setRows((cur) => cur.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  const importInto = async (i: number) => {
    const path = await chooseFile();
    if (!path) return;
    try {
      const ref = await client.referenceImport(projectId, path);
      setView((v) => (v ? { ...v, references: [ref, ...v.references.filter((r) => r.id !== ref.id)] } : v));
      setRow(i, { reference: ref.id });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    }
  };

  const rowProblem = (r: Row) => {
    if (!r.capture.trim()) return "name the capture to compare";
    if (!r.reference) return "choose a reference image";
    const t = Number(r.tolerancePct);
    if (!Number.isFinite(t) || t < 0 || t > 100) return "the allowance is a percentage from 0 to 100";
    return null;
  };
  const problems = rows.map(rowProblem);
  const commandMissing = rows.length > 0 && !command.trim();
  const canSave = !busy && !commandMissing && problems.every((p) => !p);

  const save = async () => {
    setBusy(true);
    setFailure(null);
    try {
      const next = await client.visualCheckSet(projectId, {
        command: command.trim(),
        artifacts: view.artifacts || undefined,
        enforcement,
        compare: rows.map((r) => ({
          capture: r.capture.trim(), reference: r.reference, tolerance: Number(r.tolerancePct) / 100,
          ...(r.threshold !== undefined ? { threshold: r.threshold } : {}),
        })),
      });
      setView(next);
      setEditing(false);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const n = view.compare.length;
  const summary = !view.configured
    ? "not set up — compare screenshots with reference images"
    : n === 0
      ? "captures only, no comparison"
      : `${n} comparison${n === 1 ? "" : "s"} · ${view.enforcement === "required" ? "fails the run on a mismatch" : "reports mismatches"}`;

  return (
    <div data-testid="visual-settings">
      <div className="flex items-center gap-2 text-xs">
        <span className="w-10 shrink-0 text-ink-muted">visual</span>
        <span className="min-w-0 flex-1 truncate text-ink-secondary" data-testid="visual-settings-summary">{summary}</span>
        {!editing && (
          <button type="button" onClick={open} data-testid="visual-settings-edit" className="shrink-0 text-xs text-ink-muted underline">
            {view.configured ? "edit" : "set up"}
          </button>
        )}
      </div>
      {editing && (
        <div className="mt-2 space-y-3 rounded border border-hairline p-3" data-testid="visual-editor">
          <p className="text-xs text-ink-secondary">
            Compare screenshots of your product with reference images at the end of every build. Ducklab runs your
            capture command; it must save PNG files into the folder named by <code>$DUCKLAB_RENDER_OUTPUT</code>. Any tool
            works: a browser script, a window screenshot, a device capture.
          </p>
          <label className="block space-y-1">
            <span className="text-xs font-medium text-ink">Capture command</span>
            <input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="node capture.mjs"
              data-testid="visual-command"
              className="w-full rounded border border-hairline bg-surface2 px-2 py-1 font-mono text-xs"
            />
            {commandMissing && <span className="text-xs text-ink-muted">A comparison needs a capture command to produce the screenshot.</span>}
          </label>

          <fieldset className="space-y-1">
            <legend className="text-xs font-medium text-ink">When a screenshot does not match</legend>
            <label className="flex items-center gap-2 text-xs">
              <input type="radio" name={`enf-${projectId}`} checked={enforcement === "diagnostic"} onChange={() => setEnforcement("diagnostic")} data-testid="visual-enf-diagnostic" />
              Report it, but let the run pass (a caveat you review)
            </label>
            <label className="flex items-center gap-2 text-xs">
              <input type="radio" name={`enf-${projectId}`} checked={enforcement === "required"} onChange={() => setEnforcement("required")} data-testid="visual-enf-required" />
              Fail the run, like a failing test
            </label>
          </fieldset>

          <div className="space-y-2">
            <span className="text-xs font-medium text-ink">Comparisons</span>
            {view.recent_captures?.length ? (
              <datalist id={`captures-${projectId}`}>{view.recent_captures.map((c) => <option key={c} value={c} />)}</datalist>
            ) : null}
            {rows.map((r, i) => (
              <div key={i} className="space-y-2 rounded border border-hairline p-2" data-testid={`visual-row-editor-${i}`}>
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="text-ink-muted">Screenshot</span>
                  <input
                    value={r.capture}
                    onChange={(e) => setRow(i, { capture: e.target.value })}
                    list={`captures-${projectId}`}
                    placeholder="scene-01.png"
                    data-testid={`visual-capture-${i}`}
                    className="w-40 rounded border border-hairline bg-surface2 px-1 py-0.5 font-mono"
                  />
                  <span className="text-ink-muted">may differ in up to</span>
                  <input
                    value={r.tolerancePct}
                    onChange={(e) => setRow(i, { tolerancePct: e.target.value })}
                    inputMode="decimal"
                    data-testid={`visual-tolerance-${i}`}
                    className="w-14 rounded border border-hairline bg-surface2 px-1 py-0.5 text-right"
                  />
                  <span className="text-ink-muted">% of its pixels</span>
                  <button type="button" onClick={() => setRows((cur) => cur.filter((_, j) => j !== i))} className="ml-auto text-ink-muted underline">remove</button>
                </div>
                <div className="flex flex-wrap items-start gap-2" role="radiogroup" aria-label="Reference image">
                  {view.references.map((ref) => (
                    <button
                      type="button"
                      key={ref.id}
                      role="radio"
                      aria-checked={r.reference === ref.id}
                      onClick={() => setRow(i, { reference: ref.id })}
                      data-testid={`visual-ref-${i}-${ref.id}`}
                      className={`w-24 rounded border p-1 text-left ${r.reference === ref.id ? "border-good ring-1 ring-good" : "border-hairline"}`}
                      title={`${ref.id} — ${ref.stored}`}
                    >
                      {thumbs[ref.id]
                        ? <img src={thumbs[ref.id]} alt={ref.id} className="h-16 w-full rounded object-contain" />
                        : <div className="h-16 w-full rounded bg-surface2" />}
                      <span className="mt-1 block truncate text-[10px] text-ink-muted">{ref.width && ref.height ? `${ref.width}×${ref.height}` : ref.id}</span>
                    </button>
                  ))}
                  {canChooseFile() && (
                    <button type="button" onClick={() => void importInto(i)} data-testid={`visual-import-${i}`} className="h-16 w-24 rounded border border-dashed border-hairline text-xs text-ink-muted">
                      Add image…
                    </button>
                  )}
                </div>
                {view.references.length === 0 && !canChooseFile() && (
                  <p className="text-xs text-ink-muted">No reference images yet: attach them to the requirements when you start or revise them.</p>
                )}
                {problems[i] && <p className="text-xs text-ink-muted">To save: {problems[i]}.</p>}
              </div>
            ))}
            <button
              type="button"
              onClick={() => setRows((cur) => [...cur, { capture: "", reference: view.references[0]?.id ?? "", tolerancePct: "2" }])}
              data-testid="visual-add"
              className="rounded border border-hairline px-2 py-0.5 text-xs"
            >
              Add a comparison
            </button>
          </div>

          <div className="flex items-center gap-2">
            <button type="button" disabled={!canSave} onClick={() => void save()} data-testid="visual-save" className="rounded border border-good px-3 py-1 text-xs text-good disabled:border-hairline disabled:text-ink-muted disabled:opacity-50">
              {busy ? "Saving…" : "Save"}
            </button>
            <button type="button" onClick={() => setEditing(false)} className="text-xs text-ink-muted underline">cancel</button>
          </div>
          {failure && <p className="text-xs text-critical" data-testid="visual-failure">{failure}</p>}
        </div>
      )}
    </div>
  );
}
