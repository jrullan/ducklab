/**
 * The last link in the guard chain (docs/ux-evaluation.md §5.7).
 *
 * The engine-side guards prove every route is callable and every event known.
 * Both are weaker claims than "a person can reach it": three client methods
 * sat unused for weeks, and the Bench view was built, tested, and mounted
 * nowhere. This walks the client's surface and fails when a method is
 * referenced by no view, store, or lib — the class where capability quietly
 * fails to become interface.
 */
import { describe, it, expect } from "vitest";
import fs from "node:fs";
import path from "node:path";

const SRC = path.resolve(__dirname, "..");

/** Deliberately unwired, each with the reason written down. An empty reason is
 * not an exception, it is a gap somebody silenced. Removing a line here is the
 * definition of done for wiring one. */
const knownUnwired: Record<string, string> = {
  ducklingProbe:
    "capability probing is engine-initiated today; the card shows declared caps and re-probing has no surface yet",
  projectStatus:
    "per-stage progress and task counts — the natural feed for a richer Work header, unbuilt",
  traceShow:
    "the spine walk from one id; the Cycle rail renders the whole check instead",
  restart:
    "an attributed restart REQUEST that checkpoints live runs; issued by the supervising Go shell through engineclt, not by this webview — the window cannot restart the engine it stands in, only resume the runs a restart left checkpointed",
};

function collect(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) collect(p, out);
    else if (/\.(ts|tsx)$/.test(entry.name) && !entry.name.includes(".test.")) out.push(p);
  }
  return out;
}

describe("every client capability is reachable from a surface", () => {
  const clientSrc = fs.readFileSync(path.join(SRC, "api", "client.ts"), "utf8");
  const methods = [...clientSrc.matchAll(/^  ([a-z][A-Za-z0-9]*)\(/gm)]
    .map((m) => m[1]!)
    .filter((m) => m !== "request");

  const surfaces = ["views", "components", "app", "store", "lib"]
    .map((d) => path.join(SRC, d))
    .filter(fs.existsSync)
    .flatMap((d) => collect(d))
    .map((p) => fs.readFileSync(p, "utf8"))
    .join("\n");

  it("finds a real number of methods, or the pattern has rotted", () => {
    expect(methods.length).toBeGreaterThan(30);
  });

  it("leaves no method unreferenced and unexcused", () => {
    const unreachable = methods.filter(
      (m) => !new RegExp(`\\.${m}\\s*\\(`).test(surfaces) && !(m in knownUnwired),
    );
    expect(
      unreachable,
      `client methods no view, store or lib calls: ${unreachable.join(", ")} — ` +
        "wire them or list them in knownUnwired with the reason",
    ).toEqual([]);
  });

  it("crosses off what got wired", () => {
    const stale = Object.keys(knownUnwired).filter((m) =>
      new RegExp(`\\.${m}\\s*\\(`).test(surfaces),
    );
    expect(stale, `now wired — remove from knownUnwired: ${stale.join(", ")}`).toEqual([]);
  });

  it("accepts no empty excuses", () => {
    for (const [m, reason] of Object.entries(knownUnwired)) {
      expect(reason.trim().length, `${m} is excused with no reason`).toBeGreaterThan(10);
    }
  });
});
