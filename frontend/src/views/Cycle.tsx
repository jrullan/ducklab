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

import { useCallback, useEffect, useRef, useState } from "react";
import type { Artifact, ConfigFinding, Duckling, EngineClient, RosterEntry, Run, Section, Task, TraceError } from "../api/client";
import { ChatAbout } from "../components/ChatAbout";
import { SeatChips, type MeasuredSpend } from "../components/SeatChips";
import { DiffView } from "../components/DiffView";
import { parseDiff } from "../lib/runview";
import { Prose } from "../components/Prose";
import { DecisionCard } from "../components/DecisionCard";
import { SurveyCoverageLine } from "../components/SurveyInventory";
import { canChooseFile, chooseFile } from "../lib/picker";
import { routeHref } from "../app/routes";

const STAGES = [
  { stage: "intake", kind: "requirements", label: "Requirements", prefix: "REQ", story: "you write this; nobody codes from it" },
  { stage: "spec", kind: "spec", label: "Spec", prefix: "SPEC", story: "ducklings draft; you agree behavior" },
  { stage: "plan", kind: "plan", label: "Plan", prefix: "M", story: "cut into tasks; you birth them" },
] as const;

type StageDef = (typeof STAGES)[number];
type TraceNode = { id: string; kind: string; title: string; up?: string[]; down?: string[] };

