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
import { useEffect, useRef, useState } from "react";
import type { Bug, Duckling, EngineClient, NextStep, Run, Task, RosterEntry, TraceError } from "../api/client";
import { useRuns, pendingForHuman } from "../store/runs";
import type { LiveSpend } from "../store/runs";
import { StatusChip } from "../components/StatusChip";
import { WaitingCard } from "../components/WaitingCard";
import { PlanCard } from "../components/PlanCard";
import { pickedSeats, RunLauncher, type LaunchOpts, type ModeEstimates, type PhaseConfig } from "../components/RunLauncher";
import { TddLaunch } from "../components/TddLaunch";
import { EmptyState } from "../components/EmptyState";
import { VerificationLedger } from "../components/VerificationLedger";
import { NextStepCards } from "../components/NextStepCards";
import { duration, moneyOrZero, tokens, waitingFor } from "../lib/format";
import { runLabel } from "../lib/runview";
import { runStatusRole } from "../lib/colors";
import { routeHref } from "../app/routes";
import { ContextStrip, PageHeader } from "../components/PageShell";


export function Now({ client, projectId }: { client: EngineClient; projectId: string }) {
  const runs = useRuns((s) => s.runs);
  const spend = useRuns((s) => s.spend);
  const acceptState = useRuns((s) => s.acceptState);

  const [next, setNext] = useState<Task | null>(null);
  const [nextSteps, setNextSteps] = useState<NextStep[]>([]);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [roster, setRoster] = useState<RosterEntry[]>([]);
  const [testRoster, setTestRoster] = useState<RosterEntry[]>([]);
  const [preferred, setPreferred] = useState<Record<string, string[]>>({});
  const [buildMode, setBuildMode] = useState("solo");
  const [testMode, setTestMode] = useState("solo");
  const [estimates, setEstimates] = useState<ModeEstimates>({});
  const [started, setStarted] = useState<string | null>(null);
  // Reports whose fix landed. "Verified" is the one judgement a run must not
  // make for a person — the gate that passed may be a syntax check — but the
  // system never ASKED for it either: a bug reached fixed and sat there unless
  // the person remembered the bugs board existed. The question belongs in the
  // queue of questions.
  const [bugs, setBugs] = useState<Bug[]>([]);
  const [failure, setFailure] = useState<string | null>(null);
  const [plan, setPlan] = useState<import("../api/client").Artifact | null>(null);
  const [planTrace, setPlanTrace] = useState<TraceError[]>([]);
  // Runs can change several times while an artifact request is in flight
  // (notably accept-plan followed immediately by start-task). Do not let the
  // older response put a consumed proposal back into Now.
  const planRequest = useRef(0);

  useEffect(() => {
    if (!projectId) return;
    client.taskNext(projectId).then(setNext).catch(() => setNext(null));
    if (typeof client.artifact === "function" && typeof client.traceCheck === "function") {
      const request = ++planRequest.current;
      Promise.all([client.artifact(projectId, "plan"), client.traceCheck(projectId)])
        .then(([artifact, trace]) => {
          if (request !== planRequest.current) return;
          setPlan(artifact.proposal ? artifact : null);
          setPlanTrace(trace.errors ?? []);
        })
        .catch(() => {
          if (request !== planRequest.current) return;
          setPlan(null);
          setPlanTrace([]);
        });
    }
    client.projectNext(projectId).then(setNextSteps).catch(() => setNextSteps([]));
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
        setTestMode(d.test_mode || "solo");
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
  // Conversations are records, not inbox work, once they have ended for any
  // reason. Keep terminal chats out of the source list so no classification
  // (including a future one) can accidentally put a dead conversation in Now.
  const nowList = list.filter((r) => !isTerminalChat(r));
  const waiting = pendingForHuman(Object.fromEntries(nowList.map((r) => [r.id, r]))).sort((a, b) =>
    (a.pending_since ?? "").localeCompare(b.pending_since ?? ""),
  );
  const active = nowList.filter((r) => r.status === "running" || r.status === "queued");
  const failures = actionableFailures(nowList);

  // A pending run owns its decision. The guide may describe the same run and
  // the artifact endpoint may expose the same proposal; neither is a second
  // decision. Keep the standalone plan card only for an orphan proposal whose
  // producing run is no longer waiting.
  const waitingIDs = new Set(waiting.map((run) => run.id));
  const planRunID = plan?.proposal?.run_id ?? plan?.run_id;
  const standalonePlan = !!plan?.proposal && (!planRunID || !waitingIDs.has(planRunID));
  const actionSteps = nextSteps.filter((step) => {
    // taskNext owns the one-click launcher below. ProjectNext describes the
    // same task as navigation; rendering both produced two doors, two cost
    // figures and no clear primary action.
    if (step.kind === "task" && next && step.ref === next.id) return false;
    if (step.kind !== "run" || !step.ref) return true;
    if (waitingIDs.has(step.ref)) return false;
    // Conversations remain reachable from Runs. They are not decisions and a
    // five-hour-old reply invitation must not masquerade as current work.
    return runs[step.ref]?.stage !== "chat";
  });

  const toVerify = bugs.filter((b) => b.status === "fixed");
  // A report sent back after its fix landed, with nothing running for it. The
  // person said "still broken", and then the system said nothing at all: the
  // verify card only exists at fixed, in_progress reads as "being worked on",
  // and nobody was working on it. The reopened state is a queue item — the
  // next act is a new run — or it is a silence shaped like progress.
  const inFlight = new Set(
    nowList
      .filter((r) => r.status === "running" || r.status === "queued" || r.status === "paused")
      .map((r) => r.task_id),
  );
  const reopened = bugs.filter(
    (b) => b.status === "in_progress" && !!b.task_id && !inFlight.has(b.task_id),
  );
  const lastModeFor = (taskID: string) =>
    nowList
      .filter((r) => r.task_id === taskID)
      .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""))[0]?.mode ?? "solo";

  const quiet =
    waiting.length === 0 && !standalonePlan && failures.length === 0 && toVerify.length === 0 && reopened.length === 0;
  const waitingCount = waiting.length + (standalonePlan ? 1 : 0);
  const attentionCount = waitingCount + toVerify.length + failures.length + reopened.length;

  // The launcher seeds its seats from the PROJECT's resolved roster for the
  // mode it will run, never from the global saved line-up: a global default
  // seeded as a pick travelled as an explicit request and overrode the
  // project's pins (B-394). Each phase resolves its own mode.
  useEffect(() => {
    if (typeof client.roster !== "function") return;
    let live = true;
    client.roster(projectId, buildMode).then((r) => { if (live) setRoster(r.entries); }).catch(() => { if (live) setRoster([]); });
    client.roster(projectId, testMode).then((r) => { if (live) setTestRoster(r.entries); }).catch(() => { if (live) setTestRoster([]); });
    return () => { live = false; };
  }, [client, projectId, buildMode, testMode]);

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

  // The suggested task deserves the same front door the board gives it: the
  // chain, not a bare Run. Same component, same defaults, same one click.
  const launchTdd = async (test: PhaseConfig, build: PhaseConfig) => {
    if (!next) return;
    setFailure(null);
    try {
      // Only hand-made picks travel, keyed by role; the roster seats the
      // launcher shows stay out of the request so the engine resolves them
      // (the same contract the Board rail uses; B-394).
      const run = await client.testStart(projectId, next.id, "", {
        thenBuild: true,
        testMode: test.mode,
        testDucklings: [],
        testSeats: pickedSeats(test.mode, test),
        mode: build.mode,
        ducklings: [],
        seats: pickedSeats(build.mode || buildMode, build),
        maxTokens: build.maxTokens,
        agentTurns: build.agentTurns,
        note: build.note,
      });
      setStarted(run.id);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    }
  };
  const launchTestOnly = async (test: PhaseConfig) => {
    if (!next) return;
    setFailure(null);
    try {
      const run = await client.testStart(projectId, next.id, "", {
        thenBuild: false,
        testMode: test.mode,
        testDucklings: [],
        testSeats: pickedSeats(test.mode, test),
        note: test.note,
      });
      setStarted(run.id);
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className="relative mx-auto max-w-5xl space-y-4 p-4" data-testid="now-view">
      <PageHeader
        eyebrow="Your attention"
        title="Now"
        subtitle="What needs you, what is moving, and the one clearest next step."
      />
      <ContextStrip tone={attentionCount > 0 ? "attention" : "neutral"}>
        <div aria-live="polite" className="flex flex-wrap items-center gap-x-5 gap-y-1 text-ink-secondary">
          {attentionCount === 0 ? (
            <span className="font-medium text-good" data-testid="now-clear">✓ No decisions waiting</span>
          ) : (
            <>
              {waitingCount > 0 && <span><strong className="font-medium text-ink" data-testid="now-waiting-count">{waitingCount}</strong> waiting for you</span>}
              {toVerify.length > 0 && <span><strong className="font-medium text-ink">{toVerify.length}</strong> to verify</span>}
              {failures.length > 0 && <span><strong className="font-medium text-critical">{failures.length}</strong> failed</span>}
              {reopened.length > 0 && <span><strong className="font-medium text-serious">{reopened.length}</strong> reopened</span>}
            </>
          )}
          {active.length > 0 && <span><strong className="font-medium text-ink">{active.length}</strong> in progress</span>}
        </div>
      </ContextStrip>

      {/* Decisions are the reason this page exists; they precede ambient
          activity, matching the page's stated hierarchy. */}
      {standalonePlan && plan?.proposal && (
        <PlanCard
          artifact={plan}
          traceErrors={planTrace}
          onApprove={() => void client.promote(projectId, "plan").then(() => setPlan(null)).catch(() => {})}
          onChanges={() => void client.artifactDiscard(projectId, "plan").then(() => setPlan(null)).catch(() => {})}
        />
      )}

      {waiting.length > 0 && (
        <section data-testid="now-waiting">
          <h2 className="text-sm font-medium text-ink">Waiting for you</h2>
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
                onAbort={() => void client.abort(r.id).catch(() => {})}
                onRequestChanges={r.stage === "intake" || r.stage === "spec" || r.stage === "plan"
                  ? async (note) => {
                    await client.stageStart(projectId, r.stage, { revise: note });
                  }
                  : undefined}
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
          <h2 className="text-sm font-medium text-ink">Fixed — did it actually answer the report?</h2>
          <div className="mt-2">
            <VerificationLedger
              bugs={toVerify}
              client={client}
              projectId={projectId}
              onMoved={(id, status) =>
                setBugs((cur) =>
                  status === "verified"
                    ? cur.filter((x) => x.id !== id)
                    : cur.map((x) => (x.id === id ? { ...x, status } : x)),
                )
              }
            />
          </div>
        </section>
      )}

      {reopened.length > 0 && (
        <section className="mt-4" data-testid="now-reopened">
          <h2 className="text-sm font-medium text-ink">Still broken — nothing is running for these</h2>
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
          <h2 className="text-sm font-medium text-ink">Failed, awaiting your call</h2>
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

      {/* Running work is ambient context, not a decision. Give it enough
          information to decide whether to open it without turning Now into
          the full monitoring view. */}
      {active.length > 0 && (
        <section data-testid="now-running">
          <h2 className="text-sm font-medium text-ink">In progress</h2>
          <ul className="mt-2 space-y-2">
            {active.map((r) => (
              <RunningRow key={r.id} run={r} live={spend[r.id]} />
            ))}
          </ul>
        </section>
      )}

      {quiet && actionSteps.length > 0 && <NextStepCards
        steps={actionSteps}
        costFor={(step) => {
          // When there is no taskNext launcher, this card is the only cost
          // surface. Report rows are totals, so always divide by sample count.
          if (step.kind !== "task" || (step.id !== "test-first" && step.id !== "build")) return undefined;
          const history = estimates[buildMode];
          if (!history || history.runs <= 0) return undefined;
          return `opens ${buildMode} · ~${moneyOrZero(history.usd / history.runs)} per run (${history.runs} samples)`;
        }}
      />}

      {/* Overview's job, absorbed when it retired (docs/ux-evaluation.md
          phase 3): cost as ambient information rather than a report consulted
          after the money is gone. Spend used to be a prop there, and the one
          caller passed zero. */}
      {quiet && (
        <section className="mt-4" data-testid="now-quiet">
          {active.length === 0 && list.length === 0 && !next && actionSteps.length === 0 && (
            <EmptyState message="No runs yet. Start below, or plan the work from Documents." />
          )}
          {next ? (
            <div className="rounded-card border border-hairline bg-surface1 p-4" data-testid="now-next">
              <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Ready when you are</h2>
              <p className="mt-1 text-md font-medium text-ink">
                <span className="font-mono">{next.id}</span> — {next.title}
              </p>
              {active.length > 0 && (
                <p className="mt-1 text-xs text-ink-secondary" data-testid="now-parallel-note">
                  {active.some((run) => run.stage === "intake" || run.stage === "spec" || run.stage === "plan")
                    ? `Ready under the currently accepted plan. A ${active.find((run) => run.stage === "intake" || run.stage === "spec" || run.stage === "plan")!.stage} run is also active; review it first if it may change this task.`
                    : "Available alongside current work; Ducklab will queue it automatically if the project is held."}
                </p>
              )}
              <div className="mt-3">
                {(next.next ?? [])[0] === "test_first" ? (
                  <TddLaunch
                    key={`${testMode}:${buildMode}`}
                    ducklings={fleet}
                    preferred={preferred}
                    phaseDefaults={{ build: buildMode, test: testMode }}
                    estimates={estimates}
                    busy={false}
                    embedded
                    testRoster={testRoster}
                    buildRoster={roster}
                    onTdd={(t, b) => void launchTdd(t, b)}
                    onTestOnly={(t) => void launchTestOnly(t)}
                    onBuildOnly={(b) =>
                      void launch({ mode: b.mode, ducklings: [], seats: pickedSeats(b.mode || buildMode, b), maxTokens: b.maxTokens, agentTurns: b.agentTurns, note: b.note })
                    }
                  />
                ) : (
                  <RunLauncher
                    key={buildMode}
                    ducklings={fleet}
                    initialMode={buildMode}
                    preferred={preferred}
                    estimates={estimates}
                    label={`Run ${next.id}`}
                    roster={roster}
                    initiallyOpen={false}
                    onLaunch={(opts) => void launch(opts)}
                  />
                )}
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
            nowList.length > 0 && (
              <p className="mt-1 text-xs text-ink-muted" data-testid="now-all-done">
                Nothing is ready to start either: everything is done, running, or waiting
                on something.
              </p>
            )
          )}
        </section>
      )}
      <NowFooter runs={nowList} />
    </div>
  );
}

/** A completed conversation is history regardless of how it terminated. */
function isTerminalChat(run: Run): boolean {
  if (run.stage !== "chat") return false;
  return new Set(["done", "failed", "aborted", "canceled", "cancelled", "ended"]).has(String(run.status).toLowerCase());
}

/** Failures worth acting on: the LATEST run of its task or stage, still failed,
 * for work that was never subsequently accepted. An old failure whose task a
 * later run completed is history, and history lives in Records — showing it
 * here would offer redoing finished work. */
function actionableFailures(list: Run[]): Run[] {
  const latest = new Map<string, Run>();
  // The docstring's promise, at last kept: T-101's accepted build was
  // followed a minute later by a redundant run someone aborted, and that
  // corpse sat in the inbox for eight hours "awaiting your call" — over
  // work already committed to the tree.
  const settled = new Set<string>();
  for (const r of list) {
    const key = r.task_id || r.stage || r.id;
    if (r.accepted) settled.add(key);
    const cur = latest.get(key);
    if (!cur || (r.started_at ?? "") > (cur.started_at ?? "")) {
      latest.set(key, r);
    }
  }
  return [...latest.values()]
    .filter((r) => r.status === "failed" && r.verdict !== "ABORTED" && r.stage !== "chat" && !settled.has(r.task_id || r.stage || r.id))
    .sort((a, b) => (b.ended_at ?? "").localeCompare(a.ended_at ?? ""));
}


/** A live run with its live spend: cost as ambient information, not a report
 * consulted after the money is gone. */
function RunningRow({ run, live }: { run: Run; live?: LiveSpend }) {
  const title = run.task_id
    ? `${run.stage === "test" ? "Testing" : run.stage === "review" ? "Reviewing" : "Building"} ${run.task_id}`
    : run.stage === "intake"
      ? "Drafting the requirements"
      : run.stage === "spec"
        ? "Drafting the specification"
        : run.stage === "plan"
          ? "Drafting the plan"
          : run.subject || runLabel(run);
  const ducklings = [...new Set(Object.values(run.roster ?? {}).filter(Boolean))];
  const elapsed = live?.wallclock_s !== undefined
    ? duration(live.wallclock_s * 1000)
    : run.started_at
      ? waitingFor(run.started_at)
      : "";
  return (
    <li data-testid="now-running-row" className="rounded-card border border-hairline bg-surface1 p-3">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <StatusChip role={runStatusRole(run.status)} label={run.status === "running" ? "in progress" : run.status} />
        <span className="text-sm font-medium text-ink">{title}</span>
        <span className="text-xs text-ink-secondary">{run.mode}</span>
        {ducklings.length > 0 && <span className="text-xs text-ink-secondary">· {ducklings.slice(0, 2).join(" + ")}{ducklings.length > 2 ? ` +${ducklings.length - 2}` : ""}</span>}
        {elapsed && <span className="text-xs tabular-nums text-ink-muted">· {elapsed}</span>}
        <a href={routeHref({ name: "run", id: run.id })} className="ml-auto rounded border border-hairline px-2 py-1 text-xs text-ink hover:bg-surface2">
          Open run
        </a>
      </div>
      {run.status === "queued" && run.queued_reason && (
        <p className="mt-1 text-xs text-ink-secondary">{run.queued_reason === "engine at max_concurrent_runs" ? <a href="#/settings?section=engine" className="underline">{run.queued_reason}</a> : run.queued_reason}</p>
      )}
      {live && (
        <p className="mt-1 text-xs tabular-nums text-ink-muted" data-testid="now-running-spend">
          {tokens(live.tokens)}{live.limit?.tokens ? ` / ${tokens(live.limit.tokens)} tokens` : " tokens"}
          {live.turns > 0 ? ` · ${live.turns} model ${live.turns === 1 ? "turn" : "turns"}` : ""}
          {` · ${moneyOrZero(live.usd)}`}
        </p>
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
    <details className="mt-4 border-t border-hairline pt-2 text-xs text-ink-muted" data-testid="now-footer">
      <summary className="cursor-pointer select-none hover:text-ink">Project history</summary>
      <p className="mt-1">
        Today {moneyOrZero(spentToday)} · project total {moneyOrZero(spentAll)}
        {finished > 0 && <> · {passed} of {finished} finished runs passed</>}
      </p>
      {finished > 0 && <p className="mt-1">All project history, including experimental and unsuccessful attempts.</p>}
    </details>
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
