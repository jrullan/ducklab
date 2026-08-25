/**
 * The Cycle view: the three artifact stages and the spine that links them
 * (08 §4, AC-40, AC-43).
 *
 * The point of this screen is that traceability is *visible*. A requirement
 * nothing implements, a spec section no task delivers — those are the failures
 * that make a plan look finished while leaving holes, and they are computed
 * deterministically by the engine, never judged by a model. So the rail states
 * them as fact.
 */

import { useCallback, useEffect, useState } from "react";
import type { Artifact, ConfigFinding, Duckling, EngineClient, RosterEntry, Run, Section, Task, TraceError } from "../api/client";
import { ChatAbout } from "../components/ChatAbout";
import { SeatChips, type MeasuredSpend } from "../components/SeatChips";
import { DiffView } from "../components/DiffView";
import { parseDiff } from "../lib/runview";
import { Prose } from "../components/Prose";
import { DecisionCard } from "../components/DecisionCard";
import { SurveyCoverageLine } from "../components/SurveyInventory";
import { canChooseFile, chooseFile } from "../lib/picker";

const STAGES = [
  { stage: "intake", kind: "requirements", label: "Requirements", prefix: "REQ", story: "you write this; nobody codes from it" },
  { stage: "spec", kind: "spec", label: "Spec", prefix: "SPEC", story: "ducklings draft; you agree behavior" },
  { stage: "plan", kind: "plan", label: "Plan", prefix: "M", story: "cut into tasks; you birth them" },
] as const;

type StageDef = (typeof STAGES)[number];

