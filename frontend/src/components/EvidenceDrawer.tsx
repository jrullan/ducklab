import { useEffect, useState } from "react";
import { runCaptureUrl, type Run, type Artifact } from "../api/client";
import { money, moneyOrZero } from "../lib/format";

type Evidence = Record<string, unknown>;

function value(data: Evidence, ...keys: string[]): unknown {
  for (const key of keys) if (data[key] !== undefined && data[key] !== null) return data[key];
  return undefined;
}

function text(v: unknown, fallback: string): string {
  return typeof v === "string" && v.trim() ? v : fallback;
}

function filesFrom(data: Evidence): { name: string; summary: string }[] {
  const raw = value(data, "files", "changed_files", "diff_files");
  if (Array.isArray(raw)) return raw.map((file) => {
    if (typeof file === "string") return { name: file, summary: "changed" };
    const item = (file ?? {}) as Evidence;
    return { name: text(value(item, "name", "path", "file"), "unknown file"), summary: text(value(item, "summary", "description", "change"), "changed") };
  });
  return [];
}

function netLines(data: Evidence): string {
  const additions = Number(value(data, "additions", "insertions") ?? 0);
  const deletions = Number(value(data, "deletions") ?? 0);
  if (additions || deletions) return `+${additions} −${deletions}`;
  const diff = value(data, "diff", "patch");
  if (typeof diff === "string") {
    const plus = diff.split("\n").filter((line) => line.startsWith("+") && !line.startsWith("+++" )).length;
    const minus = diff.split("\n").filter((line) => line.startsWith("-") && !line.startsWith("---" )).length;
    return `+${plus} −${minus}`;
  }
  return "not recorded";
}

