import { useEffect, useState } from "react";
import type { EngineClient, Candidate, Duckling, LLMCall, Run, Task } from "../api/client";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";
import { buildTurns, anonymiseTurns, buildTimeline, buildGate, buildPending, buildTriage, buildTriageFailures, parseDiff, reviewerDissent, finalVerdict } from "../lib/runview";
import { ConversationTurn } from "../components/ConversationLane";
import { VirtualList } from "../components/VirtualList";
import { ToolTimeline } from "../components/ToolTimeline";
import { GateCard } from "../components/GateCard";
import { CandidateCard } from "../components/CandidateCard";
import { DiffView } from "../components/DiffView";
import { BudgetMeter } from "../components/BudgetMeter";
import { Prose } from "../components/Prose";
import { StatusChip } from "../components/StatusChip";
import { DecisionCard } from "../components/DecisionCard";
import { RunLauncher, type LaunchOpts, type ModeEstimates } from "../components/RunLauncher";
import { money, tokens, duration } from "../lib/format";
import { seatsFromRoster } from "../lib/seats";
import { verdictStatus, verdictLabel, assignDucklingColors, type Verdict } from "../lib/colors";
import { runLabel } from "../lib/runview";

type Tab = "diff" | "verify" | "candidates" | "calls";

/** The Run view: conversation lanes, gate and budget, tool timeline, tabs. */
export function RunView({ runId, client }: { runId: string; client: EngineClient }) {
  const run = useRuns((s) => s.runs[runId]);
  const events = useRuns((s) => s.events[runId] ?? []);
  const deltas = useRuns((s) => s.deltas[runId] ?? {});
  const reasoning = useRuns((s) => s.reasoning[runId] ?? {});
  // What the run has spent so far. The run record only carries the totals once
  // the run has ended, so without this the meter read zero for the whole run
  // and jumped to the final number at exactly the moment it stopped mattering.
  const live = useRuns((s) => s.spend[runId]);
  const acceptState = useRuns((s) => s.acceptState[runId] ?? { kind: "idle" as const });

  // Fetch the run's history on open.
  //
  // The store is fed by the live event stream, so everything derived from
  // events — the conversation, the gate, the tool timeline — was empty for any
  // run that finished before this client connected. Opening a completed run
  // showed a blank lane beside a header saying it passed. Clients hold no
  // state (I11), so the record has to come from the engine each time.
  // Refetched whenever the live stream changes the run's state, not just on
  // mount. The legal actions (run.next) are computed by the engine and travel
  // only in this response — the pause event carries the kind but not the
  // verdict buttons. Fetched once, a run watched live said "waiting for you"
  // at the gate and showed nothing to decide with; the controls appeared only
  // after leaving for Now and coming back, because coming back is a mount.
  // The engine updates the run before emitting the event, so the refetch this
  // triggers always finds the new actions.
  const streamedStatus = run?.status ?? "";
  const streamedPending = run?.pending_kind ?? "";
  useEffect(() => {
    let cancelled = false;
    client
      .run(runId)
      .then((d) => {
        if (!cancelled) {
          useRuns.getState().resyncRun(d.run, d.events as DucklabEvent[]);
        }
      })
      .catch(() => {
        // A run the engine does not know is already handled below by the
        // "no such run" branch; a transient failure must not blank what the
        // live stream has delivered.
      });
    return () => {
      cancelled = true;
    };
  }, [client, runId, streamedStatus, streamedPending]);

  const [tab, setTab] = useState<Tab>("diff");
  const [diff, setDiff] = useState("");
  const [testHunks, setTestHunks] = useState("");
  const [verify, setVerify] = useState("");
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  // What the models were actually sent. A prompt is assembled from a task, a
  // spec, a transcript and a toolbelt, and when an answer is wrong the question
  // is almost always where to look — which was reachable only by opening
  // llm.jsonl by hand.
  const [calls, setCalls] = useState<LLMCall[]>([]);
  const [answer, setAnswer] = useState("");
  const [revisionRun, setRevisionRun] = useState<string | null>(null);
  // The fleet, for colours. A duckling's colour is a property of the duckling,
  // not of its position in this run's roster — otherwise the same model is blue
  // in one transcript and orange in the next.
  const [fleet, setFleet] = useState<Duckling[]>([]);
  // The same feeds the Board's launcher gets. Without them the relaunch panel
  // WAS the shared component and still behaved like a stranger — no saved
  // line-ups on mode change, no measured cost beside the modes — which reads,
  // correctly, as a second implementation.
  const [preferred, setPreferred] = useState<Record<string, string[]>>({});
  const [estimates, setEstimates] = useState<ModeEstimates>({});
  const [relaunched, setRelaunched] = useState<string | null>(null);
  // The task's own status, which is not the same question as this run's. A run
  // that failed and whose task was finished by a later run still reported
  // `accepted: false` — it was never accepted — so the relaunch panel sat on
  // every old failure in the project's history offering to redo finished work.
  const [task, setTask] = useState<Task | null>(null);
  const [anyway, setAnyway] = useState(false);
  const [relaunchBusy, setRelaunchBusy] = useState(false);
  const [relaunchError, setRelaunchError] = useState<string | null>(null);
  // The accept-and-fix chain: one click accepts the green work and starts a
  // follow-up run with the reviewer's findings riding its prompt.
  const [fixBusy, setFixBusy] = useState(false);
  const [fixError, setFixError] = useState<string | null>(null);
  // Findings filed as bugs — the click's answer, with the ids it created.
  const [filedBugs, setFiledBugs] = useState<string[] | null>(null);
  const [fileBusy, setFileBusy] = useState(false);
  const [fileError, setFileError] = useState<string | null>(null);
  useEffect(() => {
    client.ducklings().then(setFleet).catch(() => setFleet([]));
    client
      .modeDefaults()
      .then((d) => setPreferred(d.ducklings ?? {}))
      .catch(() => setPreferred({}));
  }, [client]);

  const projectId = run?.project_id ?? "";
  useEffect(() => {
    if (!projectId) return;
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
  }, [client, projectId]);
  const taskId = run?.task_id ?? "";
  useEffect(() => {
    if (!projectId || !taskId) return;
    client
      .tasks(projectId)
      .then((all) => setTask(all.find((t) => t.id === taskId) ?? null))
      .catch(() => setTask(null));
  }, [client, projectId, taskId]);

  useEffect(() => {
    client
      .runDiff(runId)
      .then((d) => {
        setDiff(d.diff);
        setTestHunks(d.tests);
      })
      .catch(() => {
        setDiff("");
        setTestHunks("");
      });
    client.runVerify(runId).then(setVerify).catch(() => setVerify(""));
    client.runCandidates(runId).then(setCandidates).catch(() => setCandidates([]));
    client.runLLM(runId).then(setCalls).catch(() => setCalls([]));
  }, [runId, client, run?.status]);

  if (!run) return <p className="p-4 text-ink-muted">Loading run…</p>;

  const roster = Object.values(run.roster ?? {});
  const ducklingColors = assignDucklingColors(fleet);
  // Only ducklings that actually spent something: the roster names one for every
  // role whether or not that role ran, and listing six models for a solo run
  // credits five with work they never did.
  const perDuckling = Object.entries(live?.ducklings ?? run.spend ?? {})
    .filter(([, d]) => d.calls > 0)
    .sort((a, b) => b[1].tokens - a[1].tokens);
  // A judge's turns are anonymised; the mapping is dropped, not hidden.
  const anonymise = run.mode === "tournament";
  const turns = anonymiseTurns(buildTurns(events), anonymise);
  const gate = buildGate(events);
  const pending = buildPending(events);
  // A green gate over an unconvinced reviewer must not be silent (T-028:
  // three straight request-changes verdicts under "tests passed").
  const dissent = run.verdict === "PASSED" ? reviewerDissent(turns) : null;
  // Any final findings at all — an approval "with two minor findings" found
  // real work too; approval means "not worth blocking", not "not worth
  // remembering". Filing them as bugs puts them in the loop instead of in a
  // transcript a future testing phase re-discovers at full price.
  const lastVerdict = finalVerdict(turns);
  const fileable = lastVerdict && lastVerdict.findings.length > 0 &&
    (run.status === "paused" || run.status === "done");
  const timeline = buildTimeline(events);
  const triage = buildTriage(events);
  const triageFailed = buildTriageFailures(events);
  // The live figures while the run is GOING, the recorded ones once it is
  // not — by status, not by which happens to exist. The last streamed budget
  // event can predate the final turn's accounting by a moment, and a paused
  // run kept wearing that stale frame over an exact record: the meter said 3
  // turns while state.json said 4, and the person audited the arithmetic.
  const runIsLive = run.status === "running" || run.status === "queued";
  const budget = runIsLive ? (live ?? run.budget) : (run.budget ?? live);
  // Drawn against the ceiling this run actually got. These were hardcoded, so a
  // run started with a raised limit showed a bar that looked full when it had
  // used a quarter of its budget.
  const limit = (runIsLive ? live?.limit : run.budget?.limit ?? live?.limit) ??
    run.budget?.limit ?? { tokens: 400000, usd: 2, turns: 24, wallclock_s: 3600 };

  // A run is still working while it runs or waits its turn, and while it is
  // paused — a pause is a waiting state, not an ending (01 §7.1).
  const isWorking = run.status === "running" || run.status === "queued" || run.status === "paused";
  // Caps can be lifted only once the budget exists: a queued run's budget is
  // created when it starts, and the engine refuses until then.
  const canLift = run.status === "running" || run.status === "paused";
  // The human gate is the one state where accepting or rejecting means
  // anything. A run paused on a question needs an answer instead: accepting
  // work that has not finished would commit a half-done change.
  // A stage run's gate is a decision about a document, so it has the third
  // answer the Cycle view has: send it back with a note. It appeared in only
  // one of the two places the same gate shows up, so someone watching the work
  // happen had to go and find another screen to say "almost".
  const stageToRevise = ["intake", "spec", "plan"].includes(run.stage) ? run.stage : "";
  const next = run.next ?? [];
  const decisionOpen = next.some((v) => ["accept", "reject", "resume", "request_changes"].includes(v));
  // What accepting DOES, per kind. Three incidents were the person discovering
  // it after the click.
  const consequence = stageToRevise
    ? `replaces the approved ${run.stage} and closes the run`
    : run.stage === "triage"
      ? `applies ${triage.length || "the"} classification${triage.length === 1 ? "" : "s"} to the report${triage.length === 1 ? "" : "s"}`
      : next.includes("resume")
        ? run.pending_kind === "budget"
          ? "This run hit its own budget cap; its work is intact. Lift the binding cap on the meter below, then resume."
          : run.pending_kind === "provider"
            ? "The model provider dropped the connection and retries ran out; the work is intact. Resume when the provider is reachable, or abort."
            : run.pending_kind === "error"
              ? "The run stopped on an error — see why above. Its work is intact: resume to continue over it, or abort to discard it."
              : "The engine restarted while this run was working; resuming re-enters it from its checkpoint."
        : "commits the diff to the project";
  const outcome = (() => {
    if (isWorking) return "";
    if (run.accepted) {
      return run.commit_sha ? `accepted · ${run.commit_sha.slice(0, 7)}` : "accepted";
    }
    if (run.status === "failed" || run.verdict === "FAILED") return "not accepted";
    return "finished";
  })();

  // Offered when a task run has ended without being accepted. Not for a run
  // still going — there is nothing to learn from yet — and not for a stage,
  // whose gate already has "request changes".
  const canRelaunch =
    !!run.task_id && !isWorking && !run.accepted && !stageToRevise;
  // Why this is not the obvious next step, if it is not. Both are about the
  // TASK, which is a different question from this run: a run that failed is
  // never accepted, so asking about the run offered a relaunch on every old
  // failure in the project's history.
  //
  // Re-running stays possible in both cases — a result can be regretted, and a
  // stuck run may be worth racing — but neither should be one click away.
  const relaunchCaveat =
    task?.status === "accepted"
      ? `${run.task_id} was finished by a later run and accepted. Running it again starts fresh work against something already committed.`
      : task?.status === "in_progress"
        ? `Another run is working on ${run.task_id} right now. A second one would edit the same tree at the same time.`
        : "";
  // The seats as THIS run filled them, positionally — the panel exists to
  // run it again. (The board's launcher opens on the Settings line-up
  // instead: a fresh launch and a re-run are different intents, and each
  // panel says which one it serves.)
  const relaunchDucklings = seatsFromRoster(run.mode, run.roster);

  const relaunch = async (opts: LaunchOpts) => {
    setRelaunchBusy(true);
    setRelaunchError(null);
    try {
      const started = await client.runStart(run.project_id, run.task_id, opts);
      setRelaunched(started.id);
    } catch (e) {
      setRelaunchError(e instanceof Error ? e.message : String(e));
    } finally {
      setRelaunchBusy(false);
    }
  };

  const requestChanges = async (text: string) => {
    try {
      const started = await client.stageStart(run.project_id, stageToRevise, { revise: text });
      setRevisionRun(started.id);
    } catch (e) {
      useRuns.getState().failAccept(runId, e instanceof Error ? e.message : String(e));
    }
  };

  const onAccept = async () => {
    const store = useRuns.getState();
    store.beginAccept(runId);
    try {
      const res = await client.accept(runId);
      store.confirmAccept(runId, res.commit_sha);
    } catch (e) {
      // Never show a commit the engine did not confirm (AC-34).
      store.failAccept(runId, e instanceof Error ? e.message : String(e));
    }
  };

  // Not a fixed-height layout. The run view stacks four regions of unbounded
  // content — gate, conversation, tool timeline, diff — and forcing them into
  // one viewport made flex shrink the ones below their content while their
  // children kept painting, so a finished run drew its diff on top of its own
  // conversation. The page scrolls; only the conversation is bounded, and the
  // nav stays visible because the app shell holds the scroll, not this view.
  return (
    <div data-testid="run-view">
      <header className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-3">
        {/* A run with no task showed nothing at all: the header of a triage or
            a stage opened with an empty space where its name should be. The
            same fallback the runs list uses — task, else stage, else id. */}
        <span className="text-md">{runLabel(run)}</span>
        <span className="text-ink-secondary">{run.mode}</span>
        <StatusChip role={verdictStatus(run.verdict as Verdict)} label={verdictLabel(run.verdict as Verdict)} />
        <div className="ml-auto flex items-center gap-2">
          {/* A decision that has been made is not still open. These used to be
              shown on every run whatever its state, so an accepted run went on
              offering Accept, Reject and Abort as if nothing had happened —
              and clicking one would have asked the engine to redo a decision
              that was already recorded. */}
          {/* From the engine's list, not this view's opinion of the state:
              the decision itself lives in the card below, the header keeps
              only the one control that stops work in flight. */}
          {next.includes("abort") && (
            <button
              type="button"
              onClick={() => client.abort(runId).catch(() => {})}
              data-testid="abort-button"
              className="rounded border border-hairline px-2 py-1 text-sm"
            >
              Abort
            </button>
          )}
          {outcome && (
            // What became of it, in the space the buttons used to occupy: the
            // question "is there anything for me to do here" is answered
            // either way, never by an absence.
            <span className="text-sm text-ink-secondary" data-testid="run-outcome">
              {outcome}
            </span>
          )}
        </div>
      </header>

      {/* The moment you most want to change a setting and go again is while
          looking at the run that just failed. Doing it meant leaving for the
          board and finding the task by hand, which is enough friction that a
          re-run tends to carry the settings that just failed. */}
      {canRelaunch && (
        <section
          data-testid="relaunch"
          className="m-2 rounded-card border border-hairline p-3"
        >
          <h2 className="text-sm font-medium text-ink mb-2">Run {run.task_id} again</h2>
          {relaunchCaveat && !anyway ? (
            <p className="text-sm text-ink-muted" data-testid="relaunch-done">
              {relaunchCaveat}{" "}
              <button
                type="button"
                onClick={() => setAnyway(true)}
                data-testid="relaunch-anyway"
                className="text-ink underline"
              >
                run it anyway
              </button>
            </p>
          ) : (
          <>
          {/* The provenance, said out loud: this panel and the board's rail
              open with DIFFERENT seats by design, and unlabelled that read
              as one of them being wrong. */}
          <p className="mb-1 text-xs text-ink-muted" data-testid="relaunch-provenance">
            seated as this run ran — the board launches with your Settings line-up
          </p>
          <RunLauncher
            // Remounted when the mode arrives: the run reaches the store in two
            // steps (a run_start event, then the resync), and a launcher that
            // read initialMode from the first was offering "solo" to relaunch a
            // pair — the read-a-prop-once disease, again.
            key={`${run.id}:${run.mode}`}
            ducklings={fleet}
            initialMode={run.mode}
            initialDucklings={relaunchDucklings}
            preferred={preferred}
            estimates={estimates}
            label="Run again"
            busy={relaunchBusy}
            onLaunch={relaunch}
          />
          </>
          )}
          {relaunched && (
            <p className="mt-2 text-sm">
              <a href={`#/runs/${relaunched}`} data-testid="relaunch-link" className="text-ink underline">
                started {relaunched}
              </a>
            </p>
          )}
          {relaunchError && (
            <p className="mt-2 text-sm text-critical" data-testid="relaunch-error">
              {relaunchError}
            </p>
          )}
        </section>
      )}

      {/* A failed run used to show FAILED and nothing else — the reason went to
          an `error` event that no view rendered, so the only way to learn why
          was to open events.jsonl. Some of these messages exist to be acted on:
          split refuses a decomposition with the exact file two subtasks both
          claimed. */}
      {dissent && (
        <section
          data-testid="reviewer-dissent"
          className="m-2 rounded-card border border-serious p-3"
        >
          <StatusChip role="serious" label="green gate, unconvinced reviewer" />
          <p className="mt-1 text-sm text-ink">
            The tests pass, but the reviewer's last verdict was “{dissent.verdict}”
            {dissent.findings > 0 &&
              ` with ${dissent.findings} finding${dissent.findings === 1 ? "" : "s"}`}
            {" "}and its rounds ran out. The gate decides the verdict; the reviewer only
            advises — read them before accepting:
          </p>
          <ul className="mt-2 space-y-1 text-sm" data-testid="dissent-findings-list">
            {dissent.notes.map((n, i) => (
              <li key={i} className="text-ink-secondary">{n}</li>
            ))}
          </ul>
          {/* "Almost" for code is a new run — but the new run used to be born
              amnesiac: nothing carried the findings into its prompt, and
              reject would RESTORE the green work away. One click: accept
              what passed, then a follow-up run that reads the objections. */}
          {(run.next ?? []).includes("accept") && dissent.findings > 0 && (
            <div className="mt-2">
              <button
                type="button"
                data-testid="accept-and-fix"
                disabled={fixBusy}
                onClick={() => {
                  setFixBusy(true);
                  setFixError(null);
                  const note =
                    "The previous run passed its gate but its reviewer requested changes. " +
                    "Address these outstanding findings:\n" +
                    dissent.notes.map((n) => `- ${n}`).join("\n");
                  void client
                    .accept(run.id)
                    .then(() =>
                      client.runStart(run.project_id, run.task_id, {
                        mode: run.mode,
                        ducklings: seatsFromRoster(run.mode, run.roster),
                        note,
                      }),
                    )
                    .then((r) => {
                      location.hash = `#/runs/${r.id}`;
                    })
                    .catch((e) => setFixError(e instanceof Error ? e.message : String(e)))
                    .finally(() => setFixBusy(false));
                }}
                className="rounded border border-hairline px-2 py-1 text-sm"
              >
                {fixBusy ? "Starting…" : "Accept, then fix the findings"}
              </button>
              {fixError && (
                <p className="mt-1 text-xs text-critical" data-testid="accept-and-fix-error">
                  {fixError}
                </p>
              )}
            </div>
          )}
        </section>
      )}
      {fileable && (
        <section data-testid="file-findings" className="m-2 rounded-card border border-hairline p-3">
          <p className="text-sm text-ink">
            The reviewer's last verdict ({lastVerdict!.verdict}) carries{" "}
            {lastVerdict!.findings.length} finding{lastVerdict!.findings.length === 1 ? "" : "s"}.
            {" "}Filed as bugs they enter the loop — triage, promote, fix — instead of waiting
            in this transcript for a future testing phase to re-find them.
          </p>
          {/* The findings themselves, where the decision is. Announcing "1
              finding" and making the person scroll to the transcript and back
              up to act on it is a treasure hunt with one treasure. */}
          <ul className="mt-2 space-y-1 text-sm" data-testid="file-findings-list">
            {lastVerdict!.findings.map((f, i) => (
              <li key={i} className="text-ink-secondary">
                {f.severity && <span className="mr-1 text-xs uppercase text-ink-muted">[{f.severity}]</span>}
                {f.issue}
                {f.file && (
                  <span className="text-ink-muted"> — {f.file}{f.line ? `:${f.line}` : ""}</span>
                )}
                {f.fix && <span className="text-ink-muted"> · fix: {f.fix}</span>}
              </li>
            ))}
          </ul>
          <div className="mt-2">
            {filedBugs ? (
              <p className="text-sm text-good" data-testid="file-findings-done">
                filed as {filedBugs.join(", ")} —{" "}
                <a href="#/board?tab=bugs" className="underline">see the bugs board</a>
              </p>
            ) : (
              <button
                type="button"
                data-testid="file-findings-button"
                disabled={fileBusy}
                onClick={() => {
                  setFileBusy(true);
                  setFileError(null);
                  void client
                    .runFileFindings(run.id)
                    .then((r) => setFiledBugs(r.items.map((b) => b.id)))
                    .catch((e) => setFileError(e instanceof Error ? e.message : String(e)))
                    .finally(() => setFileBusy(false));
                }}
                className="rounded border border-hairline px-2 py-1 text-sm"
              >
                {fileBusy ? "Filing…" : `File ${lastVerdict!.findings.length} finding${lastVerdict!.findings.length === 1 ? "" : "s"} as bugs`}
              </button>
            )}
            {fileError && (
              <p className="mt-1 text-xs text-critical" data-testid="file-findings-error">{fileError}</p>
            )}
          </div>
        </section>
      )}
      {run.failure && (
        <section
          data-testid="run-failure"
          className="m-2 rounded-card border border-critical p-3"
        >
          {/* A budget pause records its reason here while it waits; calling
              that "failed" on a paused (or resumed) run announced a death
              that had not happened. */}
          <h2 className="text-sm font-medium text-critical mb-1">
            {run.status === "failed" ? "Why it failed" : "Why it stopped"}
          </h2>
          <p className="whitespace-pre-wrap break-words text-sm text-ink">{run.failure}</p>
        </section>
      )}

      {/* What the gate is actually asking about. The proposals were written to
          the event stream in full and nothing rendered them, so a triage run
          paused offering Accept and Reject with the thing being decided nowhere
          on screen. */}
      {/* One bad report does not poison the others: the batch carries on and
          the failure is written down. Nothing rendered it, so a run that
          triaged two of three looked exactly like one that triaged two, and the
          third stayed open with no explanation anywhere a person would look. */}
      {triageFailed.length > 0 && (
        <section
          data-testid="triage-failures"
          className="m-2 rounded-card border border-critical p-3"
        >
          <h2 className="text-sm font-medium text-critical mb-2">
            Could not classify · {triageFailed.length} report
            {triageFailed.length === 1 ? "" : "s"}
          </h2>
          <ul className="space-y-1 text-sm">
            {triageFailed.map((f) => (
              <li key={f.bug} data-testid="triage-failure">
                <span className="font-mono text-ink">{f.bug}</span>{" "}
                <span className="text-ink-secondary">{f.error}</span>
              </li>
            ))}
          </ul>
          <p className="mt-2 text-xs text-ink-muted">
            These stay open. Accepting this run applies only the ones above.
          </p>
        </section>
      )}

      {triage.length > 0 && (
        <section
          data-testid="triage-proposals"
          className="m-2 rounded-card border border-hairline p-3"
        >
          <h2 className="text-sm font-medium text-ink mb-2">
            Proposed classification · {triage.length} report
            {triage.length === 1 ? "" : "s"}
          </h2>
          <ul className="space-y-3">
            {triage.map((t) => (
              <li key={t.bug} data-testid="triage-proposal" className="text-sm">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="font-mono text-ink">{t.bug}</span>
                  {t.severity && <StatusChip role="warning" label={t.severity} />}
                  {t.component && <span className="text-ink-secondary">{t.component}</span>}
                  {t.duplicate_of && (
                    <span className="text-ink-muted">duplicate of {t.duplicate_of}</span>
                  )}
                </div>
                {t.task_title && (
                  <div className="mt-1 text-ink">
                    would become a task: <span className="text-ink-secondary">{t.task_title}</span>
                  </div>
                )}
                {t.reason && (
                  <p className="mt-1 whitespace-pre-wrap text-ink-secondary">{t.reason}</p>
                )}
                {t.suspected_files && t.suspected_files.length > 0 && (
                  <p className="mt-1 font-mono text-xs text-ink-muted">
                    {t.suspected_files.join(" · ")}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      {/* One card for every kind of gate — stage, triage, build — and for a
          run the engine's own restart paused. The verdict buttons come from
          run.next; what varies by kind is the consequence and the evidence,
          never the frame. */}
      {decisionOpen && (
        <section className="m-2 rounded-card border border-serious p-3">
          <DecisionCard
            next={next}
            title={
              stageToRevise ? "Proposal awaiting your decision" : "Waiting for your decision"
            }
            subtitle={`${run.stage} · ${run.task_id || run.id}`}
            consequence={consequence}
            cost={budget && budget.usd > 0 ? `${money(budget.usd)} · ${tokens(budget.tokens)} tokens` : undefined}
            accepting={acceptState.kind === "pending"}
            onAccept={onAccept}
            onReject={() => void client.reject(runId).catch(() => {})}
            onRequestChanges={stageToRevise ? requestChanges : undefined}
            onResume={() => void client.runResume(runId).catch(() => {})}
            revisionRun={revisionRun}
          />
        </section>
      )}

      {acceptState.kind === "error" && (
        <p className="m-2 text-critical" data-testid="accept-error">
          accept failed: {acceptState.message}
        </p>
      )}
      {acceptState.kind === "committed" && (
        <p className="m-2 text-good" data-testid="accept-committed">
          committed {acceptState.sha.slice(0, 8)}
        </p>
      )}

      {pending && (
        <section className="m-2 rounded-card border border-hairline p-3" data-testid="pending-human">
          <StatusChip role="serious" label={`waiting for you — ${pending.kind}`} />
          {pending.detail && !pending.question && (
            <p className="mt-1 whitespace-pre-wrap break-words text-sm text-ink" data-testid="pending-detail">
              {pending.detail}
            </p>
          )}
          {pending.question && (
            <div className="mt-2">
              <p className="text-ink">{pending.question}</p>
              <div className="mt-1 flex gap-2">
                <input
                  aria-label="answer"
                  value={answer}
                  onChange={(e) => setAnswer(e.target.value)}
                  className="flex-1 rounded border border-hairline bg-surface2 px-2 py-1"
                />
                <button
                  type="button"
                  data-testid="answer-button"
                  onClick={() => client.answer(runId, pending.questionId ?? "", answer).catch(() => {})}
                  className="rounded border border-hairline px-2 py-1 text-sm"
                >
                  Answer
                </button>
              </div>
            </div>
          )}
        </section>
      )}

      <div className="grid gap-4 p-4 md:grid-cols-[1fr_260px]">
        <section data-testid="conversation">
          {/* Viewport-relative, so it adapts to the window without depending on
              a chain of parent heights resolving — which is what broke. */}
          <VirtualList items={turns} height="60vh">
            {(t) => (
              <ConversationTurn
                block={t}
                roster={roster}
                color={ducklingColors[t.duckling]}
                streamed={deltas[`${t.round}:${t.turn}`]}
                reasoning={reasoning[`${t.round}:${t.turn}`]}
              />
            )}
          </VirtualList>
        </section>

        <aside className="flex flex-col gap-3">
          {/* What was asked for, next to what was done. Judging a run means
              reading the diff against the task's own words, and those lived
              only on the board — a different screen from the decision. */}
          {task && (task.body ?? "").trim() !== "" && (
            <div className="rounded-card border border-hairline p-3" data-testid="run-task-card">
              <div className="text-sm text-ink-muted">the task</div>
              <div className="mt-1 text-sm text-ink">
                {task.id} — {task.title}
              </div>
              <div className="mt-2 max-h-64 overflow-y-auto text-sm">
                <Prose body={task.body ?? ""} />
              </div>
            </div>
          )}
          <GateCard gate={gate} stage={run.stage} />
          {budget && (
            <div className="rounded-card border border-hairline p-3">
              <div className="text-sm text-ink-muted">budget</div>
              <div className="mt-2 flex flex-col gap-2">
                {/* While the run lives, each cap carries its own "no cap"
                    checkbox: a run near a ceiling gets headroom in place —
                    per-cap, one-way, recorded — instead of dying at the limit
                    and losing the work. The engine's budget event refreshes
                    the meter the moment the lift lands. */}
                <BudgetMeter
                  label="tokens" used={budget.tokens} limit={limit.tokens} format={tokens}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "tokens").catch(() => {}) } : undefined}
                />
                <BudgetMeter
                  label="cost" used={budget.usd} limit={limit.usd} format={money}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "usd").catch(() => {}) } : undefined}
                />
                <BudgetMeter
                  label="turns"
                  used={budget.turns}
                  limit={limit.turns}
                  format={(n) => String(Math.round(n))}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "turns").catch(() => {}) } : undefined}
                />
              </div>
              {/* One tracker serves every duckling and every turn, so the run's
                  total cannot say which model is burning it. In a mode with two
                  models that is usually the only question worth asking. */}
              {perDuckling.length > 1 && (
                <dl className="mt-3 border-t border-hairline pt-2 text-xs" data-testid="spend-by-duckling">
                  {perDuckling.map(([id, d]) => (
                    <div key={id} className="flex justify-between gap-2">
                      <dt style={{ color: ducklingColors[id] }}>{id}</dt>
                      <dd className="tabular-nums text-ink-secondary">
                        {tokens(d.tokens)} · {money(d.cost_usd)} · {d.calls} call
                        {d.calls === 1 ? "" : "s"}
                      </dd>
                    </div>
                  ))}
                </dl>
              )}
            </div>
          )}
        </aside>
      </div>

      <div className="px-4">
        <ToolTimeline calls={timeline} />
      </div>

      {/* A tab with nothing in it is dimmed and counted, so an empty one reads
          as "there was none" rather than "something failed to load". A run
          with no candidates is not a broken run — solo, pair and split never
          have any. */}
      <nav className="mt-3 flex gap-2 border-b border-hairline px-4">
        {([
          ["diff", testHunks ? "edits tests" : diff ? undefined : "empty"],
          ["verify", verify ? undefined : "no output"],
          ["candidates", candidates.length ? String(candidates.length) : "none"],
          ["calls", calls.length ? String(calls.length) : "none"],
        ] as [Tab, string | undefined][]).map(([t, note]) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            data-testid={`tab-${t}`}
            className={`px-2 py-1 text-sm ${tab === t ? "text-ink" : "text-ink-muted"}`}
          >
            {t}
            {note && <span className="ml-1 text-xs text-ink-muted">{note}</span>}
          </button>
        ))}
      </nav>

      <div className="p-2">
        {/* The test hunks come first, above the rest of the diff, because the
            whole point is that they are read before the decision and not
            after. Not a blocker — sometimes a test is genuinely wrong (05
            §5.3) — so the Accept button stays exactly where it was. */}
        {tab === "diff" && testHunks && (
          <section
            className="mb-3 rounded-card border border-serious p-2"
            data-testid="tests-modified"
          >
            <p className="mb-2 text-sm text-serious">
              this change edits tests; read these hunks before accepting
            </p>
            <DiffView files={parseDiff(testHunks)} />
          </section>
        )}
        {tab === "diff" &&
          (diff ? (
            <DiffView files={parseDiff(diff)} />
          ) : (
            <p className="p-2 text-sm text-ink-muted" data-testid="diff-empty">
              {run.status === "running"
                ? "Nothing written yet."
                : "This run changed no files."}
            </p>
          ))}
        {tab === "verify" &&
          (verify ? (
            <pre className="overflow-x-auto bg-surface2 p-2 font-mono text-xs">{verify}</pre>
          ) : (
            <p className="p-2 text-sm text-ink-muted" data-testid="verify-empty">
              {gate?.unverified
                ? "No gate could run, so nothing was verified."
                : "The gate ran and printed nothing, which is what passing looks like."}
            </p>
          ))}
        {tab === "candidates" &&
          (candidates.length === 0 ? (
            <p className="p-2 text-sm text-ink-muted">
              Only a tournament produces candidates; this run is {run.mode}.
            </p>
          ) : (
            <div className="flex flex-col gap-2">
              {candidates.map((c) => (
                <CandidateCard key={c.label} candidate={c} applied={c.gate === "green"} />
              ))}
            </div>
          ))}

        {tab === "calls" &&
          (calls.length === 0 ? (
            <p className="text-sm text-ink-muted" data-testid="calls-empty">
              No model calls recorded for this run.
            </p>
          ) : (
            <ul className="flex flex-col gap-2" data-testid="calls">
              {calls.map((c) => (
                <LLMCallRow key={c.seq} call={c} color={ducklingColors[c.duckling]} />
              ))}
            </ul>
          ))}
      </div>
    </div>
  );
}