export function Cycle({
  client,
  projectId,
  stage,
}: {
  client: EngineClient;
  projectId: string;
  stage?: string;
}) {
  const [active, setActive] = useState<StageDef>(
    () => STAGES.find((s) => s.stage === stage) ?? STAGES[0],
  );
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [errors, setErrors] = useState<TraceError[]>([]);
  // Which stages the check read from a pending proposal rather than the
  // approved artifact. Without this the rail cannot say whether a break is in
  // what you are deciding on or in what you accepted last week.
  const [checkedProposed, setCheckedProposed] = useState<string[]>([]);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [traceDown, setTraceDown] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [brief, setBrief] = useState("");
  // Reference documents for intake and spec: paths to files or folders of
  // .md/.txt, one per line — a wiki outside the project root is the
  // commonest home of adoption context, and run tools cannot reach it.
  const [refsText, setRefsText] = useState("");
  // The intake's path: survey the code (adopt) or interview from a brief.
  // ONE choice, ONE launch button whose label follows it — the old layout
  // had two doors ("Survey the code" boxed at the top, "Draft it" at the
  // bottom) with the shared inputs attached to the second: a person who
  // filled in references was led straight past the survey.
  const [intakePath, setIntakePath] = useState<"adopt" | "brief" | null>(null);
  const [refsOpen, setRefsOpen] = useState(false);
  const refsList = () => refsText.split("\n").map((l) => l.trim()).filter(Boolean);
  async function pickReferenceFile() {
    const path = await chooseFile();
    if (path) setRefsText((current) => (current ? `${current}\n${path}` : path));
  }
  // The plan amendment's text — Review's light exit, separate from the brief
  // so the two doors never share a box.
  const [amendment, setAmendment] = useState("");
  // The amendment's screenshots, as data URLs — the cosmetic change shown,
  // not just described.
  const [amendImages, setAmendImages] = useState<string[]>([]);
  // Which plan action is unfolded. Both panels stacked open read as noise;
  // an action's UI appears when the action is chosen, per the person's own
  // mockup.
  const [planAction, setPlanAction] = useState<"" | "extend" | "amend">("");
  // The fleet, for seat chips and the vision check.
  const [fleet, setFleet] = useState<Duckling[]>([]);
  const [adoptFinding, setAdoptFinding] = useState<ConfigFinding | null>(null);
  // The amendment's own seat: the solo roster's architect.
  const [amendSeat, setAmendSeat] = useState<RosterEntry | null>(null);
  // Per-run seat picks from clicked chips, keyed panel:index. A pick changes
  // THIS run only; the team's saved seats stay untouched.
  const [seatPicks, setSeatPicks] = useState<Record<string, string>>({});
  // Measured spend per duckling — the project's own record, for the chips.
  const [measured, setMeasured] = useState<MeasuredSpend>({});
  useEffect(() => {
    Promise.resolve()
      .then(() => client.report(projectId, "duckling"))
      .then((rep) => {
        const out: MeasuredSpend = {};
        for (const row of rep.rows) out[row.key] = { usd: row.cost_usd, runs: row.runs };
        setMeasured(out);
      })
      .catch(() => setMeasured({}));
  }, [client, projectId]);
  useEffect(() => {
    Promise.all([
      Promise.resolve().then(() => {
        const getRoster = client.RosterGet ?? client.rosterGet ?? client.roster;
        return getRoster.call(client, projectId, "solo");
      }),
      Promise.resolve().then(() => client.ducklings()),
    ])
      .then(([r, ds]) => {
        setFleet(ds);
        setAmendSeat((r.entries ?? []).find((e) => e.role === "architect") ?? null);
      })
      .catch(() => {
        setFleet([]);
        setAmendSeat(null);
      });
  }, [client, projectId]);
  const amendDuckling = seatPicks["amend:0"] || amendSeat?.duckling || "";
  const architectSees = (() => {
    if (!amendDuckling) return null;
    const d = fleet.find((x) => x.id === amendDuckling);
    return d ? Boolean(d.caps?.vision) : null;
  })();
  // How many tasks the spec has not caught up with — the settle button's
  // number, fetched with the artifact so the spec tab can offer the one-click
  // repayment without the person counting markers on the board.
  const [debtCount, setDebtCount] = useState(0);
  const [starting, setStarting] = useState(false);
  const [mode, setMode] = useState("council");
  // A pick belongs to the panel and mode it was made in.
  useEffect(() => {
    setSeatPicks({});
  }, [planAction, mode]);
  const [rounds, setRounds] = useState(2);
  // Calls-per-reply for the stage's seats. The intake that died at 12/12
  // had no launch-time door for more; empty keeps the default, "no cap"
  // lifts it.
  const [agentTurns, setAgentTurns] = useState("");
  const [turnsNoCap, setTurnsNoCap] = useState(false);
  const stageAgentTurns = () => (turnsNoCap ? -1 : Number(agentTurns) || undefined);
  const [roster, setRoster] = useState<RosterEntry[]>([]);
  const [startedRun, setStartedRun] = useState<string | null>(null);
  // Whether the tree already holds code, which decides the doors the empty
  // state offers: a codebase that exists is adopted, not interviewed.
  const [hasCode, setHasCode] = useState(false);
  // What a proposal is shown as. Reading comes first: a person accepting a
  // draft is deciding whether the content is right, and a diff answers a
  // different question — what changed.
  const [proposalAsDiff, setProposalAsDiff] = useState(false);
  // Named for what it is, not what it does: `brief` is already the textarea
  // someone types into, and this is the one a past run was given.
  const [askedFor, setAskedFor] = useState("");
  const [showAsked, setShowAsked] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      const [a, e] = await Promise.all([
        client.artifact(projectId, active.kind),
        client.traceCheck(projectId),
      ]);
      setArtifact(a);
      setErrors(e.errors);
      setCheckedProposed(e.proposed);
    } catch (err) {
      // A project with no artifact yet is not an error worth a red banner, but
      // a real failure must not be shown as "empty" — that reads as "nothing
      // to do here", which is the opposite of the truth.
      setArtifact(null);
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [client, projectId, active.kind]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    // Tasks are the live record; traceShow supplies the Down walk used to keep
    // this thread anchored to the engine's links rather than guessed by the UI.
    Promise.resolve()
      .then(() => client.tasks(projectId))
      .then((ts) => {
        setTasks(ts);
        setDebtCount(ts.filter((t) => t.spec_debt).length);
      })
      .catch(() => {
        setTasks([]);
        setDebtCount(0);
      });
  }, [client, projectId, startedRun]);

  useEffect(() => {
    if (active.stage !== "plan" || !artifact?.sections?.length) {
      setTraceDown({});
      return;
    }
    let cancelled = false;
    const planSections = artifact.sections;
    Promise.all(planSections.map((s) =>
      typeof client["traceShow"] === "function" ? client["traceShow"](projectId, s.id).catch(() => null) : Promise.resolve(null),
    ))
      .then((walks) => {
        if (!cancelled) setTraceDown(Object.fromEntries(planSections.map((s, i) => [s.id, walks[i]])));
      });
    return () => { cancelled = true; };
  }, [active.stage, artifact, client, projectId]);

  // Who will actually do it. A button that says only "Draft it" hides the two
  // things worth knowing before spending minutes and tokens: which models, and
  // whether one of them is going to critique the other.
  // Asked FOR THE CHOSEN MODE, and refetched when it changes: a council
  // line-up overrides the roster at run time, and a preview reading the bare
  // roster warned that one model would critique its own draft while the run
  // was going to use the two the person had saved.
  useEffect(() => {
    const getRoster = client.RosterGet ?? client.rosterGet ?? client.roster;
    (getRoster ? getRoster.call(client, projectId, mode) : Promise.resolve({ entries: [] as RosterEntry[] }))
      .then((r) => setRoster(r.entries))
      .catch(() => setRoster([]));
  }, [client, projectId, mode]);

  // What was asked for, so the document can be read against it. The run id
  // comes from whichever version is on screen: the proposal while one is
  // pending, the accepted document afterwards.
  const briefRun = artifact?.proposal?.run_id ?? artifact?.run_id;
  // The proposing run's legal actions, from the engine. A hardcoded
  // ["accept","request_changes","reject"] here would re-encode the rules this
  // exists to stop encoding; while they load, no button is better than a wrong
  // one.
  const [proposalNext, setProposalNext] = useState<string[]>([]);
  const [proposalRun, setProposalRun] = useState<Run | null>(null);
  // Whether the proposing run was already decided. The lifecycle keeps a
  // rejected proposal on disk (05 §1.1: a failed attempt is a record) — and
  // this view read "file exists" as "decision pending", so a person who had
  // just rejected a draft came back to a screen still awaiting their decision.
  const [proposalDecided, setProposalDecided] = useState(false);
  const proposalRunId = artifact?.proposal?.run_id ?? "";
  useEffect(() => {
    if (!proposalRunId) {
      setProposalNext([]);
      setProposalRun(null);
      return;
    }
    let cancelled = false;
    client
      .run(proposalRunId)
      .then((d) => {
        if (cancelled) return;
        setProposalNext(d.run.next ?? []);
        setProposalRun(d.run);
        setProposalDecided(d.run.status === "done" || d.run.status === "failed");
      })
      .catch(() => !cancelled && setProposalNext([]));
    return () => {
      cancelled = true;
    };
  }, [client, proposalRunId]);
  useEffect(() => {
    if (!briefRun) {
      setAskedFor("");
      return;
    }
    let cancelled = false;
    client
      .runBrief(briefRun)
      .then((b) => !cancelled && setAskedFor(b))
      .catch(() => !cancelled && setAskedFor(""));
    return () => {
      cancelled = true;
    };
  }, [client, briefRun]);

  async function accept() {
    setPromoting(true);
    try {
      await client.promote(projectId, active.kind);
      await load();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setPromoting(false);
    }
  }

  // Starting a stage is the step that was missing: the view could accept a
  // proposal but not produce one, so the first thing anyone wants to do had to
  // be done from a terminal.
  // The third answer. Accept and reject are a verdict on a document that is
  // usually almost right, and "almost" had no button: rejecting left the draft
  // alone and redrafting regenerated the parts you were happy with.
  async function requestChanges(text: string) {
    setFailure(null);
    try {
      const run = await client.stageStart(projectId, active.stage, { revise: text });
      setStartedRun(run.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  }

  // Closing the run matters as much as the verdict: a proposal left undecided
  // sits in the inbox claiming to wait for an answer that was given by walking
  // away. The draft stays on disk — it is the only record of what the
  // ducklings produced.
  async function reject() {
    setFailure(null);
    try {
      if (artifact?.proposal?.run_id) await client.reject(artifact.proposal.run_id);
      await load();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    }
  }

  useEffect(() => {
    client
      .projects()
      .then((ps) => setHasCode(!!ps.find((p) => p.id === projectId)?.has_code))
      .catch(() => setHasCode(false));
  }, [client, projectId]);

  async function start(adopt = false) {
    setStarting(true);
    setFailure(null);
    try {
      // Chip picks override seats for THIS run: architect first, critics
      // after, holes filled from the saved seat they replaced.
      let ducklings: string[] | undefined;
      {
        const seats = stageSeats(mode, roster).map(
          (e, i) => seatPicks[`extend:${i}`] || e.duckling,
        );
        if (seats.some((_d, i) => seatPicks[`extend:${i}`])) ducklings = seats;
      }
      const run = await client.stageStart(projectId, active.stage, {
        from: brief.trim(),
        mode,
        rounds,
        adopt,
        ducklings,
        agentTurns: stageAgentTurns(),
        ...(refsList().length ? { refs: refsList() } : {}),
      });
      setStartedRun(run.id);
      setBrief("");
      if (adopt && typeof client.configDoctor === "function") {
        void client.configDoctor(projectId).then((findings) => setAdoptFinding(findings[0] ?? null)).catch(() => {});
      }
      // The run is where the work is visible. Not navigated to automatically:
      // someone who just wrote a brief may want to read it back, and a view
      // that jumps out from under them is a view that lost their place.
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  }

  // Artifact responses can contain sections from more than one lifecycle
  // stage (for example, a mixed fixture or a document assembled from several
  // sources). A tab must show only its own namespace; using the document kind
  // here is not enough because the response's sections are the rendered data.
  // Also collapse repeated ids: an id is the stable identity of a section, so
  // rendering it twice creates contradictory cards and duplicate React keys.
  const sections = sectionsForStage(artifact?.sections ?? [], active.stage);
  const proposalSections = sectionsForStage(artifact?.proposal?.sections ?? [], active.stage);
  // Only the breaks this stage can answer for: showing a plan's dangling
  // references while the user is reading requirements is noise.
  //
  // Matched against the ids this document actually contains, not against the
  // artifact's own prefix. The plan's prefix is M, but its breaks land on
  // tasks (T-), so a prefix test meant a task implementing nothing was never
  // marked on the one tab that could show it.
  const broken = new Set<string>();
  for (const s of sections) {
    if (errors.some((e) => e.id === s.id)) broken.add(s.id);
    for (const c of s.children ?? []) {
      if (errors.some((e) => e.id === c.id)) broken.add(c.id);
    }
  }

  return (
    <div data-testid="cycle-view" className="flex gap-6">
      <div className="flex-1 min-w-0">
        <div role="tablist" className="flex gap-1 border-b border-hairline mb-4">
          {STAGES.map((s) => (
            <button
              key={s.kind}
              role="tab"
              aria-selected={s.kind === active.kind}
              data-testid={`cycle-tab-${s.stage}`}
              onClick={() => setActive(s)}
              className={
                "px-3 py-2 text-sm border-b-2 -mb-px " +
                (s.kind === active.kind
                  ? "border-hairline text-ink"
                  : "border-transparent text-ink-muted hover:text-ink")
              }
            >
              {s.label}
            </button>
          ))}
        </div>
        <section data-testid="cycle-stage-narrative" className="mb-5">
          <h1 className="text-lg font-medium text-ink">The document chain</h1>
          <div className="mt-2 grid gap-2 sm:grid-cols-3">
            {STAGES.map((s) => (
              <div key={s.kind} data-testid={`cycle-stage-${s.stage}`} className={"rounded-card border p-2 " + (s.kind === active.kind ? "border-ink" : "border-hairline")}>
                <div className="text-sm font-medium text-ink">{s.label}</div>
                <p className="text-xs text-ink-secondary">{s.story}</p>
              </div>
            ))}
          </div>
        </section>

        {failure && (
          <div data-testid="cycle-error" className="mb-4 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* Checking that requirements match what was asked for is the first
            thing anyone does with them, and the brief was previously reachable
            only by digging it out of a prompt in the run log.

            Collapsed: it is a reference, not the subject. */}
        {askedFor && (
          <section className="mb-4 rounded-card border border-hairline" data-testid="asked-for-panel">
            <button
              type="button"
              onClick={() => setShowAsked((v) => !v)}
              aria-expanded={showAsked}
              data-testid="asked-for-toggle"
              className="w-full px-3 py-2 text-left text-sm text-ink-secondary"
            >
              {showAsked ? "▾" : "▸"} What you asked for
            </button>
            {showAsked && (
              <pre
                data-testid="asked-for"
                className="overflow-x-auto whitespace-pre-wrap border-t border-hairline px-3 py-2 text-sm text-ink"
              >
                {askedFor}
              </pre>
            )}
          </section>
        )}

        {artifact?.proposal && proposalDecided && (
          <section data-testid="cycle-rejected-draft" className="mb-6 rounded-card border border-hairline p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="text-sm font-medium text-ink">A rejected draft is on disk</div>
                <div className="text-xs text-ink-muted">
                  You already decided this one. It stays as the record of a failed attempt;
                  a new draft will replace it, or let it go now. Your original brief is in
                  the run&apos;s record either way — reuse it below and edit before starting.
                </div>
              </div>
              <div className="flex items-center gap-2">
                {/* The brief lives in the run's own record, not in the draft —
                    discarding one never touches the other. But the only path
                    back to it was retyping: a person who rejected a draft and
                    wanted another try had to reconstruct their own words from
                    memory while the originals sat on disk. */}
                <button
                  type="button"
                  data-testid="reuse-brief"
                  onClick={() =>
                    void client
                      .runBrief(proposalRunId)
                      .then((b) => {
                        if (b) setBrief(b);
                      })
                      .catch(() => {})
                  }
                  className="rounded border border-hairline px-2 py-1 text-xs"
                >
                  Reuse its brief
                </button>
                <button
                  type="button"
                  data-testid="discard-draft"
                  onClick={() =>
                    void client.artifactDiscard(projectId, active.kind).then(() => load()).catch(() => {})
                  }
                  className="rounded border border-hairline px-2 py-1 text-xs"
                >
                  Discard draft
                </button>
              </div>
            </div>
          </section>
        )}

        {artifact?.proposal && !proposalDecided && (
          <section data-testid="cycle-proposal" className="mb-6 rounded-card border border-serious p-3">
            <DecisionCard
              next={proposalNext}
              title="A run proposes changing this section — read it and decide"
              subtitle={
                artifact.proposal.ducklings
                  ? `from ${artifact.proposal.ducklings.join(", ")}`
                  : undefined
              }
              consequence={`replaces the approved ${active.kind} and closes the run`}
              accepting={promoting}
              onAccept={() => void accept()}
              onReject={() => void reject()}
              onRequestChanges={requestChanges}
              revisionRun={startedRun}
              documentGate
              extraAction={
                <button
                  type="button"
                  data-testid="proposal-view-toggle"
                  aria-pressed={proposalAsDiff}
                  onClick={() => setProposalAsDiff((v) => !v)}
                  className="rounded border border-hairline px-2 py-1 text-xs text-ink-secondary"
                >
                  {proposalAsDiff ? "Read it" : "What changed"}
                </button>
              }
            />
            <p data-testid="proposal-coverage-line" className="mb-2 text-xs text-ink-secondary">
              {coverageLine(errors)}
            </p>
            <SurveyCoverageLine run={proposalRun} testId="proposal-unaccounted" />
            {(artifact.proposal.unread_refs?.length ?? 0) > 0 && (
              <p data-testid="proposal-unread-refs" className="mb-2 text-xs text-warn">
                ⚠ {artifact.proposal.unread_refs!.length} reference document
                {artifact.proposal.unread_refs!.length === 1 ? " was" : "s were"} digested but never
                opened during this run:{" "}
                {artifact.proposal.unread_refs!.map((r) => r.split("/").pop()).join(", ")} — the
                draft may miss their detail.
              </p>
            )}
            {proposalAsDiff ? (
              <DiffView files={parseDiff(artifact.proposal.diff)} />
            ) : proposalSections.length > 0 ? (
              <ol className="space-y-3" data-testid="proposal-sections">
                {proposalSections.map((s) => (
                  <SectionCard
                    key={s.id}
                    section={s}
                    broken={new Set()}
                    tasks={tasks}
                    traceDown={traceDown[s.id]}
                    isPlan={active.stage === "plan"}
                  />
                ))}
              </ol>
            ) : artifact.proposal.markdown ? (
              // No sections parsed but a document exists: show it whole rather
              // than nothing. A draft the parser did not understand is still a
              // draft a person can read and reject.
              <Prose body={stripFront(artifact.proposal.markdown)} suppress={[]} />
            ) : (
              <DiffView files={parseDiff(artifact.proposal.diff)} />
            )}

          </section>
        )}

        {loading && <div className="text-sm text-ink-muted">Loading…</div>}

        {!loading && (!artifact?.proposal || proposalDecided) && (
          <section data-testid="cycle-start" className="mb-6 rounded-card border border-hairline p-3">
            {/* "Redraft" undersold the normal case: growing a project. A brief
                against approved requirements ADDS — the engine hands the
                architect the whole document with orders to keep it — and the
                cycle then carries the feature to spec and plan. Nobody could
                know that from a button that only spoke of redrafting; the user
                who found this gap asked whether features had to arrive as fake
                bug reports. */}
            <div className="mb-2 text-sm text-ink">
              {sections.length === 0
                ? `Draft the ${active.label.toLowerCase()}`
                : active.stage === "intake"
                  ? "Add to the requirements — a feature, a change of scope"
                  : `Extend the ${active.label.toLowerCase()}`}
            </div>
            {active.stage === "plan" && sections.length > 0 && (
              <>
                {/* Two actions, one unfolded at a time — both panels stacked
                    open read as noise; the UI appears when the action is
                    chosen, per the person's own mockup. */}
                <div className="mb-2 flex items-center gap-2" data-testid="plan-actions">
                  {(["extend", "amend"] as const).map((a) => (
                    <button
                      key={a}
                      type="button"
                      data-testid={`plan-action-${a}`}
                      aria-pressed={planAction === a}
                      onClick={() => setPlanAction((cur) => (cur === a ? "" : a))}
                      className={
                        "rounded border px-2 py-1 text-sm " +
                        (planAction === a ? "border-ink text-ink" : "border-hairline text-ink-muted")
                      }
                    >
                      {a === "extend" ? "Extend" : "Amend"}
                    </button>
                  ))}
                </div>
                {planAction === "" && (
                  <p className="mb-2 text-xs text-ink-muted" data-testid="plan-actions-hint">
                    Extend redrafts the plan with the council; Amend adds one to three tasks
                    for a quick change, no redesign.
                  </p>
                )}
              </>
            )}
            {/* The other door. A project initialised on an existing repo went
                mute here: the brief asked what to build as if the product were
                an idea, while forty thousand lines already ran. Adoption
                surveys the tree into the requirements the code ALREADY
                satisfies; the extension flow is then the development model. */}
            {active.stage === "intake" && sections.length === 0 && hasCode && (
              <div
                className="mb-3 rounded-card border border-hairline bg-surface2 p-2"
                data-testid="cycle-adopt-door"
              >
                <p className="text-sm text-ink">This project already has code. Where do the requirements come from?</p>
                <div className="mt-2 flex flex-col gap-1 text-sm" role="radiogroup" aria-label="intake path">
                  <label className="flex items-start gap-2">
                    <input
                      type="radio"
                      name="intake-path"
                      data-testid="cycle-adopt"
                      checked={(intakePath ?? "adopt") === "adopt"}
                      onChange={() => setIntakePath("adopt")}
                    />
                    <span>
                      <span className="text-ink">Survey the code</span>{" "}
                      <span className="text-xs text-ink-muted">
                        — the council reads the tree and drafts the requirements the code already
                        satisfies. The brief and references below travel along as context.
                      </span>
                    </span>
                  </label>
                  <label className="flex items-start gap-2">
                    <input
                      type="radio"
                      name="intake-path"
                      data-testid="cycle-greenfield"
                      checked={intakePath === "brief"}
                      onChange={() => setIntakePath("brief")}
                    />
                    <span>
                      <span className="text-ink">Start from the brief alone</span>{" "}
                      <span className="text-xs text-ink-muted">— as if greenfield; the code is ignored.</span>
                    </span>
                  </label>
                </div>
              </div>
            )}
            {active.stage === "intake" && (
              <>
                <textarea
                  aria-label="brief"
                  data-testid="cycle-brief"
                  rows={4}
                  placeholder={
                    sections.length === 0
                      ? "What do you want built? A paragraph is enough. A file path works too."
                      : "Describe the new feature. Existing requirements survive with their ids; the diff at the gate shows exactly what was added."
                  }
                  value={brief}
                  onChange={(e) => setBrief(e.target.value)}
                  className="mb-2 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                />
                <p className="mb-2 text-xs text-ink-muted">
                  {sections.length === 0
                    ? "Leave it empty and the council will interview you instead, asking questions you answer in the run."
                    : "Accept the new requirements, then run spec and plan the same way — each stage extends its document and the new tasks arrive with full traceability."}
                </p>
              </>
            )}
            {active.stage !== "intake" && (
              <p className="mb-2 text-xs text-ink-muted">
                {active.stage === "spec"
                  ? "Reads the accepted requirements and proposes a specification."
                  : "Reads the accepted spec and proposes milestones and tasks."}
              </p>
            )}
            {(active.stage === "intake" || active.stage === "spec") && (
              <div className="mb-2">
                {/* A door, not a form field: most launches carry no
                    references, and an always-open input would tax the card.
                    Open, it is the same quiet surface2 input the brief is. */}
                {!refsOpen && refsList().length === 0 ? (
                  <button
                    type="button"
                    data-testid="cycle-refs-door"
                    onClick={() => setRefsOpen(true)}
                    className="text-xs text-ink-muted underline hover:text-ink"
                  >
                    attach reference documents…
                  </button>
                ) : (
                  <>
                    <label className="mb-1 block text-xs text-ink-muted" htmlFor="cycle-refs">
                      reference documents — paths to .md/.txt files or folders, one per line
                      (loaded bounded into the prompt; the run records what was included)
                    </label>
                    <div className="flex items-start gap-2">
                      <textarea
                        id="cycle-refs"
                        data-testid="cycle-refs"
                        rows={2}
                        placeholder={"~/wiki/Desarrollo/miempresa/MiEmpresa.md\n~/wiki/Desarrollo/miempresa/feedback-pipeline.md"}
                        value={refsText}
                        onChange={(e) => setRefsText(e.target.value)}
                        className="min-w-0 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 font-mono text-xs"
                      />
                      {canChooseFile() && (
                        <button
                          type="button"
                          data-testid="cycle-refs-pick"
                          onClick={() => void pickReferenceFile()}
                          className="rounded border border-hairline px-2 py-1 text-xs text-ink-secondary"
                        >
                          Browse…
                        </button>
                      )}
                    </div>
                  </>
                )}
              </div>
            )}
            {active.stage === "spec" && debtCount > 0 && (
              <div className="mb-3 rounded-card border border-hairline p-2" data-testid="spec-settle">
                <p className="mb-1 text-xs text-ink-muted">
                  {debtCount} task(s) wear spec-debt: the plan grew without a redesign and the spec
                  has not caught up. One click — the engine assembles the revision from the debt
                  itself; you accept the diff at the gate, and the markers come off.
                </p>
                <button
                  type="button"
                  data-testid="spec-settle-start"
                  disabled={starting}
                  onClick={() => {
                    setStarting(true);
                    setFailure(null);
                    void client
                      .stageStart(projectId, "spec", { settle: true })
                      .then((run) => setStartedRun(run.id))
                      .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
                      .finally(() => setStarting(false));
                  }}
                  className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
                >
                  Settle spec-debt ({debtCount})
                </button>
              </div>
            )}
            {active.stage === "plan" && sections.length > 0 && planAction === "amend" && (
              <div className="mb-3 rounded-card border border-hairline p-2" data-testid="plan-extend">
                {amendSeat && (
                  <div className="mb-1">
                    <SeatChips
                      entries={[{ ...amendSeat, duckling: amendDuckling }]}
                      fleet={fleet}
                      measured={measured}
                      onPick={(i, id) => setSeatPicks((cur) => ({ ...cur, [`amend:${i}`]: id }))}
                    />
                  </div>
                )}
                <p className="mb-1 text-xs text-ink-muted">
                  Small change, no redesign: an architect amends the plan with one to three tasks.
                  Tasks no spec section covers wear a spec-debt marker. If it changes what the
                  product IS, write a brief instead.
                </p>
                <textarea
                  aria-label="plan amendment"
                  data-testid="plan-extend-text"
                  rows={2}
                  placeholder="What to add or improve, in a sentence or two"
                  value={amendment}
                  onChange={(e) => setAmendment(e.target.value)}
                  className="mb-1 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                />
                <div className="mb-1 flex flex-wrap items-center gap-2">
                  <label className="cursor-pointer text-xs text-ink-muted underline">
                    add a screenshot
                    <input
                      type="file"
                      accept="image/*"
                      multiple
                      className="hidden"
                      data-testid="plan-extend-image"
                      onChange={(e) => {
                        const files = Array.from(e.target.files ?? []).slice(0, 3);
                        e.target.value = "";
                        for (const f of files) {
                          const reader = new FileReader();
                          reader.onload = () =>
                            setAmendImages((cur) => [...cur, String(reader.result)].slice(0, 3));
                          reader.readAsDataURL(f);
                        }
                      }}
                    />
                  </label>
                  {amendImages.map((_, i) => (
                    <span key={i} data-testid="plan-extend-image-chip" className="flex items-center gap-1 rounded-full border border-hairline px-2 py-0.5 text-xs text-ink-secondary">
                      image {i + 1}
                      <button
                        type="button"
                        aria-label={`remove image ${i + 1}`}
                        onClick={() => setAmendImages(amendImages.filter((_, j) => j !== i))}
                        className="text-ink-muted"
                      >
                        ✕
                      </button>
                    </span>
                  ))}
                </div>
                {amendImages.length > 0 && architectSees === false && (
                  <p className="mb-1 text-xs text-warn" data-testid="plan-extend-vision-warn">
                    the architect seated for amendments cannot see images — these will be
                    dropped. Seat a vision model (Settings → my ducklings) for the screenshot
                    to count.
                  </p>
                )}
                <button
                  type="button"
                  data-testid="plan-extend-start"
                  disabled={!amendment.trim() || starting}
                  onClick={() => {
                    setStarting(true);
                    setFailure(null);
                    void client
                      .stageStart(projectId, "plan", {
                        extend: amendment.trim(),
                        images: amendImages.length ? amendImages : undefined,
                        ducklings: seatPicks["amend:0"] ? [seatPicks["amend:0"]] : undefined,
                        agentTurns: stageAgentTurns(),
                      })
                      .then((run) => {
                        setStartedRun(run.id);
                        setAmendment("");
                        setAmendImages([]);
                      })
                      .catch((err) => setFailure(err instanceof Error ? err.message : String(err)))
                      .finally(() => setStarting(false));
                  }}
                  className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
                >
                  Amend the plan
                </button>
              </div>
            )}
            {(active.stage !== "plan" || sections.length === 0 || planAction === "extend") && (
            <>
            <div className="mb-2 flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
              <select
                aria-label="mode"
                data-testid="stage-mode"
                value={mode}
                onChange={(e) => setMode(e.target.value)}
                className="rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
              >
                <option value="council">council — one drafts, the others critique</option>
                <option value="solo">solo — one model, one draft</option>
              </select>
              {mode === "council" && (
                <label className="flex items-center gap-1 text-xs text-ink-secondary">
                  at most
                  <input
                    type="number"
                    min={1}
                    max={5}
                    aria-label="rounds"
                    data-testid="stage-rounds"
                    value={rounds}
                    onChange={(e) => setRounds(Math.max(1, Number(e.target.value) || 1))}
                    className="w-14 rounded border border-hairline bg-surface2 px-2 py-1"
                  />
                  {rounds === 1 ? "round" : "rounds"}
                </label>
              )}
              <input
                aria-label="calls per reply"
                data-testid="stage-agent-turns"
                placeholder={turnsNoCap ? "no cap" : "calls/reply (default)"}
                disabled={turnsNoCap}
                value={turnsNoCap ? "" : agentTurns}
                onChange={(e) => setAgentTurns(e.target.value)}
                className="w-32 rounded border border-hairline bg-surface2 px-2 py-1 text-xs disabled:opacity-40"
              />
              <label
                className="flex items-center gap-1 text-xs text-ink-muted"
                title="no cap on model calls per reply — the token and cost budgets still guard"
              >
                <input
                  type="checkbox"
                  data-testid="stage-turns-nocap"
                  checked={turnsNoCap}
                  onChange={(e) => setTurnsNoCap(e.target.checked)}
                />
                no cap
              </label>
              <span data-testid="stage-who" className="text-xs text-ink-muted">
                {describeRun(mode, roster, rounds)}
              </span>
            </div>
            {/* The seats under the mode that decides them — mode first, who
                second, on every document stage alike. Chips stay doors: a
                click picks a different duckling for this run only. */}
            {roster.length > 0 && (
              <div className="mb-2">
                <SeatChips
                  entries={stageSeats(mode, roster).map((e, i) => ({
                    ...e,
                    duckling: seatPicks[`extend:${i}`] || e.duckling,
                    provenance: seatPicks[`extend:${i}`] ? "picked now" : (e.source === "project pin" ? "project" : "global"),
                  }))}
                  fleet={fleet}
                  measured={measured}
                  onPick={(i, id) => setSeatPicks((cur) => ({ ...cur, [`extend:${i}`]: id }))}
                />
              </div>
            )}
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => void start(active.stage === "intake" && sections.length === 0 && hasCode && (intakePath ?? "adopt") === "adopt")}
                disabled={starting}
                data-testid="cycle-run"
                className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
              >
                {starting
                  ? "Starting…"
                  : active.stage === "intake" && sections.length === 0 && hasCode && (intakePath ?? "adopt") === "adopt"
                    ? "Survey the code"
                    : sections.length === 0
                      ? "Draft it"
                      : "Redraft"}
              </button>
              {adoptFinding && fleet.length > 0 && (
                <div className="rounded border border-warning p-2 text-xs" data-testid="adopt-config-offer">
                  <p><code>{adoptFinding.key}</code> needs attention: {adoptFinding.reason}</p>
                  <ChatAbout client={client} projectId={projectId} aboutKind="ducklab" aboutId="configuration" ducklings={fleet} label="Ask the configuration consultant" initialMessage={`The adoption is complete. Please prioritize this configuration finding and explain the safe amendment: ${adoptFinding.key} → ${adoptFinding.proposed}. Reason: ${adoptFinding.reason}`} />
                </div>
              )}
              {startedRun && (
                <a
                  href={`#/runs/${startedRun}`}
                  data-testid="cycle-run-link"
                  className="text-sm text-ink underline"
                >
                  watch the run
                </a>
              )}
              {sections.length > 0 && (
                // Said out loud: redrafting does not overwrite anything until
                // the proposal is accepted, and a person about to click a
                // button on work they already approved deserves to know that.
                <span className="text-xs text-ink-muted">
                  leaves the accepted document alone until you accept the proposal
                </span>
              )}
            </div>
            </>
            )}
          </section>
        )}

        {/* When a proposal includes parsed sections, it is the document being
            decided on. Do not print the approved copy underneath it as well:
            the same stable section id would appear twice on the page. */}
        {!(artifact?.proposal && !proposalDecided && proposalSections.length > 0) && (
          <ol className="space-y-3">
            {sections.map((s) => (
              <SectionCard key={s.id} section={s} broken={broken} tasks={tasks} traceDown={traceDown[s.id]} isPlan={active.stage === "plan"} />
            ))}
          </ol>
        )}
      </div>

      <aside data-testid="trace-rail" className="w-72 shrink-0">
        <h2 className="text-sm font-medium text-ink mb-2">Traceability</h2>
        {checkedProposed.length > 0 && (
          <p data-testid="trace-scope" className="text-xs text-ink-muted mb-2">
            Checking the proposed {checkedProposed.join(", ")} — this is what you are
            about to accept.
          </p>
        )}
        {!artifact?.proposal && (
          <p data-testid="coverage-line" className="mb-2 text-xs text-ink-secondary">
            {coverageLine(errors)}
          </p>
        )}
        {errors.length === 0 ? (
          <p data-testid="trace-clean" className="text-sm text-good">
            The cycle is linked end to end.
          </p>
        ) : (
          <>
            <p className="text-sm text-serious mb-2">
              {errors.length} break{errors.length === 1 ? "" : "s"} in the spine
            </p>
            <ul className="space-y-2">
              {errors.map((e, i) => (
                <li key={`${e.id}-${i}`} data-testid="trace-error" className="text-xs">
                  <span className="font-mono text-ink">{e.id}</span>
                  <span className="text-ink-muted"> — {e.detail}</span>
                </li>
              ))}
            </ul>
          </>
        )}
      </aside>
    </div>
  );
}

function SectionCard({ section, broken, tasks = [], traceDown, isPlan = false }: { section: Section; broken: Set<string>; tasks?: Task[]; traceDown?: unknown; isPlan?: boolean }) {
  // The Down walk is the engine's link, not a UI guess based on where a task
  // happens to be printed. Keep only task ids found in that walk, then use the
  // task record for its live state.
  const linkedIds = new Set(taskIdsFromTrace(traceDown));
  const childTasks = tasks.filter((task) => linkedIds.has(task.id));
  // The engine calls delivered work "accepted"; "done" is retained for
  // older task fixtures and responses. Both mean the change is in the tree.
  const landedTask = childTasks.find((task) => ["accepted", "done"].includes(task.status.toLowerCase()));
  const liveState = isPlan
    ? childTasks.length === 0
      ? "no task born yet"
      : childTasks.some((task) => task.blocked || task.waiting || ["queued", "paused"].includes(task.status.toLowerCase()))
        ? "waiting at its gate"
        : landedTask
          ? `${landedTask.id} landed`
          : "work is in progress"
    : undefined;
  return (
    <li
      data-testid="cycle-section"
      data-broken={broken.has(section.id) ? "true" : "false"}
      className={
        "rounded-card border p-3 " +
        (broken.has(section.id) ? "border-serious" : "border-hairline")
      }
    >
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-xs text-ink-muted">{section.id}</span>
        <span className="text-sm font-medium text-ink">{section.title}</span>
      </div>
      {liveState && (
        <p data-testid="cycle-live-state" data-trace-loaded={traceDown ? "true" : "false"} className="mt-1 text-xs text-ink-secondary">
          {liveState}
        </p>
      )}
      {section.implements && section.implements.length > 0 && (
        <div className="mt-1 text-xs text-ink-muted">
          implements {section.implements.join(", ")}
        </div>
      )}
      <Prose body={section.body} className="mt-2 space-y-2 text-sm text-ink-secondary" />
      {section.children && section.children.length > 0 && (
        <ul className="mt-2 space-y-1 pl-3 border-l border-hairline">
          {section.children.map((c) => (
            <li
              key={c.id}
              data-testid="cycle-child"
              data-id={c.id}
              data-broken={broken.has(c.id) ? "true" : "false"}
              className={"text-xs " + (broken.has(c.id) ? "text-serious" : "")}
            >
              <span className="font-mono text-ink-muted">{c.id}</span>{" "}
              <span className="text-ink">{c.title}</span>
              {/* A task's Implements line is the edge that makes the plan
                  traceable. Rendering only id and title made the Plan tab look
                  like it had no traceability at all, when every task had it. */}
              {c.implements && c.implements.length > 0 && (
                <span className="text-ink-muted"> — implements {c.implements.join(", ")}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

function sectionsForStage(sections: readonly Section[], stage: string): Section[] {
  const seen = new Set<string>();
  const belongsToStage = (id: string) =>
    stage === "intake"
      ? id.startsWith("REQ-")
      : stage === "spec"
        ? id.startsWith("SPEC-")
        : id.startsWith("M-") || id.startsWith("T-");
  const filter = (items: readonly Section[]): Section[] => items.flatMap((section) => {
    // Section ids are the engine's stage attribution. Do not let a mixed
    // artifact leak spec/plan material into the Requirements tab.
    if (!belongsToStage(section.id) || seen.has(section.id)) return [];
    seen.add(section.id);
    return [{ ...section, children: section.children ? filter(section.children) : section.children }];
  });
  return filter(sections);
}

function taskIdsFromTrace(value: unknown): string[] {
  const ids: string[] = [];
  function walk(v: unknown) {
    if (typeof v === "string") {
      if (/^T-\d+$/.test(v)) ids.push(v);
      return;
    }
    if (Array.isArray(v)) {
      v.forEach(walk);
      return;
    }
    if (v && typeof v === "object") Object.values(v).forEach(walk);
  }
  walk(value);
  return ids;
}

function coverageLine(errors: TraceError[]): string {
  // A trace check reports several kinds of breaks. Only a missing downstream
  // task is honest to describe as a section without work; orphan requirements
  // and unjustified tasks are different facts shown in the rail below.
  const missing = errors.filter((e) => e.kind === "unimplemented_spec" || /(?:no|missing).*task/i.test(e.detail));
  return missing.length === 0
    ? "Every normative section has work behind it."
    : `${missing.length} sections have no task yet.`;
}

/** Drops the frontmatter a document carries for machines.
 *
 * The card above already says which ducklings wrote it and that it is awaiting
 * a decision; repeating that as raw YAML is noise between a person and the
 * thing they are deciding about. */
function stripFront(md: string): string {
  if (!md.startsWith("---\n")) return md;
  const end = md.indexOf("\n---\n", 4);
  return end < 0 ? md : md.slice(end + 5);
}

/** Names the ducklings that will actually take part.
 *
 * The roster resolves every role whether or not the project declares it, so
 * this reports what the run will do rather than what the file says. Solo runs
 * the architect alone: naming a reviewer that will never be asked would be
 * worse than naming nobody.
 */
/** The seats a document stage ACTUALLY runs: the architect drafts, and in
 * council the reviewer entries critique — one per critic. The full roster
 * also lists implementer, judge, triager and scribe, and rendering those
 * here claimed models a redraft never calls. Chips are a promise about who
 * participates; only the participants earn one. */
function stageSeats(mode: string, roster: readonly RosterEntry[]): RosterEntry[] {
  const seats = roster.filter(
    (r) => r.role === "architect" || (mode !== "solo" && r.role === "reviewer"),
  );
  // Council language on the chip: a critic critiques; "reviewer" is the
  // build pair's word.
  return seats.map((r) => (r.role === "reviewer" ? { ...r, role: "critic" } : r));
}

function describeRun(mode: string, roster: readonly RosterEntry[], rounds = 2): string {
  const architect = roster.find((r) => r.role === "architect")?.duckling;
  if (!architect) return "roster not loaded";
  if (mode === "solo") return `${architect} drafts, and nothing reviews it`;
  // One reviewer entry per critic: the engine lists them all, and each critique
  // turn runs pinned to its own model.
  const critics = roster.filter((r) => r.role === "reviewer").map((r) => r.duckling);
  if (critics.length === 0 || (critics.length === 1 && critics[0] === architect)) {
    // Both sides on one duckling measures self-consistency, not review
    // (05 §3.2). Said here, where the choice is still open.
    return `${architect} drafts and critiques its own draft — set a second duckling in Ducklings`;
  }
  const listed =
    critics.length === 1 ? critics[0] : `${critics.slice(0, -1).join(", ")} and ${critics[critics.length - 1]}`;
  // The stop condition is EVERY critic approving, so the limit is a ceiling
  // and not a plan: raising it costs nothing on a draft that converges.
  return `${architect} drafts, ${listed} ${critics.length === 1 ? "critiques" : "each critique"}` +
    (rounds > 1 ? `, and they go round again unless ${critics.length === 1 ? critics[0] : "every critic"} approves` : "");
}

