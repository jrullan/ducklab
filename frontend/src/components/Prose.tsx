/**
 * Renders a model's markdown — an artifact body, or what a duckling said.
 *
 * Shared by the Cycle view and the conversation lanes because they had the
 * same problem: a document rendered as plain text puts `**Changed:**` and
 * `### Clients` on screen, and the whole job of both views is to make what a
 * model wrote readable.
 *
 * Fenced blocks stay verbatim. The text protocol carries tool calls inside
 * ```ducklab fences, so reformatting their contents would rewrite what a
 * duckling actually said.
 */

import { parseProse, type Span } from "../lib/prose";

export function Prose({
  body,
  suppress,
  className = "space-y-2 text-sm text-ink-secondary",
}: {
  body: string;
  suppress?: string[];
  className?: string;
}) {
  return (
    <div className={className} data-testid="prose">
      {parseProse(body, suppress).map((b, i) => {
        switch (b.kind) {
          case "code":
            return (
              <pre
                key={i}
                data-testid="prose-code"
                data-lang={b.lang}
                className="overflow-x-auto rounded border border-hairline p-2 font-mono text-xs text-ink-secondary"
              >
                {b.text}
              </pre>
            );
          case "rule":
            return <hr key={i} className="border-hairline" />;
          case "heading":
            return (
              <h4 key={i} className="mt-3 text-sm font-medium text-ink">
                <Spans spans={b.spans} />
              </h4>
            );
          case "table":
            // Wide tables scroll inside their own box; the page never scrolls
            // sideways.
            return (
              <div key={i} className="overflow-x-auto">
                <table className="w-full border-collapse text-xs">
                  {b.head.length > 0 && (
                    <thead>
                      <tr>
                        {b.head.map((c, j) => (
                          <th
                            key={j}
                            className="border-b border-hairline px-2 py-1 text-left font-medium text-ink"
                          >
                            <Spans spans={c} />
                          </th>
                        ))}
                      </tr>
                    </thead>
                  )}
                  <tbody>
                    {b.rows.map((r, j) => (
                      <tr key={j}>
                        {r.map((c, k) => (
                          <td key={k} className="border-b border-hairline px-2 py-1 align-top">
                            <Spans spans={c} />
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          case "list":
            return (
              <ul key={i} className="list-disc space-y-1 pl-5">
                {b.items.map((spans, j) => (
                  <li key={j}>
                    <Spans spans={spans} />
                  </li>
                ))}
              </ul>
            );
          default:
            return (
              <p key={i}>
                <Spans spans={b.spans} />
              </p>
            );
        }
      })}
    </div>
  );
}

function Spans({ spans }: { spans: Span[] }) {
  return (
    <>
      {spans.map((s, i) =>
        s.kind === "strong" ? (
          <strong key={i} className="font-medium text-ink">
            {s.text}
          </strong>
        ) : s.kind === "code" ? (
          <code key={i} className="font-mono text-ink">
            {s.text}
          </code>
        ) : (
          <span key={i}>{s.text}</span>
        ),
      )}
    </>
  );
}
