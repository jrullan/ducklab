import { useEffect, useState } from "react";
import type { EngineClient, Candidate, Run } from "../api/client";
import { useRuns } from "../store/runs";
import { buildTurns, anonymiseTurns, buildTimeline, buildGate, buildPending, parseDiff } from "../lib/runview";
import { ConversationTurn } from "../components/ConversationLane";
import { ToolTimeline } from "../components/ToolTimeline";
import { GateCard } from "../components/GateCard";
import { CandidateCard } from "../components/CandidateCard";
import { DiffView } from "../components/DiffView";
import { BudgetMeter } from "../components/BudgetMeter";
import { StatusChip } from "../components/StatusChip";
import { money, tokens, duration } from "../lib/format";
import { verdictStatus, verdictLabel, type Verdict } from "../lib/colors";

type Tab = "diff" | "verify" | "candidates";

/** The Run view: conversation lanes, gate and budget, tool timeline, tabs. */
export function RunView({ runId, client }: { runId: string; client: EngineClient }) {
  const run = useRuns((s) => s.runs[runId]);
  const events = useRuns((s) => s.events[runId] ?? []);
  const deltas = useRuns((s) => s.deltas[runId] ?? {});
  const acceptState = useRuns((s) => s.acceptState[runId] ?? { kind: "idle" as const });

  const [tab, setTab] = useState<Tab>("diff");
  const [diff, setDiff] = useState("");
  const [verify, setVerify] = useState("");
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [answer, setAnswer] = useState("");

  useEffect(() => {
    client.runDiff(runId).then(setDiff).catch(() => setDiff(""));
    client.runVerify(runId).then(setVerify).catch(() => setVerify(""));
    client.runCandidates(runId).then(setCandidates).catch(() => setCandidates([]));
  }, [runId, client, run?.status]);

  if (!run) return <p className="p-4 text-ink-muted">Loading run…</p>;

  const roster = Object.values(run.roster ?? {});
  // A judge's turns are anonymised; the mapping is dropped, not hidden.
  const anonymise = run.mode === "tournament";
  const turns = anonymiseTurns(buildTurns(events), anonymise);
  const gate = buildGate(events);
  const pending = buildPending(events);
  const timeline = buildTimeline(events);
  const budget = run.budget;

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

  return (
    <div data-testid="run-view">
      <header className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-3">
        <span className="text-md">{run.task_id}</span>
        <span className="text-ink-secondary">{run.mode}</span>
        <StatusChip role={verdictStatus(run.verdict as Verdict)} label={verdictLabel(run.verdict as Verdict)} />
        <div className="ml-auto flex gap-2">
          <button
            type="button"
            onClick={onAccept}
            disabled={run.verdict === "FAILED" || acceptState.kind === "pending"}
            data-testid="accept-button"
            className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
          >
            {acceptState.kind === "pending" ? "Accepting…" : "Accept"}
          </button>
          <button
            type="button"
            onClick={() => client.reject(runId).catch(() => {})}
            className="rounded border border-hairline px-2 py-1 text-sm"
          >
            Reject
          </button>
          <button
            type="button"
            onClick={() => client.abort(runId).catch(() => {})}
            data-testid="abort-button"
            className="rounded border border-hairline px-2 py-1 text-sm"
          >
            Abort
          </button>
        </div>
      </header>

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
          {turns.map((t) => (
            <ConversationTurn
              key={t.key}
              block={t}
              roster={roster}
              streamed={t.duckling ? deltas[t.duckling] : undefined}
            />
          ))}
        </section>

        <aside className="flex flex-col gap-3">
          <GateCard gate={gate} />
          {budget && (
            <div className="rounded-card border border-hairline p-3">
              <div className="text-sm text-ink-muted">budget</div>
              <div className="mt-2 flex flex-col gap-2">
                <BudgetMeter label="tokens" used={budget.tokens} limit={400000} format={tokens} />
                <BudgetMeter label="cost" used={budget.usd} limit={2} format={money} />
                <BudgetMeter label="turns" used={budget.turns} limit={24} format={(n) => String(Math.round(n))} />
              </div>
            </div>
          )}
        </aside>
      </div>

      <div className="px-4">
        <ToolTimeline calls={timeline} />
      </div>

      <nav className="mt-3 flex gap-2 border-b border-hairline px-4">
        {(["diff", "verify", "candidates"] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            data-testid={`tab-${t}`}
            className={`px-2 py-1 text-sm ${tab === t ? "text-ink" : "text-ink-muted"}`}
          >
            {t}
          </button>
        ))}
      </nav>

      <div className="p-2">
        {tab === "diff" && <DiffView files={parseDiff(diff)} />}
        {tab === "verify" && (
          <pre className="overflow-x-auto bg-surface2 p-2 font-mono text-xs">{verify || "no output"}</pre>
        )}
        {tab === "candidates" &&
          (candidates.length === 0 ? (
            <p className="p-2 text-ink-muted">This run has no candidates.</p>
          ) : (
            <div className="flex flex-col gap-2">
              {candidates.map((c) => (
                <CandidateCard key={c.label} candidate={c} applied={c.gate === "green"} />
              ))}
            </div>
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
