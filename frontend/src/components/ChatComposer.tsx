import { useRef, useState } from "react";
import type { Duckling, EngineClient, Run } from "../api/client";
import { useRuns } from "../store/runs";
import { canSeeImages, knownBlind } from "../lib/vision";
import { ConsultantPicker, ImageChips, SwitchToSeeing, VisionNote, useImageDraft } from "./ConsultantVision";

/** The live chat's composer: who is answering (and whether it can see), the
 * reply box, screenshots by button, paste or drop, send, end.
 *
 * The consultant is switchable here, mid-conversation (B-513): the person who
 * needs a screenshot examined by a duckling that can see no longer has to end
 * the chat and lose its context. */
export function ChatComposer({
  client,
  run,
  ducklings,
  waiting,
  className = "mt-auto border-t border-hairline bg-surface p-3",
}: {
  client: EngineClient;
  run: Run;
  ducklings: readonly Duckling[];
  /** The chat is waiting for the person (paused at pending_kind "chat"). */
  waiting: boolean;
  className?: string;
}) {
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [triedToAttach, setTriedToAttach] = useState(false);
  const draft = useImageDraft();
  const imageInput = useRef<HTMLInputElement>(null);
  // The consultant SEAT, not the roster's first entry: a chat run's roster has
  // one seat today, but reading position 0 is how a different seat ends up
  // deciding whether "Add image" works.
  const consultantId = run.roster?.consultant ?? "";
  const consultant = ducklings.find((d) => d.id === consultantId);
  const blind = knownBlind(consultant);
  const canSee = canSeeImages(consultant);
  const holdingImages = blind && draft.images.length > 0;
  const switchWhy = "You can switch ducklings once the consultant has replied.";

  const switchTo = (id: string) => {
    if (!id || id === consultantId) return;
    setSwitching(true);
    setError(null);
    void client.chatSwitch(run.id, id)
      .then((updated) => {
        // The event stream says it too; the response is the faster witness.
        const current = useRuns.getState().runs[run.id] ?? run;
        useRuns.getState().setRun({ ...current, roster: { ...(current.roster ?? {}), ...(updated?.roster ?? { consultant: id }) } });
        setTriedToAttach(false);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setSwitching(false));
  };

  const send = () => {
    if (!message.trim() || busy || !waiting || holdingImages) return;
    setBusy(true);
    setError(null);
    void client.chatSend(run.id, message.trim(), draft.images.map((image) => image.data))
      .then(() => { setMessage(""); draft.clear(); })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <section
      className={className}
      data-testid="chat-reply"
      onDragOver={draft.onDragOver}
      onDrop={(e) => { draft.onDrop(e); setTriedToAttach(true); }}
    >
      <div className="mb-2 flex flex-wrap items-start gap-2 text-xs">
        <span className="pt-0.5 text-ink-muted">Talking to</span>
        <div className="min-w-[12rem] flex-1">
          <ConsultantPicker
            ducklings={ducklings}
            value={consultantId}
            onChange={switchTo}
            testId="chat-consultant"
            ariaLabel="switch the consultant duckling"
            disabled={!waiting || switching}
            title={waiting ? "Switch ducklings: the next reply comes from the one you pick, with the whole conversation so far" : switchWhy}
          />
        </div>
      </div>
      <VisionNote duckling={consultant} id={consultantId} />
      <div className="flex flex-wrap items-start gap-2">
        <textarea
          aria-label="chat message"
          data-testid="chat-message"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          onPaste={(e) => { const pastedImage = Array.from(e.clipboardData?.files ?? []).some((file) => file.type.startsWith("image/")); draft.onPaste(e); if (pastedImage) setTriedToAttach(true); }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          rows={2}
          disabled={!waiting}
          placeholder={waiting ? "your reply… (Enter to send; paste or drop a screenshot)" : "the consultant is thinking…"}
          className="min-w-[12rem] flex-1 rounded border border-hairline bg-surface2 px-2 py-1 disabled:opacity-60"
        />
        <input ref={imageInput} type="file" accept="image/*" multiple data-testid="chat-image" className="hidden" onChange={(e) => { draft.add(Array.from(e.target.files ?? [])); setTriedToAttach(true); e.currentTarget.value = ""; }} />
        <button
          type="button"
          data-testid="chat-add-image"
          disabled={!waiting}
          title={canSee ? "Add images" : blind ? `${consultantId} can't see images — click to see who can` : "Add images"}
          onClick={() => {
            // A blind consultant: say so and offer the ducklings that can see,
            // rather than letting the person pick a file nobody will look at.
            if (blind) setTriedToAttach(true);
            else imageInput.current?.click();
          }}
          className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
        >
          Add image
        </button>
        <button
          type="button"
          data-testid="chat-send"
          disabled={busy || !waiting || !message.trim() || holdingImages}
          title={holdingImages ? `${consultantId} can't see the attached images: switch ducklings or remove them` : undefined}
          onClick={send}
          className="rounded border border-hairline px-2 py-1 text-sm disabled:opacity-40"
        >
          {busy ? "Sending…" : "Send"}
        </button>
        <button
          type="button"
          data-testid="chat-end"
          disabled={busy}
          onClick={() => void client.chatEnd(run.id).catch((e) => setError(e instanceof Error ? e.message : String(e)))}
          title="Closes the conversation as finished; the transcript stays on the record"
          className="rounded border border-hairline px-2 py-1 text-sm text-ink-muted disabled:opacity-40"
        >
          End chat
        </button>
      </div>
      <ImageChips images={draft.images} onRemove={draft.remove} />
      {blind && (triedToAttach || draft.images.length > 0) && (
        <SwitchToSeeing
          ducklings={ducklings}
          current={consultantId}
          reason={draft.images.length > 0
            ? `${consultantId} can't see images, so it would not see ${draft.images.length === 1 ? "this screenshot" : "these screenshots"}. Switch, and ${draft.images.length === 1 ? "it goes" : "they go"} with your message to the new duckling — or remove ${draft.images.length === 1 ? "it" : "them"}.`
            : `${consultantId} can't see images. To show a screenshot, switch to a duckling that can — the conversation so far comes along.`}
          onSwitch={switchTo}
          disabled={!waiting || switching}
          disabledWhy={!waiting ? switchWhy : undefined}
        />
      )}
      {draft.error && <p className="mt-1 text-xs text-critical" data-testid="chat-image-error">{draft.error}</p>}
      {error && <p className="mt-1 text-xs text-critical" data-testid="chat-error">{error}</p>}
    </section>
  );
}
