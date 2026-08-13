import { describe, it, expect } from "vitest";
import { render, fireEvent, screen } from "@testing-library/react";
import { VirtualList } from "./VirtualList";

function sizeable(el: HTMLElement, scrollHeight: number, clientHeight: number) {
  Object.defineProperty(el, "scrollHeight", { configurable: true, value: scrollHeight });
  Object.defineProperty(el, "clientHeight", { configurable: true, value: clientHeight });
}

// The person watching a live run reads the TAIL — and had to chase it with
// the wheel as new turns arrived. followTail keeps the newest in view; their
// own scroll-up detaches it, and returning to the bottom reattaches.
describe("followTail", () => {
  const items = (n: number) => Array.from({ length: n }, (_, i) => `line ${i}`);

  it("keeps the tail in view as content arrives", () => {
    const { rerender } = render(
      <VirtualList items={items(10)} followTail>{(t) => <div>{t}</div>}</VirtualList>,
    );
    const el = screen.getByTestId("virtual-list");
    sizeable(el, 1000, 200);
    rerender(<VirtualList items={items(12)} followTail>{(t) => <div>{t}</div>}</VirtualList>);
    expect(el.scrollTop).toBe(1000);
  });

  it("stops following when the person scrolls up, and resumes at the bottom", () => {
    const { rerender } = render(
      <VirtualList items={items(10)} followTail>{(t) => <div>{t}</div>}</VirtualList>,
    );
    const el = screen.getByTestId("virtual-list");
    sizeable(el, 1000, 200);
    // The person scrolls up to read something earlier.
    el.scrollTop = 100;
    fireEvent.scroll(el);
    rerender(<VirtualList items={items(12)} followTail>{(t) => <div>{t}</div>}</VirtualList>);
    expect(el.scrollTop).toBe(100); // their place is respected
    // They come back to the tail.
    el.scrollTop = 790; // within 80px of the bottom (1000 - 790 - 200 = 10)
    fireEvent.scroll(el);
    rerender(<VirtualList items={items(14)} followTail>{(t) => <div>{t}</div>}</VirtualList>);
    expect(el.scrollTop).toBe(1000); // reattached
  });

  it("never scrolls a finished run", () => {
    const { rerender } = render(
      <VirtualList items={items(10)}>{(t) => <div>{t}</div>}</VirtualList>,
    );
    const el = screen.getByTestId("virtual-list");
    sizeable(el, 1000, 200);
    rerender(<VirtualList items={items(12)}>{(t) => <div>{t}</div>}</VirtualList>);
    expect(el.scrollTop).toBe(0);
  });
});
