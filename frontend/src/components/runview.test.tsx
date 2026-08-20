import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ConversationTurn } from "./ConversationLane";
import { ToolTimeline } from "./ToolTimeline";
import { GateCard } from "./GateCard";
import { CandidateCard } from "./CandidateCard";
import { DiffView } from "./DiffView";
import { buildTurns, anonymiseTurns, buildTimeline, buildGate, parseDiff } from "../lib/runview";
import type { DucklabEvent } from "../api/events";

const ev = (type: string, seq: number, data: Record<string, unknown> = {}): DucklabEvent =>
  ({ type, seq, run_id: "r-1", data });

const roster = ["pato-uno", "pato-dos"];

describe("ConversationTurn", () => {
  it("collapses tool calls to one line each", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("tool_call", 2, { tool: "fs_read", ok: true, ms: 3 }),
      ev("tool_call", 3, { tool: "fs_patch", ok: true, ms: 12 }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.getAllByTestId("tool-call")).toHaveLength(2);
  });

  // Thinking is usually far longer than the reply, so a lane that opens with a
  // wall of deliberation buries what was actually decided.
  it("folds thinking away once the answer has arrived", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("message", 2, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno", content: "Done." }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} reasoning={"a\nb\nc"} />);
    const details = screen.getByTestId("turn-reasoning") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(details.textContent).toContain("3 lines");
  });

  // Open while it is the only thing arriving: that is exactly when a person is
  // deciding whether to abort a model that is going in circles.
  it("opens thinking while it is still the only thing on screen", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
    ]);
    render(
      <ConversationTurn block={block!} roster={roster} reasoning="Let me check" streamed="Do" />,
    );
    expect((screen.getByTestId("turn-reasoning") as HTMLDetailsElement).open).toBe(true);
  });

  // "3,914 lines" behind a disclosure triangle says it is busy but not what it
  // is busy with, and expanding to find out means scrolling to the bottom of a
  // wall of deliberation.
  it("shows the newest thinking line while folded", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("message", 2, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno", content: "Done." }),
      ev("turn_end", 3, { round: 1, turn: 0, role: "implementer" }),
    ]);
    render(
      <ConversationTurn
        block={block!}
        roster={roster}
        reasoning={"first thought\nsecond thought\n\n"}
      />,
    );
    // Trailing blank lines are not the newest thing it said.
    expect(screen.getByTestId("turn-reasoning-tail").textContent).toBe("second thought");
  });

  // When it is open the whole text is on screen, so repeating its last line on
  // the summary would be noise.
  it("drops the tail once the thinking is open", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
    ]);
    render(
      <ConversationTurn block={block!} roster={roster} reasoning="only thought" streamed="Do" />,
    );
    expect(screen.queryByTestId("turn-reasoning-tail")).toBeNull();
  });

  it("shows no thinking block when the model reported none", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("message", 2, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno", content: "Done." }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.queryByTestId("turn-reasoning")).toBeNull();
  });

  // A turn that ran out of budget still did real work, and its record is now
  // kept — but a partial record read as a complete one is worse than none.
  it("marks a turn that did not finish", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("tool_call", 2, { tool: "fs_patch", ok: true, ms: 9 }),
      ev("turn_end", 3, { round: 1, turn: 0, role: "implementer", incomplete: true }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.getByTestId("turn-incomplete").textContent).toContain("did not finish");
    // And what it managed to do is still on screen.
    expect(screen.getAllByTestId("tool-call")).toHaveLength(1);
  });

  it("says nothing about finishing when the turn finished", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("turn_end", 2, { round: 1, turn: 0, role: "implementer" }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.queryByTestId("turn-incomplete")).toBeNull();
  });

  // AC-32 in the UI: an anonymised turn must not contain the id anywhere in
  // the rendered DOM, not merely be styled to hide it.
  it("never renders a duckling id for an anonymised turn", () => {
    const blocks = anonymiseTurns(
      buildTurns([
        ev("turn_start", 1, { round: 1, turn: 0, role: "judge", duckling: "pato-uno" }),
      ]),
      true,
    );
    const { container } = render(<ConversationTurn block={blocks[0]!} roster={roster} />);
    expect(container.innerHTML).not.toContain("pato-uno");
    expect(screen.getByTestId("conversation-turn").getAttribute("data-anonymous")).toBe("true");
    expect(container.textContent).toContain("A");
  });

  it("shows an in-flight marker until the turn ends", () => {
    const [open] = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" })]);
    const { rerender } = render(<ConversationTurn block={open!} roster={roster} />);
    expect(screen.queryByTestId("in-flight")).not.toBeNull();

    const [closed] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("turn_end", 2),
    ]);
    rerender(<ConversationTurn block={closed!} roster={roster} />);
    expect(screen.queryByTestId("in-flight")).toBeNull();
  });

  it("expands a policy violation by default", () => {
    const [block] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" }),
      ev("policy_violation", 2, { tool: "fs_write", detail: "path escapes root: ../etc" }),
    ]);
    render(<ConversationTurn block={block!} roster={roster} />);
    expect(screen.getByTestId("tool-call").getAttribute("data-violation")).toBe("true");
    expect(screen.getByText(/path escapes root/)).toBeTruthy();
  });

  // A verify_run can legally run for its whole 900s ceiling; unnamed, those
  // minutes read as a hang and taught the person to abort healthy work —
  // four pytest orphans in one night. The lane names the tool in flight and
  // clears it the moment it completes.
  it("shows the tool in flight, and clears it on completion", () => {
    const [started] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "luna" }),
      ev("tool_call_started", 2, { round: 1, turn: 0, tool: "verify_run", args: "{}" }),
    ]);
    render(<ConversationTurn block={started!} roster={["luna"]} />);
    expect(screen.getByTestId("tool-in-flight").textContent).toContain("verify_run");

    const [done] = buildTurns([
      ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "luna" }),
      ev("tool_call_started", 2, { round: 1, turn: 0, tool: "verify_run", args: "{}" }),
      ev("tool_call", 3, { round: 1, turn: 0, tool: "verify_run", ok: true }),
    ]);
    expect(done!.pendingTool).toBeUndefined();
  });

  it("renders streamed text when present", () => {
    const [block] = buildTurns([ev("turn_start", 1, { round: 1, turn: 0, role: "implementer", duckling: "pato-uno" })]);
    render(<ConversationTurn block={block!} roster={roster} streamed="func Add" />);
    expect(screen.getByText("func Add")).toBeTruthy();
  });
});

