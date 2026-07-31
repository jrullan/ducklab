import { useRef, type ReactNode } from "react";
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
  children,
}: {
  items: readonly T[];
  estimateSize?: number;
  overscan?: number;
  threshold?: number;
  /** Number of pixels, or a CSS length. "100%" lets the list fill a flex parent
   * instead of a magic number that ignores the window. */
  height?: number | string;
  children: (item: T, index: number) => ReactNode;
}) {
  const parentRef = useRef<HTMLDivElement>(null);

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
