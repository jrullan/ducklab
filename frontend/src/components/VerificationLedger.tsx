import { useState } from "react";
import type { Bug, EngineClient, Run } from "../api/client";
import { useRuns } from "../store/runs";
import { parseDiff, type DiffFile } from "../lib/runview";
import { SideDrawer } from "./SideDrawer";
import { ShellCmd } from "./ShellCmd";
import { Prose } from "./Prose";

/**
 * The verification queue, folded (B-281, absorbing B-216).
 *
 * N fixed bugs used to render as N identical ~180px cards, each saying "try
 * what the report describes" — a homogeneous work queue drawn as N unique
 * decisions, drowning everything else in Now. This is one card for the whole
 * queue; the drawer holds compact rows with the two verdict buttons inline,
 * and an expanded row tells the person HOW to verify.
 *
 * How depends on the fix's nature (B-216). The evidence is the accepted run's
 * own diff: when it adds a test, that test is the proof — the row names it and
 * the exact command that runs it, and the accept affordance says so. Only a
 * fix that carries no pinning test gets the "try what the report describes"
 * phrasing, with the report's steps and the files the fix changed.
 */

/** One pinning test and the command that runs it. */
export interface Proof {
  name: string;
  cmd: string;
}

/** What the accepted run's diff says about how to verify. */
export interface FixEvidence {
  files: string[];
  proofs: Proof[];
}