describe("ToolTimeline", () => {
  it("renders one tick per call", () => {
    const calls = buildTimeline([
      ev("tool_call", 1, { tool: "fs_read" }),
      ev("tool_call", 2, { tool: "fs_read" }),
      ev("tool_call", 3, { tool: "verify_run" }),
    ]);
    render(<ToolTimeline calls={calls} />);
    expect(screen.getAllByTestId("timeline-tick")).toHaveLength(3);
  });

  it("renders nothing when no tools ran", () => {
    render(<ToolTimeline calls={[]} />);
    expect(screen.queryByTestId("tool-timeline")).toBeNull();
  });
});

describe("GateCard", () => {
  // P3: a none gate must never present as success.
  it("labels a none gate as unverified", () => {
    render(<GateCard gate={buildGate([ev("gate", 1, { gate: "none", exit: 0 })])} />);
    const card = screen.getByTestId("gate-card");
    expect(card.getAttribute("data-unverified")).toBe("true");
    expect(card.textContent).toContain("unverified");
    expect(card.textContent).not.toContain("passed");
  });

  it("shows the command and the result for a real gate", () => {
    render(<GateCard gate={buildGate([ev("gate", 1, { gate: "tests", cmd: "go test ./...", exit: 0 })])} />);
    expect(screen.getByTestId("gate-card").textContent).toContain("go test ./...");
    expect(screen.getByTestId("gate-card").textContent).toContain("passed");
  });

  it("says not run yet before a gate exists", () => {
    render(<GateCard gate={null} />);
    expect(screen.getByTestId("gate-card").textContent).toContain("not run yet");
  });
});

