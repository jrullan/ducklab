/**
 * Now: the inbox. The first screen, because for one person it answers the only
 * question that matters on arrival — what needs me?
 *
 * The structural fact of solo use is that the human is the scarcest resource
 * in the system and the only one that cannot be parallelised. Runs take
 * minutes and run unattended. This screen is a query over state the store
 * already holds: decisions first, failures with their next step, live work
 * with live spend, and — when the queue is empty — what is ready to start,
 * because "nothing needs me" and "what should I do next" are the same moment.
 */
import { useEffect, useState } from "react";
import type { Bug, Duckling, EngineClient, Run, Task } from "../api/client";
import { useRuns, pendingForHuman } from "../store/runs";
import type { LiveSpend } from "../store/runs";
import { StatusChip } from "../components/StatusChip";
import { RunLauncher, type LaunchOpts, type ModeEstimates } from "../components/RunLauncher";
import { EmptyState } from "../components/EmptyState";
import { money, tokens, waitingFor } from "../lib/format";
import { runLabel } from "../lib/runview";
import { runStatusRole } from "../lib/colors";
import { routeHref } from "../app/routes";

export function Now({ client, projectId }: { client: EngineClient; projectId: string }) {
  const runs = useRuns((s) => s.runs);
  const spend = useRuns((s) => s.spend);
  const acceptState = useRuns((s) => s.acceptState);

  const [next, setNext] = useState<Task | null>(null);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [preferred, setPreferred] = useState<Record<string, string[]>>({});
  const [buildMode, setBuildMode] = useState("solo");
  const [estimates, setEstimates] = useState<ModeEstimates>({});
  const [started, setStarted] = useState<string | null>(null);
  // Reports whose fix landed. "Verified" is the one judgement a run must not
  // make for a person — the gate that passed may be a syntax check — but the
  // system never ASKED for it either: a bug reached fixed and sat there unless
  // the person remembered the bugs board existed. The question belongs in the
  // queue of questions.
  const [bugs, setBugs] = useState<Bug[]>([]);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    if (!projectId) return;
    client.taskNext(projectId).then(setNext).catch(() => setNext(null));
    client
      .bugs(projectId)
      .then((all) => setBugs(all))
      .catch(() => setBugs([]));
    client.ducklings().then(setFleet).catch(() => setFleet([]));
    client
      .modeDefaults()
      .then((d) => {
        setPreferred(d.ducklings ?? {});
        setBuildMode(d.build_mode || "solo");
      })
      .catch(() => setPreferred({}));
    void (async () => {
      try {
        const rep = await client.report(projectId, "mode");
        const est: ModeEstimates = {};
        for (const row of rep.rows) {
          est[row.key] = { usd: row.cost_usd, runs: row.runs };
        }
        setEstimates(est);
      } catch {
        setEstimates({});
      }
    })();
  }, [client, projectId, runs]);

  const list = Object.values(runs);
  const waiting = pendingForHuman(runs).sort((a, b) =>
    (a.pending_since ?? "").localeCompare(b.pending_since ?? ""),
  );
  const active = list.filter((r) => r.status === "running" || r.status === "queued");
  const failures = actionableFailures(list);

  const toVerify = bugs.filter((b) => b.status === "fixed");
  // A report sent back after its fix landed, with nothing running for it. The
  // person said "still broken", and then the system said nothing at all: the
  // verify card only exists at fixed, in_progress reads as "being worked on",
  // and nobody was working on it. The reopened state is a queue item — the
  // next act is a new run — or it is a silence shaped like progress.
  const inFlight = new Set(
    list
      .filter((r) => r.status === "running" || r.status === "queued" || r.status === "paused")
      .map((r) => r.task_id),
  );
  const reopened = bugs.filter(
    (b) => b.status === "in_progress" && !!b.task_id && !inFlight.has(b.task_id),
  );
  const lastModeFor = (taskID: string) =>
    list
      .filter((r) => r.task_id === taskID)
      .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""))[0]?.mode ?? "solo";

  const quiet =
    waiting.length === 0 && failures.length === 0 && toVerify.length === 0 && reopened.length === 0;

  const launch = async (opts: LaunchOpts) => {
    if (!next) return;
    setFailure(null);
    try {
      const run = await client.runStart(projectId, next.id, opts);
      setStarted(run.id);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="mx-auto max-w-3xl p-4" data-testid="now-view">
      {waiting.length > 0 && (
        <section data-testid="now-waiting">
          <h2 className="text-sm text-ink-muted">waiting for you</h2>
          <ul className="mt-2 space-y-2">
            {waiting.map((r) => (
              <WaitingCard
                key={r.id}
                run={r}
                accepting={acceptState[r.id]?.kind === "pending"}
                onAccept={() => {
                  const store = useRuns.getState();
                  store.beginAccept(r.id);
                  client
                    .accept(r.id)
                    .then((res) => store.confirmAccept(r.id, res.commit_sha))
                    .catch((e) =>
                      store.failAccept(r.id, e instanceof Error ? e.message : String(e)),
                    );
                }}
                onReject={() => void client.reject(r.id).catch(() => {})}
                acceptError={(() => {
                  const st = acceptState[r.id];
                  return st?.kind === "error" ? st.message : undefined;
                })()}
              />
            ))}
          </ul>
        </section>
      )}

      {toVerify.length > 0 && (
        <section className="mt-4" data-testid="now-verify">
          <h2 className="text-sm text-ink-muted">fixed — did it actually answer the report?</h2>
          <ul className="mt-2 space-y-2">
            {toVerify.map((b) => (
              <li key={b.id} data-testid="now-verify-card" className="rounded-card border border-hairline p-3">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="font-mono text-ink">{b.id}</span>
                  <span className="text-sm text-ink-secondary">{b.title}</span>
                  {b.task_id && <span className="text-xs text-ink-muted">fixed by {b.task_id}</span>}
                </div>
                <p className="mt-1 text-xs text-ink-muted">
                  Try what the report describes. The gate that passed may prove much less.
                </p>
                <div className="mt-2 flex items-center gap-2">
                  {(b.next ?? []).includes("verified") && (
                    <button
                      type="button"
                      data-testid="now-verify-yes"
                      onClick={() =>
                        void client
                          .moveBug(projectId, b.id, "verified")
                          .then(() => setBugs((cur) => cur.filter((x) => x.id !== b.id)))
                          .catch(() => {})
                      }
                      className="rounded border border-hairline px-2 py-1 text-xs"
                    >
                      Verified — it works
                    </button>
                  )}
                  {(b.next ?? []).includes("in_progress") && (
                    <button
                      type="button"
                      data-testid="now-verify-no"
                      onClick={() =>
                        void client
                          .moveBug(projectId, b.id, "in_progress")
                          .then(() =>
                            setBugs((cur) =>
                              cur.map((x) => (x.id === b.id ? { ...x, status: "in_progress" } : x)),
                            ),
                          )
                          .catch(() => {})
                      }
                      className="rounded border border-hairline px-2 py-1 text-xs"
                    >
                      Still broken
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {reopened.length > 0 && (
        <section className="mt-4" data-testid="now-reopened">
          <h2 className="text-sm text-ink-muted">still broken — nothing is running for these</h2>
          <ul className="mt-2 space-y-2">
            {reopened.map((b) => (
              <ReopenedCard
                key={b.id}
                bug={b}
                mode={lastModeFor(b.task_id!)}
                client={client}
                projectId={projectId}
              />
            ))}
          </ul>
        </section>
      )}

      {failures.length > 0 && (
        <section className="mt-4" data-testid="now-failures">
          <h2 className="text-sm text-ink-muted">failed, awaiting your call</h2>
          <ul className="mt-2 space-y-2">
            {failures.map((r) => (
              <li key={r.id} data-testid="now-failure" className="rounded-card border border-critical p-3">
                <div className="flex flex-wrap items-baseline gap-2">
                  <a href={routeHref({ name: "run", id: r.id })} className="text-ink underline">
                    {runLabel(r)}
                  </a>
                  <StatusChip role="critical" label={r.verdict === "ABORTED" ? "aborted" : "failed"} />
                  <span className="text-xs text-ink-muted">
                    {r.ended_at ? waitingFor(r.ended_at) + " ago" : ""}
                  </span>
                </div>
                {r.failure && (
                  <p className="mt-1 truncate text-sm text-ink-secondary" title={r.failure}>
                    {r.failure.split("\n")[0]}
                  </p>
                )}
                <p className="mt-1 text-xs text-ink-muted">
                  Open it to see what it did and run it again with changed settings.
                </p>
              </li>
            ))}
          </ul>
        </section>
      )}

      {active.length > 0 && (
        <section className="mt-4" data-testid="now-running">
          <h2 className="text-sm text-ink-muted">running</h2>
          <ul className="mt-2 space-y-1">
            {active.map((r) => (
              <RunningRow key={r.id} run={r} live={spend[r.id]} />
            ))}
          </ul>
        </section>
      )}

      {/* Overview's job, absorbed when it retired (docs/ux-evaluation.md
          phase 3): cost as ambient information rather than a report consulted
          after the money is gone. Spend used to be a prop there, and the one
          caller passed zero. */}
      <NowFooter runs={list} />

      {quiet && (
        <section className="mt-4" data-testid="now-quiet">
          {active.length === 0 && list.length === 0 && (
            <EmptyState message="No runs yet. Start below, or plan the work from Cycle." />
          )}
          <p className="text-sm text-ink-secondary">Nothing needs you.</p>
          {next ? (
            <div className="mt-2 rounded-card border border-hairline p-3" data-testid="now-next">
              <p className="text-sm text-ink">
                Ready to start: <span className="font-mono">{next.id}</span> — {next.title}
              </p>
              <div className="mt-2">
                <RunLauncher
                  key={buildMode}
                  ducklings={fleet}
                  initialMode={buildMode}
                  initialDucklings={preferred[buildMode] ?? []}
                  preferred={preferred}
                  estimates={estimates}
                  label={`Run ${next.id}`}
                  onLaunch={(opts) => void launch(opts)}
                />
              </div>
              {started && (
                <a
                  href={routeHref({ name: "run", id: started })}
                  data-testid="now-started"
                  className="text-xs text-ink underline"
                >
                  watch {started}
                </a>
              )}
              {failure && (
                <p className="text-xs text-critical" data-testid="now-launch-error">
                  {failure}
                </p>
              )}
            </div>
          ) : (
            list.length > 0 && (
              <p className="mt-1 text-xs text-ink-muted" data-testid="now-all-done">
                Nothing is ready to start either: everything is done, running, or waiting
                on something.
              </p>
            )
          )}
        </section>
      )}
    </div>
  );
}

/** Failures worth acting on: the LATEST run of its task or stage, still failed,
 * for work that was never subsequently accepted. An old failure whose task a
 * later run completed is history, and history lives in Records — showing it
 * here would offer redoing finished work. */
function actionableFailures(list: Run[]): Run[] {
  const latest = new Map<string, Run>();
  for (const r of list) {
    const key = r.task_id || r.stage || r.id;
    const cur = latest.get(key);
    if (!cur || (r.started_at ?? "") > (cur.started_at ?? "")) {
      latest.set(key, r);
    }
  }
  return [...latest.values()]
    .filter((r) => r.status === "failed")
    .sort((a, b) => (b.ended_at ?? "").localeCompare(a.ended_at ?? ""));
}

function WaitingCard({
  run,
  accepting,
  onAccept,
  onReject,
  acceptError,
}: {
  run: Run;
  accepting: boolean;
  onAccept: () => void;
  onReject: () => void;
  acceptError?: string;
}) {
  // From the engine's list, never this card's opinion of the state
  // (docs/ux-evaluation.md §5.4).
  const next = run.next ?? [];
  return (
    <li data-testid="now-waiting-card" className="rounded-card border border-serious p-3">
      <div className="flex flex-wrap items-baseline gap-2">
        <a href={routeHref({ name: "run", id: run.id })} className="text-ink underline">
          {runLabel(run)}
        </a>
        <span className="text-xs text-ink-secondary">{run.mode}</span>
        {run.verdict && <StatusChip role="warning" label={run.verdict.toLowerCase()} />}
        <span className="text-xs text-ink-muted">
          waiting {run.pending_since ? waitingFor(run.pending_since) : ""}
        </span>
        {run.budget && run.budget.usd > 0 && (
          <span className="ml-auto text-xs tabular-nums text-ink-secondary">
            {money(run.budget.usd)}
          </span>
        )}
      </div>
      <div className="mt-2 flex items-center gap-2">
        {/* The evidence — diff, transcript, gate output — is one click away on
            the label. AC-34 holds: nothing optimistic, the commit shows only
            when the engine confirms, which the run view handles. */}
        {next.includes("accept") && (
          <button
            type="button"
            data-testid="now-accept"
            onClick={onAccept}
            disabled={accepting}
            className="rounded border border-hairline px-2 py-1 text-xs disabled:opacity-40"
          >
            {accepting ? "Accepting…" : "Accept"}
          </button>
        )}
        {next.includes("reject") && (
          <button
            type="button"
            data-testid="now-reject"
            onClick={onReject}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Reject
          </button>
        )}
        {next.includes("answer") && (
          <a
            href={routeHref({ name: "run", id: run.id })}
            data-testid="now-answer"
            className="text-xs text-ink underline"
          >
            a duckling asked a question — answer it
          </a>
        )}
        {(next.includes("accept") || next.includes("resume")) && (
          <a
            href={routeHref({ name: "run", id: run.id })}
            className="text-xs text-ink-muted underline"
          >
            see the evidence
          </a>
        )}
      </div>
      {acceptError && (
        <p className="mt-1 text-xs text-critical" data-testid="now-accept-error">
          accept failed: {acceptError}
        </p>
      )}
    </li>
  );
}

/** A live run with its live spend: cost as ambient information, not a report
 * consulted after the money is gone. */
function RunningRow({ run, live }: { run: Run; live?: LiveSpend }) {
  return (
    <li data-testid="now-running-row" className="flex flex-wrap items-baseline gap-2 text-sm">
      <StatusChip role={runStatusRole(run.status)} label={run.status} />
      <a href={routeHref({ name: "run", id: run.id })} className="text-ink underline">
        {runLabel(run)}
      </a>
      <span className="text-xs text-ink-secondary">{run.mode}</span>
      {live && (
        <span className="text-xs tabular-nums text-ink-muted">
          {tokens(live.tokens)}
          {live.limit?.tokens ? ` / ${tokens(live.limit.tokens)}` : ""} · {money(live.usd)}
        </span>
      )}
    </li>
  );
}

function NowFooter({ runs }: { runs: Run[] }) {
  if (runs.length === 0) return null;
  // Today by the run's own start: one that began yesterday and finished this
  // morning was paid for yesterday.
  const today = new Date().toISOString().slice(0, 10);
  const spentToday = runs
    .filter((r) => (r.started_at ?? "").slice(0, 10) === today)
    .reduce((sum, r) => sum + (r.budget?.usd ?? 0), 0);
  const spentAll = runs.reduce((sum, r) => sum + (r.budget?.usd ?? 0), 0);
  const finished = runs.filter((r) => r.verdict !== "").length;
  const passed = runs.filter((r) => r.verdict === "PASSED").length;
  return (
    <p className="mt-4 border-t border-hairline pt-2 text-xs text-ink-muted" data-testid="now-footer">
      today {money(spentToday)} · all time {money(spentAll)}
      {finished > 0 && (
        <>
          {" "}
          · {passed}/{finished} passed
        </>
      )}
    </p>
  );
}

/** A reopened report, and the one act that moves it: new work. Launched with
 * the task's own last mode; the engine fills the mode's saved line-up, the
 * same as any launch that names no ducklings. */
function ReopenedCard({
  bug,
  mode,
  client,
  projectId,
}: {
  bug: Bug;
  mode: string;
  client: EngineClient;
  projectId: string;
}) {
  const [started, setStarted] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  return (
    <li data-testid="now-reopened-card" className="rounded-card border border-serious p-3">
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="font-mono text-ink">{bug.id}</span>
        <span className="text-sm text-ink-secondary">{bug.title}</span>
        <span className="text-xs text-ink-muted">
          {bug.task_id}&apos;s fix was accepted, and you sent the report back
        </span>
      </div>
      <div className="mt-2 flex items-center gap-2">
        <button
          type="button"
          data-testid="now-reopened-run"
          onClick={() =>
            void client
              .runStart(projectId, bug.task_id!, { mode })
              .then((r) => setStarted(r.id))
              .catch((e) => setFailure(e instanceof Error ? e.message : String(e)))
          }
          className="rounded border border-hairline px-2 py-1 text-xs"
        >
          Run {bug.task_id} again ({mode})
        </button>
        {started && (
          <a href={`#/runs/${started}`} data-testid="now-reopened-watch" className="text-xs text-ink underline">
            watch {started}
          </a>
        )}
        {failure && <span className="text-xs text-critical">{failure}</span>}
      </div>
    </li>
  );
}