export function runElapsed(run: Run, now: Date = new Date()): string {
  const started = Date.parse(run.started_at);
  if (Number.isNaN(started)) return "0s";
  const end = run.ended_at ? Date.parse(run.ended_at) : now.getTime();
  return duration(Math.max(0, end - started));
}

/** One model call, folded. What was sent is usually thousands of lines, and the
 * reason to open a call is almost always a specific one. */
function LLMCallRow({ call, color }: { call: LLMCall; color?: string }) {
  const usage = call.usage ?? {};
  const inTok = usage.prompt_tokens ?? usage.input_tokens ?? 0;
  const outTok = usage.completion_tokens ?? usage.output_tokens ?? 0;
  const reasoning = usage.reasoning_tokens ?? 0;
  return (
    <li data-testid="call-row" className="rounded border border-hairline">
      <details>
        <summary className="flex cursor-pointer flex-wrap items-baseline gap-2 px-2 py-1 text-xs">
          <span className="font-mono text-ink-muted">#{call.seq}</span>
          <span style={{ color }}>{call.duckling}</span>
          <span className="text-ink-muted">{call.role}</span>
          <span className="tabular-nums text-ink-secondary">
            {tokens(inTok)} in · {tokens(outTok)} out
            {/* Part of the output, not on top of it. Shown apart because a
                budget spent on thinking and one spent on an answer call for
                different actions. */}
            {reasoning > 0 && <> · {tokens(reasoning)} thinking</>}
          </span>
          <span className="tabular-nums text-ink-secondary">{money(call.cost_usd)}</span>
          {call.estimated && <StatusChip role="warning" label="estimated" />}
          {call.finish_reason && call.finish_reason !== "stop" && (
            <StatusChip role="serious" label={call.finish_reason} />
          )}
          <span className="ml-auto tabular-nums text-ink-muted">{duration(call.latency_ms / 1000)}</span>
        </summary>
        <div className="grid gap-2 border-t border-hairline p-2 md:grid-cols-2">
          <div>
            <div className="mb-1 text-xs text-ink-muted">sent</div>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-ink-secondary">
              {JSON.stringify(call.request ?? {}, null, 2)}
            </pre>
          </div>
          <div>
            <div className="mb-1 text-xs text-ink-muted">came back</div>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-ink-secondary">
              {JSON.stringify(call.response ?? {}, null, 2)}
            </pre>
          </div>
        </div>
      </details>
    </li>
  );
}
