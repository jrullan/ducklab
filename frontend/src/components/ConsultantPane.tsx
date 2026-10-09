import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import type { Duckling, EngineClient, Run } from "../api/client";
import type { DucklabEvent } from "../api/events";
import { useRuns } from "../store/runs";
import { PANE_MAX_WIDTH, PANE_MIN_WIDTH, useConsultant, type ConsultantSubject } from "../store/consultant";
import { buildTurns } from "../lib/runview";
import { assignDucklingColors } from "../lib/colors";
import { routeHref } from "../app/routes";
import { visionMark } from "../lib/vision";
import { ChatAbout } from "./ChatAbout";
import { ChatComposer } from "./ChatComposer";
import { ConversationTurn } from "./ConversationLane";

/** The keyboard shortcut, said the same way wherever it is offered. */
export const CONSULTANT_SHORTCUT = "Ctrl+J";

/** A conversation that ended, however it ended, is a record, not a door. */
const TERMINAL = new Set(["done", "failed", "aborted", "canceled", "cancelled", "ended"]);

export const isOpenChat = (run: Run) => run.stage === "chat" && !TERMINAL.has(String(run.status).toLowerCase());

/** "chat about bug B-4" → "bug B-4"; the harness chat reads as "the project". */
export function chatSubjectLabel(run: Pick<Run, "note">): string {
  const about = (run.note ?? "").replace(/^chat about /, "").trim();
  if (about.startsWith("ducklab ")) return "the project";
  return about || "a conversation";
}

/** Plain words for where a conversation stands. */
export function chatStatusLabel(run: Run): string {
  if (run.status === "paused" && run.pending_kind === "chat") return "waiting for you";
  if (run.status === "running") return "answering…";
  if (run.status === "queued") return "queued";
  if (run.status === "paused") return "paused";
  return "ended";
}

/** Ctrl/⌘+J toggles the pane from anywhere in the app. */
export function useConsultantShortcut() {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "j") {
        event.preventDefault();
        useConsultant.getState().toggle();
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, []);
}

/** The app-wide consultant pane (B-514). Only the consultant lives here: the
 * open conversations, a new one about what the person is looking at, and the
 * conversation itself with the full height for the reply. */
