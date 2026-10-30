import { useEffect, useRef } from "react";
import type { AppStatus } from "../api/client";
import { openExternal } from "../lib/attention";

const WINDOW_MS = 20000;
const FAST_POLL_MS = 500;

/** Opens a launched app in the system browser once its address is known
 * (B-553). Armed only by the person's own Launch: an app already running
 * when the view mounts, or one launched elsewhere, never pops a window.
 * An app that prints its address a moment after starting is read again
 * quickly until it does, or until the window passes. */
export function useAutoOpen(status: AppStatus | null, refresh: () => void, matches: (s: AppStatus) => boolean) {
  const armedUntil = useRef(0);

  useEffect(() => {
    if (!armedUntil.current) return;
    // Only the window disarms: a poll sent before the launch can still carry
    // the previous crash, and an app that dies now simply never matches.
    if (Date.now() > armedUntil.current) {
      armedUntil.current = 0;
      return;
    }
    if (status?.running && status.url && matches(status)) {
      armedUntil.current = 0;
      openExternal(status.url);
    }
  }, [status, matches]);

  useEffect(() => {
    const timer = setInterval(() => {
      if (armedUntil.current && Date.now() <= armedUntil.current) refresh();
    }, FAST_POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  return () => {
    armedUntil.current = Date.now() + WINDOW_MS;
  };
}
