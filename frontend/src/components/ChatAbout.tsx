import { useState } from "react";
import type { Duckling, EngineClient } from "../api/client";

/** "Chat about this": a conversation with a chosen duckling about one
 * subject, its history as context, read-only tools to investigate. The chat
 * is a run; starting one lands the person in the run view, which is the
 * conversation panel. */
export function ChatAbout({
  client,
  projectId,
  aboutKind,
  aboutId,
  ducklings,
}: {
  client: EngineClient;
  projectId: string;
  aboutKind: "bug" | "task";
  aboutId: string;
  ducklings: readonly Duckling[];
}) {
  const [open, setOpen] = useState(false);
  const [duckling, setDuckling] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!open) {
    return (
      <button
        type="button"
        data-testid="chat-about"
        onClick={() => setOpen(true)}
        className="text-xs text-ink-muted underline"
      >
        chat about this
      </button>
    );
  }
  return (
    <div className="space-y-1 rounded border border-hairline p-2" data-testid="chat-about-form">
      <select
        value={duckling}
        onChange={(e) => setDuckling(e.target.value)}
        data-testid="chat-duckling"
        className="w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
      >
        <option value="">pick a duckling…</option>
        {ducklings.map((d) => (
          <option key={d.id} value={d.id}>{d.id}</option>
        ))}
      </select>
      <textarea
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder={`e.g. this ${aboutKind} is not actually fixed — investigate why`}
        data-testid="chat-message"
        rows={2}
        className="w-full rounded border border-hairline bg-surface2 px-1 py-0.5 text-xs"
      />
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid="chat-start"
          disabled={busy || !duckling || !message.trim()}
          onClick={() => {
            setBusy(true);
            setError(null);
            void client
              .chatStart(projectId, { duckling, aboutKind, aboutId, message: message.trim() })
              .then((r) => {
                location.hash = `#/runs/${r.id}`;
              })
              .catch((e) => setError(e instanceof Error ? e.message : String(e)))
              .finally(() => setBusy(false));
          }}
          className="rounded border border-hairline px-2 py-0.5 text-xs disabled:opacity-40"
        >
          {busy ? "Starting…" : "Start chat"}
        </button>
        <button type="button" onClick={() => setOpen(false)} className="text-xs text-ink-muted underline">
          cancel
        </button>
      </div>
      {error && <p className="text-xs text-critical" data-testid="chat-error">{error}</p>}
    </div>
  );
}