describe("CandidateCard", () => {
  // The Candidate type has no author field, so this cannot leak one.
  it("shows the label and gate but no authorship", () => {
    const { container } = render(
      <CandidateCard candidate={{ label: "A", diff: "+return a + b", gate: "green" }} applied />,
    );
    expect(container.textContent).toContain("Candidate A");
    expect(container.textContent).not.toContain("pato");
    expect(screen.getByTestId("applied-verbatim")).toBeTruthy();
  });

  it("marks a red candidate as failed verification", () => {
    render(<CandidateCard candidate={{ label: "B", diff: "+x", gate: "red" }} />);
    expect(screen.getByTestId("candidate-card").textContent).toContain("verification failed");
  });
});

describe("DiffView", () => {
  const diff = [
    "--- a/add.go", "+++ b/add.go", "@@ -1,3 +1,3 @@", "-return a - b", "+return a + b",
    "--- a/add_test.go", "+++ b/add_test.go", "@@ -5,3 +5,3 @@", "-want 5", "+want 6",
  ].join("\n");

  // 05 §5.3: never hide an edit to the thing that decides pass/fail.
  it("pins test files to the top under a banner", () => {
    render(<DiffView files={parseDiff(diff)} />);
    expect(screen.getByTestId("tests-modified-banner")).toBeTruthy();
    const sections = screen.getAllByTestId("diff-file");
    expect(sections[0]!.getAttribute("data-test-file")).toBe("true");
  });

  it("shows no banner when tests are untouched", () => {
    render(<DiffView files={parseDiff("--- a/add.go\n+++ b/add.go\n@@ -1 +1 @@\n+x")} />);
    expect(screen.queryByTestId("tests-modified-banner")).toBeNull();
  });

  it("renders an empty state before any change exists", () => {
    render(<DiffView files={[]} />);
    expect(screen.getByTestId("diff-empty")).toBeTruthy();
  });
});

// The lane rendered only `streamed`, which comes from token_delta events that
// arrive solely during a live run. Opening a finished run showed the
// participant and its tool calls with no word of what was actually said.
describe("ConversationTurn and the recorded message", () => {
  const block = {
    key: "1:0", round: 1, turn: 0, role: "implementer", duckling: "pato-uno",
    toolCalls: [], text: "Changed add.go: a - b became a + b.", done: true,
  };

  it("shows the recorded message when nothing is streaming", () => {
    render(<ConversationTurn block={block} roster={["pato-uno"]} />);
    expect(screen.getByTestId("turn-text").textContent).toBe(
      "Changed add.go: a - b became a + b.",
    );
  });

  it("prefers live tokens while the turn is still arriving", () => {
    render(
      <ConversationTurn block={{ ...block, done: false }} roster={["pato-uno"]} streamed="Changed ad" />,
    );
    expect(screen.getByTestId("turn-text").textContent).toBe("Changed ad");
  });

  // T-064's chat: the consultant hit its call cap and answered through the
  // tools-withheld conclude call — which does not stream — so the delta
  // buffer held only its thinking-aloud between tool calls. The lane
  // rendered that scratch work over the recorded reply, and the person was
  // asked to answer a consultant who appeared to have said nothing. Once a
  // turn is done, the record is the truth — same law as the budget meter.
  it("prefers the record over streamed scratch once the turn is done", () => {
    render(
      <ConversationTurn
        block={block}
        roster={["pato-uno"]}
        streamed="Let me search only the frontend source."
      />,
    );
    expect(screen.getByTestId("turn-text").textContent).toContain("a - b became a + b");
  });
});

