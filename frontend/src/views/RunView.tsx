import { useEffect, useRef, useState } from "react";
import type { EngineClient, Candidate, Duckling, LLMCall, Run, Task } from "../api/client";
import { useRuns } from "../store/runs";
import type { DucklabEvent } from "../api/events";
import { buildTurns, anonymiseTurns, buildTimeline, buildGate, buildPending, buildTriage, buildTriageFailures, parseDiff, reviewerDissent, finalVerdict, findingsFiled, chainedBuildId, buildDeliverables } from "../lib/runview";
import { ConversationTurn } from "../components/ConversationLane";
import { VirtualList } from "../components/VirtualList";
import { ToolTimeline } from "../components/ToolTimeline";
import { GateCard } from "../components/GateCard";
import { CandidateCard } from "../components/CandidateCard";
import { DiffView } from "../components/DiffView";
import { BudgetMeter } from "../components/BudgetMeter";
import { Prose } from "../components/Prose";
import { StatusChip } from "../components/StatusChip";
import { RemoveTask } from "../components/RemoveTask";
import { ChatAbout } from "../components/ChatAbout";
import { DecisionCard } from "../components/DecisionCard";
import { SurveyCoverageLine, SurveyInventory, type SurveyInventoryItem } from "../components/SurveyInventory";
import { RunLauncher, type LaunchOpts, type ModeEstimates } from "../components/RunLauncher";
import { SeatChips, type MeasuredSpend } from "../components/SeatChips";
import { money, tokens, duration } from "../lib/format";
import { routeHref } from "../app/routes";
import { seatsFromRoster, rolesForMode } from "../lib/seats";
import { verdictStatus, verdictLabel, assignDucklingColors, type Verdict } from "../lib/colors";
import { runLabel } from "../lib/runview";

type Tab = "diff" | "verify" | "candidates" | "calls";

/** The Run view: conversation lanes, gate and budget, tool timeline, tabs. */
/** Where this run sits in the development cycle, at a glance.
 *
 * The header named the stage — "intake", "build" — and assumed the reader
 * carries the whole loop in their head. The map states it: every station,
 * this run's lit. A test run sits at build (the failing test is the first
 * half of building); a triage run sits at plan (its classifications become
 * the tasks the plan grows by). Runs outside the cycle — chat — have no
 * station and show no map. */
const CYCLE = ["intake", "spec", "plan", "build", "release"] as const;

/** The station is the activity; the artifact is what it leaves behind. Most
 * stations share a name with their artifact — spec writes the spec — but
 * intake writes REQUIREMENTS, and a reader who hasn't internalized that saw
 * a station whose output appears nowhere on the map. */
const CYCLE_LABELS: Record<(typeof CYCLE)[number], string> = {
  intake: "intake (reqs)",
  spec: "spec",
  plan: "plan",
  build: "build",
  release: "release",
};

function cycleStation(stage: string): string | null {
  if (stage === "test" || stage === "build") return "build";
  if (stage === "triage") return "plan";
  return (CYCLE as readonly string[]).includes(stage) ? stage : null;
}

