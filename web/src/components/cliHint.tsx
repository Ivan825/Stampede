import { clsx } from 'clsx';
import { Check, Copy, SquareTerminal } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';

/** Copies a text and reports, for a moment, that it did. */
function useCopy(): [boolean, (text: string) => void] {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return [
    copied,
    (text) => {
      void navigator.clipboard?.writeText(text).then(() => setCopied(true));
    },
  ];
}

/** A command in monospace with a button that copies it. */
export function CliCommand({ command, className }: { command: string; className?: string }) {
  const [copied, copy] = useCopy();
  return (
    <span
      className={clsx(
        'inline-flex max-w-full min-w-0 items-center gap-1 rounded border border-line bg-surface-2 py-0.5 pr-0.5 pl-2 align-middle',
        className,
      )}
    >
      <code className="min-w-0 truncate font-mono text-xs text-fg" title={command}>
        {command}
      </code>
      <button
        type="button"
        onClick={() => copy(command)}
        className="inline-flex size-6 shrink-0 items-center justify-center rounded text-muted hover:bg-surface hover:text-fg"
        aria-label={copied ? 'Copied' : `Copy command: ${command}`}
        title={copied ? 'Copied' : 'Copy'}
      >
        {copied ? (
          <Check className="size-3.5 text-pass" aria-hidden />
        ) : (
          <Copy className="size-3.5" aria-hidden />
        )}
      </button>
    </span>
  );
}

/**
 * Where an action used to be: says how to do it from the terminal, with the
 * command ready to copy. The web UI only reads; every change goes through
 * the stampede CLI.
 */
export function CliHint({
  children,
  command,
  className,
}: {
  /** What the command does, such as "Start a run from the terminal". */
  children: ReactNode;
  command: string | string[];
  className?: string;
}) {
  const commands = Array.isArray(command) ? command : [command];
  return (
    <div
      className={clsx(
        'flex flex-wrap items-center gap-x-2 gap-y-1.5 rounded-md border border-dashed border-line-strong px-2.5 py-1.5 text-xs text-muted',
        className,
      )}
      role="note"
    >
      <SquareTerminal className="size-3.5 shrink-0 text-accent" aria-hidden />
      <span>{children}</span>
      {commands.map((c) => (
        <CliCommand key={c} command={c} />
      ))}
    </div>
  );
}
