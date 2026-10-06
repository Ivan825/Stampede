import { clsx } from 'clsx';
import type { ReactNode } from 'react';
import type {
  AIJobStatus,
  AIJourney,
  CompareVerdict,
  RunStatus,
  Verdict,
  Worker,
} from '@/api/types';

type Tone = 'pass' | 'fail' | 'warn' | 'info' | 'neutral' | 'accent' | 'live';

const tones: Record<Tone, string> = {
  pass: 'bg-pass-bg text-pass border-pass/30',
  fail: 'bg-fail-bg text-fail border-fail/30',
  warn: 'bg-warn-bg text-warn border-warn/30',
  info: 'bg-info-bg text-info border-info/30',
  neutral: 'bg-surface-2 text-muted border-line',
  accent: 'bg-surface-2 text-fg border-line',
  live: 'bg-primary/10 text-primary border-primary/35',
};

export function Chip({
  tone = 'neutral',
  children,
  dot,
  pulse,
  className,
  title,
}: {
  tone?: Tone;
  children: ReactNode;
  dot?: boolean;
  pulse?: boolean;
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={clsx(
        'inline-flex h-5 items-center gap-1.5 rounded-full border px-2 font-mono text-[11px] leading-none font-semibold tracking-wide whitespace-nowrap',
        tones[tone],
        className,
      )}
    >
      {dot && (
        <span className="relative flex size-1.5" aria-hidden>
          {pulse && (
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-current opacity-60" />
          )}
          <span className="relative inline-flex size-1.5 rounded-full bg-current" />
        </span>
      )}
      {children}
    </span>
  );
}

export const verdictLabels: Record<Verdict, string> = {
  pass: 'PASS',
  fail: 'FAIL',
  'generator-limited': 'GENERATOR-LIMITED',
  'no-targets': 'NO TARGETS',
};

export const verdictTone: Record<Verdict, Tone> = {
  pass: 'pass',
  fail: 'fail',
  'generator-limited': 'warn',
  'no-targets': 'neutral',
};

export function VerdictChip({ verdict }: { verdict: Verdict | null | undefined }) {
  if (!verdict) return <span className="text-muted">–</span>;
  return <Chip tone={verdictTone[verdict]}>{verdictLabels[verdict]}</Chip>;
}

const statusTone: Record<RunStatus, Tone> = {
  scheduling: 'info',
  starting: 'info',
  running: 'live',
  stopping: 'warn',
  analyzing: 'info',
  completed: 'neutral',
  aborted: 'warn',
  failed: 'fail',
};

export function StatusChip({ status }: { status: RunStatus }) {
  const live = status === 'running' || status === 'starting' || status === 'scheduling';
  return (
    <Chip tone={statusTone[status]} dot pulse={live}>
      {status}
    </Chip>
  );
}

const workerTone: Record<Worker['status'], Tone> = {
  idle: 'neutral',
  busy: 'info',
  saturated: 'warn',
  lost: 'fail',
};

export function WorkerStatusChip({ status }: { status: Worker['status'] }) {
  return (
    <Chip tone={workerTone[status]} dot pulse={status === 'busy'}>
      {status}
    </Chip>
  );
}

const aiJobTone: Record<AIJobStatus, Tone> = {
  queued: 'info',
  running: 'live',
  succeeded: 'pass',
  needs_review: 'warn',
  failed: 'fail',
};

export function AIJobStatusChip({ status }: { status: AIJobStatus }) {
  return (
    <Chip tone={aiJobTone[status]} dot pulse={status === 'running' || status === 'queued'}>
      {status === 'needs_review' ? 'needs review' : status}
    </Chip>
  );
}

const journeyTone: Record<AIJourney['status'], Tone> = {
  passed: 'pass',
  flagged: 'warn',
  'not-run': 'neutral',
};

export function JourneyStatusChip({ status }: { status: AIJourney['status'] }) {
  return <Chip tone={journeyTone[status]}>{status === 'not-run' ? 'not run' : status}</Chip>;
}

export const compareVerdictLabels: Record<CompareVerdict, string> = {
  regression: 'REGRESSION',
  improvement: 'IMPROVEMENT',
  'no-change': 'NO SIGNIFICANT CHANGE',
  inconclusive: 'INCONCLUSIVE',
};

export const compareVerdictTone: Record<CompareVerdict, Tone> = {
  regression: 'fail',
  improvement: 'pass',
  'no-change': 'neutral',
  inconclusive: 'warn',
};

export function CompareVerdictChip({ verdict }: { verdict: CompareVerdict }) {
  return <Chip tone={compareVerdictTone[verdict]}>{compareVerdictLabels[verdict]}</Chip>;
}

export function RoleChip({ role }: { role: string }) {
  return <Chip tone={role === 'owner' || role === 'admin' ? 'info' : 'neutral'}>{role}</Chip>;
}
