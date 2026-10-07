import { AlertTriangle, CheckCircle2, Info, ServerCrash, XCircle } from 'lucide-react';
import type { RunEvent } from '@/api/types';
import { Chip } from '@/components/chips';
import { Card, CardHeader, Table } from '@/components/ui';
import { dryRunGate, eventTone } from './eventLog';

function EventIcon({ e }: { e: RunEvent }) {
  const tone = eventTone(e);
  if (tone === 'fail') {
    return e.type.includes('lost') ? (
      <ServerCrash className="size-3.5 text-fail" aria-hidden />
    ) : (
      <XCircle className="size-3.5 text-fail" aria-hidden />
    );
  }
  if (tone === 'pass') return <CheckCircle2 className="size-3.5 text-pass" aria-hidden />;
  if (tone === 'warn') return <AlertTriangle className="size-3.5 text-warn" aria-hidden />;
  return <Info className="size-3.5 text-info" aria-hidden />;
}

/** A run's events, newest first. */
export function EventFeed({ events, live }: { events: RunEvent[]; live?: boolean }) {
  return (
    <Card className="flex min-h-0 flex-col" role="region" aria-label="Events">
      <CardHeader
        title="Events"
        description="Workers, safety stops and the dry run before load, newest first."
      />
      {events.length === 0 ? (
        <p className="px-4 py-6 text-[13px] text-muted">
          {live ? 'No events yet.' : 'No events were recorded.'}
        </p>
      ) : (
        <ol
          className="max-h-80 divide-y divide-line overflow-y-auto"
          aria-label="Run events"
          aria-live={live ? 'polite' : undefined}
        >
          {events.map((e, i) => (
            <li key={`${e.at}-${e.type}-${i}`} className="flex gap-2.5 px-4 py-2 text-[13px]">
              <span className="mt-0.5">
                <EventIcon e={e} />
              </span>
              <div className="min-w-0 flex-1">
                <p className="break-words">{e.message}</p>
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

const gateChip = {
  running: <Chip tone="live">RUNNING</Chip>,
  passed: <Chip tone="pass">PASSED</Chip>,
  failed: <Chip tone="fail">FAILED</Chip>,
};

/**
 * The dry run the project requires before load: each journey's result, and
 * why the run stopped when one failed. Renders nothing for runs without one.
 */
export function DryRunGatePanel({ events }: { events: RunEvent[] }) {
  const gate = dryRunGate(events);
  if (!gate) return null;
  return (
    <Card role="region" aria-label="Dry run before load">
      <CardHeader
        title="Dry run before load"
        description="This project requires each journey to pass once with one user before any load starts."
        actions={gateChip[gate.state]}
      />
      {gate.summary && (
        <p
          className={
            gate.state === 'failed'
              ? 'border-b border-line px-4 py-2 text-[13px] font-medium text-fail'
              : 'border-b border-line px-4 py-2 text-[13px]'
          }
        >
          {gate.summary.charAt(0).toUpperCase() + gate.summary.slice(1)}.
        </p>
      )}
      {gate.journeys.length > 0 ? (
        <Table aria-label="Dry-run journeys">
          <thead>
            <tr>
              <th>Journey</th>
              <th>Result</th>
              <th>Problem</th>
            </tr>
          </thead>
          <tbody>
            {gate.journeys.map((j) => (
              <tr key={j.journey}>
                <td className="font-medium">{j.journey}</td>
                <td>{j.ok ? <Chip tone="pass">PASSED</Chip> : <Chip tone="fail">FAILED</Chip>}</td>
                <td className="font-mono text-xs break-words">
                  {j.problem ?? <span className="text-muted">–</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      ) : (
        gate.state === 'running' && (
          <p className="px-4 py-3 text-[13px] text-muted">Running each journey once…</p>
        )
      )}
    </Card>
  );
}