function RecoveryControls({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const recover = (action: "clean" | "commit") => {
    setBusy(action); setError(null);
    void client.projectRecover(projectId, action).catch((e) => setError(e instanceof Error ? e.message : String(e))).finally(() => setBusy(null));
  };
  return <div className="mt-2 flex gap-2 text-sm" data-testid="retire-recovery">
    <button type="button" disabled={busy !== null} onClick={() => recover("clean")} className="rounded border border-hairline px-2 py-1">{busy === "clean" ? "Cleaning…" : "Clean working tree"}</button>
    <button type="button" disabled={busy !== null} onClick={() => recover("commit")} className="rounded border border-hairline px-2 py-1">{busy === "commit" ? "Committing…" : "Commit changes"}</button>
    {error && <span className="text-critical" role="alert">{error}</span>}
  </div>;
}

function CycleMap({ stage }: { stage: string }) {
  const at = cycleStation(stage);
  if (!at) return null;
  const why =
    stage === "test"
      ? "a failing test is the first half of building"
      : stage === "triage"
        ? "triage feeds the plan: classified bugs become its tasks"
        : `this run produces the ${at} step's document or work`;
  return (
    <span
      data-testid="cycle-map"
      data-at={at}
      className="flex items-center gap-1 text-xs"
      title={`where this run sits in the development cycle — ${why}`}
    >
      {CYCLE.map((s, i) => (
        <span key={s} className="flex items-center gap-1">
          {i > 0 && <span className="text-ink-muted">→</span>}
          <span className={s === at ? "font-medium text-ink underline decoration-hairline underline-offset-4" : "text-ink-muted"}>
            {CYCLE_LABELS[s]}
          </span>
        </span>
      ))}
    </span>
  );
}

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
  const [actionError, setActionError] = useState<string | null>(null);
  const [landingSHA, setLandingSHA] = useState("");
  const [landingNote, setLandingNote] = useState("");

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
  // The tab panel folds: clicking the ACTIVE tab hides its content (the
  // calls list under a long transcript is most of a screen), clicking any
  // tab shows it again. Not persisted — a diff hidden by yesterday's fold
  // is a diff unread before an accept.
  const [tabsFoldedChoice, setTabsFoldedChoice] = useState<boolean | null>(null);
  // The task's own words live in the pinned header, folded by default: what
  // was ASKED must be reachable from anywhere in a long transcript without
  // scrolling back, and must not spend the viewport on itself when it is not
  // being read.
  const [taskOpen, setTaskOpen] = useState(false);
  const tabPanelRef = useRef<HTMLDivElement | null>(null);
  // Explicit expand/collapse choices per turn, keyed round:turn. Held HERE
  // because the virtualiser unmounts off-screen turns — state inside the turn
  // would forget the reader's choice the moment they scrolled away.
  const [turnChoice, setTurnChoice] = useState<Record<string, boolean>>({});
  // The right rail folds away like the guide rail does: budget and gate are
  // glanced at, and on a small window they tax every line of transcript.
  const [railOpen, setRailOpen] = useState(() => localStorage.getItem("ducklab.runrail") !== "off");
  const toggleRail = () => {
    setRailOpen((v) => {
      if (v) localStorage.setItem("ducklab.runrail", "off");
      else localStorage.removeItem("ducklab.runrail");
      return !v;
    });
  };
  // A stage run's subject: the document it proposed. Fetched from the
  // artifact store once the run pauses at its gate, shown only when the
  // pending proposal is THIS run's — an older proposal would be someone
  // else's question.
  const [proposal, setProposal] = useState<{ markdown?: string; diff?: string } | null>(null);
  // Silence, measured. A stalled provider used to be indistinguishable from
  // a thinking model: the person watched nothing happen and aborted healthy
  // runs. Any signal — an event, a delta, a thought — resets the clock.
  const lastSignal = useRef(Date.now());
  const [nowTick, setNowTick] = useState(() => Date.now());
  useEffect(() => {
    lastSignal.current = Date.now();
  }, [events, deltas, reasoning, live]);
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
  const [chatMsg, setChatMsg] = useState("");
  const [chatBusy, setChatBusy] = useState(false);
  const [chatImages, setChatImages] = useState<{ name: string; data: string }[]>([]);
  const [chatImageError, setChatImageError] = useState<string | null>(null);
  const chatImageInput = useRef<HTMLInputElement>(null);
  // Follow the chain: accepting a watched test starts its build, and the
  // person watching should keep watching. Auto-navigate ONLY when the run
  // was waiting under their eyes — a historical test opened later gets a
  // link, never a hijack.
  const wasWaitingHere = useRef(false);
  const followedChain = useRef("");
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
  // Measured spend per duckling, for the relaunch panel's seat chips.
  const [measured, setMeasured] = useState<MeasuredSpend>({});
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
  // The document chain, continued in place: requirements → spec → plan is
  // one process, and its next step renders where the previous one was
  // accepted.
  const [stageBusy, setStageBusy] = useState(false);
  const [stageError, setStageError] = useState<string | null>(null);
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
      try {
        const rep = await client.report(projectId, "duckling");
        const m: MeasuredSpend = {};
        for (const row of rep.rows) m[row.key] = { usd: row.cost_usd, runs: row.runs };
        setMeasured(m);
      } catch {
        setMeasured({});
      }
    })();
  }, [client, projectId]);
  const taskId = run?.task_id ?? "";
  useEffect(() => {
    // A run with no task must CLEAR the previous run's — the early return
    // left T-076's card standing over a fresh chat, which reads as "this
    // chat is about T-076" when it is about nothing of the sort.
    if (!projectId || !taskId) {
      setTask(null);
      return;
    }
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
    client
      .runLLM(runId)
      .then((cs) => {
        setCalls(cs);
        llmSeq.current = cs.reduce((m, c) => Math.max(m, c.seq), 0);
      })
      .catch(() => setCalls([]));
  }, [runId, client, run?.status]);

  // The calls panel used to load once and refresh only on a status change,
  // so during a run it froze while the timeline moved — the two looked like
  // different runs. New model calls are fetched incrementally (from_seq) as
  // events arrive; the full payloads are heavy, the increments are not.
  const llmSeq = useRef(0);
  const working = run?.status === "running" || run?.status === "queued";
  useEffect(() => {
    if (!working) return;
    const t = setTimeout(() => {
      client
        .runLLM(runId, llmSeq.current)
        .then((more) => {
          if (more.length === 0) return;
          llmSeq.current = more.reduce((m, c) => Math.max(m, c.seq), llmSeq.current);
          setCalls((prev) => [...prev, ...more.filter((c) => !prev.some((p) => p.seq === c.seq))]);
        })
        .catch(() => {});
    }, 800);
    return () => clearTimeout(t);
  }, [events.length, working, runId, client]);

  const chainBuild = chainedBuildId(events);
  useEffect(() => {
    wasWaitingHere.current = false;
    followedChain.current = "";
  }, [runId]);
  useEffect(() => {
    if (run?.status === "paused") wasWaitingHere.current = true;
  }, [run?.status]);
  useEffect(() => {
    if (chainBuild && wasWaitingHere.current && followedChain.current !== chainBuild) {
      followedChain.current = chainBuild;
      location.hash = `#/runs/${chainBuild}`;
    }
  }, [chainBuild]);

  const stageKind =
    run?.stage === "intake" ? "requirements" : run?.stage === "spec" ? "spec" : run?.stage === "plan" ? "plan" : "";
  useEffect(() => {
    if (!stageKind || !run) {
      setProposal(null);
      return;
    }
    let live = true;
    client
      .artifact(run.project_id, stageKind)
      .then((a) => {
        if (!live) return;
        setProposal(
          a.proposal && a.proposal.run_id === runId
            ? { markdown: a.proposal.markdown, diff: a.proposal.diff }
            : null,
        );
      })
      .catch(() => {});
    return () => {
      live = false;
    };
    // Refetched when the run's status moves: the proposal lands exactly when
    // the run pauses at its gate.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, runId, stageKind, run?.status]);

  const liveNow = run?.status === "running" || run?.status === "queued";
  useEffect(() => {
    if (!liveNow) return;
    const t = setInterval(() => setNowTick(Date.now()), 5000);
    return () => clearInterval(t);
  }, [liveNow]);

  if (!run) return <p className="p-4 text-ink-muted">Loading run…</p>;

  const roster = Object.values(run.roster ?? {});
  const ducklingColors = assignDucklingColors(fleet);
  // Only the seats this mode actually uses, in speaking order; the record's
  // roster names every role, and a pair run showing seven chips read as
  // "my whole team is on this run".
  // Keyed by the run's MODE except where the stage IS the shape (triage,
  // release, chat): a spec run asked rolesForMode("spec"), got null, and
  // showed all seven chairs of a council that seats three.
  const seatRoles = rolesForMode(["triage", "release", "chat"].includes(run.stage) ? run.stage : run.mode);
  const rosterEntries = Object.entries(run.roster ?? {})
    .filter(([role]) => !seatRoles || seatRoles.includes(role))
    .sort(([a], [b]) => (seatRoles ? seatRoles.indexOf(a) - seatRoles.indexOf(b) : 0))
    .map(([role, duckling]) => ({
    role,
    // Tournament identities are intentionally hidden in the run view, including
    // the seat summary; exposing them here would defeat lane anonymisation.
    duckling: run.mode === "tournament" ? "" : duckling,
    provenance: run.roster_sources?.[role] ? `(${run.roster_sources[role]})` : undefined,
  }));
  // Everyone with a seat in this run, from the first frame. Rows used to
  // appear only as each model's first call landed, so a pair run opened
  // showing nobody and the line-up assembled itself over minutes. Spenders
  // sort first (biggest is who you would act on); the seated-but-silent
  // follow in seat order. seatsFromRoster keeps solo honest — the roster
  // names every role, but a solo run seats one model, so the one-row
  // breakdown stays hidden as before.
  const spend = live?.ducklings ?? run.spend ?? {};
  // Only the seats this run's MODE fields: the resolver fills every role
  // (terra is council's architect), and the spend line said "architect" on
  // the duckling that implemented this pair run.
  const modeRoles = rolesForMode(run.mode);
  const rolesByDuckling: Record<string, string[]> = {};
  for (const [role, id] of Object.entries(run.roster ?? {})) {
    if (id && (!modeRoles || modeRoles.includes(role))) (rolesByDuckling[id] ??= []).push(role);
  }
  const spenders = Object.keys(spend)
    .filter((id) => (spend[id]?.calls ?? 0) > 0)
    .sort((a, b) => (spend[b]?.tokens ?? 0) - (spend[a]?.tokens ?? 0));
  const perDuckling: [string, { calls: number; tokens: number; cost_usd: number } | undefined][] = [
    ...spenders,
    ...seatsFromRoster(run.mode, run.roster).filter((id) => !spenders.includes(id)),
  ].map((id) => [id, spend[id]]);
  // A judge's turns are anonymised; the mapping is dropped, not hidden.
  const anonymise = run.mode === "tournament";
  const turns = anonymiseTurns(buildTurns(events), anonymise);
  // Spend inside the CURRENT streaming call, estimated from the text that
  // has arrived (chars/4). Settled usage lands only when a call completes —
  // with long streams allowed to live, a ten-minute architect read as frozen
  // zeros while its words visibly flowed. Approximate on purpose, worn with
  // a ~: turns whose message has landed are excluded, their usage is real.
  const inFlightTokens = (() => {
    if (!liveNow) return 0;
    let chars = 0;
    for (const t of turns) {
      if (t.done || t.messageOnly) continue;
      const k = `${t.round}:${t.turn}`;
      chars += (deltas[k]?.length ?? 0) + (reasoning[k]?.length ?? 0);
    }
    return Math.round(chars / 4);
  })();
  const gate = buildGate(events);
  const deliverables = buildDeliverables(events);
  const pending = buildPending(events);
  // A green gate over an unconvinced reviewer must not be silent (T-028:
  // three straight request-changes verdicts under "tests passed").
  const dissent = run.verdict === "PASSED" ? reviewerDissent(turns) : null;
  // Any final findings at all — an approval "with two minor findings" found
  // real work too; approval means "not worth blocking", not "not worth
  // remembering". Filing them as bugs puts them in the loop instead of in a
  // transcript a future testing phase re-discovers at full price.
  const lastVerdict = finalVerdict(turns);
  // Only CODE runs file findings as bugs: a bug is a claim about the
  // software, and a stage reviewer's findings are about a draft — their
  // destiny is a revision (request changes), not the bug board.
  const codeRun = run.stage === "build" || run.stage === "test";
  const finished = run.status !== "running" && run.status !== "queued" && run.status !== "paused";
  // The tab a non-code run actually shows: its bar offers only calls — and
  // once a finished run's diff moves up beside the verdict, the dock's own
  // default falls through to verify rather than pointing at a tab that is
  // no longer there.
  const shownTab: Tab = !codeRun ? "calls" : finished && (diff || testHunks) && tab === "diff" ? "verify" : tab;
  // A non-code run's only panel is the model-calls list — debugging
  // material, not the content — so it starts folded; a build's diff starts
  // open. The person's own click wins over either default.
  const tabsFolded = tabsFoldedChoice ?? !codeRun;
  const setTabsFolded = (v: boolean | ((cur: boolean) => boolean)) =>
    setTabsFoldedChoice(typeof v === "function" ? v(tabsFolded) : v);
  const fileable = codeRun && lastVerdict && lastVerdict.findings.length > 0 &&
    (run.status === "paused" || run.status === "done");
  // A stage proposal whose reviewer asked for changes, waiting at its gate:
  // the objections belong beside the decision, pointing at the action made
  // for them.
  const stageDissent = !codeRun && run.status === "paused" && run.pending_kind === "gate" &&
    lastVerdict && lastVerdict.verdict.toLowerCase().replace(/_/g, "-") !== "approve"
    ? lastVerdict
    : null;
  // Already filed, from the RECORD — local state only remembers this mount's
  // clicks, and a filed run re-visited offered to file again.
  const recordedFiling = findingsFiled(events);
  const filed = filedBugs ?? recordedFiling;
  const timeline = buildTimeline(events);
  const surveyInventory = events
    .filter((event) => event.type === "survey_inventory")
    .flatMap((event) => Array.isArray(event.data?.items) ? event.data.items as SurveyInventoryItem[] : []);
  const chatLive =
    run?.stage === "chat" &&
    (run.status === "running" || run.status === "paused" || run.status === "queued");
  const chatCanSee = !!fleet.find((d) => d.id === Object.values(run.roster ?? {})[0])?.caps?.vision;
  const readChatImages = (files: FileList | null) => {
    if (!files) return;
    setChatImageError(null);
    const picked = Array.from(files);
    if (picked.some((file) => !file.type.startsWith("image/"))) {
      setChatImageError("Only image files can be attached.");
      return;
    }
    void Promise.all(picked.map((file) => new Promise<{ name: string; data: string }>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve({ name: file.name, data: String(reader.result) });
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    }))).then((pickedImages) => setChatImages((current) => [...current, ...pickedImages]))
      .catch(() => setChatImageError("Could not read the selected image."));
  };
  const sendChat = () => {
    setChatBusy(true);
    void client.chatSend(runId, chatMsg.trim(), chatImages.map((image) => image.data))
      .then(() => { setChatMsg(""); setChatImages([]); })
      .catch(() => {})
      .finally(() => setChatBusy(false));
  };
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
        : !next.includes("accept")
          ? "nothing passed, so there is nothing to accept — reject discards this run's diff and frees the task to retry"
          : events.some((e) => e.type === "commit_withdrawn")
            ? "The last accept committed the diff, but it did not reproduce from a clean checkout, so the commit was taken back — the diff is still in the tree, uncommitted. Fix the tree and accept again (a new commit, verified again), or reject to restore the tree."
            : "commits the diff to the project";
  const outcome = (() => {
    if (isWorking) return "";
    if (run.resolution === "landed") {
      return run.commit_sha ? `landed · ${run.commit_sha.slice(0, 7)}` : "landed";
    }
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
    setActionError(null);
    setRelaunchBusy(true);
    setRelaunchError(null);
    try {
      // A failed TEST relaunches as a TEST, chain included. This called
      // runStart unconditionally, so relaunching a failed test+build with a
      // different model quietly launched a bare build: the phase the person
      // watched die was skipped, and the chain they authorized vanished
      // (T-076). The launcher's picks drive the test phase; the promised
      // build keeps its own recorded settings.
      const chain = run.chain_build;
      const started =
        run.stage === "test"
          ? await client.testStart(run.project_id, run.task_id, "", {
              thenBuild: !!chain,
              testMode: opts.mode,
              testDucklings: opts.ducklings,
              note: opts.note,
              mode: chain?.mode || "solo",
              ducklings: chain?.ducklings ?? [],
              maxTokens: chain?.budget?.max_tokens,
              agentTurns: chain?.agent_turns,
              // The relaunch panel already states the caveat when the task
              // was finished by a later run; clicking past it is the consent
              // the engine's accepted-task door asks for.
              redo: true,
            })
          : await client.runStart(run.project_id, run.task_id, { ...opts, redo: true });
      setRelaunched(started.id);
    } catch (e) {
      setRelaunchError(e instanceof Error ? e.message : String(e));
    } finally {
      setRelaunchBusy(false);
    }
  };

  const requestChanges = async (text: string) => {
    setActionError(null);
    try {
      // A release's draft revises through its own door; the document stages
      // through theirs. Same button, same meaning: "almost".
      const started =
        run.stage === "release"
          ? await client.releasePlan(run.project_id, "", text)
          : await client.stageStart(run.project_id, stageToRevise, { revise: text });
      setRevisionRun(started.id);
    } catch (e) {
      useRuns.getState().failAccept(runId, e instanceof Error ? e.message : String(e));
    }
  };

  const onAccept = async () => {
    setActionError(null);
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
      {/* Pinned while the transcript scrolls: the header is the run's
          identity — what is being built, where in the cycle, and the one
          control that stops it — and losing it to the scroll meant reading
          a wall of tool calls with no way to tell WHOSE they were, or to
          abort without scrolling back up. Opaque so the lanes pass under
          it, not through it. */}
      <header className="sticky top-0 z-20 border-b border-hairline bg-page px-4 py-3">
        <div className="flex flex-wrap items-center gap-3">
        {/* A run with no task showed nothing at all: the header of a triage or
            a stage opened with an empty space where its name should be. The
            same fallback the runs list uses — task, else stage, else id. */}
        <span className="text-md">{runLabel(run)}</span>
        {/* The task's title beside its id: the header answers "what is being
            built" without a trip down to the card. Truncated; the card and
            the hover carry the whole of it. */}
        {task?.title && (
          <button
            type="button"
            className="flex max-w-md items-center gap-1 text-sm text-ink-muted hover:text-ink"
            data-testid="run-task-toggle"
            aria-expanded={taskOpen}
            aria-controls="run-task-card"
            title={taskOpen ? "fold the task" : "read the task"}
            onClick={() => setTaskOpen((v) => !v)}
          >
            <span aria-hidden="true" className="text-xs">{taskOpen ? "▾" : "▸"}</span>
            <span className="truncate" data-testid="run-task-title" title={task.title}>— {task.title}</span>
          </button>
        )}
        <span className="text-ink-secondary" title={run.mode_source ? `mode source: ${run.mode_source}` : undefined}>{run.mode}{run.mode_source ? ` (${run.mode_source})` : ""}</span>
        {isWorking && run.worktree_path && (
          <span data-testid="worktree-badge" className="rounded border border-hairline px-2 py-0.5 font-mono text-xs text-ink-secondary" title={run.worktree_path}>
            worktree · {run.branch ?? "branch"} · {run.worktree_path}
          </span>
        )}
        {/* The cycle map places a stage run in its pipeline; a triage or a
            chat is not IN the pipeline, and "unverified" is its loudest chip
            for what is simply a run with no gate by design. Their headers
            say what the run did instead. */}
        {run.stage !== "triage" && run.stage !== "chat" && <CycleMap stage={run.stage} />}
        {/* WHO is working, said where it cannot scroll away: a triager's
            forty searches took the turn header off screen and nothing else
            named the hands. Live runs only — a finished run's header talks
            about the outcome. */}
        {liveNow && (() => {
          const active = [...turns].reverse().find((t) => !t.done && t.duckling) ?? [...turns].reverse().find((t) => t.duckling);
          return active ? (
            <span className="flex items-center gap-1 text-sm" data-testid="run-active-duckling">
              <span aria-hidden="true" className="animate-pulse text-xs">▸</span>
              <span style={{ color: ducklingColors[active.duckling] }} className="font-medium">{active.duckling}</span>
              <span className="text-ink-muted">{active.role}</span>
            </span>
          ) : null;
        })()}
        {run.stage === "triage" ? (
          <StatusChip role={run.accepted ? "good" : isWorking ? "muted" : "serious"} label={run.accepted ? `triaged ${triage.length || "the"} report${triage.length === 1 ? "" : "s"} · applied` : isWorking ? "triaging" : "triage awaiting your decision"} />
        ) : run.stage === "chat" ? (
          <StatusChip role="muted" label={isWorking ? "conversing" : "conversation ended"} />
        ) : run.no_changes ? (
          <StatusChip role="muted" label="no changes — already in the tree" />
        ) : run.resolution === "landed" ? (
          <StatusChip role="good" label="landed" />
        ) : (
          <StatusChip role={verdictStatus(run.verdict as Verdict)} label={run.acceptance_gate?.green ? `${verdictLabel(run.verdict as Verdict)} · reproduced green at accept` : verdictLabel(run.verdict as Verdict)} />
        )}
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
              onClick={() => {
                setActionError(null);
                void client.abort(runId).catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
              }}
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
            run.accepted || run.resolution === "landed" ? (
              <span data-testid="run-outcome"><StatusChip role="good" label={outcome} /></span>
            ) : (
              <span className="text-sm text-ink-secondary" data-testid="run-outcome">
                {outcome}
              </span>
            )
          )}
          {run.status === "done" && !run.accepted && run.resolution !== "landed" && (
            <form
              className="flex items-center gap-1"
              onSubmit={(event) => {
                event.preventDefault();
                setActionError(null);
                void client.land(runId, landingSHA, landingNote)
                  .then(() => client.run(runId))
                  .then((detail) => useRuns.getState().resyncRun(detail.run, detail.events as DucklabEvent[]))
                  .catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
              }}
            >
              <input
                aria-label="landing commit sha"
                value={landingSHA}
                onChange={(event) => setLandingSHA(event.target.value)}
                placeholder="landing commit SHA"
                required
                className="w-36 rounded border border-hairline bg-surface2 px-2 py-1 text-xs font-mono"
              />
              <input
                aria-label="landing note"
                value={landingNote}
                onChange={(event) => setLandingNote(event.target.value)}
                placeholder="landing note"
                className="w-32 rounded border border-hairline bg-surface2 px-2 py-1 text-xs"
              />
              <button type="submit" className="rounded border border-good px-2 py-1 text-xs text-good">Mark landed</button>
            </form>
          )}
          {liveNow && nowTick - lastSignal.current > 30000 && (
            <span className="text-xs text-ink-muted" data-testid="quiet-chip" title="no events, tokens or thinking since then — a slow model is normal; provider retries land in the lane as they happen">
              quiet {Math.round((nowTick - lastSignal.current) / 1000)}s
            </span>
          )}
        </div>
        </div>
        {/* The task's own words, unfolded from the pinned title: judging a
            run means reading what it did against what was ASKED, and the ask
            used to sit in a card that the transcript scrolled away. Bounded:
            a long body scrolls inside its own box, never the page. */}
        {task && taskOpen && (
          <section
            id="run-task-card"
            data-testid="run-task-card"
            className="mt-3 rounded-card border border-hairline p-3"
          >
          <div className="text-sm text-ink">
            <span className="text-ink-muted">the task · </span>
            {task.id} — {task.title}
          </div>
          {/* Shown even bodiless — ESPECIALLY bodiless. Gating the card on
              task.body hid it for exactly the malformed task whose actions
              (remove, chat) the person was hunting: the phantom with a title
              and nothing else. An empty brief is a fact worth stating. */}
          {task.spec_debt ? (
            <p className="mt-1 text-xs text-warn" data-testid="task-spec-debt">
              spec-debt — no spec section covers this task; it settles into the spec after its build is accepted
            </p>
          ) : (task.implements?.length ?? 0) > 0 ? (
            <p className="mt-1 text-xs text-ink-muted" data-testid="task-coverage">
              covered by {task.implements!.join(", ")}
            </p>
          ) : null}
          {task.body ? (
            <div className="mt-1 max-h-36 overflow-y-auto overscroll-contain text-sm">
              <Prose body={task.body} />
            </div>
          ) : (
            <p className="mt-1 text-xs text-warn" data-testid="task-empty-body">
              this task has no body — a model working it would have to guess what it means
            </p>
          )}
          {/* Every legal manipulation, offered where the task is on screen:
              the person reading this run's failure should not have to hunt
              the task down on the board to act on what they just learned. */}
          {task.next?.includes("remove") && (
            <div className="mt-2">
              <RemoveTask
                task={task}
                client={client}
                projectId={run.project_id}
                onDone={() => setTask(null)}
              />
            </div>
          )}
          <div className="mt-2">
            <ChatAbout client={client} projectId={run.project_id} aboutKind="task" aboutId={task.id} ducklings={fleet} />
          </div>
          </section>
        )}
      </header>

      {/* Breathing room so the first card clears the pinned header's shadow
          instead of peeking out cut in half. */}
      <div className="pt-1" />
      {/* The rail is metadata of the WHOLE run — budget, spend, gate — so it
          docks at the right edge for the whole read, not just the stretch of
          page its old grid column happened to span. The wrapper holds every
          scrolling region (cards, transcript, dock, diff), which is what
          lets the aside's sticky keep its grip from the first card to the
          last hunk of the diff instead of drowning when the grid ended. */}
      <div className="md:flex md:items-start">
        {/* While the run lives the column takes the viewport's height and the
            dock rides its bottom edge (mt-auto): a short transcript used to
            leave the dock adrift with dead space beneath it. */}
        <div className={finished ? "min-w-0 flex-1" : "min-w-0 flex-1 md:flex md:min-h-[calc(100vh-8rem)] md:flex-col"}>

      {/* The moment you most want to change a setting and go again is while
          looking at the run that just failed. Doing it meant leaving for the
          board and finding the task by hand, which is enough friction that a
          re-run tends to carry the settings that just failed. */}
      {canRelaunch && (
        <section
          data-testid="relaunch"
          className="m-2 rounded-card border border-hairline p-3"
        >
          <h2 className="text-sm font-medium text-ink mb-2">
            {run.stage === "test"
              ? `Test ${run.task_id} again${run.chain_build ? " → then build" : ""}`
              : `Run ${run.task_id} again`}
          </h2>
          {relaunchCaveat && !anyway ? (
            <p className="text-sm text-ink-muted" data-testid="relaunch-done">
              {relaunchCaveat}{" "}
              {/* Says what it does: it UNFOLDS the launcher — mode, seats,
                  and the note that carries the new expectations. Labelled
                  "run it anyway" it read as fire-without-setup, so the person
                  who most needed the note never clicked to find it. */}
              <button
                type="button"
                onClick={() => setAnyway(true)}
                data-testid="relaunch-anyway"
                className="text-ink underline"
              >
                set up the rerun →
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
            measured={measured}
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
                        // The accept a moment ago made this task "accepted";
                        // the fix-forward run is authorized by that same click.
                        redo: true,
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
      {run.stage === "test" && run.accepted && chainBuild && (
        <section data-testid="chain-followed" className="m-2 rounded-card border border-good p-3">
          <p className="text-sm text-ink">
            The red test was committed and the chained build took over —{" "}
            <a href={`#/runs/${chainBuild}`} className="text-good underline">watch {chainBuild}</a>
          </p>
        </section>
      )}
      {run.stage === "plan" && run.accepted && (
        <section data-testid="plan-landed" className="m-2 rounded-card border border-good p-3">
          <p className="text-sm text-ink">
            The plan landed — its tasks are on the board, ready to launch.{" "}
            <a href={routeHref({ name: "board" })} className="text-good underline">see the tasks</a>
          </p>
        </section>
      )}
      {(next.includes("run_spec") || next.includes("run_plan")) && (
        <section data-testid="next-stage" className="m-2 rounded-card border border-good p-3">
          <p className="text-sm text-ink">
            {next.includes("run_spec")
              ? "The requirements landed. The next step of the same process is extending the spec against them."
              : "The spec landed. The next step of the same process is extending the plan against it."}
          </p>
          <button
            type="button"
            data-testid="run-next-stage"
            disabled={stageBusy}
            onClick={() => {
              setStageBusy(true);
              setStageError(null);
              const stage = next.includes("run_spec") ? "spec" : "plan";
              void client
                .stageStart(run.project_id, stage)
                .then((r) => {
                  location.hash = `#/runs/${r.id}`;
                })
                .catch((e) => setStageError(e instanceof Error ? e.message : String(e)))
                .finally(() => setStageBusy(false));
            }}
            className="mt-2 rounded border border-good px-3 py-1.5 text-sm text-good disabled:opacity-40"
          >
            {stageBusy
              ? "Starting…"
              : next.includes("run_spec")
                ? "Extend the spec"
                : "Extend the plan"}
          </button>
          {stageError && (
            <p className="mt-1 text-xs text-critical" data-testid="next-stage-error">{stageError}</p>
          )}
        </section>
      )}
      {stageDissent && (
        <section data-testid="stage-dissent" className="m-2 rounded-card border border-serious p-3">
          <StatusChip role="serious" label={`the reviewer asked for changes — ${stageDissent.findings.length} finding${stageDissent.findings.length === 1 ? "" : "s"}`} />
          <ul className="mt-2 space-y-1 text-sm" data-testid="stage-dissent-list">
            {stageDissent.findings.map((f, i) => (
              <li key={i} className="text-ink-secondary">
                {f.severity && <span className="mr-1 text-xs uppercase text-ink-muted">[{f.severity}]</span>}
                {f.issue}
              </li>
            ))}
          </ul>
          <p className="mt-2 text-sm text-ink">
            These are revision notes on the draft, not bugs. If they should be addressed,
            send the proposal back with <strong>Request changes</strong> below — the note
            carries them into the revision run.
          </p>
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
                {/* Clamped: the card is a decision surface, not the reading
                    copy — the whole finding is one click away in the verdict. */}
                <div className="line-clamp-3" title={`${f.issue}${f.fix ? ` · fix: ${f.fix}` : ""}`}>
                  {f.severity && <span className="mr-1 text-xs uppercase text-ink-muted">[{f.severity}]</span>}
                  {f.issue}
                  {f.file && (
                    <span className="text-ink-muted"> — {f.file}{f.line ? `:${f.line}` : ""}</span>
                  )}
                  {f.fix && <span className="text-ink-muted"> · fix: {f.fix}</span>}
                </div>
              </li>
            ))}
          </ul>
          <div className="mt-2">
            {filed ? (
              <p className="text-sm text-good" data-testid="file-findings-done">
                filed as {filed.join(", ")} —{" "}
                <a href={routeHref({ name: "board", tab: "bugs" })} className="underline">see the bugs board</a>
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
      <SurveyInventory items={surveyInventory} />

      {/* A governance edit is one TOML line in a possibly huge diff: said in
          plain words at the decision, or it rides through unseen (B-138). */}
      {run.pending_kind === "gate" && Array.isArray(run.pending_data?.governance_callouts) && (
        <section data-testid="governance-callouts" className="m-2 rounded-card border border-serious p-3">
          <h2 className="text-sm font-medium text-serious">Governance change</h2>
          <ul className="mt-1 list-disc pl-5 text-sm text-ink">
            {(run.pending_data?.governance_callouts as unknown[]).map((c, i) => <li key={i}>{String(c)}</li>)}
          </ul>
          <p className="mt-2 text-sm text-ink-muted">Project settings change through PATCH /v1/projects, not a task diff — accepting this run commits them.</p>
        </section>
      )}
      {run.pending_kind === "gate" && Array.isArray(run.pending_data?.conflicting_files) && (
        <section data-testid="worktree-conflict" className="m-2 rounded-card border border-serious p-3">
          <h2 className="text-sm font-medium text-serious">Rebase conflict</h2>
          <p className="mt-1 text-sm text-ink">resolve by hand at <code>{String(run.pending_data?.worktree ?? run.worktree_path ?? "the worktree")}</code>, then resume; or reject this run.</p>
          <p className="mt-1 font-mono text-xs text-ink-muted">base {String(run.pending_data?.base_sha ?? "—")} · default {String(run.pending_data?.default_sha ?? "—")}</p>
          <ul className="mt-2 list-disc pl-5 font-mono text-sm text-ink-secondary">
            {(run.pending_data?.conflicting_files as unknown[]).map((file) => <li key={String(file)}>{String(file)}</li>)}
          </ul>
          <p className="mt-2 text-sm text-ink-muted">Lawful options: resolve by hand at the shown path, or reject.</p>
        </section>
      )}
      {run.warning && (
        <section data-testid="run-warning" className="m-2 rounded-card border border-serious p-3">
          <h2 className="mb-1 text-sm font-medium" style={{ color: "var(--status-serious)" }}>Warning</h2>
          <p className="whitespace-pre-wrap break-words text-sm text-ink">{run.warning}</p>
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
          {run.stage === "test" && /retire-test|working tree is dirty|commit or clean/i.test(run.failure) && (
            <RecoveryControls client={client} projectId={run.project_id} />
          )}
        </section>
      )}

      {/* WHAT CHANGED, where the reading order wants it: verdict, findings,
          then the diff — a finished build's deliverable lived at the very
          bottom of the page, "open by default" and never seen. The bottom
          tab bar drops its diff tab while this section owns it. */}
      {finished && codeRun && (diff || testHunks) && (
        <section className="m-2 rounded-card border border-hairline p-3" data-testid="diff-inline">
          <h2 className="mb-2 text-sm font-medium text-ink">what changed</h2>
          {testHunks && (
            <section className="mb-3 rounded-card border border-serious p-2" data-testid="tests-modified">
              <p className="mb-2 text-sm text-serious">
                this change edits tests; read these hunks before accepting
              </p>
              <DiffView files={parseDiff(testHunks)} />
            </section>
          )}
          {diff && <DiffView files={parseDiff(diff)} />}
        </section>
      )}

      {/* The stage run's subject IS a document. The lanes show the council
          arguing; this is the thing being decided, rendered where the
          decision happens — before this, a spec run paused at its gate
          offering Accept over a diff tab that is empty by design. */}
      {proposal && (proposal.markdown || proposal.diff) && (
        <section data-testid="stage-proposal" className="m-2 rounded-card border border-hairline p-3">
          <h2 className="text-sm text-ink-muted">the proposal — what Accept would approve</h2>
          {proposal.markdown && (
            <div className="mt-2 max-h-[50vh] overflow-y-auto overscroll-contain">
              <Prose body={proposal.markdown} suppress={[]} className="space-y-2 text-sm text-ink-secondary" />
            </div>
          )}
          {proposal.diff && proposal.diff.trim() !== "" && (
            <details className="mt-2">
              <summary className="cursor-pointer text-xs text-ink-muted">
                what changed against the approved document
              </summary>
              <div className="mt-1">
                <DiffView files={parseDiff(proposal.diff)} />
              </div>
            </details>
          )}
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
      {/* The triage's RESULT, as a card — it used to live only as the raw
          JSON tail of the transcript. Severity, duplicate, component and the
          proposed task, per report; the reasoning stays in the transcript. */}
      {run.stage === "triage" && triage.length > 0 && (
        <section data-testid="triage-results" className="m-2 rounded-card border border-hairline p-3">
          <h2 className="mb-2 text-sm font-medium text-ink">triaged {triage.length} report{triage.length === 1 ? "" : "s"}</h2>
          <ul className="space-y-2 text-sm">
            {triage.map((t) => (
              <li key={t.bug} data-testid={`triage-result-${t.bug}`}>
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="font-mono text-xs text-ink-muted">{t.bug}</span>
                  {t.severity && <span className="text-xs" style={{ color: `var(--status-${t.severity === "critical" ? "critical" : t.severity === "high" ? "serious" : t.severity === "low" ? "good" : "warning"})` }}>{t.severity}</span>}
                  <span className="text-xs text-ink-muted">{t.duplicate_of ? `duplicate of ${t.duplicate_of}` : "not a duplicate"}</span>
                  {t.component && <span className="text-xs text-ink-muted">· {t.component}</span>}
                  {t.reproducible !== undefined && <span className="text-xs text-ink-muted">· {t.reproducible ? "reproducible" : "not reproducible"}</span>}
                </div>
                {t.task_title && <div className="mt-0.5 text-ink">→ {t.task_title}</div>}
                {t.reason && <p className="mt-0.5 line-clamp-2 text-xs text-ink-muted" title={t.reason}>{t.reason}</p>}
                {(t.suspected_files ?? []).length > 0 && (
                  <div className="mt-0.5 flex flex-wrap gap-1">{t.suspected_files!.map((f) => <span key={f} className="rounded border border-hairline px-1 font-mono text-[11px] text-ink-muted">{f}</span>)}</div>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
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
            onReject={() => {
              setActionError(null);
              void client.reject(runId).catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
            }}
            onAbort={() => {
              setActionError(null);
              void client.abort(runId).catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
            }}
            onRequestChanges={stageToRevise || run.stage === "release" ? requestChanges : undefined}
            onResume={() => {
              setActionError(null);
              void client.runResume(runId).then((r) => useRuns.getState().setRun(r)).catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
            }}
            revisionRun={revisionRun}
            redoNote={run.redo_note}
            onRetry={(note) => void relaunch({ mode: run.mode, ducklings: relaunchDucklings, note })}
          />
          <SurveyCoverageLine run={run} testId="proposal-unaccounted" />
          {(() => {
            const unread = (run.pending_data?.unread_refs as string[] | undefined) ?? [];
            if (unread.length === 0) return null;
            return (
              <p data-testid="run-unread-refs" className="mt-2 text-xs text-warn">
                ⚠ {unread.length} reference document{unread.length === 1 ? " was" : "s were"} digested
                but never opened during this run:{" "}
                {unread.map((r) => r.split("/").pop()).join(", ")} — the draft may miss their detail.
              </p>
            );
          })()}
          {/* The declared-fallback door: provider weather, a stand-in named
              in Settings, one click to swap the seats and go — recorded as
              seat_failover, never a router's silent choice. */}
          {(() => {
            if (run.pending_kind !== "provider" && run.pending_kind !== "error") return null;
            if (!next.includes("resume")) return null;
            for (let i = events.length - 1; i >= 0; i--) {
              const e = events[i]!;
              if (e.type === "provider_retry") {
                const from = (e.data as { duckling?: string }).duckling ?? "";
                const to = fleet.find((d) => d.id === from)?.fallback;
                if (!from || !to) return null;
                return (
                  <div className="mt-2 flex items-center gap-2" data-testid="reseat-offer">
                    <button
                      type="button"
                      data-testid="reseat-button"
                      onClick={() => {
                        setActionError(null);
                        void client.runReseat(runId, from, to).catch((e) => setActionError(e instanceof Error ? e.message : String(e)));
                      }}
                      className="rounded border border-hairline px-2 py-1 text-sm"
                    >
                      Reseat to {to} & resume
                    </button>
                    <span className="text-xs text-ink-muted">
                      {from} is unreachable; {to} is its declared fallback and inherits the work
                    </span>
                  </div>
                );
              }
            }
            return null;
          })()}
        </section>
      )}

      {(actionError || relaunchError) && (
        <p className="m-2 text-critical" role="alert" data-testid={actionError ? "abort-error" : "action-error"}>
          action failed: {actionError ?? relaunchError}
        </p>
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

      {/* The chat's composer does NOT live here: it is pinned to the bottom
          of the view, where the conversation grows toward it — the reply box
          at the top had the person scrolling down to read and back up to
          answer, on every turn. */}
      {pending && pending.kind !== "chat" && (
        <section className="m-2 rounded-card border border-hairline p-3" data-testid="pending-human">
          <StatusChip role="serious" label={`waiting for you — ${pending.kind}`} />
          {pending.detail && !pending.question && (
            <p className="mt-1 whitespace-pre-wrap break-words text-sm text-ink" data-testid="pending-detail">
              {pending.detail}
            </p>
          )}
          {/* When Abort is the only door, say so — "waiting for you" over a
              provider pause read as a missing Resume button. */}
          {["provider", "error", "budget"].includes(pending.kind) && !next.includes("resume") && (
            <p className="mt-1 text-sm text-ink-muted" data-testid="pending-abort-only">
              A {run.stage} run cannot be resumed once stopped — Abort is the only door, and it loses
              nothing: {run.stage === "release" ? "Draft next release rewrites the notes from the record" : "starting it again rebuilds from the record"}.
            </p>
          )}
          {pending.question && (
            <div className="mt-2">
              <p className="text-ink">{pending.question}</p>
              {pending.advisorPending && (
                <p className="mt-1 text-sm text-ink-muted" data-testid="advisor-pending"><span className="cog-turn" aria-hidden="true">⚙</span> {pending.advisorPending} is preparing a recommendation</p>
              )}
              {pending.detail && pending.kind === "question" && !pending.advice && (
                <p className="mt-1 text-sm text-critical" data-testid="advice-failed">Advisor recommendation failed: {pending.detail}</p>
              )}
              {pending.advice && (
                <div className="mt-2 rounded border border-hairline p-2" data-testid="advice">
                  <p className="text-xs text-ink-muted">
                    {pending.advisor ?? "the advisor"} recommends:
                  </p>
                  <p className="mt-1 whitespace-pre-wrap text-sm text-ink">{pending.advice}</p>
                  <button
                    type="button"
                    data-testid="advice-use"
                    onClick={() =>
                      client.answer(runId, pending.questionId ?? "", pending.advice!).catch(() => {})
                    }
                    className="mt-2 rounded border border-good px-2 py-1 text-sm text-good"
                  >
                    Answer with this
                  </button>
                </div>
              )}
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

      <div className="p-4">
        {/* min-w-0: a flex/grid column will not shrink below its content
            without it, so one long unbroken thinking line forced the column
            wide and shoved the rail off the window's edge on resize. */}
        <section data-testid="conversation" className="min-w-0">
          {rosterEntries.length > 0 && (
            <div className="mb-3" data-testid="run-seat-chips">
              <SeatChips entries={rosterEntries} fleet={fleet} measured={measured} activeDuckling={pending?.advisorPending} />
            </div>
          )}
          {/* Viewport-relative, so it adapts to the window without depending on
              a chain of parent heights resolving — which is what broke. */}
          <VirtualList items={turns} height="60vh" followTail={liveNow}>
            {(t, i) => {
              // A finished turn folds to its summary; the LIVE turn and the
              // last one stay open — that is where the reader's eyes are.
              // Human chat messages never fold: they are short and they ARE
              // the conversation.
              const key = `${t.round}:${t.turn}`;
              const foldable = t.done && !t.messageOnly;
              const isCollapsed = foldable && !(turnChoice[key] ?? i === turns.length - 1);
              return (
                <ConversationTurn
                  block={t}
                  roster={roster}
                  color={ducklingColors[t.duckling]}
                  streamed={t.messageOnly ? undefined : deltas[`${t.round}:${t.turn}`]}
                  reasoning={t.messageOnly ? undefined : (reasoning[`${t.round}:${t.turn}`] ?? t.reasoning)}
                  collapsed={isCollapsed}
                  deliverableTexts={deliverables?.lines.map((l) => l.text)}
                  onToggle={
                    foldable
                      ? () => setTurnChoice((c) => ({ ...c, [key]: isCollapsed }))
                      : undefined
                  }
                />
              );
            }}
          </VirtualList>
      {/* The chat composer, attached to the conversation box itself: the
          transcript scrolls INSIDE the list above, so sitting right under it
          keeps the reply box in reach without floating over the timeline and
          the model-calls dock, which a viewport-sticky composer did. */}
      {chatLive && (
        <section
          className="mt-auto border-t border-hairline bg-surface p-3"
          data-testid="chat-reply"
        >
          <div className="flex items-start gap-2">
            <textarea
              aria-label="chat message"
              data-testid="chat-message"
              value={chatMsg}
              onChange={(e) => setChatMsg(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && chatMsg.trim() && !chatBusy && pending?.kind === "chat") {
                  e.preventDefault();
                  sendChat();
                }
              }}
              rows={2}
              disabled={pending?.kind !== "chat"}
              placeholder={pending?.kind === "chat" ? "your reply… (Enter to send)" : "the consultant is thinking…"}
              className="flex-1 rounded border border-hairline bg-surface2 px-2 py-1 disabled:opacity-60"
            />
            <input ref={chatImageInput} type="file" accept="image/*" multiple data-testid="chat-image" className="hidden" onChange={(e) => { readChatImages(e.target.files); e.currentTarget.value = ""; }} />
            <button type="button" data-testid="chat-add-image" disabled={!chatCanSee || pending?.kind !== "chat"} title={chatCanSee ? "Add images" : "The chat duckling needs vision to see attached images"} onClick={() => chatImageInput.current?.click()} className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40">Add image</button>
            <button
              type="button"
              data-testid="chat-send"
              disabled={chatBusy || pending?.kind !== "chat" || !chatMsg.trim()}
              onClick={sendChat}
              className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
            >
              {chatBusy ? "Sending…" : "Send"}
            </button>
            <button
              type="button"
              data-testid="chat-end"
              disabled={chatBusy}
              onClick={() => void client.chatEnd(runId).catch(() => {})}
              title="Closes the conversation as finished; the transcript stays on the record"
              className="rounded border border-hairline px-2 py-1 text-sm text-ink-muted disabled:opacity-40"
            >
              End chat
            </button>
          </div>
          {chatImages.length > 0 && <div className="mt-2 flex flex-wrap gap-1">{chatImages.map((image, index) => <span key={`${image.name}-${index}`} data-testid="chat-image-chip" className="flex items-center gap-1 rounded border border-hairline px-1 py-0.5 text-xs"><img src={image.data} alt="" className="h-6 w-6 object-cover" />{image.name}<button type="button" aria-label={`remove image ${image.name}`} onClick={() => setChatImages((current) => current.filter((_, i) => i !== index))}>×</button></span>)}</div>}
          {chatImageError && <p className="mt-1 text-xs text-critical" data-testid="chat-image-error">{chatImageError}</p>}
        </section>
      )}
        </section>

      </div>


      {/* The bottom dock: timeline and tab bar pinned together to the
          viewport's bottom edge while the transcript scrolls — the same
          "consulted, not read" contract as the rail. The tab bar lived below
          a build's long diff and went unseen: calls looked absent from build
          runs when it was one unscrolled click away. Except in a live chat:
          the composer owns the bottom edge there. */}
      {/* Sticky only while the run works: mid-transcript the dock is
          consulted, not read. On a finished run the reading is top-to-bottom
          — verdict, findings, what changed — and a dock pinned to the
          viewport under an empty stretch read as another page's footer. */}
      <div
        className={finished ? "border-t border-hairline" : chatLive ? "border-t border-hairline" : "mt-auto sticky bottom-0 z-10 border-t border-hairline bg-page"}
        data-testid="bottom-dock"
      >
        <div className={codeRun ? "px-4 pt-2" : "flex flex-wrap items-center gap-x-4 px-4 pt-1"}>
          <ToolTimeline calls={timeline} />
        </div>

        {/* A tab with nothing in it is dimmed and counted, so an empty one
            reads as "there was none" rather than "something failed to load".
            A run with no candidates is not a broken run — solo, pair and
            split never have any. */}
        <nav className={codeRun ? "mt-1 flex gap-2 px-4" : "flex gap-2 px-4 pb-1"}>
        {(codeRun
          ? (([
              ["diff", testHunks ? "edits tests" : diff ? undefined : "empty"],
              ["verify", verify ? undefined : "no output"],
              ["candidates", candidates.length ? String(candidates.length) : "none"],
              ["calls", calls.length ? String(calls.length) : "none"],
            ] as [Tab, string | undefined][])
              // A section that is empty BY DESIGN is not announced on a
              // finished run: "candidates none" on every solo build taught
              // the eye to skip the bar — and the diff moves up beside the
              // verdict once the run ends, so its tab goes with it.
              .filter(([t, note]) => !(finished && t === "candidates" && note === "none"))
              .filter(([t]) => !(t === "diff" && finished && (diff || testHunks))))
          : // A document, triage or chat run has no diff, no gate output and
            // no candidates BY DESIGN — three dimmed tabs whose empty states
            // all truthfully said "none" taught the eye to skip the bar
            // entirely, including the one tab that mattered.
            ([["calls", calls.length ? String(calls.length) : "none"]] as [Tab, string | undefined][])
        ).map(([t, note]) => (
          <button
            key={t}
            type="button"
            onClick={() => {
              if (shownTab === t) {
                setTabsFolded((v) => !v);
              } else {
                setTab(t);
                setTabsFolded(false);
              }
              // The panel sits below the dock's natural position; a click
              // from mid-transcript must land the reader on the content.
              requestAnimationFrame(() =>
                tabPanelRef.current?.scrollIntoView?.({ behavior: "smooth", block: "start" }),
              );
            }}
            data-testid={`tab-${t}`}
            className={`px-2 py-1 text-sm ${shownTab === t && !tabsFolded ? "text-ink" : "text-ink-muted"}`}
          >
            {shownTab === t && (
              <span aria-hidden="true" className="mr-1 text-xs">
                {tabsFolded ? "›" : "⌄"}
              </span>
            )}
            {/* The timeline above counts TOOL calls; this tab counts calls
                to the MODEL. Two counters labelled alike read as one broken
                counter. */}
            {t === "calls" ? "model calls" : t}
            {note && <span className="ml-1 text-xs text-ink-muted">{note}</span>}
          </button>
        ))}
        </nav>
      </div>

      {!tabsFolded && (
      <div className="p-2" ref={tabPanelRef}>
        {/* The test hunks come first, above the rest of the diff, because the
            whole point is that they are read before the decision and not
            after. Not a blocker — sometimes a test is genuinely wrong (05
            §5.3) — so the Accept button stays exactly where it was. */}
        {shownTab === "diff" && !(finished && (diff || testHunks)) && testHunks && (
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
        {shownTab === "diff" && !(finished && (diff || testHunks)) &&
          (diff ? (
            <DiffView files={parseDiff(diff)} />
          ) : (
            <p className="p-2 text-sm text-ink-muted" data-testid="diff-empty">
              {run.status === "running"
                ? "Nothing written yet."
                : "This run changed no files."}
            </p>
          ))}
        {shownTab === "verify" &&
          (verify ? (
            <pre className="overflow-x-auto bg-surface2 p-2 font-mono text-xs">{verify}</pre>
          ) : (
            <p className="p-2 text-sm text-ink-muted" data-testid="verify-empty">
              {gate?.unverified
                ? "No gate could run, so nothing was verified."
                : "The gate ran and printed nothing, which is what passing looks like."}
            </p>
          ))}
        {shownTab === "candidates" &&
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

        {shownTab === "calls" &&
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
      )}
        </div>

        {railOpen ? (
        <aside
          data-testid="run-rail"
          // overscroll-contain: the dock's scroll ends AT the dock. Without
          // it, reaching its bottom chained the wheel into the page scroller
          // and the transcript crawled away under a rail that felt "linked".
          className={`flex flex-col gap-3 p-4 md:sticky md:top-14 md:w-72 md:shrink-0 md:self-start md:overflow-y-auto md:overscroll-contain md:border-l md:border-hairline ${finished ? "md:max-h-[calc(100vh-12rem)]" : "md:h-[calc(100vh-12rem)]"}`}
        >
          <button
            type="button"
            data-testid="run-rail-hide"
            onClick={toggleRail}
            title="hide the rail (a strip stays to bring it back)"
            className="self-end text-xs text-ink-muted underline"
          >
            hide
          </button>
          {budget && finished && (
            /* A finished run's meters measure nothing any more; one line of
               what it actually spent, spenders beneath. */
            <div className="rounded-card border border-hairline p-3" data-testid="spent-line">
              <div className="text-sm text-ink-muted">spent</div>
              <div className="mt-1 text-sm text-ink tabular-nums">{money(budget.usd)} · {tokens(budget.tokens)} tokens · {Math.round(budget.turns)} turn{Math.round(budget.turns) === 1 ? "" : "s"}</div>
              <dl className="mt-2 border-t border-hairline pt-2 text-xs" data-testid="spend-by-duckling-done">
                {perDuckling.filter(([, v]) => (v?.calls ?? 0) > 0).map(([id, v]) => (
                  <div key={id} className="flex justify-between gap-2">
                    <dt className="truncate"><span style={{ color: ducklingColors[id] }}>{id}</span>{rolesByDuckling[id] && <span className="ml-1.5 text-ink-muted">{rolesByDuckling[id].join(" · ")}</span>}</dt>
                    <dd className="shrink-0 tabular-nums text-ink-muted">{tokens(v?.tokens ?? 0)} · {money(v?.cost_usd ?? 0)}</dd>
                  </div>
                ))}
              </dl>
            </div>
          )}
          {budget && !finished && (
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
                  inFlight={inFlightTokens}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "tokens").then((r) => useRuns.getState().setRun(r)).catch(() => {}) } : undefined}
                />
                <BudgetMeter
                  label="cost" used={budget.usd} limit={limit.usd} format={money}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "usd").then((r) => useRuns.getState().setRun(r)).catch(() => {}) } : undefined}
                />
                <BudgetMeter
                  label="turns"
                  used={budget.turns}
                  limit={limit.turns}
                  format={(n) => String(Math.round(n))}
                  lift={canLift ? { onLift: () => void client.runBudgetLift(run.id, "turns").then((r) => useRuns.getState().setRun(r)).catch(() => {}) } : undefined}
                />
                {/* Not a meter — the per-reply call cap inside the agent
                    loop, the ceiling a reviewer once died on at exactly call
                    one hundred. The lift lands mid-reply: every live loop
                    consults it before its next call. */}
                <div
                  className="flex items-baseline justify-between gap-2 text-sm text-ink-secondary"
                  data-testid="calls-cap"
                >
                  <span>calls / reply</span>
                  <span className="flex items-baseline gap-2">
                    {/* Live: where the CURRENT reply stands against its real
                        cap, from the loop's own count — the only thing that
                        knows both numbers. At rest: the configured shape. */}
                    <span className="tabular-nums" data-testid="calls-cap-value">
                      {(() => {
                        if (liveNow) {
                          for (let i = events.length - 1; i >= 0; i--) {
                            const e = events[i]!;
                            if (e.type === "reply_call") {
                              const d = e.data as { n?: number; max?: number };
                              const max = d.max ?? 0;
                              return `${d.n ?? "?"} / ${max >= 10000 ? "no cap" : max}`;
                            }
                          }
                        }
                        return run.agent_turns === -1 ? "no cap" : run.agent_turns ? String(run.agent_turns) : "default";
                      })()}
                    </span>
                    {canLift && (
                      <label
                        className="flex items-center gap-1 text-xs text-ink-muted"
                        title={
                          run.agent_turns === -1
                            ? "no cap on calls per reply — the budgets still guard"
                            : "remove the per-reply call cap now, mid-flight (cannot be undone)"
                        }
                      >
                        <input
                          type="checkbox"
                          data-testid="lift-calls"
                          checked={run.agent_turns === -1}
                          disabled={run.agent_turns === -1}
                          onChange={() => void client.runBudgetLift(run.id, "calls").then((r) => useRuns.getState().setRun(r)).catch(() => {})}
                        />
                        no cap
                      </label>
                    )}
                  </span>
                </div>
              </div>
              {/* One tracker serves every duckling and every turn, so the run's
                  total cannot say which model is burning it. In a mode with two
                  models that is usually the only question worth asking. */}
              {/* Even a solo run's one row earns its place: it is the answer
                  to "who is doing this" once the turn header has scrolled
                  away, with the live share beside the name. */}
              {perDuckling.length > 0 && (
                <dl className="mt-3 border-t border-hairline pt-2 text-xs" data-testid="spend-by-duckling">
                  {perDuckling.map(([id, d]) => (
                    <div key={id} className="flex justify-between gap-2">
                      <dt className="min-w-0 truncate">
                        <span style={{ color: ducklingColors[id] }}>{id}</span>
                        {rolesByDuckling[id] && (
                          <span className="ml-1.5 text-ink-muted">{rolesByDuckling[id].join(" · ")}</span>
                        )}
                      </dt>
                      {d && d.calls > 0 ? (
                        <dd className="shrink-0 tabular-nums text-ink-secondary">
                          {tokens(d.tokens)} · {money(d.cost_usd)} · {d.calls} call
                          {d.calls === 1 ? "" : "s"}
                        </dd>
                      ) : (
                        <dd className="shrink-0 text-ink-muted">
                          {runIsLive ? "no calls yet" : "never called"}
                        </dd>
                      )}
                    </div>
                  ))}
                </dl>
              )}
            </div>
          )}
          {/* The gate on a finished run is already said once, in the turn
              list and the header; the box earned its place only while the
              command is still to run. */}
          {!finished && <GateCard gate={gate} stage={run.stage} />}
        </aside>
        ) : (
          <button
            type="button"
            data-testid="run-rail-pill"
            onClick={toggleRail}
            title="show budget and gate"
            // mr-3 keeps the pill out of the overlay scrollbar's lane: flush
            // against the window edge, every click landed on the scrollbar
            // and the collapsed rail could never be reopened. A full border
            // now that it floats free of the edge it used to hug.
            className="mr-3 self-start rounded border border-hairline px-2 py-2 text-xs text-ink-muted md:sticky md:top-14"
          >
            ‹
          </button>
        )}
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
          {call.upstream && <span className="text-ink-muted">via {call.upstream}</span>}
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
