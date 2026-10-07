import { clsx } from 'clsx';
import { useMemo } from 'react';
import { CopyButton } from '@/components/misc';
import { tokenizeLine, type TokenKind } from './yamlTokens';

const tone: Record<TokenKind, string | undefined> = {
  plain: undefined,
  key: 'text-[var(--syntax-key)]',
  string: 'text-[var(--syntax-string)]',
  number: 'text-[var(--syntax-number)]',
  keyword: 'text-[var(--syntax-number)]',
  comment: 'text-muted italic',
  punct: 'text-muted',
  interp: 'text-accent',
};

/**
 * Scenario YAML, read-only, with syntax highlighting and line numbers.
 * Scenarios change from the terminal (stampede push), so there is no editor.
 */
export function YamlView({
  yaml,
  label = 'Scenario YAML',
  className,
}: {
  yaml: string;
  label?: string;
  className?: string;
}) {
  const lines = useMemo(() => yaml.replace(/\n$/, '').split('\n').map(tokenizeLine), [yaml]);
  const gutter = String(lines.length).length;
  return (
    <div className={clsx('relative min-h-0', className)}>
      <div className="absolute top-2 right-3 z-10">
        <CopyButton value={yaml} label="Copy YAML" />
      </div>
      <pre
        className="h-full overflow-auto py-3 font-mono text-[12.5px] leading-[1.6] text-fg"
        aria-label={label}
        tabIndex={0}
      >
        <code>
          {lines.map((tokens, i) => (
            <div key={i} className="flex hover:bg-surface-2/60">
              <span
                className="sticky left-0 shrink-0 bg-inherit pr-4 pl-3 text-right text-muted/70 select-none"
                style={{ width: `${gutter + 3}ch` }}
                aria-hidden
              >
                {i + 1}
              </span>
              <span className="pr-4 whitespace-pre">
                {tokens.length === 0
                  ? ' '
                  : tokens.map((t, j) => (
                      <span key={j} className={tone[t.kind]}>
                        {t.text}
                      </span>
                    ))}
              </span>
            </div>
          ))}
        </code>
      </pre>
    </div>
  );
}
