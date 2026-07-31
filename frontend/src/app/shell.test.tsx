import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { VirtualList } from "../components/VirtualList";

// The whole page scrolled, so a long run pushed the nav off the top of the
// window and getting back to it meant scrolling past everything the run had
// said. The scroll belongs inside the content area, not on the page.
describe("the scrollable region", () => {
  // Below the virtualising threshold the list simply grew, so a run scrolled
  // its own container at fifty turns and the whole page at forty-nine — the
  // layout changed under you as the run went on.
  it("bounds a short list the same way as a long one", () => {
    render(
      <VirtualList items={["a", "b", "c"]} height={300}>
        {(t) => <p>{t}</p>}
      </VirtualList>,
    );
    const list = screen.getByTestId("virtual-list");
    expect(list.dataset.virtualised).toBe("false");
    expect(list.style.overflow).toBe("auto");
    expect(list.style.height).toBe("300px");
  });

  // A CSS length lets the conversation fill the window instead of a magic
  // number that ignores how tall it actually is.
  it("accepts a CSS length so it can fill its parent", () => {
    render(
      <VirtualList items={["a"]} height="100%">
        {(t) => <p>{t}</p>}
      </VirtualList>,
    );
    expect(screen.getByTestId("virtual-list").style.height).toBe("100%");
  });

  it("still bounds the virtualised path", () => {
    const many = Array.from({ length: 60 }, (_, i) => `turn ${i}`);
    render(
      <VirtualList items={many} height={400}>
        {(t) => <p>{t}</p>}
      </VirtualList>,
    );
    const list = screen.getByTestId("virtual-list");
    expect(list.dataset.virtualised).toBe("true");
    expect(list.style.overflow).toBe("auto");
  });
});