export function ConsultantPane({
  client,
  projectId,
  subject,
}: {
  client: EngineClient;
  projectId: string;
  /** What the person is looking at: the subject a new conversation is about. */
  subject: ConsultantSubject | null;
}) {
  const width = useConsultant((s) => s.width);
  const activeRunId = useConsultant((s) => s.activeRunId);
  const composing = useConsultant((s) => s.composing);
  const runs = useRuns((s) => s.runs);
  const [fleet, setFleet] = useState<Duckling[]>([]);
  useEffect(() => {
    let cancelled = false;
    client.ducklings().then((ds) => { if (!cancelled) setFleet(ds); }).catch(() => {});
    return () => { cancelled = true; };
  }, [client]);

  const open = useMemo(
    () => Object.values(runs).filter(isOpenChat).sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? "")),
    [runs],
  );
  // What a new conversation can be about: what the person is looking at,
  // and always the project itself — the harness chat that used to live in
  // the sidebar footer.
  const subjects: ConsultantSubject[] = [];
  if (subject) subjects.push(subject);
  if (projectId && !(subject?.aboutKind === "ducklab" && subject.aboutId === projectId)) {
    subjects.push({ aboutKind: "ducklab", aboutId: projectId, label: "the project" });
  }

  return (
    <aside
      data-testid="consultant-pane"
      aria-label="Consultant"
      style={{ width }}
      className="relative flex h-full min-h-0 shrink-0 flex-col border-l border-hairline bg-page"
    >
      <ResizeHandle width={width} />
      <header className="flex items-center gap-2 border-b border-hairline px-3 py-2">
        {(activeRunId || composing) && (
          <button
            type="button"
            data-testid="consultant-pane-back"
            onClick={() => useConsultant.getState().showList()}
            className="rounded px-1 text-xs text-ink-muted hover:text-ink"
            title="All conversations"
          >
            ← All
          </button>
        )}
        <h2 className="text-sm font-semibold">Consultant</h2>
        {activeRunId && open.length > 1 && (
          <select
            aria-label="switch conversation"
            data-testid="consultant-pane-switch"
            value={activeRunId}
            onChange={(e) => useConsultant.getState().openChat(e.target.value)}
            className="min-w-0 flex-1 truncate rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
          >
            {!open.some((r) => r.id === activeRunId) && <option value={activeRunId}>{activeRunId}</option>}
            {open.map((r) => (
              <option key={r.id} value={r.id}>{chatSubjectLabel(r)} · {r.roster?.consultant ?? "?"} · {chatStatusLabel(r)}</option>
            ))}
          </select>
        )}
        <button
          type="button"
          data-testid="consultant-pane-close"
          aria-label={`Hide the consultant (${CONSULTANT_SHORTCUT})`}
          title={`Hide (${CONSULTANT_SHORTCUT}) — conversations keep going`}
          onClick={() => useConsultant.getState().hide()}
          className="ml-auto rounded border border-hairline px-2 py-0.5 text-ink-muted hover:text-ink"
        >
          ×
        </button>
      </header>
      {activeRunId ? (
        <ConsultantConversation key={activeRunId} client={client} runId={activeRunId} fleet={fleet} />
      ) : composing ? (
        <div className="min-h-0 flex-1 overflow-y-auto p-3" data-testid="consultant-pane-compose">
          <p className="mb-2 text-xs text-ink-muted">New conversation about {composing.label}</p>
          <ChatAbout
            client={client}
            projectId={projectId}
            aboutKind={composing.aboutKind}
            aboutId={composing.aboutId}
            ducklings={fleet}
            initialMessage={composing.initialMessage}
            startOpen
            onCancel={() => useConsultant.getState().showList()}
          />
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto p-3" data-testid="consultant-pane-list">
          {subjects.map((about, index) => {
            // B-285's rule, kept: a live conversation about the subject is the
            // door back to it, not a reason to start a second one.
            const live = open.find((r) => r.note === `chat about ${about.aboutKind} ${about.aboutId}`);
            const testId = index === 0 ? "consultant-pane-new" : "consultant-pane-new-project";
            return (
              <button
                key={`${about.aboutKind}:${about.aboutId}`}
                type="button"
                data-testid={testId}
                onClick={() => (live ? useConsultant.getState().openChat(live.id) : useConsultant.getState().compose(about))}
                className="mb-2 w-full rounded border border-hairline px-2 py-1.5 text-left text-sm hover:bg-surface2"
              >
                {live ? `Continue the conversation about ${about.label}` : `+ New conversation about ${about.label}`}
              </button>
            );
          })}
          <h3 className="text-xs font-semibold uppercase tracking-wide text-ink-muted">Open conversations</h3>
          {open.length === 0 ? (
            <p className="mt-2 text-sm text-ink-muted" data-testid="consultant-pane-empty">
              No open conversations. Start one about what you are looking at, or from any “chat about this”.
            </p>
          ) : (
            <ul className="mt-2 flex flex-col gap-1">
              {open.map((r) => {
                const duckling = fleet.find((d) => d.id === r.roster?.consultant);
                const mark = visionMark(duckling);
                return (
                  <li key={r.id}>
                    <button
                      type="button"
                      data-testid={`consultant-conversation-${r.id}`}
                      onClick={() => useConsultant.getState().openChat(r.id)}
                      className="w-full rounded border border-hairline px-2 py-1.5 text-left hover:bg-surface2"
                    >
                      <span className="block truncate text-sm text-ink">{chatSubjectLabel(r)}</span>
                      <span className="block truncate text-xs text-ink-muted">
                        {r.roster?.consultant ?? "?"}{mark ? ` · ${mark}` : ""} · <span className={r.status === "paused" ? "text-serious" : undefined}>{chatStatusLabel(r)}</span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}
    </aside>
  );
}

/** Drag the pane's left edge (or focus it and use the arrow keys). */
function ResizeHandle({ width }: { width: number }) {
  const onMouseDown = (event: ReactMouseEvent) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = width;
    const move = (e: MouseEvent) => useConsultant.getState().setWidth(startWidth + (startX - e.clientX));
    const up = () => {
      removeEventListener("mousemove", move);
      removeEventListener("mouseup", up);
    };
    addEventListener("mousemove", move);
    addEventListener("mouseup", up);
  };
  const onKeyDown = (event: ReactKeyboardEvent) => {
    const step = event.shiftKey ? 80 : 20;
    if (event.key === "ArrowLeft") { event.preventDefault(); useConsultant.getState().setWidth(width + step); }
    if (event.key === "ArrowRight") { event.preventDefault(); useConsultant.getState().setWidth(width - step); }
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the consultant pane"
      aria-valuemin={PANE_MIN_WIDTH}
      aria-valuemax={PANE_MAX_WIDTH}
      aria-valuenow={width}
      tabIndex={0}
      data-testid="consultant-pane-resize"
      onMouseDown={onMouseDown}
      onKeyDown={onKeyDown}
      className="absolute -left-1 top-0 z-10 h-full w-2 cursor-col-resize hover:bg-hairline focus:bg-hairline focus:outline-none"
    />
  );
}

/** One conversation, full height: the transcript scrolls, the composer stays
 * pinned to the bottom. */
function ConsultantConversation({ client, runId, fleet }: { client: EngineClient; runId: string; fleet: Duckling[] }) {
  const run = useRuns((s) => s.runs[runId]);
  const events = useRuns((s) => s.events[runId]);
  const deltas = useRuns((s) => s.deltas[runId]);
  const reasoning = useRuns((s) => s.reasoning[runId]);
  const [missing, setMissing] = useState(false);
  const status = run?.status ?? "";
  const pendingKind = run?.pending_kind ?? "";
  // The record comes from the engine: the store holds only what streamed
  // since this window connected, and a conversation opened from the list
  // must show everything said before that. Refetched when its state moves,
  // the same contract as the run view.
  useEffect(() => {
    let cancelled = false;
    client.run(runId)
      .then((d) => {
        if (cancelled) return;
        setMissing(false);
        const current = useRuns.getState().runs[runId];
        useRuns.getState().resyncRun(d.run.next === undefined && current?.next ? { ...d.run, next: current.next } : d.run, d.events as DucklabEvent[]);
      })
      .catch(() => { if (!cancelled && !useRuns.getState().runs[runId]) setMissing(true); });
    return () => { cancelled = true; };
  }, [client, runId, status, pendingKind]);

  const turns = useMemo(() => buildTurns(events ?? []), [events]);
  const colors = useMemo(() => assignDucklingColors(fleet), [fleet]);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const streamedLength = Object.values(deltas ?? {}).reduce((n, text) => n + text.length, 0);
  useEffect(() => {
    // Follow the conversation while the reader is at the bottom; leave them
    // where they are when they scrolled up to read.
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [turns.length, streamedLength]);

  if (!run) {
    return (
      <p className="p-3 text-sm text-ink-muted" data-testid="consultant-pane-loading">
        {missing ? "This conversation could not be loaded. It may belong to another project." : "Loading the conversation…"}
      </p>
    );
  }
  const live = run.status === "running" || run.status === "paused" || run.status === "queued";
  const consultant = run.roster?.consultant ?? "";
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="consultant-pane-conversation">
      <div className="border-b border-hairline px-3 py-1.5 text-xs text-ink-muted">
        <span className="text-ink">About {chatSubjectLabel(run)}</span> · {consultant || "?"} · {chatStatusLabel(run)} ·{" "}
        <a href={routeHref({ name: "run", id: run.id })} className="underline" data-testid="consultant-pane-record">open the record</a>
      </div>
      <div
        ref={scroller}
        data-testid="consultant-pane-transcript"
        onScroll={(e) => {
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
        }}
        className="min-h-0 flex-1 overflow-y-auto px-3 py-2"
      >
        {turns.length === 0 && <p className="text-sm text-ink-muted">The conversation will appear here.</p>}
        {turns.map((t) => (
          <ConversationTurn
            key={`${t.role}:${t.key}`}
            block={t}
            roster={Object.values(run.roster ?? {})}
            color={colors[t.duckling]}
            streamed={t.messageOnly || !t.streamKey ? undefined : deltas?.[t.streamKey]}
            reasoning={t.messageOnly || !t.streamKey ? t.reasoning : (reasoning?.[t.streamKey] ?? t.reasoning)}
          />
        ))}
      </div>
      {live ? (
        <ChatComposer
          client={client}
          run={run}
          ducklings={fleet}
          waiting={run.status === "paused" && run.pending_kind === "chat"}
          className="shrink-0 border-t border-hairline bg-surface p-3"
        />
      ) : (
        <p className="shrink-0 border-t border-hairline p-3 text-xs text-ink-muted" data-testid="consultant-pane-ended">
          This conversation has ended; its record stays in Records → Runs.
        </p>
      )}
    </div>
  );
}