// A reviewer's turn is already structured. Rendering its raw text put
// {"verdict":"approve", "findings":[]} on screen — the one turn whose content
// the engine has already parsed, shown to a person as a blob.
describe("ConversationTurn and a reviewer's verdict", () => {
  const base = {
    key: "1:1", round: 1, turn: 1, role: "reviewer", duckling: "pato-dos",
    toolCalls: [], done: true,
  };

  it("shows an approval as a verdict, not as JSON", () => {
    render(
      <ConversationTurn
        block={{ ...base, text: '{"verdict":"approve", "findings":[]}', verdict: "approve", findings: [] }}
        roster={["pato-dos"]}
      />,
    );
    expect(screen.getByTestId("turn-verdict").dataset.verdict).toBe("approve");
    expect(screen.queryByTestId("turn-text")).toBeNull();
    expect(screen.queryByText(/"findings"/)).toBeNull();
  });

  it("lists findings with where and what", () => {
    render(
      <ConversationTurn
        block={{
          ...base,
          text: "{...}",
          verdict: "request-changes",
          findings: [
            { severity: "major", file: "add.go", line: 4, issue: "off-by-one", fix: "start at 0" },
          ],
        }}
        roster={["pato-dos"]}
      />,
    );
    const f = screen.getByTestId("finding");
    expect(f.textContent).toContain("major");
    expect(f.textContent).toContain("add.go:4");
    expect(f.textContent).toContain("off-by-one");
    expect(f.textContent).toContain("start at 0");
  });

  // Rejecting with nothing to fix is a reviewer failing its job, and the lane
  // must not make that look like an empty but valid review.
  it("says so when changes are requested with no findings", () => {
    render(
      <ConversationTurn
        block={{ ...base, text: "{...}", verdict: "request-changes", findings: [] }}
        roster={["pato-dos"]}
      />,
    );
    expect(screen.getByTestId("turn-verdict").textContent).toContain("no findings given");
  });

  // An ordinary turn is unaffected.
  it("still renders prose for a turn with no verdict", () => {
    render(
      <ConversationTurn block={{ ...base, role: "implementer", text: "Fixed add.go." }} roster={[]} />,
    );
    expect(screen.getByTestId("turn-text").textContent).toBe("Fixed add.go.");
    expect(screen.queryByTestId("turn-verdict")).toBeNull();
  });
});

// A thinking turn looked exactly like a finished one that said nothing, which
// is the difference between "wait" and "something is wrong".
describe("the in-flight duck", () => {
  const block = {
    key: "1:0", round: 1, turn: 0, role: "implementer", duckling: "pato-uno",
    toolCalls: [], text: "",
  };

  it("bobs while the turn is in flight", () => {
    render(<ConversationTurn block={{ ...block, done: false }} roster={["pato-uno"]} />);
    expect(screen.getByTestId("duck-avatar").dataset.bobbing).toBe("true");
    expect(screen.getByTestId("in-flight")).toBeTruthy();
  });

  it("stops the moment the turn ends", () => {
    render(<ConversationTurn block={{ ...block, done: true, text: "Done." }} roster={["pato-uno"]} />);
    expect(screen.getByTestId("duck-avatar").dataset.bobbing).toBe("false");
    expect(screen.queryByTestId("in-flight")).toBeNull();
  });
});


// test-first inverts the gate's reading, and the card says so: red is the
// goal reached — a person saw "tests failed" beside a PASSED verdict and
// reasonably read a contradiction. Green is the actual bad news there.
describe("the gate card on a test-first run", () => {
  const red = { gate: "red", cmd: "pytest -q", role: "critical", label: "tests failed", unverified: false } as never;
  const green = { gate: "green", cmd: "pytest -q", role: "good", label: "tests passed", unverified: false } as never;

  it("presents red as the intended outcome", () => {
    render(<GateCard gate={red} stage="test" />);
    expect(screen.getByTestId("gate-card").textContent).toContain("as intended");
  });

  it("presents green as the warning", () => {
    render(<GateCard gate={green} stage="test" />);
    expect(screen.getByTestId("gate-card").textContent).toContain("asserts nothing");
  });

  it("keeps the plain reading for a build run", () => {
    render(<GateCard gate={red} stage="build" />);
    expect(screen.getByTestId("gate-card").textContent).toContain("tests failed");
  });
});

