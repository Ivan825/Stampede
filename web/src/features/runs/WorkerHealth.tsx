import { clsx } from 'clsx';
import type { RunWorkerHealth, RunWorkers } from '@/api/types';
import { Chip } from '@/components/chips';
import { Card, CardHeader } from '@/components/ui';
import { ms, relativeTime } from '@/lib/format';

const statusTone = { running: 'info', saturated: 'warn', lost: 'fail' } as const;

/** Scheduling lag above this is a sign the generator is falling behind. */
const lagWarn = 0.01;

/** Seconds since the heartbeat, so a stalled worker stands out. */
function heartbeatAge(iso: string, now: number): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  return s < 120 ? `${s}s ago` : relativeTime(iso, now);
}

function Meter({ value, label, warn }: { value: number; label: string; warn: boolean }) {
  const pct = Math.max(0, Math.min(100, value));
  return (
    <div
      className="h-1.5 overflow-hidden rounded-full bg-surface-2"
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <div
        className={clsx('h-full rounded-full', warn ? 'bg-warn' : 'bg-accent')}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}

function WorkerCard({ w, now }: { w: RunWorkerHealth; now: number }) {
  const cpuWarn = w.cpuPercent >= 85;
  const lagHigh = w.schedLagP99 > lagWarn;
  return (
    <li
      className={clsx(
        'flex flex-col gap-2 rounded-md border bg-surface px-3 py-2.5',
        w.status === 'lost' ? 'border-fail/50' : w.saturated ? 'border-warn/50' : 'border-line',
      )}
      aria-label={w.name}
    >
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium" title={w.id}>
          {w.name}
        </span>
        <Chip tone={statusTone[w.status]} dot pulse={w.status === 'running'}>
          {w.status}
        </Chip>
      </div>
      <dl className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-1.5 text-xs">
        <dt className="text-muted">Region</dt>
        <dd className="font-mono">{w.region || '–'}</dd>
        <dt className="text-muted">CPU</dt>
        <dd className="flex items-center gap-2">
          <span className={clsx('num w-10 shrink-0', cpuWarn && 'text-warn')}>
            {w.cpuPercent.toFixed(0)}%
          </span>
          <span className="flex-1">
            <Meter value={w.cpuPercent} label={`${w.name} CPU`} warn={cpuWarn} />
          </span>
        </dd>
        <dt className="text-muted">Sched. lag p99</dt>
        <dd className={clsx('num', lagHigh && 'text-warn')}>{ms(w.schedLagP99)}</dd>
        <dt className="text-muted">Heartbeat</dt>
        <dd className="num" title={w.lastHeartbeatAt ?? undefined}>
          {w.lastHeartbeatAt ? heartbeatAge(w.lastHeartbeatAt, now) : 'none yet'}
        </dd>
      </dl>
      {w.saturated && (
        <p className="text-xs text-warn" role="note">
          Saturated
          {w.reasons?.length ? `: ${w.reasons.join('; ')}` : ''}. Latency measured now may reflect
          this worker rather than the target.
        </p>
      )}
      {w.status === 'lost' && (
        <p className="text-xs text-fail" role="note">
          No heartbeat; its share of the load is not being generated.
        </p>
      )}
    </li>
  );
}

/** Per-worker health while a run executes: CPU, scheduling lag, saturation, heartbeat. */
export function WorkerHealthGrid({
  data,
  error,
  now,
}: {
  data: RunWorkers | undefined;
  error?: unknown;
  now: number;
}) {
  const workers = data?.workers ?? [];
  const saturated = workers.filter((w) => w.saturated).length;
  const lost = workers.filter((w) => w.status === 'lost').length;
  const summary = data
    ? workers.length === 0
      ? data.live
        ? 'Waiting for the first health sample.'
        : 'Health is shown while the run executes on this server.'
      : `${workers.length} worker${workers.length === 1 ? '' : 's'}` +
        (saturated ? ` · ${saturated} saturated` : '') +
        (lost ? ` · ${lost} lost` : '')
    : error
      ? 'Worker health is not available.'
      : 'Loading…';
  return (
    <Card>
      <CardHeader title="Worker health" description={summary} />
      {workers.length > 0 && (
        <ul
          className="grid gap-2.5 p-3 sm:grid-cols-2 xl:grid-cols-4"
          aria-label="Worker health"
          aria-live="polite"
        >
          {workers.map((w) => (
            <WorkerCard key={w.id} w={w} now={now} />
          ))}
        </ul>
      )}
    </Card>
  );
}
