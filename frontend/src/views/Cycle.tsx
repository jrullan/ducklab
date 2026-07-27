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
import type { Artifact, EngineClient, Section, TraceError } from "../api/client";
import { DiffView } from "../components/DiffView";
import { parseDiff } from "../lib/runview";
import { parseProse, type Span } from "../lib/prose";
import { EmptyState } from "../components/EmptyState";

const STAGES = [
  { stage: "intake", kind: "requirements", label: "Requirements", prefix: "REQ" },
  { stage: "spec", kind: "spec", label: "Spec", prefix: "SPEC" },
  { stage: "plan", kind: "plan", label: "Plan", prefix: "M" },
] as const;

type StageDef = (typeof STAGES)[number];

export function Cycle({ client, projectId }: { client: EngineClient; projectId: string }) {
  const [active, setActive] = useState<StageDef>(STAGES[0]);
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [errors, setErrors] = useState<TraceError[]>([]);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [promoting, setPromoting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setFailure(null);
    try {
      const [a, e] = await Promise.all([
        client.artifact(projectId, active.kind),
        client.traceCheck(projectId),
      ]);
      setArtifact(a);
      setErrors(e);
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

  const sections = artifact?.sections ?? [];
  // Only the breaks this stage can answer for: showing a plan's dangling
  // references while the user is reading requirements is noise.
  const mine = errors.filter((e) => e.id.startsWith(active.prefix + "-"));

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

        {artifact?.proposal && (
          <section data-testid="cycle-proposal" className="mb-6 rounded-card border border-serious p-3">
            <div className="flex items-center justify-between mb-2">
              <div>
                <div className="text-sm font-medium text-ink">Proposal awaiting your decision</div>
                {artifact.proposal.ducklings && (
                  <div className="text-xs text-ink-muted">
                    from {artifact.proposal.ducklings.join(", ")}
                  </div>
                )}
              </div>
              {/* No Reject button: rejecting is leaving it alone. A button that
                  deleted the draft would destroy the evidence of what the
                  ducklings actually produced. */}
              <button
                data-testid="cycle-accept"
                disabled={promoting}
                onClick={() => void accept()}
                className="rounded border border-hairline px-3 py-1 text-sm text-ink disabled:opacity-50"
              >
                {promoting ? "Accepting…" : "Accept"}
              </button>
            </div>
            <DiffView files={parseDiff(artifact.proposal.diff)} />
          </section>
        )}

        {loading && <div className="text-sm text-ink-muted">Loading…</div>}

        {!loading && sections.length === 0 && !artifact?.proposal && (
          <EmptyState
            message={`No ${active.label.toLowerCase()} yet — run \`ducklab ${active.stage}\` to draft it.`}
          />
        )}

        <ol className="space-y-3">
          {sections.map((s) => (
            <SectionCard key={s.id} section={s} broken={mine.some((e) => e.id === s.id)} />
          ))}
        </ol>
      </div>

      <aside data-testid="trace-rail" className="w-72 shrink-0">
        <h2 className="text-sm font-medium text-ink mb-2">Traceability</h2>
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

/** Renders an artifact body as the markdown it is. */
function Prose({ body }: { body: string }) {
  return (
    <div className="mt-2 space-y-2 text-sm text-ink-secondary">
      {parseProse(body).map((b, i) =>
        b.kind === "list" ? (
          <ul key={i} className="list-disc space-y-1 pl-5">
            {b.items.map((spans, j) => (
              <li key={j}>
                <Spans spans={spans} />
              </li>
            ))}
          </ul>
        ) : (
          <p key={i}>
            <Spans spans={b.spans} />
          </p>
        ),
      )}
    </div>
  );
}

function Spans({ spans }: { spans: Span[] }) {
  return (
    <>
      {spans.map((s, i) =>
        s.kind === "strong" ? (
          <strong key={i} className="font-medium text-ink">
            {s.text}
          </strong>
        ) : s.kind === "code" ? (
          <code key={i} className="font-mono text-ink">
            {s.text}
          </code>
        ) : (
          <span key={i}>{s.text}</span>
        ),
      )}
    </>
  );
}

function SectionCard({ section, broken }: { section: Section; broken: boolean }) {
  return (
    <li
      data-testid="cycle-section"
      data-broken={broken ? "true" : "false"}
      className={"rounded-card border p-3 " + (broken ? "border-serious" : "border-hairline")}
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
      <Prose body={section.body} />
      {section.children && section.children.length > 0 && (
        <ul className="mt-2 space-y-1 pl-3 border-l border-hairline">
          {section.children.map((c) => (
            <li key={c.id} className="text-xs">
              <span className="font-mono text-ink-muted">{c.id}</span>{" "}
              <span className="text-ink">{c.title}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}
