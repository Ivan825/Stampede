import { clsx } from 'clsx';
import { AlertTriangle, Info, Radio, ServerCrash, WifiOff } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { Point, Run, RunEvent } from '@/api/types';
import { Card, CardHeader, Stat } from '@/components/ui';
import { clock, count, ms, num, pct, rate } from '@/lib/format';
import type { StreamState } from '@/lib/useRunStream';
import { LiveChart, type LiveSeries } from './LiveChart';

const perSec = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0));
const vusFmt = (v: number) => v.toFixed(0);
const percent = (v: number) => `${(v * 100).toFixed(1)}%`;

function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

const eventIcon = (type: string) => {
  if (type.includes('lost')) return <ServerCrash className="size-3.5 text-fail" aria-hidden />;
  if (type.includes('saturat') || type.includes('safety') || type.includes('cap'))
    return <AlertTriangle className="size-3.5 text-warn" aria-hidden />;
  return <Info className="size-3.5 text-info" aria-hidden />;
};

export function EventFeed({ events }: { events: RunEvent[] }) {
  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader title="Events" description="Worker and safety events, newest first." />
      {events.length === 0 ? (
        <p className="px-4 py-6 text-[13px] text-muted">No events yet.</p>
      ) : (
        <ol className="max-h-80 divide-y divide-line overflow-y-auto" aria-live="polite">
          {events.map((e, i) => (
            <li key={`${e.at}-${i}`} className="flex gap-2.5 px-4 py-2 text-[13px]">
              <span className="mt-0.5">{eventIcon(e.type)}</span>
              <div className="min-w-0 flex-1">
                <p>{e.message}</p>
                <p className="font-mono text-[11px] text-muted">
                  {e.type}
                  {e.worker ? ` · ${e.worker}` : ''}
                </p>
              </div>
              <time className="num shrink-0 text-[11px] text-muted" dateTime={e.at}>
                {new Date(e.at).toLocaleTimeString('en-GB')}
              </time>
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}

export function LiveView({
  run,
  points,
  events,
  state,
}: {
  run: Run;
  points: Point[];
  events: RunEvent[];
  state: StreamState;
}) {
  const now = useNow(true);
  const started = run.startedAt ? Date.parse(run.startedAt) : null;
  const elapsed = started ? (now - started) / 1000 : 0;
  const planned = run.plan?.durationSeconds ?? 0;
  const progress = planned > 0 ? Math.min(1, elapsed / planned) : 0;
  const last = points[points.length - 1];
  const dropped = points.reduce((n, p) => n + p.dropped, 0);
  const rateMode = (run.overrides?.mode ?? run.plan?.mode) === 'rate';

  const { xs, load, latency, errors } = useMemo(() => {
    const xs = points.map((p) => p.t + 1);
    const empty = (p: Point) => p.rps === 0;
    const load: LiveSeries[] = [
      { label: 'req/s', color: '--s1', values: points.map((p) => p.rps), fmt: perSec },
      ...(rateMode
        ? [
            {
              label: 'planned',
              color: '--s4',
              dashed: true,
              values: points.map((p) => p.planned),
              fmt: perSec,
            },
          ]
        : []),
      { label: 'VUs', color: '--s2', right: true, values: points.map((p) => p.vus), fmt: vusFmt },
    ];
    const latency: LiveSeries[] = [
      {
        label: 'p50',
        color: '--s3',
        values: points.map((p) => (empty(p) ? null : p.p50)),
        fmt: ms,
      },
      {
        label: 'p95',
        color: '--s1',
        values: points.map((p) => (empty(p) ? null : p.p95)),
        fmt: ms,
      },
      {
        label: 'p99',
        color: '--s5',
        values: points.map((p) => (empty(p) ? null : p.p99)),
        fmt: ms,
      },
    ];
    const errors: LiveSeries[] = [
      {
        label: 'error rate',
        color: '--s5',
        values: points.map((p) => (empty(p) ? null : p.errorRate)),
        fmt: percent,
      },
    ];
    return { xs, load, latency, errors };
  }, [points, rateMode]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="num text-2xl font-semibold tracking-tight">
          {clock(elapsed)}
          <span className="text-base font-normal text-muted">
            {' '}
            / {planned ? clock(planned) : '–'}
          </span>
        </div>
        <div
          className="h-1.5 min-w-40 flex-1 overflow-hidden rounded-full bg-surface-2"
          role="progressbar"
          aria-label="Run progress"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(progress * 100)}
        >
          <div
            className="h-full rounded-full bg-accent transition-[width] duration-700"
            style={{ width: `${progress * 100}%` }}
          />
        </div>
        <span
          className={clsx(
            'flex items-center gap-1.5 text-xs',
            state === 'open' ? 'text-pass' : 'text-warn',
          )}
        >
          {state === 'open' ? (
            <Radio className="size-3.5" aria-hidden />
          ) : (
            <WifiOff className="size-3.5" aria-hidden />
          )}
          {state === 'open'
            ? 'Live'
            : state === 'connecting'
              ? 'Connecting…'
              : state === 'reconnecting'
                ? 'Reconnecting…'
                : 'Stream closed'}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-2.5 md:grid-cols-3 xl:grid-cols-6">
        <Stat
          label="requests/s"
          value={last ? rate(last.rps) : '–'}
          sub={rateMode && last ? `plan ${num(Math.round(last.planned))}` : undefined}
        />
        <Stat label="p95 latency" value={last && last.rps ? ms(last.p95) : '–'} />
        <Stat label="p99 latency" value={last && last.rps ? ms(last.p99) : '–'} />
        <Stat
          label="error rate"
          value={last ? pct(last.errorRate) : '–'}
          tone={last && last.errorRate > 0.01 ? 'fail' : undefined}
        />
        <Stat label="active VUs" value={last ? count(last.vus) : '–'} />
        <Stat
          label="dropped iterations"
          value={count(dropped)}
          tone={dropped > 0 ? 'warn' : undefined}
        />
      </div>

      <div className="grid items-start gap-3 xl:grid-cols-2">
        <LiveChart
          title="Throughput and users"
          xs={xs}
          series={load}
          leftFmt={perSec}
          rightFmt={vusFmt}
        />
        <LiveChart title="Latency" xs={xs} series={latency} leftFmt={ms} />
        <LiveChart title="Error rate" xs={xs} series={errors} leftFmt={percent} height={150} />
        <EventFeed events={events} />
      </div>
    </div>
  );
}
