/**
 * An intention and its revisions, as one chain (B-521).
 *
 * A revision of an intake proposal is the person asking for the same thing
 * again with a correction — not a new intention. The Documents view shows the
 * original brief first and each revision under it, the person's own note
 * apart from the reviewer findings they carried over: the TI-36X journal had
 * made three findings blobs look like three things the person asked for.
 */

import { routeHref } from "../app/routes";
import { parseIntentChain } from "../lib/intent";
import { Prose } from "./Prose";

export function IntentChain({ body }: { body: string }) {
  const chain = parseIntentChain(body);
  return (
    <div data-testid="intent-chain" className="mt-2 space-y-3">
      <p data-testid="intent-chain-status" className="text-xs text-ink-muted">
        {chain.outcome ?? "pending"}
        {chain.requirements.length > 0 && <> · realized as {chain.requirements.join(", ")}</>}
        {chain.revisions.length > 0 && <> · {chain.revisions.length} revision{chain.revisions.length === 1 ? "" : "s"}</>}
        {chain.imported && <> · imported from the run record</>}
        {chain.run && <> · first run <a className="underline" href={routeHref({ name: "run", id: chain.run })}>{chain.run}</a></>}
      </p>
      <div data-testid="intent-chain-brief">
        <p className="text-xs uppercase tracking-wide text-ink-muted">What you asked for</p>
        <Prose body={chain.brief || "_No brief was recorded._"} className="mt-1 space-y-2 text-sm text-ink" />
      </div>
      {chain.revisions.length > 0 && (
        <ol data-testid="intent-chain-revisions" className="space-y-3 border-l border-hairline pl-3">
          {chain.revisions.map((revision) => (
            <li key={revision.run} data-testid="intent-revision" data-run={revision.run}>
              <p className="text-xs text-ink-muted">
                Revision {revision.number} · <a className="underline" href={routeHref({ name: "run", id: revision.run })}>{revision.run}</a>
                {revision.submittedAt && <> · {revision.submittedAt}</>}
              </p>
              {revision.note && (
                <div data-testid="intent-revision-note" className="mt-1">
                  <p className="text-xs text-ink-secondary">Your note</p>
                  <Prose body={revision.note} className="mt-0.5 space-y-1 text-sm text-ink" />
                </div>
              )}
              {revision.findings && (
                <div data-testid="intent-revision-findings" className="mt-1 rounded border border-hairline px-2 py-1">
                  <p className="text-xs text-ink-secondary">Reviewer findings carried over — the reviewer's words, not yours</p>
                  <Prose body={revision.findings} className="mt-0.5 space-y-1 text-sm text-ink-secondary" />
                </div>
              )}
              {!revision.note && !revision.findings && (
                <p className="mt-1 text-xs text-ink-muted">Nothing new: the revision repeated earlier notes.</p>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