export function Cycle({
  client,
  projectId,
  stage,
  section,
}: {
  client: EngineClient;
  projectId: string;
  stage?: string;
  section?: string;
}) {
  const [active, setActive] = useState<StageDef>(
    () => STAGES.find((s) => s.stage === stage) ?? STAGES[0],
  );
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [errors, setErrors] = useState<TraceError[]>([]);
  // Retained for compatibility with the engine's proposal scope response; the
  // visible surface is the pinned frame health line, not a trace rail.
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
  const [indexQuery, setIndexQuery] = useState("");
  const [indexFilter, setIndexFilter] = useState<"all" | "breaks" | "no-task">("all");
  const [selectedSection, setSelectedSection] = useState<string | null>(section ?? null);
  const [traceChain, setTraceChain] = useState<TraceNode[]>([]);
  const [operationOpen, setOperationOpen] = useState(false);
  const [requirementIds, setRequirementIds] = useState<Set<string>>(new Set());
  const detailRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const nextStage = STAGES.find((candidate) => candidate.stage === stage);
    if (nextStage) setActive(nextStage);
    setSelectedSection(section ?? null);
  }, [stage, section]);

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
    let cancelled = false;
    const source = active.stage === "intake"
      ? Promise.resolve(artifact)
      : client.artifact(projectId, "requirements").catch(() => null);
    source.then((a) => {
      if (cancelled) return;
      const ids = new Set<string>();
      const collect = (items: readonly Section[] | null | undefined) => items?.forEach((s) => {
        if (s.id.startsWith("REQ-")) ids.add(s.id);
        collect(s.children);
      });
      collect(a?.sections);
      setRequirementIds(ids);
    });
    return () => { cancelled = true; };
  }, [active.stage, artifact, client, projectId]);

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
  useEffect(() => {
    if (!selectedSection) return;
    const section = detailRef.current?.querySelector<HTMLElement>(`[id="cycle-section-${selectedSection}"]`);
    if (typeof section?.scrollIntoView === "function") section.scrollIntoView({ block: "start" });
  }, [selectedSection]);

  useEffect(() => {
    setIndexQuery("");
    setIndexFilter("all");
  }, [active.stage]);

  // The pinned index is the one finder. Keep it reachable without making a
  // second, overlapping palette compete for the same sections.
  useEffect(() => {
    const focusIndex = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        document.getElementById("cycle-index-filter")?.focus();
      }
    };
    window.addEventListener("keydown", focusIndex);
    return () => window.removeEventListener("keydown", focusIndex);
  }, []);

  // While a parsed proposal is open, the detail pane shows that document; keep
  // the index pointing at the same ids rather than at the hidden accepted copy.
  const indexedRoots = artifact?.proposal && !proposalDecided && proposalSections.length > 0
    ? proposalSections
    : sections;
  // Tasks are sections in the plan's document too. Flatten only the index so
  // nested plan work is findable without changing the document's reading shape.
  const indexedSections = indexedRoots.flatMap((s) => [s, ...(s.children ?? [])]);
  // Counts and markers are intentionally computed by the same trace/task join.
  // This prevents a chip from claiming a different truth than the health line.
  const markers = traceMarkers(indexedSections, errors, tasks, traceDown, active.stage, requirementIds);
  const broken = new Set(markers.filter((m) => m.break).map((m) => m.id));
  const sectionHasNoTask = (s: Section) => markers.find((m) => m.id === s.id)?.noTask ?? false;
  const visibleSections = indexedSections.filter((s) => {
    const q = indexQuery.trim().toLowerCase();
    const matchesText = !q || `${s.id} ${s.title}`.toLowerCase().includes(q);
    const matchesState = indexFilter === "all"
      || (indexFilter === "breaks" && broken.has(s.id))
      || (indexFilter === "no-task" && sectionHasNoTask(s));
    return matchesText && matchesState;
  });
  const inspectedSection = indexedSections.find((section) => section.id === selectedSection);
  const focusedSections = selectedSection && inspectedSection ? [inspectedSection] : [];
  const inspectedMarker = markers.find((marker) => marker.id === inspectedSection?.id);
  const inspectedErrors = errors.filter((error) => error.id === inspectedSection?.id);
  const selectDocumentSection = (id: string | null) => {
    if (!id) {
      setSelectedSection(null);
      location.hash = routeHref({ name: "cycle", stage: active.stage });
      return;
    }
    const target = id.startsWith("REQ-") ? STAGES[0] : id.startsWith("SPEC-") ? STAGES[1] : STAGES[2];
    setActive(target);
    setSelectedSection(id);
    location.hash = routeHref({ name: "cycle", stage: target.stage, section: id });
  };

  useEffect(() => {
    if (!selectedSection) {
      setTraceChain([]);
      return;
    }
    let cancelled = false;
    const loadChain = async () => {
      const found = new Map<string, TraceNode>();
      const visit = async (id: string, depth: number) => {
        if (found.has(id) || depth > 2) return;
        const raw = await client.traceShow(projectId, id).catch(() => null);
        if (!raw || typeof raw.id !== "string") return;
        const node: TraceNode = {
          id: raw.id,
          kind: typeof raw.kind === "string" ? raw.kind : "section",
          title: typeof raw.title === "string" ? raw.title : raw.id,
          up: Array.isArray(raw.up) ? raw.up.filter((value): value is string => typeof value === "string") : [],
          down: Array.isArray(raw.down) ? raw.down.filter((value): value is string => typeof value === "string") : [],
        };
        found.set(id, node);
        await Promise.all([...(node.up ?? []), ...(node.down ?? [])].map((related) => visit(related, depth + 1)));
      };
      await visit(selectedSection, 0);
      if (!cancelled) setTraceChain([...found.values()]);
    };
    void loadChain();
    return () => { cancelled = true; };
  }, [client, projectId, selectedSection]);
  const stageAction = inspectedSection ? `Propose change to ${inspectedSection.id}` : active.stage === "intake"
    ? sections.length === 0 ? "Draft requirements" : "Enter requirements"
    : active.stage === "spec"
      ? sections.length === 0 ? "Draft specification" : "Propose specification update"
      : sections.length === 0 ? "Draft plan" : "Extend plan";

  return (
    <div data-testid="cycle-view" className="flex h-[calc(100vh-4rem)] min-h-0 flex-col overflow-hidden">
      <header data-testid="cycle-frame-header" id="cycle-ledger" className="sticky top-0 z-10 border-b border-hairline bg-surface pb-3">
        <div className="flex items-center gap-3 py-3">
          <div className="min-w-0 flex-1"><h1 className="text-xl font-semibold text-ink">Documents</h1><p className="text-xs text-ink-muted">Requirements, specification and plan — one traceable project spine.</p></div>
          <a href="#/cycle/ledger" className="rounded border border-hairline px-3 py-1.5 text-sm text-ink-secondary hover:text-ink">Review issues</a>
          {(!artifact?.proposal || proposalDecided) && <button type="button" data-testid="cycle-primary-action" onClick={() => { if (active.stage === "plan" && sections.length > 0) setPlanAction("extend"); setOperationOpen(true); }} className="rounded bg-ink px-3 py-1.5 text-sm font-medium text-page">+ {stageAction}</button>}
        </div>
        <div role="tablist" aria-label="Document stage" data-testid="cycle-stage-control" className="grid grid-cols-3 gap-3">
          {STAGES.map((s) => (
            <button
              key={s.kind}
              role="tab"
              aria-selected={s.kind === active.kind}
              data-testid={`cycle-tab-${s.stage}`}
              onClick={() => { setActive(s); setSelectedSection(null); location.hash = routeHref({ name: "cycle", stage: s.stage }); }}
              className={"rounded-card border px-4 py-3 text-left transition-colors " + (s.kind === active.kind ? "border-warning bg-surface2 text-ink" : "border-hairline text-ink-muted hover:bg-surface2 hover:text-ink")}
            >
              <span className="flex items-center gap-2"><span className="flex h-5 w-5 items-center justify-center rounded-full bg-surface1 text-xs">{STAGES.indexOf(s) + 1}</span><span className="font-medium">{s.label}</span></span>
              <span className="mt-1 block text-xs font-normal text-ink-muted" data-testid={s.kind === active.kind ? "cycle-stage-caption" : undefined}>{s.kind === active.kind ? `${indexedSections.length} sections · current view` : s.story}</span>
            </button>
          ))}
        </div>
        <section data-testid="cycle-stage-narrative" className="sr-only"><h2>The document chain</h2><p>{STAGES.map((s) => s.story).join(" ")}</p></section>
        <div data-testid="cycle-health" className={`mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 rounded border px-3 py-2 text-xs ${errors.length ? "border-warning bg-surface2 text-ink" : "border-hairline text-ink-secondary"}`}>
          <a href="#/cycle/ledger" className="font-medium underline-offset-2 hover:underline"><span>{errors.length === 0 ? "✓ 0 breaks in the spine — the spine is intact" : <>⚠ {errors.length} break{errors.length === 1 ? "" : "s"} in the spine</>}</span></a>
          <p data-testid="cycle-coverage-line">{coverageLine(errors)}</p>
          <a href="#/cycle/ledger" className="ml-auto rounded border border-hairline px-2 py-1 hover:text-ink">Review issues</a>
        </div>
      </header>
      <div className="grid min-h-0 flex-1 grid-cols-[15rem_minmax(0,1fr)] overflow-hidden xl:grid-cols-[17rem_minmax(24rem,1fr)_19rem]">
        <nav data-testid="cycle-index" aria-label={`${active.label} section index`} className="sticky top-0 min-h-0 overflow-y-auto border-r border-hairline py-4 pr-3">
          <div className="mb-3 flex items-center justify-between"><h2 className="font-medium text-ink">{active.label}</h2><span className="text-xs text-ink-muted">{indexedSections.length}</span></div>
          <label className="sr-only" htmlFor="cycle-index-filter">Find a section</label>
          <input id="cycle-index-filter" data-testid="cycle-index-filter" value={indexQuery} onChange={(e) => setIndexQuery(e.target.value)} placeholder="Find a section…" className="mb-2 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm" />
          <div className="mb-2 flex gap-1" role="group" aria-label="section state filters">
            {([["all", "All"], ["breaks", "Breaks"], ["no-task", "No task yet"]] as const).map(([value, label]) => (
              <button
                key={value}
                type="button"
                data-testid={`cycle-filter-${value}`}
                aria-pressed={indexFilter === value}
                onClick={() => setIndexFilter(value)}
                className="rounded border border-hairline px-1.5 py-0.5 text-xs text-ink-muted aria-pressed:border-ink aria-pressed:text-ink"
              >{label}</button>
            ))}
          </div>
          <ol className="space-y-1" data-testid="cycle-index-entries">
            {visibleSections.map((s) => {
              const hasBreak = broken.has(s.id);
              const noTask = sectionHasNoTask(s);
              return <li key={s.id}>
                <button type="button" data-testid="cycle-index-row" data-id={s.id} aria-current={selectedSection === s.id ? "true" : undefined} onClick={() => selectDocumentSection(s.id)} className={"w-full rounded-r border-l-4 px-2 py-1.5 text-left text-xs transition-colors " + (selectedSection === s.id ? "border-warning bg-surface1 font-medium text-ink shadow-sm" : "border-transparent text-ink-secondary hover:bg-surface2 hover:text-ink")}>
                  <span className="font-mono text-ink-muted">{s.id}</span><span className="ml-2">{s.title}</span>
                  {hasBreak && <span className="ml-1 text-serious" title="break">break</span>}
                  {noTask && <span className="ml-1 text-warn" title="no task yet">no task yet</span>}
                  {artifact?.proposal && !proposalDecided && <span className="ml-1 text-ink-muted" title="proposal pending">proposal pending</span>}
                </button>
              </li>;
            })}
          </ol>
        </nav>
        <main ref={detailRef} data-testid="cycle-detail" className="flex min-h-0 min-w-0 flex-col overflow-y-auto overscroll-contain px-6 py-4">
        <div className="mb-4 flex items-center gap-2 text-xs text-ink-muted">
          {selectedSection ? <button type="button" data-testid="cycle-show-all" onClick={() => selectDocumentSection(null)} className="font-medium text-ink underline-offset-2 hover:underline">← Clear selection</button> : <span>{active.label}</span>}
          {selectedSection && inspectedSection && <><span>/</span><span className="font-mono text-ink">{inspectedSection.id}</span></>}
        </div>

        {failure && (
          <div data-testid="cycle-error" className="mb-4 text-sm text-critical">
            {failure}
          </div>
        )}

        {/* The person's words are the first thing to read, before machine text. */}
        {askedFor && (
          <section className="order-1 mb-4 rounded-card border border-hairline" data-testid="asked-for-panel">
            <div className="px-3 py-2 text-sm text-ink-secondary">What you asked for</div>
            <pre
              data-testid="asked-for"
              className="overflow-x-auto whitespace-pre-wrap border-t border-hairline px-3 py-2 text-sm text-ink"
            >
              {askedFor}
            </pre>
          </section>
        )}

        {artifact?.proposal && proposalDecided && (
          <section data-testid="cycle-rejected-draft" className="order-2 mb-6 rounded-card border border-hairline p-3">
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
          <section data-testid="cycle-proposal" className="order-2 mb-6 rounded-card border border-serious p-3">
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
                {(selectedSection ? focusedSections : proposalSections).map((s) => (
                  <SectionCard
                    key={s.id}
                    section={s}
                    broken={broken}
                    tasks={tasks}
                    traceDown={traceDown[s.id]}
                    isPlan={active.stage === "plan"}
                    proposalPending
                    requirementIds={requirementIds}
                    focused={Boolean(selectedSection)}
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

        {!loading && !selectedSection && !(artifact?.proposal && !proposalDecided) && (
          <DocumentSelectionEmpty stage={active.stage} />
        )}

        {/* The document stays ahead of its redraft machinery in reading order. */}
        {!(artifact?.proposal && !proposalDecided && proposalSections.length > 0) && (
          <ol className="order-3 space-y-3" data-testid="cycle-sections">
            {focusedSections.map((s) => (
              <SectionCard key={s.id} section={s} broken={broken} tasks={tasks} traceDown={traceDown[s.id]} isPlan={active.stage === "plan"} proposalPending={Boolean(artifact?.proposal && !proposalDecided)} requirementIds={requirementIds} focused={Boolean(selectedSection)} />
            ))}
          </ol>
        )}

        {!loading && (!artifact?.proposal || proposalDecided) && (
          <details open={operationOpen} onToggle={(event) => setOperationOpen(event.currentTarget.open)} data-testid="cycle-start" className={operationOpen ? "fixed inset-y-0 right-0 z-40 m-0 w-full max-w-lg overflow-y-auto border-l border-hairline bg-page p-5 shadow-2xl" : "hidden"}>
            <summary className={operationOpen ? "hidden" : "cursor-pointer text-sm text-ink"}>{redraftSummary(mode, roster, measured)}</summary>
            <div className={operationOpen ? "" : "pt-2"}>
            {operationOpen && <div className="mb-5 flex items-start gap-3 border-b border-hairline pb-4"><div className="min-w-0 flex-1"><p className="text-xs uppercase tracking-wide text-ink-muted">{active.label} operation</p><h2 className="mt-1 text-lg font-semibold text-ink">{stageAction}</h2><p className="mt-1 text-xs text-ink-muted">Creates a proposal; approved {active.label.toLowerCase()} remain unchanged until you accept it.</p></div><button type="button" aria-label="Close document operation" onClick={() => setOperationOpen(false)} className="rounded border border-hairline px-2 py-1 text-ink-muted">×</button></div>}
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
                    Extend grows the plan with the group; Amend adds one to three tasks for a quick change.
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
                      : "Describe the new feature. Existing requirements survive while the new one is added."
                  }
                  value={brief}
                  onChange={(e) => setBrief(e.target.value)}
                  className="mb-2 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm"
                />
                <p className="mb-2 text-xs text-ink-muted">
                  {sections.length === 0
                    ? "Leave it empty and the council will interview you instead, asking questions you answer in the run."
                    : "Accept the new requirements, then continue to spec and plan — each stage extends its document and the new tasks arrive with full traceability."}
                </p>
              </>
            )}
            {active.stage !== "intake" && (
              <div className="mb-2">
              <p className="text-xs text-ink-muted">
                {active.stage === "spec"
                  ? "Reads the accepted requirements and proposes a specification."
                  : "Reads the accepted spec and proposes milestones and tasks."}
              </p>
              {active.stage === "spec" && selectedSection && <textarea aria-label="requested specification change" data-testid="cycle-brief" rows={4} value={brief} onChange={(event) => setBrief(event.target.value)} className="mt-2 w-full rounded border border-hairline bg-surface2 px-2 py-1 text-sm" />}
              </div>
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
                  leaves the accepted document alone until you accept the new draft
                </span>
              )}
            </div>
            </>
            )}
            </div>
          </details>
        )}


        </main>
      <aside data-testid="cycle-inspector" className="hidden min-h-0 overflow-y-auto border-l border-hairline px-4 py-4 xl:block">
          <h2 className="text-sm font-semibold text-ink">Section inspector</h2>
          {!inspectedSection ? <div className="mt-4 rounded border border-dashed border-hairline p-4"><p className="text-sm font-medium text-ink">No section selected</p><p className="mt-1 text-xs text-ink-muted">Select a {active.stage === "intake" ? "requirement" : active.stage === "spec" ? "spec section" : "plan section"} to inspect its traceability.</p></div> : <>
            <div className="mt-4 border-b border-hairline pb-4"><p className="font-mono text-xs text-ink-muted">{inspectedSection.id}</p><p className="mt-1 text-sm font-medium text-ink">{inspectedSection.title}</p><span className="mt-2 inline-flex rounded-full border border-hairline px-2 py-0.5 text-xs text-ink-secondary">{artifact?.proposal && !proposalDecided ? "Proposal pending" : "Approved section"}</span></div>
            <section className="py-4"><h3 className="text-xs font-semibold uppercase tracking-wide text-ink-muted">Document chain</h3>
              {inspectedErrors.length === 0 && !inspectedMarker?.noTask ? <p className="mt-2 text-sm text-good">✓ No break reported for this section</p> : <div className="mt-2 space-y-2">{inspectedErrors.map((error) => <p key={`${error.kind}-${error.detail}`} className="text-sm text-warn">⚠ {error.detail}</p>)}{inspectedMarker?.noTask && <p className="text-sm text-warn">⚠ No planned task yet</p>}</div>}
              <div className="mt-4 space-y-3" data-testid="cycle-document-chain">
                {(["requirement", "spec_section", "milestone", "task"] as const).map((kind) => {
                  const nodes = traceChain.filter((node) => node.kind === kind);
                  if (nodes.length === 0) return null;
                  const label = kind === "requirement" ? "Requirement" : kind === "spec_section" ? "Specification" : kind === "milestone" ? "Milestone" : "Plan tasks";
                  return <div key={kind}><p className="mb-1 text-[10px] uppercase tracking-wide text-ink-muted">{label}</p><div className="space-y-1">{nodes.map((node) => {
                    const task = tasks.find((item) => item.id === node.id);
                    return <button key={node.id} type="button" data-testid="cycle-chain-node" data-id={node.id} aria-current={node.id === selectedSection ? "true" : undefined} onClick={() => selectDocumentSection(node.id)} className="block w-full rounded border border-hairline px-2 py-1.5 text-left hover:border-warning hover:bg-surface2"><span className="font-mono text-xs text-ink">{node.id}</span><span className="ml-2 text-xs text-ink-secondary">{node.title}</span>{task && <span className="ml-2 text-[10px] text-ink-muted">{task.status}</span>}</button>;
                  })}</div></div>;
                })}
              </div>
            </section>
            <div className="border-t border-hairline pt-4">
              <button type="button" data-testid="cycle-propose-section" onClick={() => {
                const prompt = `Propose a focused change to ${inspectedSection.id} — ${inspectedSection.title}. Preserve every unrelated section.\n\nRequested change: `;
                if (active.stage === "plan") { setPlanAction("amend"); setAmendment(prompt); } else setBrief(prompt);
                setOperationOpen(true);
              }} className="w-full rounded bg-ink px-3 py-2 text-sm font-medium text-page">Propose change to {inspectedSection.id}</button>
              {(inspectedErrors.length > 0 || inspectedMarker?.break) && <a href="#/cycle/ledger" className="mt-2 block rounded border border-hairline px-3 py-2 text-center text-sm">Review traceability issue</a>}
              {fleet.length > 0 && <div className="mt-3"><ChatAbout client={client} projectId={projectId} aboutKind="document" aboutId={inspectedSection.id} ducklings={fleet} label={`Chat about ${inspectedSection.id}`} placeholder={`What would you like to understand or explore about ${inspectedSection.id}?`} /></div>}
              <p className="mt-3 text-xs text-ink-muted">Changes are proposed through a run. The approved document remains unchanged until you accept the proposal.</p>
            </div>
          </>}
        </aside>
      </div>
    </div>
  );
}

