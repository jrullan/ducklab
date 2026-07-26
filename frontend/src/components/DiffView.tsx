import { orderDiffFiles, touchesTests, type DiffFile } from "../lib/runview";

/**
 * The working diff, test files first.
 *
 * A change to the thing that decides pass/fail is pinned to the top under a
 * banner (05 §5.3). Ducklab does not block it — sometimes tests must change —
 * but it never hides it either.
 */
export function DiffView({ files }: { files: readonly DiffFile[] }) {
  if (files.length === 0) {
    return <p className="p-4 text-ink-muted" data-testid="diff-empty">No changes yet.</p>;
  }
  const ordered = orderDiffFiles(files);
  return (
    <div data-testid="diff-view">
      {touchesTests(ordered) && (
        <div
          data-testid="tests-modified-banner"
          className="m-2 rounded border border-hairline p-2 text-sm"
          style={{ color: "var(--status-warning)" }}
        >
          ⚠ this change edits tests — read these hunks before accepting
        </div>
      )}
      {ordered.map((f) => (
        <section key={f.path} data-testid="diff-file" data-test-file={String(f.isTest)}>
          <h3 className="px-2 py-1 font-mono text-sm text-ink-secondary">{f.path}</h3>
          {f.hunks.map((h, i) => (
            <pre key={i} className="overflow-x-auto bg-surface2 px-2 py-1 font-mono text-xs">
              {h.split("\n").map((line, j) => (
                <div key={j} style={{ color: lineColor(line) }}>{line}</div>
              ))}
            </pre>
          ))}
        </section>
      ))}
    </div>
  );
}

function lineColor(line: string): string {
  if (line.startsWith("+")) return "var(--status-good)";
  if (line.startsWith("-")) return "var(--status-critical)";
  if (line.startsWith("@@")) return "var(--text-muted)";
  return "var(--text-secondary)";
}
