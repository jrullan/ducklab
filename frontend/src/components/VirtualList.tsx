import { useEffect, useRef, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

/**
 * Windowed list.
 *
 * A long run produces thousands of turns and tool lines. Rendering them all
 * makes scrolling drop frames, so only the visible slice is mounted (AC-33).
 *
 * Below `threshold` the list renders plainly: virtualising a short
 * conversation costs a measurement pass and buys nothing, and a plain list is
 * easier to assert against in tests.
 */
export function VirtualList<T>({
  items,
  estimateSize = 96,
  overscan = 8,
  threshold = 50,
  height = 520,
  followTail = false,
  children,
}: {
  items: readonly T[];
  estimateSize?: number;
  overscan?: number;
  threshold?: number;
  /** Number of pixels, or a CSS length. "100%" lets the list fill a flex parent
   * instead of a magic number that ignores the window. */
  height?: number | string;
  /** Keep the newest content in view while it arrives — the person watching
   * a live run reads the tail. Sticky, not tyrannical: scrolling up detaches
   * the follow; returning to the bottom reattaches it. */
  followTail?: boolean;
  children: (item: T, index: number) => ReactNode;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  // Whether the person is AT the tail. Starts true; their own scrolling is
  // the only thing that changes it.
  const stick = useRef(true);
  const onScroll = () => {
    const el = parentRef.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };
  // After every render: content grew (new turns, streamed tokens) and the
  // reader was at the tail, so the tail stays in view.
  useEffect(() => {
    if (!followTail || !stick.current) return;
    const el = parentRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  });
  // And between renders. A stage run streams one long turn whose text grows
  // through the store WITHOUT re-rendering this list — the after-render
  // effect never fired and the spec run's transcript sat still while its
  // words arrived. The list watches its own content instead of trusting
  // whoever re-renders.
  useEffect(() => {
    if (!followTail) return;
    const el = parentRef.current;
    if (!el) return;
    const obs = new MutationObserver(() => {
      if (stick.current) el.scrollTop = el.scrollHeight;
    });
    obs.observe(el, { childList: true, subtree: true, characterData: true });
    return () => obs.disconnect();
  }, [followTail]);

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => estimateSize,
    overscan,
    enabled: items.length >= threshold,
  });

  if (items.length < threshold) {
    // The same box as the virtualised path. Without it the list simply grew,
    // so a run scrolled its own container at fifty turns and the whole page at
    // forty-nine — the layout changed under you as the run went on.
    return (
      <div
        ref={parentRef}
        onScroll={onScroll}
        data-testid="virtual-list"
        data-virtualised="false"
        style={{ height, overflow: "auto" }}
      >
        {items.map((item, i) => (
          <div key={i}>{children(item, i)}</div>
        ))}
      </div>
    );
  }

  const virtualItems = virtualizer.getVirtualItems();

  return (
    <div
      ref={parentRef}
      onScroll={onScroll}
      data-testid="virtual-list"
      data-virtualised="true"
      data-total={items.length}
      style={{ height, overflow: "auto" }}
    >
      <div style={{ height: virtualizer.getTotalSize(), width: "100%", position: "relative" }}>
        {virtualItems.map((v) => (
          <div
            key={v.key}
            data-index={v.index}
            ref={virtualizer.measureElement}
            style={{
              position: "absolute",
              top: 0,
              left: 0,
              width: "100%",
              transform: `translateY(${v.start}px)`,
            }}
          >
            {children(items[v.index]!, v.index)}
          </div>
        ))}
      </div>
    </div>
  );
}