function SectionCard({ section, broken, tasks = [], traceDown, isPlan = false, proposalPending = false, requirementIds = new Set<string>(), focused = false }: { section: Section; broken: Set<string>; tasks?: Task[]; traceDown?: unknown; isPlan?: boolean; proposalPending?: boolean; requirementIds?: Set<string>; focused?: boolean }) {
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
      id={`cycle-section-${section.id}`}
      data-testid="cycle-section"
      data-broken={broken.has(section.id) ? "true" : "false"}
      className={
        "border-b px-1 py-5 " +
        (broken.has(section.id) ? "border-serious" : "border-hairline")
      }
    >
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-xs text-ink-muted">{section.id}</span>
        <h2 className="text-lg font-semibold text-ink">{section.title}</h2>
      </div>
      {broken.has(section.id) && (
        <p data-testid="cycle-section-break" className="mt-1 text-xs text-serious">break in the spine</p>
      )}
      {proposalPending && (
        <p data-testid="cycle-section-proposal-pending" className="mt-1 text-xs text-ink-muted">proposal pending</p>
      )}
      {liveState && (
        <p data-testid="cycle-live-state" data-trace-loaded={traceDown ? "true" : "false"} className="mt-1 text-xs text-ink-secondary">
          {liveState}
        </p>
      )}
      {focused ? (
        <div className="mt-3" data-testid="cycle-section-details">
          {section.implements && section.implements.length > 0 && (
            <div className="text-xs text-ink-muted">
              {section.implements.map((claim) => claim.startsWith("REQ-")
                ? requirementIds.has(claim)
                  ? <span key={claim} className="mr-2">implements {claim}</span>
                  : <span key={claim} data-testid="cycle-invalid-claim" className="mr-2 text-serious">claims {claim} — no such requirement exists</span>
                : <span key={claim} className="mr-2">implements {claim}</span>)}
            </div>
          )}
          <Prose body={cleanSectionBody(section.body)} className="mt-2 space-y-3 text-sm leading-relaxed text-ink-secondary" />
        </div>
      ) : <>
        <div data-testid="cycle-section-summary" className="mt-2 text-sm text-ink">
          <SectionSummary section={section} />
        </div>
        <details className="mt-2" data-testid="cycle-section-details">
          <summary className="cursor-pointer text-xs text-ink-muted">More detail</summary>
          <div className="pt-1">
            {section.implements && section.implements.length > 0 && (
              <div className="text-xs text-ink-muted">
                {section.implements.map((claim) => claim.startsWith("REQ-")
                  ? requirementIds.has(claim)
                    ? <span key={claim} className="mr-2">implements {claim}</span>
                    : <span key={claim} data-testid="cycle-invalid-claim" className="mr-2 text-serious">claims {claim} — no such requirement exists</span>
                  : <span key={claim} className="mr-2">implements {claim}</span>)}
              </div>
            )}
            <Prose body={cleanSectionBody(section.body)} className="mt-1 space-y-2 text-sm text-ink-secondary" />
          </div>
        </details>
      </>}
      {section.children && section.children.length > 0 && (
        <ul className="mt-2 space-y-1 pl-3 border-l border-hairline">
          {section.children.map((c) => (
            <li
              key={c.id}
              id={`cycle-section-${c.id}`}
              data-testid="cycle-child"
              data-id={c.id}
              data-broken={broken.has(c.id) ? "true" : "false"}
              className={"text-xs " + (broken.has(c.id) ? "text-serious" : "")}
            >
              <span className="font-mono text-ink-muted">{c.id}</span>{" "}
              <span className="text-ink">{c.title}</span>
              {broken.has(c.id) && <span className="ml-1 text-serious">break</span>}
              {c.implements && c.implements.length > 0 && (
                <span className="text-ink-muted"> — {c.implements.map((claim) => claim.startsWith("REQ-")
                  ? requirementIds.has(claim)
                    ? `implements ${claim}`
                    : `claims ${claim} — no such requirement exists`
                  : `implements ${claim}`).join(", ")}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

function DocumentSelectionEmpty({ stage }: { stage: string }) {
  const section = stage === "intake" ? "requirement" : stage === "spec" ? "spec section" : "plan section";
  return (
    <section data-testid="cycle-selection-empty" className="order-3 flex min-h-64 items-center justify-center border-y border-hairline px-8 py-16 text-center">
      <div><p className="text-sm font-medium text-ink">Select a {section}</p><p className="mt-1 text-xs text-ink-muted">Choose an item from the outline to read its complete content and inspect its traceability.</p></div>
    </section>
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

function cleanSectionBody(body: string): string {
  return body.replace(/\bAs-built:\s*yes\b\.?\s*/gi, "").trim();
}

function summarySentence(section: Section): string {
  const text = summaryBody(section).replace(/\s+/g, " ").trim();
  const sentence = text.match(/^.*?[.!?](?:\s|$)/)?.[0]?.trim();
  return sentence || `${section.title} is described here.`;
}

/** Keep labelled metadata readable and distinct from the prose it describes. */
function summaryBody(section: Section): string {
  return cleanSectionBody(section.body).replace(/^\s*\*\*Priority:\*\*[^\n]*(?:\n|$)/im, "").trim();
}

function priorityLine(section: Section): string | null {
  const match = cleanSectionBody(section.body).match(/^\s*(\*\*Priority:\*\*[^\n]*)/im);
  return match?.[1]?.trim() ?? null;
}

function SectionSummary({ section }: { section: Section }) {
  const priority = priorityLine(section);
  const sentence = summarySentence(section);
  return (
    <>
      {priority && <Prose body={priority} className="text-sm text-ink" />}
      <Prose body={sentence} className="mt-1 text-sm text-ink" />
    </>
  );
}

function redraftSummary(mode: string, roster: readonly RosterEntry[], measured: MeasuredSpend): string {
  const seats = stageSeats(mode, roster);
  const architect = seats.find((s) => s.role === "architect")?.duckling ?? "one model";
  const critics = seats.filter((s) => s.role === "critic").map((s) => s.duckling);
  if (mode === "solo" || critics.length === 0) return `${architect} drafts a new version`;
  const criticText = critics.length === 1 ? critics[0] : `${critics.slice(0, -1).join(", ")} and ${critics[critics.length - 1]}`;
  const usd = seats.reduce((sum, seat) => sum + (measured[seat.duckling]?.usd ?? 0), 0);
  const cost = Math.max(0, Math.round(usd));
  return `${architect} drafts, ${criticText} critiques until it approves — about $${cost}`;
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

type TraceMarker = { id: string; break: boolean; noTask: boolean };

export function traceMarkers(sections: readonly Section[], errors: readonly TraceError[], tasks: readonly Task[], traceDown: Record<string, unknown>, stage: string, requirementIds: Set<string>): TraceMarker[] {
  return sections.map((section) => {
    const linked = taskIdsFromTrace(traceDown[section.id]);
    const hasTask = linked.some((id) => tasks.some((task) => task.id === id));
    const invalidRequirement = (section.implements ?? []).some((claim) => claim.startsWith("REQ-") && !requirementIds.has(claim));
    return { id: section.id, break: errors.some((error) => error.id === section.id) || invalidRequirement, noTask: stage === "plan" && !hasTask };
  });
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