const GO_TEST = /^\+func (Test\w+)\(/gm;
const VITEST = /^\+\s*(?:it|test)\(\s*(["'`])(.+?)\1/gm;
const PYTEST = /^\+def (test_\w+)\(/gm;

/** The tests a diff adds, each with its runnable command. Derived from the
 * record, never from a model: a test that is not in the diff is not a proof. */
export function proofsFromDiff(files: readonly DiffFile[]): Proof[] {
  const out: Proof[] = [];
  const seen = new Set<string>();
  const add = (name: string, cmd: string) => {
    if (seen.has(cmd)) return;
    seen.add(cmd);
    out.push({ name, cmd });
  };
  for (const file of files) {
    if (!file.isTest) continue;
    const added = file.hunks.join("\n");
    if (/_test\.go$/.test(file.path)) {
      const dir = file.path.includes("/") ? file.path.slice(0, file.path.lastIndexOf("/")) : ".";
      for (const m of added.matchAll(GO_TEST)) add(m[1]!, `go test ./${dir} -run '^${m[1]}$'`);
    } else if (/\.test\.(ts|tsx|js|jsx)$/.test(file.path)) {
      const rel = file.path.replace(/^frontend\//, "");
      const prefix = file.path.startsWith("frontend/") ? "cd frontend && " : "";
      for (const m of added.matchAll(VITEST)) {
        const name = m[2]!.replace(/"/g, '\\"');
        add(m[2]!, `${prefix}npx vitest run ${rel} -t "${name}"`);
      }
    } else if (/\.py$/.test(file.path)) {
      for (const m of added.matchAll(PYTEST)) add(m[1]!, `pytest ${file.path} -k ${m[1]}`);
    }
  }
  return out;
}

export function evidenceFromDiff(diff: string): FixEvidence {
  const files = parseDiff(diff);
  return { files: files.map((f) => f.path).filter(Boolean), proofs: proofsFromDiff(files) };
}

/** The run whose accept landed this task's fix, when the record holds one. */
function acceptedRunFor(runs: Record<string, Run>, taskId: string | undefined): Run | undefined {
  if (!taskId) return undefined;
  return Object.values(runs)
    .filter((r) => r.task_id === taskId && r.accepted)
    .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""))[0];
}

export function VerificationLedger({
  bugs,
  client,
  projectId,
  onMoved,
}: {
  bugs: Bug[];
  client: EngineClient;
  projectId: string;
  onMoved: (bugId: string, status: string) => void;
}) {
  const runs = useRuns((s) => s.runs);
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  // Evidence by run id, fetched once per expansion. null while loading; an
  // empty-files evidence when the diff could not be read.
  const [evidence, setEvidence] = useState<Record<string, FixEvidence | null>>({});

  if (bugs.length === 0) return null;
  const n = bugs.length;
  const headline = n === 1 ? "1 fixed bug awaits your verification" : `${n} fixed bugs await your verification`;

  const expand = (bug: Bug) => {
    const next = expanded === bug.id ? null : bug.id;
    setExpanded(next);
    if (!next) return;
    const run = acceptedRunFor(runs, bug.task_id);
    if (!run || run.id in evidence || typeof client.runDiff !== "function") return;
    setEvidence((cur) => ({ ...cur, [run.id]: null }));
    client
      .runDiff(run.id)
      .then((d) => setEvidence((cur) => ({ ...cur, [run.id]: evidenceFromDiff(d.diff ?? "") })))
      .catch(() => setEvidence((cur) => ({ ...cur, [run.id]: { files: [], proofs: [] } })));
  };

  const move = (bug: Bug, status: string) =>
    void client
      .moveBug(projectId, bug.id, status)
      .then(() => onMoved(bug.id, status))
      .catch(() => {});

  return (
    <>
      <div data-testid="now-verify-ledger" className="rounded-card border border-hairline p-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <p className="text-sm text-ink">{headline}</p>
          <button
            type="button"
            data-testid="now-verify-open"
            onClick={() => setOpen(true)}
            className="rounded border border-hairline px-2 py-1 text-xs"
          >
            Review {n === 1 ? "it" : `all ${n}`}
          </button>
        </div>
        <p className="mt-1 text-xs text-ink-muted">
          Each row says how to verify: the test that proves an internal fix, or the report's steps for a fix you can see. The gate that passed may prove much less.
        </p>
        <p className="mt-1 truncate font-mono text-xs text-ink-muted" title={bugs.map((b) => b.id).join(", ")}>
          {bugs.slice(0, 6).map((b) => b.id).join(" · ")}
          {n > 6 ? ` · +${n - 6}` : ""}
        </p>
      </div>
      {open && (
        <SideDrawer
          title={headline}
          subtitle="Verified means the report is answered, not that a gate passed. Reopen anything that is still broken."
          onClose={() => setOpen(false)}
          testId="now-verify-drawer"
        >
          <ul className="space-y-2">
            {bugs.map((b) => {
              const isOpen = expanded === b.id;
              const run = acceptedRunFor(runs, b.task_id);
              const ev = run ? evidence[run.id] : undefined;
              const proven = !!ev && ev.proofs.length > 0;
              return (
                <li key={b.id} data-testid="now-verify-row" data-proof={isOpen && proven ? "test" : undefined} className="rounded border border-hairline p-2">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="font-mono text-sm text-ink">{b.id}</span>
                    <span className="min-w-0 flex-1 truncate text-sm text-ink-secondary" title={b.title}>{b.title}</span>
                    {b.task_id && <span className="text-xs text-ink-muted">fixed by {b.task_id}</span>}
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-2">
                    {(b.next ?? []).includes("verified") && (
                      <button type="button" data-testid="now-verify-yes" onClick={() => move(b, "verified")} className="rounded border border-hairline px-2 py-0.5 text-xs">
                        {isOpen && proven ? "The test proves it — Verified" : "Verified — it works"}
                      </button>
                    )}
                    {(b.next ?? []).includes("in_progress") && (
                      <button type="button" data-testid="now-verify-no" onClick={() => move(b, "in_progress")} className="rounded border border-hairline px-2 py-0.5 text-xs">
                        Still broken
                      </button>
                    )}
                    <button
                      type="button"
                      data-testid="now-verify-expand"
                      aria-expanded={isOpen}
                      onClick={() => expand(b)}
                      className="text-xs text-ink-muted underline"
                    >
                      {isOpen ? "hide" : "how to verify"}
                    </button>
                  </div>
                  {isOpen && (
                    <div data-testid="now-verify-guide" className="mt-2 space-y-3 border-t border-hairline pt-2">
                      {!b.task_id ? (
                        <p className="text-xs text-ink-muted">No task is recorded for this fix. Try what the report describes.</p>
                      ) : !run ? (
                        <p className="text-xs text-ink-muted">No accepted run for {b.task_id} is in the record here. Try what the report describes.</p>
                      ) : ev === undefined || ev === null ? (
                        <p className="text-xs text-ink-muted">Reading what {run.id} changed…</p>
                      ) : proven ? (
                        <section data-testid="now-verify-proof">
                          <h4 className="text-xs font-medium text-ink">The proof</h4>
                          <p className="mt-1 text-xs text-ink-secondary">
                            {b.task_id} landed with {ev.proofs.length === 1 ? "a test that pins it" : `${ev.proofs.length} tests that pin it`}
                            {run.commit_sha ? ` (commit ${run.commit_sha.slice(0, 7)})` : ""}; it ran green in the landing gate. Run it yourself:
                          </p>
                          <ul className="mt-1 space-y-1">
                            {ev.proofs.map((p) => (
                              <li key={p.cmd} className="font-mono text-xs">
                                <ShellCmd cmd={p.cmd} className="whitespace-pre-wrap break-all" title={p.name} />
                              </li>
                            ))}
                          </ul>
                        </section>
                      ) : (
                        <section data-testid="now-verify-try">
                          <h4 className="text-xs font-medium text-ink">Try what the report describes</h4>
                          <p className="mt-1 text-xs text-ink-muted">
                            {b.task_id} landed without a pinning test{run.commit_sha ? ` (commit ${run.commit_sha.slice(0, 7)})` : ""}; the only proof is your eyes.
                          </p>
                        </section>
                      )}
                      {b.task_id && run && ev && !proven && (
                        <section>
                          <h4 className="text-xs font-medium text-ink">The report</h4>
                          {b.body?.trim() ? (
                            <Prose body={b.body} className="mt-1 space-y-1 text-xs text-ink-secondary" />
                          ) : (
                            <p className="mt-1 text-xs text-ink-muted">The report has no body; its title is all there is to try.</p>
                          )}
                        </section>
                      )}
                      {(!b.task_id || !run) && b.body?.trim() && (
                        <Prose body={b.body} className="space-y-1 text-xs text-ink-secondary" />
                      )}
                      {ev && ev.files.length > 0 && (
                        <section>
                          <h4 className="text-xs font-medium text-ink">What the fix changed</h4>
                          <ul className="mt-1 font-mono text-xs text-ink-muted">
                            {ev.files.map((f) => <li key={f}>{f}</li>)}
                          </ul>
                        </section>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </SideDrawer>
      )}
    </>
  );
}
