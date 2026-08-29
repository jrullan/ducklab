import { useEffect, useState } from "react";
import type { EngineClient, Section, Task } from "../api/client";
import { InspectorPane, PageHeader } from "../components/PageShell";

type LedgerRow = { id: string; says: string; exists: string; since: string; detail: string };

/** The deterministic spine ledger: each engine finding gets both legal exits. */
export function Ledger({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [rows, setRows] = useState<LedgerRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    Promise.all([client.traceCheck(projectId), client.artifact(projectId, "requirements").catch(() => null), client.tasks(projectId).catch(() => [])])
      .then(([check, requirements, tasks]) => {
        if (!live) return;
        const sections = flatten(requirements?.sections ?? []);
        const nextRows = check.errors.map((error) => {
          const section = sections.find((item) => item.id === error.id);
          const task = (tasks as Task[]).find((item) => item.id === error.id);
          const says = section?.title ?? error.id;
          const exists = task ? `${task.id} — ${task.title}` : "Nothing linked yet";
          const since = task ? `task ${task.status}` : `trace check: ${error.kind.replaceAll("_", " ")}`;
          return { id: error.id, says, exists, since, detail: error.detail };
        });
        setRows(nextRows);
        setSelected((current) => current ?? nextRows[0]?.id ?? null);
      })
      .catch(() => setRows([]))
      .finally(() => live && setLoading(false));
    return () => { live = false; };
  }, [client, projectId]);
  const current = rows.find((row) => row.id === selected) ?? null;
  return <section data-testid="cycle-ledger" className="space-y-4 p-4">
    <PageHeader eyebrow="Documents" title="Spine ledger" subtitle="Inspect every break between the project documents and the work that exists." actions={<a href="#/cycle" className="rounded border border-hairline px-3 py-1.5 text-sm text-ink">Back to Documents</a>} />
    {loading ? <p className="text-sm text-ink-muted">Loading…</p> : rows.length === 0 ? <p data-testid="cycle-ledger-empty" className="text-sm text-ink-secondary">No breaks in the spine.</p> : <div className="grid min-h-[28rem] grid-cols-1 overflow-hidden rounded-card border border-hairline bg-surface1 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="overflow-x-auto"><table className="w-full border-collapse text-left text-sm" data-testid="cycle-ledger-table"><thead><tr className="border-b border-hairline"><th className="p-2">What the document says</th><th className="p-2">What exists</th><th className="p-2">Since when</th><th className="p-2">Settle it</th></tr></thead><tbody>
        {rows.map((row) => <tr key={row.id} tabIndex={0} aria-selected={selected === row.id} onClick={() => setSelected(row.id)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") setSelected(row.id); }} className={`cursor-pointer border-b border-hairline ${selected === row.id ? "bg-surface2 shadow-[inset_3px_0_0_var(--accent)]" : "hover:bg-surface2"}`} data-testid="cycle-ledger-row"><td className="p-2"><span className="font-mono text-xs">{row.id}</span> {row.says}<div className="text-xs text-ink-muted">{row.detail}</div></td><td className="p-2">{row.exists}</td><td className="p-2 text-ink-secondary">{row.since}</td><td className="p-2"><div className="flex flex-wrap gap-2"><a className="rounded border border-hairline px-2 py-1 text-xs underline" href="#/board">Create missing piece → birth a task</a><a className="rounded border border-hairline px-2 py-1 text-xs underline" href="#/cycle">Mark non-normative or amend → document flow</a></div></td></tr>)}
      </tbody></table></div>
      <InspectorPane title="Break inspector" empty="Select a break to inspect its traceability and available exits.">
        {current ? <div className="mt-4 space-y-4"><div><div className="font-mono text-xs text-ink-muted">{current.id}</div><div className="mt-1 text-base font-medium text-ink">{current.says}</div><p className="mt-2 text-sm text-ink-secondary">{current.detail}</p></div><dl className="space-y-2 border-y border-hairline py-3 text-sm"><div><dt className="text-xs uppercase tracking-wide text-ink-muted">What exists</dt><dd className="mt-1 text-ink">{current.exists}</dd></div><div><dt className="text-xs uppercase tracking-wide text-ink-muted">Since</dt><dd className="mt-1 text-ink">{current.since}</dd></div></dl><div className="space-y-2"><a className="block rounded bg-ink px-3 py-2 text-center text-sm text-page" href="#/board">Create the missing work</a><a className="block rounded border border-hairline px-3 py-2 text-center text-sm text-ink" href="#/cycle">Amend the document flow</a></div></div> : undefined}
      </InspectorPane>
    </div>}
  </section>;
}

function flatten(sections: Section[]): Section[] { return sections.flatMap((section) => [section, ...flatten(section.children ?? [])]); }
