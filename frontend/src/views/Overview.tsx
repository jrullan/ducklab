import { useRuns, pendingForHuman } from "../store/runs";
import { StatTile } from "../components/StatTile";
import { StatusChip } from "../components/StatusChip";
import { EmptyState } from "../components/EmptyState";
import { money, waitingFor } from "../lib/format";
import { runStatusRole } from "../lib/colors";
import { routeHref } from "../app/routes";
import { runLabel } from "../lib/runview";

/**
 * The cockpit: what is running, what is waiting, what it has cost.
 *
 * Spend is computed here rather than passed in. It used to be a prop, and the
 * one caller passed `spentToday={0} budget={2}` — so the screen whose job is to
 * say what the work has cost reported zero while runs had spent real money, one
 * of them $1.50 on its own. A component that cannot be handed a number cannot
 * be handed a wrong one.
 */
export function Overview() {
  const runs = useRuns((s) => s.runs);
  const list = Object.values(runs);
  // Today's, by the run's own start. A run that began yesterday and finished
  // this morning was paid for yesterday.
  const today = new Date().toISOString().slice(0, 10);
  const startedToday = list.filter((r) => (r.started_at ?? "").slice(0, 10) === today);
  const spentToday = startedToday.reduce((sum, r) => sum + (r.budget?.usd ?? 0), 0);
  const spentAll = list.reduce((sum, r) => sum + (r.budget?.usd ?? 0), 0);
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
        {/* No budget to compare against: there is no daily ceiling in the
            engine, and the $2 this used to show was a per-run limit dressed up
            as a daily one. */}
        <StatTile
          label="Spend today"
          value={money(spentToday)}
          sub={`${money(spentAll)} all time · ${startedToday.length} run${startedToday.length === 1 ? "" : "s"}`}
        />
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