// The tick bar folds on request and stays folded across runs — a person
// reading long transcripts wants the pixels back, but the one-line summary
// (count, failures) survives the fold so nothing important goes dark.
describe("collapsing the tool timeline", () => {
  const calls = [
    { seq: 1, tool: "fs_read", ok: true },
    { seq: 2, tool: "shell", ok: false },
  ] as never[];

  it("folds the ticks, keeps the caption, and remembers", () => {
    localStorage.removeItem("ducklab.timeline");
    const first = render(<ToolTimeline calls={calls} />);
    expect(first.getAllByTestId("timeline-tick")).toHaveLength(2);
    fireEvent.click(first.getByTestId("timeline-toggle"));
    expect(first.queryByTestId("timeline-tick")).toBeNull();
    // The failure count survives the fold.
    expect(first.getByTestId("timeline-failed").textContent).toContain("1 failed");
    first.unmount();

    // A fresh mount honours the remembered preference.
    const second = render(<ToolTimeline calls={calls} />);
    expect(second.queryByTestId("timeline-tick")).toBeNull();
    fireEvent.click(second.getByTestId("timeline-toggle"));
    expect(second.getAllByTestId("timeline-tick")).toHaveLength(2);
    second.unmount();
    localStorage.removeItem("ducklab.timeline");
  });
});

// Finished turns fold to a one-line summary so a forty-turn run is a page,
// not a scroll marathon. What must never go dark survives the fold: the
// verdict, the failure count. The header is the toggle.
describe("collapsing a finished turn", () => {
  const block = {
    duckling: "luna", role: "implementer", round: 1, turn: 1, done: true,
    text: "Updated App.jsx to provide context.\nMore detail below.",
    toolCalls: [
      { seq: 1, tool: "fs_read", ok: true },
      { seq: 2, tool: "fs_patch", ok: false },
    ],
  } as never;

  it("folds to a summary that keeps count, failures and preview", () => {
    const r = render(
      <ConversationTurn block={block} roster={["luna"]} collapsed onToggle={() => {}} />,
    );
    expect(r.queryByTestId("tool-call")).toBeNull();
    expect(r.queryByTestId("turn-text")).toBeNull();
    const summary = r.getByTestId("turn-summary").textContent!;
    expect(summary).toContain("2 tool calls");
    expect(summary).toContain("Updated App.jsx");
    expect(r.getByTestId("conversation-turn").textContent).toContain("1 failed");
    r.unmount();
  });

  it("keeps a reviewer's verdict visible while folded", () => {
    const reviewer = { ...(block as object), role: "reviewer", verdict: "request_changes", findings: [] } as never;
    const r = render(
      <ConversationTurn block={reviewer} roster={["luna"]} collapsed onToggle={() => {}} />,
    );
    expect(r.getByTestId("conversation-turn").textContent).toContain("request_changes");
    r.unmount();
  });

  it("the header toggles", () => {
    let toggled = 0;
    const r = render(
      <ConversationTurn block={block} roster={["luna"]} collapsed onToggle={() => { toggled++; }} />,
    );
    fireEvent.click(r.getByTestId("turn-toggle"));
    expect(toggled).toBe(1);
    r.unmount();
  });
});

