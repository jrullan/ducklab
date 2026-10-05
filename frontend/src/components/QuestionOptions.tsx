/**
 * The choices a paused question offered, one button each.
 *
 * TI-36X T-005 (B-502): no card rendered `options`, so the person typed
 * "Use option 2" against a list only the CLI displayed, and the decision was
 * half lost on replay (B-498). A button answers with the option's exact text,
 * which needs no interpretation by the engine.
 */
export function QuestionOptions({
  options,
  onChoose,
  testId,
  compact = false,
}: {
  options: readonly string[] | undefined;
  onChoose: (option: string) => void;
  testId: string;
  compact?: boolean;
}) {
  if (!options || options.length === 0) return null;
  return (
    <ol className="mt-2 space-y-1" data-testid={testId} aria-label="offered options">
      {options.map((option, i) => (
        <li key={i}>
          <button
            type="button"
            data-testid={`${testId}-${i + 1}`}
            onClick={() => onChoose(option)}
            className={`w-full rounded border border-hairline text-left text-ink whitespace-pre-wrap break-words ${compact ? "px-2 py-0.5" : "px-2 py-1 text-sm"}`}
          >
            <span className="mr-2 text-ink-muted">{i + 1}.</span>
            {option}
          </button>
        </li>
      ))}
    </ol>
  );
}
