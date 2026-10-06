import { clsx } from 'clsx';
import { Sparkles } from 'lucide-react';
import { useCreateNarrative } from '@/api/queries';
import type { Narrative, NarrativeFact } from '@/api/types';
import { Tip } from '@/components/misc';
import { Button, Card, ErrorAlert, SectionTitle } from '@/components/ui';

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
 * only suggest it.
 */
export function NarrativePanel({
  runId,
  narrative,
  canWrite,
}: {
  runId?: string;
  narrative?: Narrative;
  canWrite: boolean;
}) {
  const create = useCreateNarrative(runId ?? '');
  if (!narrative && (!canWrite || !runId)) return null;
  const facts = new Map((narrative?.facts ?? []).map((f) => [f.id, f]));
  const write = (
    <Button
      size="sm"
      variant={narrative ? 'ghost' : 'primary'}
      loading={create.isPending}
      onClick={() => create.mutate()}
    >
      <Sparkles className="size-3.5" aria-hidden />
      {narrative ? 'Rewrite' : 'Write a summary'}
    </Button>
  );
  return (
    <>
      <SectionTitle actions={canWrite && runId && narrative ? write : undefined}>
        AI summary
      </SectionTitle>
      <Card className="px-4 py-3 text-[13px]">
        {create.error && <ErrorAlert error={create.error} className="mb-3" />}
        {narrative ? (
          <>
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
          </>
        ) : (
          <div className="flex flex-wrap items-center gap-3">
            {write}
            <span className="text-xs text-muted">
              Uses your organisation's AI provider. Only the report's figures are sent, with error
              messages redacted; every claim must cite them.
            </span>
          </div>
        )}
      </Card>
    </>
  );
}
