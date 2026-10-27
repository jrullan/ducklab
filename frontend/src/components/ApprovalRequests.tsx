/**
 * Approval requests: an agent's ask that the person make a decision only a
 * person may make — widen a task's lane at accept, confirm manual
 * verification, answer an oracle dispute (B-530).
 *
 * The incident: an operator refused a lane widening had the person's "Ok" in
 * chat and no door to carry it, so it read the engine token and accepted as
 * "human" itself. The engine now refuses an agent that claim whatever it
 * sends; this card is the legitimate door's other half. The person sees who
 * asks, what, and why, and decides here. Approved, the decision runs as the
 * person's and the record names both.
 */
import { useState } from "react";
import type { ApprovalRequest, EngineClient, Run } from "../api/client";
import { useRuns, upsertApproval } from "../store/runs";
import { routeHref } from "../app/routes";
import { runLabel } from "../lib/runview";
import { StatusChip } from "./StatusChip";
import type { StatusRole } from "../lib/colors";

function what(req: ApprovalRequest): string {
  if (req.action === "answer") {
    return req.widen_lane?.length ? "answer the question and widen the task's lane" : "answer the question";
  }
  const parts: string[] = [];
  if (req.lane_widening?.length) parts.push("widen the task's lane");
  if (req.human_verified?.length) parts.push("confirm manual verification");
  return parts.length ? `accept and ${parts.join(" and ")}` : "accept the run";
}

function statusRole(status: string): StatusRole {
  switch (status) {
    case "landed":
    case "approved":
      return "good";
    case "failed":
    case "interrupted":
      return "critical";
    case "declined":
    case "superseded":
      return "muted";
    default:
      return "warning";
  }
}

function setRequest(runId: string, req: ApprovalRequest) {
  const store = useRuns.getState();
  const run = store.runs[runId];
  if (!run) return;
  useRuns.setState({
    runs: { ...store.runs, [runId]: { ...run, approval_requests: upsertApproval(run.approval_requests, req) } },
  });
}

function ApprovalRequestItem({ client, run, request, showRun }: {
  client: EngineClient;
  run: Run;
  request: ApprovalRequest;
  showRun?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [declining, setDeclining] = useState(false);
  const [declineReason, setDeclineReason] = useState("");
  const pending = request.status === "pending";

  const approve = async () => {
    setBusy(true);
    setError(null);
    try {
      const out = await client.approveRequest(run.id, request.id);
      if (out?.request) setRequest(run.id, out.request);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const decline = async () => {
    setBusy(true);
    setError(null);
    try {
      const out = await client.declineRequest(run.id, request.id, declineReason.trim());
      if (out?.id) setRequest(run.id, out);
      setDeclining(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <li
      data-testid="approval-request"
      data-status={request.status}
      className={`rounded-card border p-3 ${pending ? "border-warning bg-surface1" : "border-hairline"}`}
    >
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="text-sm text-ink">
          <strong className="font-medium" data-testid="approval-requested-by">{request.requested_by}</strong> asks you to{" "}
          <strong className="font-medium">{what(request)}</strong>
        </span>
        {showRun && (
          <a href={routeHref({ name: "run", id: run.id })} className="text-xs text-ink-muted underline">
            {runLabel(run)}
          </a>
        )}
        <StatusChip role={statusRole(request.status)} label={request.status} />
      </div>
      <p className="mt-1 text-sm text-ink-secondary" data-testid="approval-reason">
        {request.reason}
      </p>
      {!!request.lane_widening?.length && (
        <p className="mt-1 text-xs text-ink-secondary">
          Lane paths:{" "}
          {request.lane_widening.map((p) => (
            <code key={p} className="mr-1 font-mono">{p}</code>
          ))}
        </p>
      )}
      {!!request.human_verified?.length && (
        <p className="mt-1 text-xs text-ink-secondary">
          Manually verified slices: {request.human_verified.join(", ")}
        </p>
      )}
      {request.action === "answer" && (
        <p className="mt-1 text-xs text-ink-secondary">
          Answer: <span className="text-ink">{request.answer}</span>
          {!!request.widen_lane?.length && <> · widens {request.widen_lane.join(", ")}</>}
        </p>
      )}
      {!pending && (
        <p className="mt-1 text-xs text-ink-muted" data-testid="approval-outcome">
          {request.decided_by ? `${request.status} by ${request.decided_by}` : request.status}
          {request.decline_reason ? ` — ${request.decline_reason}` : ""}
          {request.outcome ? ` — ${request.outcome}` : ""}
        </p>
      )}
      {pending && (
        <>
          <p className="mt-1 text-xs text-ink-muted">
            Approving decides this as you; the record names {request.requested_by} as the one who asked.
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <button
              type="button"
              data-testid="approval-approve"
              disabled={busy}
              onClick={() => void approve()}
              className="rounded border border-good px-3 py-1 text-xs text-good disabled:opacity-40"
            >
              {busy && !declining ? "Approving…" : "Approve"}
            </button>
            {!declining ? (
              <button
                type="button"
                data-testid="approval-decline"
                disabled={busy}
                onClick={() => setDeclining(true)}
                className="rounded border border-hairline px-3 py-1 text-xs text-ink-secondary disabled:opacity-40"
              >
                Decline
              </button>
            ) : (
              <>
                <input
                  type="text"
                  data-testid="approval-decline-reason"
                  aria-label="Why decline (optional)"
                  placeholder="Why (optional) — the agent reads it"
                  value={declineReason}
                  onChange={(e) => setDeclineReason(e.target.value)}
                  className="min-w-0 flex-1 rounded border border-hairline bg-surface2 px-2 py-1 text-xs text-ink"
                />
                <button
                  type="button"
                  data-testid="approval-decline-confirm"
                  disabled={busy}
                  onClick={() => void decline()}
                  className="rounded border border-critical px-3 py-1 text-xs text-critical disabled:opacity-40"
                >
                  {busy ? "Declining…" : "Decline request"}
                </button>
              </>
            )}
          </div>
        </>
      )}
      {error && (
        <p role="alert" className="mt-2 text-xs text-critical" data-testid="approval-error">
          {error}
        </p>
      )}
    </li>
  );
}

/** One run's approval requests, pending first. Renders nothing when the run
 * has none. */
export function ApprovalRequests({ client, run }: { client: EngineClient; run: Run }) {
  const list = run.approval_requests ?? [];
  if (list.length === 0) return null;
  const ordered = [...list].sort((a, b) => Number(b.status === "pending") - Number(a.status === "pending"));
  const pendingCount = list.filter((r) => r.status === "pending").length;
  return (
    <section data-testid="run-approval-requests" className="space-y-2">
      <h2 className="text-sm font-medium text-ink">
        Approval requests{pendingCount > 0 ? ` — ${pendingCount} waiting for you` : ""}
      </h2>
      <ul className="space-y-2">
        {ordered.map((r) => (
          <ApprovalRequestItem key={r.id} client={client} run={run} request={r} />
        ))}
      </ul>
    </section>
  );
}

/** Every pending request across runs, for Now: an agent waiting on the
 * person is a decision like any gate. */
export function PendingApprovals({ client, items }: {
  client: EngineClient;
  items: { run: Run; request: ApprovalRequest }[];
}) {
  if (items.length === 0) return null;
  return (
    <section data-testid="now-approvals">
      <h2 className="text-sm font-medium text-ink">Agents asking for your approval</h2>
      <ul className="mt-2 space-y-2">
        {items.map(({ run, request }) => (
          <ApprovalRequestItem key={`${run.id}/${request.id}`} client={client} run={run} request={request} showRun />
        ))}
      </ul>
    </section>
  );
}
