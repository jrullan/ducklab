import { useEffect, useState } from "react";
import type { EngineClient, Section, Task } from "../api/client";

type LedgerRow = { id: string; says: string; exists: string; since: string; detail: string };

/** The deterministic spine ledger: each engine finding gets both legal exits. */
export function Ledger({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [rows, setRows] = useState<LedgerRow[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let live = true;
    Promise.all([client.traceCheck(projectId), client.artifact(projectId, "requirements").catch(() => null), client.tasks(projectId).catch(() => [])])
      .then(([check, requirements, tasks]) => {
        if (!live) return;
        const sections = flatten(requirements?.sections ?? []);
        setRows(check.errors.map((error) => {
          const section = sections.find((item) => item.id === error.id);
          const task = (tasks as Task[]).find((item) => item.id === error.id);
          const says = section?.title ?? error.id;
          const exists = task ? `${task.id} — ${task.title}` : "Nothing linked yet";
          const since = task ? `task ${task.status}` : `trace check: ${error.kind.replaceAll("_", " ")}`;
          return { id: error.id, says, exists, since, detail: error.detail };
        }));
      })
      .catch(() => setRows([]))
      .finally(() => live && setLoading(false));
    return () => { live = false; };
  }, [client, projectId]);
  return <section data-testid="cycle-ledger" className="p-4">
    <a href="#/cycle" className="text-xs text-ink-muted underline">← Documents</a>
    <h1 className="mt-2 text-xl font-medium">Spine ledger</h1>
    <p className="mt-1 text-sm text-ink-secondary">Every break has two honest ways out: create the missing piece, which births a task; or mark it non-normative / amend the document flow.</p>
    {loading ? <p className="mt-4 text-sm text-ink-muted">Loading…</p> : rows.length === 0 ? <p data-testid="cycle-ledger-empty" className="mt-4 text-sm text-ink-secondary">No breaks in the spine.</p> : <div className="mt-4 overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm" data-testid="cycle-ledger-table"><thead><tr className="border-b border-hairline"><th className="p-2">What the document says</th><th className="p-2">What exists</th><th className="p-2">Since when</th><th className="p-2">Settle it</th></tr></thead><tbody>
        {rows.map((row) => <tr key={row.id} className="border-b border-hairline" data-testid="cycle-ledger-row"><td className="p-2"><span className="font-mono text-xs">{row.id}</span> {row.says}<div className="text-xs text-ink-muted">{row.detail}</div></td><td className="p-2">{row.exists}</td><td className="p-2 text-ink-secondary">{row.since}</td><td className="p-2"><div className="flex flex-wrap gap-2"><a className="rounded border border-hairline px-2 py-1 text-xs underline" href="#/board">Create missing piece → birth a task</a><a className="rounded border border-hairline px-2 py-1 text-xs underline" href="#/cycle">Mark non-normative or amend → document flow</a></div></td></tr>)}
      </tbody></table>
    </div>}
  </section>;
}

function flatten(sections: Section[]): Section[] { return sections.flatMap((section) => [section, ...flatten(section.children ?? [])]); }