// The harness gate appears in the lane as its own turn: opened by
// gate_started ("running the suite…"), settled by round_gate. The moment
// between a reviewer's approve and the verdict used to read as a hang.
describe("the gate as a turn in the lane", () => {
  it("opens on gate_started and settles on round_gate", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", data: { round: 1, turn: 0, role: "implementer", duckling: "luna" } },
      { seq: 2, type: "turn_end", data: { round: 1, turn: 0 } },
      { seq: 3, type: "gate_started", data: { round: 1 } },
    ] as never[]);
    const gate = turns.find((t) => t.role === "gate")!;
    expect(gate.gate).toBe("running");
    expect(gate.done).toBe(false);

    const settled = buildTurns([
      { seq: 3, type: "gate_started", data: { round: 1 } },
      { seq: 4, type: "round_gate", data: { round: 1, result: "red" } },
    ] as never[]);
    const g2 = settled.find((t) => t.role === "gate")!;
    expect(g2.done).toBe(true);
    expect(g2.gate).toBe("red");
  });

  it("shows and closes accept's commit announcement without a round", () => {
    const commitStarted = ev("gate_started", 1, {
      phase: "commit",
      detail: "committing accepted work before clean-checkout verification",
    });
    const live = buildTurns([commitStarted]);
    const commit = live.find((t) => t.role === "gate")!;
    expect(commit.done).toBe(false);
    const r = render(<ConversationTurn block={commit} roster={[]} />);
    expect(r.getByTestId("conversation-turn").textContent).toContain("committing accepted work");
    r.unmount();

    // Accept announces the commit and then the clean-checkout reproduction.
    // The latter is the terminal signal for this accept gate: neither gate may
    // remain live after it has reproduced.
    const settled = buildTurns([
      commitStarted,
      ev("gate_started", 2, {
        phase: "accept",
        detail: "reproducing the gate from a clean checkout",
      }),
      ev("gate_reproduced", 3, { green: true }),
    ]);
    const gates = settled.filter((t) => t.role === "gate");
    expect(gates).toHaveLength(2);
    expect(gates.every((gate) => gate.done)).toBe(true);
    expect(gates.map((gate) => gate.gate)).toEqual(["green", "green"]);
  });

  it("historical runs without gate_started still show the settled gate", () => {
    const turns = buildTurns([
      { seq: 1, type: "round_gate", data: { round: 2, result: "green" } },
    ] as never[]);
    const g = turns.find((t) => t.role === "gate")!;
    expect(g.done).toBe(true);
    expect(g.gate).toBe("green");
  });

  it("renders running with a pulse and settles to a chip", () => {
    const running = { key: "g", round: 1, turn: -1, role: "gate", duckling: "gate", toolCalls: [], text: "", done: false, messageOnly: true, gate: "running" } as never;
    const r = render(<ConversationTurn block={running} roster={[]} />);
    expect(r.getByTestId("gate-running").textContent).toContain("running the suite");
    r.unmount();

    const settled = { ...(running as object), done: true, gate: "green" } as never;
    const r2 = render(<ConversationTurn block={settled} roster={[]} />);
    expect(r2.getByTestId("conversation-turn").textContent).toContain("green");
    expect(r2.queryByTestId("gate-running")).toBeNull();
    r2.unmount();
  });
});

// The live thinking deltas die with the window; a relaunched desktop reads
// the turn's consolidated reasoning off the record instead of showing the
// thinking gone (T-097, watched across three desktop restarts).
describe("recorded reasoning survives a relaunch", () => {
  it("lands on the turn block from its message event", () => {
    const turns = buildTurns([
      { seq: 1, type: "turn_start", data: { round: 1, turn: 0, role: "implementer", duckling: "luna" } },
      { seq: 2, type: "message", data: { round: 1, turn: 0, role: "implementer", content: "done", reasoning: "I considered the session cookie path first." } },
      { seq: 3, type: "turn_end", data: { round: 1, turn: 0 } },
    ] as never[]);
    expect(turns[0]!.reasoning).toContain("session cookie path");
  });
});

// Movement is how this UI says "working" — the ducks bob — and a still cog
// under a minutes-long gate read as a hang. It turns while the suite runs
// and stops the moment the gate settles.
it("the gate's cog turns while running and rests when settled", () => {
  const running = { key: "g1", round: 1, turn: -1, role: "gate", duckling: "gate", toolCalls: [], text: "", done: false, messageOnly: true, gate: "running" } as never;
  const { unmount } = render(<ConversationTurn block={running} roster={[]} />);
  expect(screen.getByTestId("gate-cog").getAttribute("data-turning")).toBe("true");
  expect(screen.getByTestId("gate-cog").className).toContain("cog-turn");
  unmount();
  const settled = { ...(running as object), done: true, gate: "green" } as never;
  render(<ConversationTurn block={settled} roster={[]} />);
  expect(screen.getByTestId("gate-cog").getAttribute("data-turning")).toBe("false");
});
