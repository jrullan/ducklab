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
import type { Artifact, EngineClient, RosterEntry, Section, TraceError } from "../api/client";
import { DiffView } from "../components/DiffView";
import { parseDiff } from "../lib/runview";
import { Prose } from "../components/Prose";
import { DecisionCard } from "../components/DecisionCard";

const STAGES = [
  { stage: "intake", kind: "requirements", label: "Requirements", prefix: "REQ" },
  { stage: "spec", kind: "spec", label: "Spec", prefix: "SPEC" },
  { stage: "plan", kind: "plan", label: "Plan", prefix: "M" },
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
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [promoting, setPromoting] = useState(false);
  const [brief, setBrief] = useState("");
  // The plan amendment's text — Review's light exit, separate from the brief
  // so the two doors never share a box.
  const [amendment, setAmendment] = useState("");
  // How many tasks the spec has not caught up with — the settle button's
  // number, fetched with the artifact so the spec tab can offer the one-click
  // repayment without the person counting markers on the board.
  const [debtCount, setDebtCount] = useState(0);
  const [starting, setStarting] = useState(false);
  const [mode, setMode] = useState("council");
  const [rounds, setRounds] = useState(2);
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
    // Deferred a tick so a partial test client without tasks() rejects
    // instead of throwing synchronously in the effect.
    Promise.resolve()
      .then(() => client.tasks(projectId))
      .then((ts) => setDebtCount(ts.filter((t) => t.spec_debt).length))
      .catch(() => setDebtCount(0));
  }, [client, projectId, startedRun]);

  // Who will actually do it. A button that says only "Draft it" hides the two
  // things worth knowing before spending minutes and tokens: which models, and
  // whether one of them is going to critique the other.
  // Asked FOR THE CHOSEN MODE, and refetched when it changes: a council
  // line-up overrides the roster at run time, and a preview reading the bare
  // roster warned that one model would critique its own draft while the run
  // was going to use the two the person had saved.
  useEffect(() => {
    client
      .roster(projectId, mode)
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
  // Whether the proposing run was already decided. The lifecycle keeps a
  // rejected proposal on disk (05 §1.1: a failed attempt is a record) — and
  // this view read "file exists" as "decision pending", so a person who had
  // just rejected a draft came back to a screen still awaiting their decision.
  const [proposalDecided, setProposalDecided] = useState(false);
  const proposalRunId = artifact?.proposal?.run_id ?? "";
  useEffect(() => {
    if (!proposalRunId) {
      setProposalNext([]);
      return;
    }
    let cancelled = false;
    client
      .run(proposalRunId)
      .then((d) => {
        if (cancelled) return;
        setProposalNext(d.run.next ?? []);
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
      const run = await client.stageStart(projectId, active.stage, {
        from: brief.trim(),
        mode,
        rounds,
        adopt,
      });
      setStartedRun(run.id);
      setBrief("");
      // The run is where the work is visible. Not navigated to automatically:
      // someone who just wrote a brief may want to read it back, and a view
      // that jumps out from under them is a view that lost their place.
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  }

  const sections = artifact?.sections ?? [];
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
              title="Proposal awaiting your decision"
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
            {proposalAsDiff ? (
              <DiffView files={parseDiff(artifact.proposal.diff)} />
            ) : artifact.proposal.sections && artifact.proposal.sections.length > 0 ? (
              <ol className="space-y-3" data-testid="proposal-sections">
                {artifact.proposal.sections.map((s) => (
                  <SectionCard key={s.id} section={s} broken={new Set()} />
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
                <p className="text-sm text-ink">This project already has code.</p>
                <p className="mt-1 text-xs text-ink-muted">
                  Adopt it: the council reads the tree and drafts the requirements the code
                  already satisfies — marked as derived, gated by you like everything else.
                  Anything you type below travels along as context. Or ignore this and start
                  from the brief alone, as if greenfield.
                </p>
                <button
                  type="button"
                  onClick={() => void start(true)}
                  disabled={starting}
                  data-testid="cycle-adopt"
                  className="mt-2 rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
                >
                  {starting ? "Starting…" : "Survey the code"}
                </button>
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
            {active.stage === "plan" && sections.length > 0 && (
              <div className="mb-3 rounded-card border border-hairline p-2" data-testid="plan-extend">
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
                <button
                  type="button"
                  data-testid="plan-extend-start"
                  disabled={!amendment.trim() || starting}
                  onClick={() => {
                    setStarting(true);
                    setFailure(null);
                    void client
                      .stageStart(projectId, "plan", { extend: amendment.trim() })
                      .then((run) => {
                        setStartedRun(run.id);
                        setAmendment("");
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
              <span data-testid="stage-who" className="text-xs text-ink-muted">
                {describeRun(mode, roster, rounds)}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => void start()}
                disabled={starting}
                data-testid="cycle-run"
                className="rounded border border-hairline px-3 py-1 text-sm disabled:opacity-50"
              >
                {starting ? "Starting…" : sections.length === 0 ? "Draft it" : "Redraft"}
              </button>
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
          </section>
        )}

        <ol className="space-y-3">
          {sections.map((s) => (
            <SectionCard key={s.id} section={s} broken={broken} />
          ))}
        </ol>
      </div>

      <aside data-testid="trace-rail" className="w-72 shrink-0">
        <h2 className="text-sm font-medium text-ink mb-2">Traceability</h2>
        {checkedProposed.length > 0 && (
          <p data-testid="trace-scope" className="text-xs text-ink-muted mb-2">
            Checking the proposed {checkedProposed.join(", ")} — this is what you are
            about to accept.
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

function SectionCard({ section, broken }: { section: Section; broken: Set<string> }) {
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
