import { clsx } from 'clsx';
import { Check, ChevronRight, X } from 'lucide-react';
import type { ReactNode } from 'react';
import type { AIJourney, AIStepTrace, AITrace } from '@/api/types';
import { JourneyStatusChip } from '@/components/chips';
import { Card } from '@/components/ui';

function KeyValues({ values }: { values?: Record<string, string> }) {
  const entries = Object.entries(values ?? {});
  if (entries.length === 0) return <p className="text-xs text-muted">None</p>;
  return (
    <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-0.5 font-mono text-xs">
      {entries.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="break-all">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function Body({ text }: { text?: string }) {
  if (!text) return <p className="text-xs text-muted">Empty</p>;
  return (
    <pre className="max-h-56 overflow-auto rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs break-all whitespace-pre-wrap">
      {text}
    </pre>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <h5 className="label-caps">{title}</h5>
      {children}
    </div>
  );
}

function StepTrace({ step, index }: { step: AIStepTrace; index: number }) {
  return (
    <details className="group border-b border-line last:border-b-0">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-1.5 text-[13px] hover:bg-surface-2/60 [&::-webkit-details-marker]:hidden">
        <ChevronRight
          className="size-3.5 shrink-0 text-muted transition-transform group-open:rotate-90"
          aria-hidden
        />
        {step.ok ? (
          <Check className="size-3.5 shrink-0 text-pass" aria-label="passed" />
        ) : (
          <X className="size-3.5 shrink-0 text-fail" aria-label="failed" />
        )}
        <span className="num w-5 shrink-0 text-xs text-muted">{index + 1}</span>
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">{step.step}</span>
          {step.method && (
            <span className="ml-2 font-mono text-xs text-muted">
              {step.method} {step.url}
            </span>
          )}
        </span>
        {step.status != null && step.status > 0 && (
          <span
            className={clsx(
              'num font-mono text-xs',
              step.status >= 400 ? 'text-fail' : 'text-muted',
            )}
          >
            {step.status}
          </span>
        )}
        <span className="num w-16 shrink-0 text-right text-xs text-muted">
          {step.durationMs.toFixed(0)} ms
        </span>
      </summary>
      <div className="grid gap-3 bg-bg px-3 py-3 md:grid-cols-2">
        {step.error && <p className="text-xs text-fail md:col-span-2">{step.error}</p>}
        {step.note && <p className="text-xs text-muted md:col-span-2">{step.note}</p>}
        <Section title="Request headers">
          <KeyValues values={step.requestHeaders} />
        </Section>
        <Section title="Response headers">
          <KeyValues values={step.responseHeaders} />
        </Section>
        <Section title="Request body">
          <Body text={step.requestBody} />
        </Section>
        <Section title="Response body (redacted)">
          <Body text={step.responseBody} />
        </Section>
        <Section title="Extracted values">
          <KeyValues values={step.extracted} />
        </Section>
        <Section title="Checks">
          {step.checks?.length ? (
            <ul className="flex flex-col gap-0.5 text-xs">
              {step.checks.map((c, i) => (
                <li key={i} className="flex items-start gap-1.5">
                  {c.ok ? (
                    <Check className="mt-0.5 size-3 shrink-0 text-pass" aria-label="passed" />
                  ) : (
                    <X className="mt-0.5 size-3 shrink-0 text-fail" aria-label="failed" />
                  )}
                  <span className="font-mono">{c.name}</span>
                  {c.detail && <span className="text-muted">{c.detail}</span>}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted">None</p>
          )}
        </Section>
      </div>
    </details>
  );
}

function TraceView({ trace }: { trace: AITrace }) {
  return (
    <details className="rounded-md border border-line">
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[13px] hover:bg-surface-2/60">
        <span className="font-medium">Pass {trace.pass}</span>
        {trace.branches?.length ? (
          <span className="text-xs text-muted">branches: {trace.branches.join(', ')}</span>
        ) : null}
        <span className="text-xs text-muted">
          {trace.steps.length} request{trace.steps.length === 1 ? '' : 's'}
        </span>
        <span className="flex-1" />
        <span className={clsx('text-xs font-medium', trace.ok ? 'text-pass' : 'text-fail')}>
          {trace.ok ? 'passed' : 'failed'}
        </span>
      </summary>
      {trace.error && (
        <p className="border-t border-line px-3 py-1.5 text-xs text-fail">{trace.error}</p>
      )}
      <div className="border-t border-line">
        {trace.steps.map((s, i) => (
          <StepTrace key={i} step={s} index={i} />
        ))}
      </div>
    </details>
  );
}

export function JourneyCard({ journey }: { journey: AIJourney }) {
  return (
    <Card className="overflow-hidden" aria-label={`Journey ${journey.name}`} role="region">
      <div className="flex items-center gap-3 border-b border-line px-4 py-2.5">
        <h3 className="min-w-0 flex-1 truncate text-sm font-semibold">{journey.name}</h3>
        <span className="text-xs text-muted">
          {journey.attempts} attempt{journey.attempts === 1 ? '' : 's'}
        </span>
        <JourneyStatusChip status={journey.status} />
      </div>
      <div className="flex flex-col gap-2 px-4 py-3">
        {journey.problems?.length ? (
          <ul className="list-disc space-y-0.5 pl-4 text-[13px] text-fail">
            {journey.problems.map((p, i) => (
              <li key={i}>{p}</li>
            ))}
          </ul>
        ) : null}
        {journey.traces.length === 0 ? (
          <p className="text-xs text-muted">
            {journey.status === 'not-run' ? 'Not dry-run.' : 'No traces were recorded.'}
          </p>
        ) : (
          journey.traces.map((t, i) => <TraceView key={i} trace={t} />)
        )}
      </div>
    </Card>
  );
}
