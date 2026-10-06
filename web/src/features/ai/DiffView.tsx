import { clsx } from 'clsx';

/** A unified diff with added and removed lines coloured. */
export function DiffView({ diff }: { diff: string }) {
  const lines = diff.replace(/\n$/, '').split('\n');
  return (
    <pre
      className="max-h-[460px] overflow-auto bg-surface py-2 font-mono text-xs leading-5"
      aria-label="Diff against the existing scenario"
    >
      {lines.map((l, i) => {
        const header = l.startsWith('+++') || l.startsWith('---');
        return (
          <div
            key={i}
            className={clsx(
              'px-3 whitespace-pre-wrap',
              header && 'text-muted',
              !header && l.startsWith('+') && 'bg-pass-bg text-pass',
              !header && l.startsWith('-') && 'bg-fail-bg text-fail',
              l.startsWith('@@') && 'text-info',
            )}
          >
            {l || ' '}
          </div>
        );
      })}
    </pre>
  );
}
