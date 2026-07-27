/**
 * The Board: tasks in columns, with a rail on selection (08 §4.3).
 *
 * There is no drag-to-move. A task's status is derived from its run records,
 * not stored on the task, so there is nothing for a drop to write — and that
 * derivation is deliberate: status kept in the plan document would let a model
 * rewriting the plan mark its own work accepted (I2). Moving a card means
 * running the task, so `Run` is the action the rail offers.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import type { EngineClient, Task } from "../api/client";
import { EmptyState } from "../components/EmptyState";

const COLUMNS = [
  { key: "todo", label: "Todo" },
  { key: "in_progress", label: "In progress" },
  { key: "blocked", label: "Blocked" },
  { key: "review", label: "Review" },
  { key: "accepted", label: "Accepted" },
] as const;

export function Board({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [milestone, setMilestone] = useState("");
  const [query, setQuery] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      setTasks(await client.tasks(projectId));
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [client, projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  const milestones = useMemo(
    () => [...new Set(tasks.map((t) => t.milestone).filter(Boolean))].sort(),
    [tasks],
  );

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return tasks.filter(
      (t) =>
        (!milestone || t.milestone === milestone) &&
        (!q || t.id.toLowerCase().includes(q) || t.title.toLowerCase().includes(q)),
    );
  }, [tasks, milestone, query]);

  const current = tasks.find((t) => t.id === selected) ?? null;

  if (!loading && tasks.length === 0 && !failure) {
    return <EmptyState message="No tasks yet — run `ducklab plan` to break the spec into tasks." />;
  }

  return (
    <div data-testid="board-view" className="flex gap-4">
      <div className="min-w-0 flex-1">
        {failure && (
          <div data-testid="board-error" className="mb-3 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* Filters in one row above the board (08 §4.3). */}
        <div className="mb-3 flex items-center gap-2">
          <select
            data-testid="board-milestone"
            className="rounded border border-hairline bg-page px-2 py-1 text-sm text-ink"
            value={milestone}
            onChange={(e) => setMilestone(e.target.value)}
          >
            <option value="">All milestones</option>
            {milestones.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          <input
            data-testid="board-search"
            className="rounded border border-hairline bg-page px-2 py-1 text-sm text-ink"
            placeholder="Filter tasks"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <span className="text-sm text-ink-muted">
            {shown.length} of {tasks.length}
          </span>
        </div>

        <div className="grid grid-cols-5 gap-2">
          {COLUMNS.map((col) => {
            const items = shown.filter((t) => t.status === col.key);
            return (
              <section key={col.key} data-testid={`board-col-${col.key}`}>
                <h2 className="mb-2 text-sm text-ink-muted">
                  {col.label}
                  <span className="ml-1 text-ink-muted">{items.length}</span>
                </h2>
                <ul className="space-y-2">
                  {items.map((t) => (
                    <li key={t.id}>
                      <button
                        data-testid="board-card"
                        data-task={t.id}
                        onClick={() => setSelected(t.id)}
                        aria-pressed={selected === t.id}
                        className={
                          "w-full rounded-card border p-2 text-left " +
                          (selected === t.id ? "border-serious" : "border-hairline")
                        }
                      >
                        <div className="font-mono text-xs text-ink-muted">{t.id}</div>
                        <div className="text-sm text-ink">{t.title}</div>
                        {t.complexity && (
                          <div className="mt-1 text-xs text-ink-muted">{t.complexity}</div>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      </div>

      <aside data-testid="board-rail" className="w-72 shrink-0">
        {current ? (
          <div className="space-y-2">
            <div className="font-mono text-xs text-ink-muted">{current.id}</div>
            <div className="text-sm font-medium text-ink">{current.title}</div>
            <dl className="space-y-1 text-xs text-ink-muted">
              <Row label="status" value={current.status.replace("_", " ")} />
              <Row label="milestone" value={current.milestone} />
              <Row label="implements" value={current.implements?.join(", ")} />
              <Row label="depends on" value={current.depends_on?.join(", ")} />
            </dl>
            {current.body && (
              <p className="whitespace-pre-wrap text-sm text-ink-secondary">{current.body}</p>
            )}
            {/* The command, not a button: starting a run is the engine's job and
                the desktop has no run-start path for tasks yet. Showing a dead
                button would be worse than showing the thing that works. */}
            <div className="rounded border border-hairline p-2 font-mono text-xs text-ink-secondary">
              ducklab run {current.id} --mode pair
            </div>
          </div>
        ) : (
          <p className="text-sm text-ink-muted">Select a task to see its record.</p>
        )}
      </aside>
    </div>
  );
}

function Row({ label, value }: { label: string; value?: string }) {
  if (!value) return null;
  return (
    <div className="flex gap-2">
      <dt className="w-20 shrink-0">{label}</dt>
      <dd className="text-ink-secondary">{value}</dd>
    </div>
  );
}