export function EvidenceDrawer({ run, plan, open = true, onClose, captureClient }: { run?: Run; plan?: NonNullable<Artifact["proposal"]>; open?: boolean; onClose: () => void; captureClient?: { runCaptureUrl: (runId: string, name: string) => Promise<string> } }) {
  if (!open) return null;
  if (plan) return <PlanEvidenceDrawer plan={plan} onClose={onClose} />;
  if (!run) return null;
  const [captureURLs, setCaptureURLs] = useState<Record<string, string>>({});
  useEffect(() => {
    let alive = true;
    const urls: string[] = [];
    for (const name of run.captures ?? []) {
      const promise = captureClient
        ? captureClient.runCaptureUrl(run.id, name)
        : runCaptureUrl(window.ducklab?.baseUrl ?? "", run.id, name, window.ducklab?.token ?? "");
      void promise.then((url) => {
        if (!alive) { URL.revokeObjectURL?.(url); return; }
        urls.push(url);
        setCaptureURLs((old) => ({ ...old, [name]: url }));
      });
    }
    return () => { alive = false; for (const url of urls) URL.revokeObjectURL?.(url); };
  }, [run.id, run.captures, captureClient]);
  const data = (run.pending_data ?? {}) as Evidence;
  const testStatus = text(value(data, "tests", "test_summary", "test_status"), run.verdict === "UNVERIFIED" ? "unverified" : run.verdict.toLowerCase());
  const testNote = text(value(data, "test_note", "tests_note", "test_message"), run.warning ?? "No further test note recorded.");
  const verdict = text(value(data, "reviewer_verdict", "review_verdict", "reviewer"), run.verdict ? `The reviewer marked this run ${run.verdict.toLowerCase()}.` : "No reviewer verdict recorded.");
  const spent = run.budget?.usd ?? 0;
  const ceiling = run.budget?.limit?.usd;
  const freshness = value(data, "evidence_freshness", "tests_on_final_revision", "tests_final_revision");
  const freshnessLine = typeof freshness === "boolean"
    ? (freshness ? "Tests ran on the final revision." : "Tests did not run on the final revision.")
    : typeof freshness === "string" ? freshness : "Evidence freshness was not recorded.";
  const files = filesFrom(data);
  const captures = run.captures ?? [];

  return (
    <div className="fixed inset-0 z-40" role="presentation" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <aside className="absolute right-0 top-0 h-full w-full max-w-xl overflow-y-auto border-l border-hairline bg-page p-5 shadow-lg" role="dialog" aria-modal="true" aria-label="Evidence">
        <div className="flex items-start justify-between gap-3">
          <div><p className="text-xs text-ink-muted">Evidence for</p><h2 className="text-lg font-medium text-ink">{run.task_id}</h2></div>
          <button type="button" aria-label="Close evidence" onClick={onClose} className="rounded border border-hairline px-2 py-1 text-sm">Close</button>
        </div>
        <p className="mt-4 text-sm text-ink-secondary">{text(run.subject, "This run is waiting for your decision.")}</p>
        {captures.length > 0 && <section className="mt-5" aria-label="How it looks"><h3 className="text-sm font-medium text-ink">How it looks</h3><div className="mt-2 flex gap-3 overflow-x-auto" data-testid="render-captures">{captures.map((capture) => <img key={capture} src={captureURLs[capture]} alt={capture} className="h-32 w-auto rounded border border-hairline object-cover" />)}</div></section>}
        <div className="mt-4 grid gap-2 sm:grid-cols-3" data-testid="evidence-tiles">
          <div className="rounded border border-hairline p-3"><p className="text-xs text-ink-muted">Tests</p><p className="mt-1 font-medium text-ink">{testStatus}</p><p className="mt-1 text-xs text-ink-secondary">{testNote}</p></div>
          <div className="rounded border border-hairline p-3"><p className="text-xs text-ink-muted">Reviewer verdict</p><p className="mt-1 text-sm text-ink">“{verdict.replace(/^“|”$/g, "") }”</p></div>
          <div className="rounded border border-hairline p-3"><p className="text-xs text-ink-muted">Cost so far</p><p className="mt-1 font-medium text-ink">{moneyOrZero(spent)}{ceiling !== undefined ? ` / ${money(ceiling)}` : ""}</p></div>
        </div>
        <p className="mt-3 text-xs text-ink-secondary" data-testid="evidence-freshness">{freshnessLine}</p>
        <section className="mt-5"><h3 className="text-sm font-medium text-ink">Summary of changes</h3>{files.length ? <ul className="mt-2 space-y-2 text-sm">{files.map((file) => <li key={file.name} className="flex justify-between gap-3"><code className="text-xs">{file.name}</code><span className="text-right text-ink-secondary">{file.summary}</span></li>)}</ul> : <p className="mt-2 text-sm text-ink-secondary">No file summary recorded.</p>}<p className="mt-3 text-sm text-ink-secondary">Net lines: {netLines(data)}</p></section>
        <details className="mt-5 border-t border-hairline pt-3"><summary className="cursor-pointer text-sm font-medium text-ink">Cost breakdown per seat</summary><div className="mt-2 space-y-1 text-sm">{run.spend ? Object.entries(run.spend).map(([seat, spend]) => <p key={seat} className="flex justify-between"><span>{seat}</span><span>{money(spend.cost_usd)} · {spend.calls} calls</span></p>) : <p className="text-ink-secondary">No seat breakdown recorded.</p>}</div></details>
        <details className="mt-3 border-t border-hairline pt-3"><summary className="cursor-pointer text-sm font-medium text-ink">Raw logs</summary><pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap text-xs text-ink-secondary">{text(value(data, "logs", "raw_logs", "log"), "No raw logs recorded.")}</pre></details>
      </aside>
    </div>
  );
}

function PlanEvidenceDrawer({ plan, onClose }: { plan: NonNullable<Artifact["proposal"]>; onClose: () => void }) {
  const sections = plan.sections ?? [];
  return <div className="fixed inset-0 z-40" role="presentation" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
    <aside className="absolute right-0 top-0 h-full w-full max-w-xl overflow-y-auto border-l border-hairline bg-page p-5 shadow-lg" role="dialog" aria-modal="true" aria-label="Plan evidence">
      <div className="flex items-start justify-between gap-3"><div><p className="text-xs text-ink-muted">Evidence for</p><h2 className="text-lg font-medium text-ink">Plan</h2></div><button type="button" aria-label="Close evidence" onClick={onClose} className="rounded border border-hairline px-2 py-1 text-sm">Close</button></div>
      <p className="mt-4 text-sm text-ink" data-testid="plan-drawer-meaning">you approve these tasks being born and their lanes — you are not approving code yet</p>
      <p className="mt-4 text-sm text-ink-secondary">{sections.length} proposed plan sections. Review the scope and ownership before approving.</p>
    </aside>
  </div>;
}

export function EvidenceDrawerHost({ run, open, onClose, captureClient }: { run: Run; open: boolean; onClose: () => void; captureClient?: { runCaptureUrl: (runId: string, name: string) => Promise<string> } }) {
  useEffect(() => { if (!open) return; const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); }; window.addEventListener("keydown", onKey); return () => window.removeEventListener("keydown", onKey); }, [open, onClose]);
  return open ? <EvidenceDrawer run={run} captureClient={captureClient} onClose={onClose} /> : null;
}
