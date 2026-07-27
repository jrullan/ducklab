import { useRuns, pendingForHuman } from "../store/runs";
import { StatTile } from "../components/StatTile";
import { StatusChip } from "../components/StatusChip";
import { EmptyState } from "../components/EmptyState";
import { money, waitingFor } from "../lib/format";
import { runStatusRole } from "../lib/colors";
import { routeHref } from "../app/routes";
import { runLabel } from "../lib/runview";

/** The cockpit: what is running, what is waiting, what it has cost. */
export function Overview({ spentToday, budget }: { spentToday: number; budget: number }) {
  const runs = useRuns((s) => s.runs);
  const list = Object.values(runs);
  const active = list.filter((r) => r.status === "running" || r.status === "queued");
  const waiting = pendingForHuman(runs);
  const passed = list.filter((r) => r.verdict === "PASSED").length;
  const finished = list.filter((r) => r.verdict !== "").length;

  return (
    <div className="p-4" data-testid="overview">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile label="Active runs" value={String(active.length)} sub={`${list.length} total`} />
        <StatTile
          label="Passed"
          value={finished === 0 ? "—" : `${passed} / ${finished}`}
          sub={finished === 0 ? "no finished runs yet" : "of finished runs"}
        />
        <StatTile label="Spend today" value={money(spentToday)} sub={`budget ${money(budget)}`} />
        <StatTile
          label="Waiting for you"
          value={String(waiting.length)}
          sub={waiting.length === 0 ? "nothing blocked" : "needs an answer"}
        />
      </div>

      {waiting.length > 0 && (
        <section className="mt-4" data-testid="human-gate-inbox">
          <h2 className="text-sm text-ink-muted">waiting for you</h2>
          <ul>
            {waiting.map((r) => (
              <li key={r.id} className="flex items-center gap-3 py-1">
                <StatusChip role="serious" label={r.pending_kind ?? "waiting"} />
                <a href={routeHref({ name: "run", id: r.id })} className="text-ink-secondary underline">
                  {runLabel(r)}
                </a>
                <span className="text-ink-muted">{waitingFor(r.pending_since ?? "")}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="mt-4">
        <h2 className="text-sm text-ink-muted">recent runs</h2>
        {list.length === 0 ? (
          <EmptyState message="No runs yet. Pick a task and press Run." />
        ) : (
          <ul>
            {list.map((r) => (
              <li key={r.id} className="flex items-center gap-3 py-1" data-testid="run-row">
                <StatusChip role={runStatusRole(r.status)} label={r.status} />
                <a href={routeHref({ name: "run", id: r.id })} className="text-ink underline">
                  {runLabel(r)}
                </a>
                <span className="text-ink-secondary">{r.mode}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
