import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Review, stripFrontmatter } from "./Review";
import { Release } from "./Release";
import { EngineClient } from "../api/client";

function clientWith(handler: (path: string) => Response) {
  return new EngineClient({
    baseUrl: "http://engine",
    token: "t",
    fetchFn: (async (url: string) =>
      handler(String(url).replace("http://engine", ""))) as unknown as typeof fetch,
  });
}
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const REVIEW_MD = `---
kind: review
task: T-001
verdict: request-changes
---

# Review — T-001

## MAJOR — off-by-one in the loop

**Where:** \`add.go:4\`
`;

describe("stripFrontmatter", () => {
  // The list beside the pane already shows the verdict; repeating it as raw
  // YAML is noise the reader scrolls past.
  it("drops the machine header and keeps the document", () => {
    const got = stripFrontmatter(REVIEW_MD);
    expect(got).not.toContain("kind: review");
    expect(got).toContain("# Review — T-001");
  });

  it("leaves a document with no frontmatter alone", () => {
    expect(stripFrontmatter("# Just a title")).toBe("# Just a title");
  });
});

describe("Review", () => {
  const client = () =>
    clientWith((p) => {
      if (p.endsWith("/reviews")) {
        return json({
          items: [
            { task_id: "T-002", verdict: "approve", findings: 0, reviewed_at: "2026-07-28T12:00:00Z" },
            { task_id: "T-001", verdict: "request-changes", findings: 1, reviewed_at: "2026-07-27T12:00:00Z" },
          ],
        });
      }
      if (p.includes("/reviews/")) return json({ markdown: REVIEW_MD });
      return json({}, 404);
    });

  // A list of reviews with nothing shown makes the reader click before they
  // learn anything.
  it("opens the newest review without being asked", async () => {
    render(<Review client={client()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("review-item")).toHaveLength(2));
    const first = screen.getAllByTestId("review-item")[0]!;
    expect(first.dataset.task).toBe("T-002");
    expect(first.getAttribute("aria-pressed")).toBe("true");
  });

  it("renders the review, not its frontmatter", async () => {
    render(<Review client={client()} projectId="p" />);
    await waitFor(() => expect(screen.getByText(/off-by-one/)).toBeTruthy());
    expect(screen.queryByText(/kind: review/)).toBeNull();
  });

  it("says there are none rather than showing an empty pane", async () => {
    const empty = clientWith((p) => (p.endsWith("/reviews") ? json({ items: null }) : json({}, 404)));
    render(<Review client={empty} projectId="p" />);
    await waitFor(() => expect(screen.getByText(/No reviews yet/)).toBeTruthy());
  });
});

describe("Release", () => {
  const client = () =>
    clientWith((p) => {
      if (p.endsWith("/releases")) {
        return json({
          items: [
            { version: "v0.2.0", tasks: 2, drafted: true, tagged: false, unverified: 1 },
            { version: "v0.1.0", tasks: 1, drafted: false, tagged: true, since: "" },
          ],
        });
      }
      if (p.includes("/releases/")) return json({ markdown: "---\nkind: release\n---\n\n# v0.2.0\n" });
      return json({}, 404);
    });

  // A draft read as shipped is the expensive mistake here: it is a statement
  // about software someone may act on.
  it("marks a draft as a draft and says how to cut it", async () => {
    render(<Release client={client()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("release-item")).toHaveLength(2));
    expect(screen.getAllByTestId("release-item")[0]!.textContent).toContain("drafted");
    // The CLI hint became a button: the desktop cuts its own releases now.
    expect(screen.getByTestId("release-cut").textContent).toContain("Cut v0.2.0");
  });

  it("carries the unverified count into the view", async () => {
    render(<Release client={client()} projectId="p" />);
    await waitFor(() => expect(screen.getByTestId("release-unverified")).toBeTruthy());
    expect(screen.getByTestId("release-unverified").textContent).toContain("1 of these changes");
  });

  it("shows a cut release as tagged, with no draft notice", async () => {
    render(<Release client={client()} projectId="p" />);
    await waitFor(() => expect(screen.getAllByTestId("release-item")).toHaveLength(2));
    fireEvent.click(screen.getAllByTestId("release-item")[1]!);
    await waitFor(() => expect(screen.queryByTestId("release-draft-notice")).toBeNull());
    expect(screen.getAllByTestId("release-item")[1]!.textContent).toContain("tagged");
  });
});

// The desktop can now do what the view used to delegate to the CLI: draft a
// release (starts the scribe run) and cut a drafted one. The one place a
// desktop user cannot go is the terminal the old empty-state pointed at.
describe("drafting and cutting from the desktop", () => {
  it("drafts from the empty state and reports the run", async () => {
    const releasePlan = vi.fn(() => Promise.resolve({ id: "r-rel" }));
    const client = {
      releases: vi.fn(() => Promise.resolve([])),
      releasePlan,
    } as unknown as EngineClient;
    render(<Release client={client} projectId="p" />);
    const btn = await screen.findByTestId("release-draft");
    fireEvent.click(btn);
    await waitFor(() => expect(releasePlan).toHaveBeenCalledWith("p", "minor"));
    await screen.findByTestId("release-planned");
  });

  it("cuts a drafted release and reloads", async () => {
    const releaseCut = vi.fn(() => Promise.resolve({}));
    const summaries = [
      { version: "v0.1.0", drafted: true, tagged: false, tasks: 3, since: "" },
    ];
    const client = {
      releases: vi.fn(() => Promise.resolve(summaries)),
      release: vi.fn(() => Promise.resolve("# notes")),
      releaseCut,
    } as unknown as EngineClient;
    render(<Release client={client} projectId="p" />);
    const btn = await screen.findByTestId("release-cut");
    fireEvent.click(btn);
    await waitFor(() => expect(releaseCut).toHaveBeenCalledWith("p", "v0.1.0"));
  });
});

// With releases on file the door to the next one is still there — and it
// respects the one-draft-at-a-time rule.
describe("drafting the next release beside the list", () => {
  it("offers Draft next release with a bump choice when nothing is drafted", async () => {
    const releasePlan = vi.fn(() => Promise.resolve({ id: "r-rel" }));
    const client = {
      releases: vi.fn(() => Promise.resolve([{ version: "v0.5.0", drafted: false, tagged: true, tasks: 21, since: "v0.4.0" }])),
      release: vi.fn(() => Promise.resolve("# notes")),
      releasePlan,
    } as unknown as EngineClient;
    render(<Release client={client} projectId="p" />);
    const btn = await screen.findByTestId("release-draft");
    fireEvent.change(screen.getByTestId("release-bump"), { target: { value: "patch" } });
    fireEvent.click(btn);
    await waitFor(() => expect(releasePlan).toHaveBeenCalledWith("p", "patch"));
    await screen.findByTestId("release-planned");
  });
  it("says a draft is waiting instead of opening a second", async () => {
    const client = {
      releases: vi.fn(() => Promise.resolve([{ version: "v0.6.0", drafted: true, tagged: false, tasks: 3, since: "v0.5.0" }, { version: "v0.5.0", drafted: false, tagged: true, tasks: 21, since: "v0.4.0" }])),
      release: vi.fn(() => Promise.resolve("# notes")),
    } as unknown as EngineClient;
    render(<Release client={client} projectId="p" />);
    await screen.findByTestId("release-next");
    expect(screen.queryByTestId("release-draft")).toBeNull();
    expect(screen.getByTestId("release-next").textContent).toContain("a draft is waiting: cut or revise v0.6.0");
  });
});
