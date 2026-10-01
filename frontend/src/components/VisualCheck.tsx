/**
 * The visual gate's result (B-460): each capture beside the reference it was
 * held against and the difference between them, with the measure and the
 * allowance in plain words. The same component serves the run view and the
 * evidence drawer, so a person deciding sees exactly what the gate saw.
 */

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { runCaptureUrl, type Run, type VisualCompare } from "../api/client";

type CaptureClient = { runCaptureUrl: (runId: string, name: string) => Promise<string> };

const pct = (v: number) => `${(v * 100).toFixed(1)}%`;

export function VisualCheck({ run, captureClient }: { run: Run; captureClient?: CaptureClient }) {
  const gate = run.visual;
  const [urls, setUrls] = useState<Record<string, string>>({});
  const [large, setLarge] = useState<VisualCompare | null>(null);
  useEffect(() => {
    if (!large) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setLarge(null); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [large]);
  useEffect(() => {
    if (!gate) return;
    let alive = true;
    const made: string[] = [];
    const names = gate.results.flatMap((r) => [r.capture, r.reference_capture, r.diff_capture]).filter((n): n is string => !!n);
    for (const name of new Set(names)) {
      const promise = captureClient
        ? captureClient.runCaptureUrl(run.id, name)
        : runCaptureUrl(window.ducklab?.baseUrl ?? "", run.id, name, window.ducklab?.token ?? "");
      void promise.then((url) => {
        if (!alive) { URL.revokeObjectURL?.(url); return; }
        made.push(url);
        setUrls((old) => ({ ...old, [name]: url }));
      }).catch(() => {});
    }
    return () => { alive = false; for (const url of made) URL.revokeObjectURL?.(url); };
  }, [run.id, gate, captureClient]);
  if (!gate || gate.results.length === 0) return null;

  const required = gate.enforcement === "required";
  const tone = gate.passed ? "border-good" : required ? "border-critical" : "border-warn";
  const title = gate.passed
    ? "Looks like the reference"
    : required ? "Does not look like the reference — the run fails" : "Does not look like the reference (caveat)";
  return (
    <section className={`rounded-card border p-3 ${tone}`} data-testid="visual-check" aria-label="Visual check">
      <h2 className={`text-sm font-medium ${gate.passed ? "text-good" : required ? "text-critical" : "text-warn"}`}>{title}</h2>
      <p className="mt-1 text-xs text-ink-secondary">
        Each capture is compared pixel by pixel with its reference image. {required
          ? "This check is required: a mismatch fails the run like a failing test."
          : "This check is diagnostic: a mismatch is reported but does not fail the run."}
      </p>
      {gate.results.map((r) => <VisualRow key={`${r.capture}:${r.reference}`} r={r} urls={urls} onLarge={() => setLarge(r)} />)}
      {large && createPortal(
        <div className="fixed inset-0 z-[100] overflow-auto bg-page p-6" role="dialog" aria-modal="true" aria-label="Visual comparison" data-testid="visual-large">
          <div className="flex items-start justify-between gap-3">
            <p className="text-sm text-ink">
              <code>{large.capture}</code> against <code>{large.reference}</code> — {pct(large.mismatch)} of the pixels differ (allowed {pct(large.tolerance)})
            </p>
            <button type="button" onClick={() => setLarge(null)} className="rounded border border-hairline px-2 py-1 text-sm" data-testid="visual-large-close">Close</button>
          </div>
          <div className="mt-4 grid grid-cols-3 gap-4">
            {([["Reference", large.reference_capture], ["Capture", large.capture], ["Difference (red)", large.diff_capture]] as const).map(([label, name]) => (
              <figure key={label} className="min-w-0">
                <figcaption className="mb-1 text-xs text-ink-muted">{label}</figcaption>
                {name && urls[name] ? <img src={urls[name]} alt={`${label}: ${name}`} className="max-h-[80vh] w-full rounded border border-hairline object-contain" /> : <div className="h-40 rounded border border-dashed border-hairline" />}
              </figure>
            ))}
          </div>
        </div>,
        document.body,
      )}
    </section>
  );
}

function VisualRow({ r, urls, onLarge }: { r: VisualCompare; urls: Record<string, string>; onLarge: () => void }) {
  const frame = (label: string, name: string | undefined, testid: string) => (
    <figure className="min-w-0 flex-1">
      <figcaption className="mb-1 text-xs text-ink-muted">{label}</figcaption>
      {name && urls[name]
        ? <img src={urls[name]} alt={`${label}: ${name}`} data-testid={testid} className="w-full rounded border border-hairline bg-surface2 object-contain" />
        : <div className="flex h-24 items-center justify-center rounded border border-dashed border-hairline text-xs text-ink-muted">{name ? "loading…" : "not available"}</div>}
    </figure>
  );
  return (
    <div className="mt-3 border-t border-hairline pt-3" data-testid={`visual-row-${r.capture}`}>
      <p className="text-sm text-ink">
        <code className="text-xs">{r.capture}</code> against <code className="text-xs">{r.reference}</code>
        {r.error
          ? <span className="text-critical"> — not compared: {r.error}</span>
          : <span className={r.passed ? "text-good" : "text-critical"} data-testid="visual-measure">
              {" "}— {pct(r.mismatch)} of the pixels differ (allowed {pct(r.tolerance)})
            </span>}
      </p>
      {r.scaled_from && !r.error && (
        <p className="mt-1 text-xs text-ink-muted">
          The reference is {r.scaled_from} px and was scaled to the capture&apos;s {r.width}x{r.height} px to compare.
        </p>
      )}
      {!r.error && (
        <div className="mt-2 flex gap-2">
          {frame("Reference", r.reference_capture, "visual-reference")}
          {frame("Capture", r.capture, "visual-capture")}
          {frame("Difference (red)", r.diff_capture, "visual-diff")}
        </div>
      )}
      {!r.error && (
        <button type="button" onClick={onLarge} className="mt-2 text-xs text-ink-muted underline" data-testid="visual-view-large">
          View large
        </button>
      )}
    </div>
  );
}
