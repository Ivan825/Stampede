import { clsx } from 'clsx';
import type { Narrative, NarrativeFact } from '@/api/types';
import { CliHint } from '@/components/cliHint';
import { Tip } from '@/components/misc';
import { Card, SectionTitle } from '@/components/ui';
import { cli } from '@/lib/cli';

const labelStyle = {
  measured: 'border-pass/50 text-pass',
  suspected: 'border-warn/50 text-warn',
} as const;

const labelHelp = {
  measured: 'The cited figures state this directly.',
  suspected:
    'A likely cause the figures suggest but do not prove. Investigate before acting on it.',
} as const;

function Ref({ id, fact }: { id: string; fact?: NarrativeFact }) {
  const chip = (
    <span className="inline-flex h-5 cursor-help items-center rounded border border-line bg-surface-2 px-1.5 font-mono text-[11px] text-muted">
      {id}
    </span>
  );
  if (!fact) return chip;
  return (
    <Tip
      content={
        <span>
          <span className="font-mono text-[11px] text-muted">{fact.where}</span>
          <br />
          {fact.text}
        </span>
      }
    >
      {chip}
    </Tip>
  );
}

/**
 * The AI-written summary of a report. Every claim cites report figures
 * (hover a citation to see it) and says whether the figures show it or
 * only suggest it. Summaries are written from the terminal with
 * stampede narrative; without one, the panel says how.
 */
export function NarrativePanel({ runId, narrative }: { runId?: string; narrative?: Narrative }) {
  if (!narrative) {
    if (!runId) return null;
    return (
      <>
        <SectionTitle>AI summary</SectionTitle>
        <CliHint command={cli.narrative(runId)}>
          No AI summary yet. Write one with your organisation's AI provider
        </CliHint>
      </>
    );
  }
  const facts = new Map((narrative.facts ?? []).map((f) => [f.id, f]));
  return (
    <>
      <SectionTitle>AI summary</SectionTitle>
      <Card className="px-4 py-3 text-[13px]">
        <p className="text-sm">{narrative.summary}</p>
        {narrative.claims.length > 0 && (
          <ul className="mt-3 flex flex-col gap-2" aria-label="Claims">
            {narrative.claims.map((c, i) => (
              <li key={i} className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                <span
                  title={labelHelp[c.label]}
                  className={clsx(
                    'rounded-full border px-1.5 font-mono text-[10.5px] font-semibold tracking-wider uppercase',
                    labelStyle[c.label],
                  )}
                >
                  {c.label}
                </span>
                <span>{c.text}</span>
                <span className="flex flex-wrap gap-1">
                  {c.refs.map((r) => (
                    <Ref key={r} id={r} fact={facts.get(r)} />
                  ))}
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="mt-3 text-xs text-muted">
          Written by {narrative.model ?? 'an AI model'} from this report's figures. Claims that
          cited figures not in the report were removed.
        </p>
      </Card>
    </>
  );
}
