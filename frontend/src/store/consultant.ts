import { create } from "zustand";

/** What a new conversation is about — the same subject a "Chat about this"
 * door names. */
export type ConsultantSubject = {
  aboutKind: "bug" | "task" | "run" | "ducklab" | "document";
  aboutId: string;
  /** How the pane says it: "run r-…", "the project". */
  label: string;
  initialMessage?: string;
};

/** The consultant pane (B-514): an app-wide, hideable right pane where only
 * the consultant lives. The chat opened as a full run view and competed with
 * the run detail for height; asking about one run meant leaving the chat for
 * Runs and coming back through Runs to answer. The pane stays open while the
 * person navigates, so they can look at things and keep talking.
 *
 * Open/closed, width and the conversation in front are per-viewer
 * conveniences, remembered in localStorage. Storage can be unavailable (a
 * private window, blocked site data): every read and write is guarded and
 * the pane works without it. */
export interface ConsultantState {
  open: boolean;
  width: number;
  /** The conversation shown; null shows the list and the new-chat form. */
  activeRunId: string | null;
  /** A new conversation being composed in the pane, about this subject. */
  composing: ConsultantSubject | null;
  show: () => void;
  hide: () => void;
  toggle: () => void;
  setWidth: (width: number) => void;
  /** Show this conversation in the pane, opening the pane. */
  openChat: (runId: string) => void;
  /** Start composing a new conversation about a subject, in the pane. */
  compose: (subject: ConsultantSubject) => void;
  /** Back to the list of conversations. */
  showList: () => void;
}

export const PANE_MIN_WIDTH = 320;
export const PANE_MAX_WIDTH = 960;
export const PANE_DEFAULT_WIDTH = 440;

const KEYS = {
  open: "ducklab.consultantPane.open",
  width: "ducklab.consultantPane.width",
  active: "ducklab.consultantPane.active",
};

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    /* storage unavailable: the pane still works, it just forgets */
  }
}

export function clampWidth(width: number): number {
  if (!Number.isFinite(width)) return PANE_DEFAULT_WIDTH;
  return Math.round(Math.min(PANE_MAX_WIDTH, Math.max(PANE_MIN_WIDTH, width)));
}

/** The remembered state, read fresh — exported so tests (and a reset) can
 * rebuild the store from storage. */
export function loadConsultantPane(): Pick<ConsultantState, "open" | "width" | "activeRunId" | "composing"> {
  const width = Number(read(KEYS.width));
  return {
    open: read(KEYS.open) === "true",
    width: read(KEYS.width) ? clampWidth(width) : PANE_DEFAULT_WIDTH,
    activeRunId: read(KEYS.active) || null,
    composing: null,
  };
}

export const useConsultant = create<ConsultantState>((set, get) => ({
  ...loadConsultantPane(),
  show: () => {
    write(KEYS.open, "true");
    set({ open: true });
  },
  hide: () => {
    write(KEYS.open, "false");
    set({ open: false });
  },
  toggle: () => (get().open ? get().hide() : get().show()),
  setWidth: (width) => {
    const clamped = clampWidth(width);
    write(KEYS.width, String(clamped));
    set({ width: clamped });
  },
  openChat: (runId) => {
    write(KEYS.open, "true");
    write(KEYS.active, runId);
    set({ open: true, activeRunId: runId, composing: null });
  },
  compose: (subject) => {
    write(KEYS.open, "true");
    set({ open: true, composing: subject, activeRunId: null });
  },
  showList: () => {
    write(KEYS.active, null);
    set({ activeRunId: null, composing: null });
  },
}));
