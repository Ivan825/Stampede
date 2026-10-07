import { clsx } from 'clsx';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { Fragment, useState } from 'react';
import type {
  Breakpoint,
  CurvePoint,
  ErrorExample,
  ErrorRow,
  Knee,
  Recovery,
  Span,
  WorkerRow,
} from '@/api/types';
import { Chip } from '@/components/chips';
import { Card, SectionTitle, Table } from '@/components/ui';
import { count, dateTime, ms, num, pct, rate, secs } from '@/lib/format';
import { CurveChart } from './ReportCharts';
import { requestText, responseText } from './reportData';

const level = (v: number, unit: string) => `${num(v)}${unit}`;

/** Where the targets stopped holding, and the holds that narrowed it. */
export function BreakpointSection({ bp }: { bp: Breakpoint }) {
  return (
    <>
      <SectionTitle>Breakpoint</SectionTitle>
      <Card className="flex flex-col gap-2 px-4 py-3 text-[13px]">
        {bp.found ? (
          <p>
            Targets held up to <b className="num">{level(bp.lastPass, bp.unit)}</b> and failed at{' '}
            <b className="num text-fail">{level(bp.firstFail ?? 0, bp.unit)}</b>
            {bp.failedOn?.length ? (
              <>
                {' '}
                on{' '}
                {bp.failedOn.map((f, i) => (
                  <Fragment key={f}>
                    {i > 0 && ', '}
                    <code className="font-mono text-xs">{f}</code>
                  </Fragment>
                ))}
              </>
            ) : null}
            : the breakpoint is between the two.
          </p>
        ) : (
          <p>
            Not reached: every target held at each level up to{' '}
            <b className="num">{level(bp.lastPass, bp.unit)}</b>.
          </p>
        )}
        {bp.refined && bp.refined.length > 0 && (
          <div>
            <p className="mb-1.5 text-xs text-muted">
              Narrowed by {bp.refined.length} confirmation hold
              {bp.refined.length === 1 ? '' : 's'} after the first failing step:
            </p>
            <ol className="flex flex-wrap gap-1.5" aria-label="Confirmation holds">
              {bp.refined.map((s, i) => (
                <li key={i}>
                  <Chip
                    tone={s.pass ? 'pass' : 'fail'}
                    title={s.failedOn?.length ? `failed on ${s.failedOn.join(', ')}` : undefined}
                  >
                    {level(s.level, bp.unit)} {s.pass ? 'held' : 'failed'}
                  </Chip>
                </li>
              ))}
            </ol>
          </div>
        )}
      </Card>
    </>
  );
}

/** Throughput against load and the knee, for runs whose load changed. */
export function CurveSection({
  curve,
  knee,
  unit,
}: {
  curve: CurvePoint[];
  knee?: Knee;
  unit: string;
}) {
  if (curve.length < 2) return null;
  const u = knee?.unit ?? unit;
  return (
    <>
      <SectionTitle>Throughput against load</SectionTitle>
      <Card className="flex flex-col gap-2 px-4 py-3 text-[13px]">
        {knee &&
          (knee.found && knee.next ? (
            <p>
              Throughput kept up with load to <b className="num">{level(knee.at.offered, u)}</b>. At{' '}
              <b className="num text-warn">{level(knee.next.offered, u)}</b> {knee.reason}:{' '}
              <span className="num">
                {rate(knee.next.throughput)} it/s, p95 {ms(knee.next.p95)}
              </span>
              .
            </p>
          ) : (
            <p>
              No knee: throughput kept up with load at every level up to{' '}
              <b className="num">{level(knee.at.offered, u)}</b>.
            </p>
          ))}
        <CurveChart curve={curve} knee={knee} unit={u} />
        <Table className="[&_td]:text-right [&_td:first-child]:text-left [&_th]:text-right [&_th:first-child]:text-left">
          <thead>
            <tr>
              <th>Offered</th>
              <th>Completed it/s</th>
              <th>Requests/s</th>
              <th>p50</th>
              <th>p95</th>
              <th>p99</th>
              <th>Errors</th>
              <th>Seconds</th>
            </tr>
          </thead>
          <tbody className="num">
            {curve.map((c) => {
              const isKnee = knee?.found && knee.next?.offered === c.offered;
              return (
                <tr key={c.offered} className={isKnee ? 'bg-warn-bg' : undefined}>
                  <td>
                    {level(c.offered, u)}
                    {isKnee && <span className="ml-2 font-sans text-xs text-warn">knee</span>}
                  </td>
                  <td>{rate(c.throughput)}</td>
                  <td>{rate(c.rps)}</td>
                  <td>{ms(c.p50)}</td>
                  <td>{ms(c.p95)}</td>
                  <td>{ms(c.p99)}</td>
                  <td className={c.errorRate > 0 ? 'text-fail' : undefined}>{pct(c.errorRate)}</td>
                  <td>{c.seconds}</td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      </Card>
    </>
  );
}

/** How long the target took to return to its baseline after the overload. */
export function RecoverySection({ recovery: r }: { recovery: Recovery }) {
  return (
    <>
      <SectionTitle>Recovery</SectionTitle>
      <Card className="px-4 py-3 text-[13px]">
        {r.recovered ? (
          <p>
            Back to normal <b className="num text-pass">{secs(r.seconds ?? 0)}</b> after load
            returned to normal at <span className="num">{secs(r.normalAt)}</span>: p95 within 25% of
            the baseline <span className="num">{ms(r.baselineP95)}</span> and errors within a point
            of <span className="num">{pct(r.baselineErrorRate)}</span>, for three seconds in a row.
          </p>
        ) : (
          <p>
            <b className="text-fail">Not back to normal</b> by the end of the run. Load returned to
            normal at <span className="num">{secs(r.normalAt)}</span>; before the overload, p95 was{' '}
            <span className="num">{ms(r.baselineP95)}</span> and errors{' '}
            <span className="num">{pct(r.baselineErrorRate)}</span>.
          </p>
        )}
      </Card>
    </>
  );
}

const spans = (ss: Span[]) => ss.map((s) => `${secs(s.from)}–${secs(s.to)}`).join(', ');

/** Each worker's share of the load and its health during the run. */
export function WorkersSection({ workers }: { workers: WorkerRow[] }) {
  if (workers.length === 0) return null;
  return (
    <>
      <SectionTitle>Workers</SectionTitle>
      <Card>
        <Table>
          <thead>
            <tr>
              <th>Worker</th>
              <th>Region</th>
              <th className="!text-right">Share</th>
              <th>State</th>
              <th className="!text-right">Peak VUs</th>
              <th className="!text-right">Requests</th>
              <th>Saturated</th>
              <th>Lost</th>
              <th className="!text-right">Clock offset</th>
            </tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr key={w.id || w.name}>
                <td>
                  <span className="font-medium">{w.name}</span>
                  {w.replaces && (
                    <span className="ml-1.5 text-xs text-muted">replaces {w.replaces}</span>
                  )}
                </td>
                <td className="font-mono text-xs">{w.region || '–'}</td>
                <td className="num text-right">{(100 * (w.shareHi - w.shareLo)).toFixed(1)}%</td>
                <td>
                  <span
                    className={clsx(
                      'font-mono text-xs',
                      (w.state === 'lost' || w.state === 'failed') && 'text-fail',
                    )}
                  >
                    {w.state}
                  </span>
                  {(w.error || w.stopReason) && (
                    <span className="ml-1.5 text-xs text-muted">{w.error || w.stopReason}</span>
                  )}
                </td>
                <td className="num text-right">{count(w.peakVUs)}</td>
                <td className="num text-right">{count(w.requests)}</td>
                <td className="text-xs">
                  {w.saturated?.length ? (
                    <span className="text-warn">
                      <span className="num">{spans(w.saturated)}</span>
                      {w.saturationReasons?.length ? (
                        <span className="text-muted"> ({w.saturationReasons.join(', ')})</span>
                      ) : null}
                    </span>
                  ) : (
                    <span className="text-muted">no</span>
                  )}
                </td>
                <td className="text-xs">
                  {w.lost ? (
                    <span className="num text-fail">{spans([w.lost])}</span>
                  ) : (
                    <span className="text-muted">no</span>
                  )}
                </td>
                <td className="num text-right text-xs">{(w.clockOffset * 1000).toFixed(1)}ms</td>
              </tr>
            ))}
          </tbody>
        </Table>
        <p className="border-t border-line px-3 py-2 text-xs text-muted">
          Saturated and lost windows are shaded on the charts. Latency measured while a worker was
          saturated may reflect the load generator rather than the target; while a lost worker's
          share was not generated, the run produced less load than planned.
        </p>
      </Card>
    </>
  );
}

const pre =
  'max-h-80 overflow-auto rounded-md border border-line bg-bg p-2.5 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap';

function Example({ ex, n }: { ex: ErrorExample; n: number }) {
  return (
    <div className="flex flex-col gap-1.5">
      <p className="flex flex-wrap gap-x-3 text-xs text-muted">
        <span>Example {n}</span>
        <time dateTime={ex.at}>{dateTime(ex.at)}</time>
        {ex.traceId && <span className="font-mono">trace {ex.traceId}</span>}
      </p>
      <div className="grid gap-2 md:grid-cols-2">
        <section aria-label={`Example ${n} request`}>
          <h4 className="label-caps mb-1">Request</h4>
          <pre className={pre}>{requestText(ex)}</pre>
        </section>
        <section aria-label={`Example ${n} response`}>
          <h4 className="label-caps mb-1">Response</h4>
          <pre className={pre}>{responseText(ex)}</pre>
        </section>
      </div>
    </div>
  );
}

/** Errors by kind and step; rows with examples expand to what was sent and received. */
export function ErrorsSection({ errors }: { errors: ErrorRow[] }) {
  const [open, setOpen] = useState<Set<number>>(() => new Set());
  if (errors.length === 0) return null;
  const toggle = (i: number) =>
    setOpen((s) => {
      const n = new Set(s);
      if (n.has(i)) n.delete(i);
      else n.add(i);
      return n;
    });
  return (
    <>
      <SectionTitle>Errors</SectionTitle>
      <Card>
        <Table>
          <thead>
            <tr>
              <th>Error</th>
              <th>Journey</th>
              <th>Step</th>
              <th className="!text-right">Count</th>
              <th>Examples</th>
            </tr>
          </thead>
          <tbody>
            {errors.map((e, i) => {
              const exs = e.examples ?? [];
              const expanded = open.has(i);
              const id = `error-examples-${i}`;
              return (
                <Fragment key={i}>
                  <tr>
                    <td className="max-w-md font-mono text-xs break-words">{e.error}</td>
                    <td>{e.journey}</td>
                    <td className="font-mono text-xs">{e.step}</td>
                    <td className="num text-right">{count(e.count)}</td>
                    <td>
                      {exs.length > 0 ? (
                        <button
                          type="button"
                          className="inline-flex items-center gap-1 text-xs text-info hover:underline"
                          aria-expanded={expanded}
                          aria-controls={id}
                          onClick={() => toggle(i)}
                        >
                          {expanded ? (
                            <ChevronDown className="size-3.5" aria-hidden />
                          ) : (
                            <ChevronRight className="size-3.5" aria-hidden />
                          )}
                          {expanded ? 'Hide' : 'Show'} {exs.length} example
                          {exs.length === 1 ? '' : 's'}
                        </button>
                      ) : (
                        <span className="text-xs text-muted">none</span>
                      )}
                    </td>
                  </tr>
                  {expanded && (
                    <tr id={id}>
                      <td colSpan={5} className="bg-surface-2/50">
                        <div className="flex flex-col gap-4 py-1">
                          {exs.map((ex, k) => (
                            <Example key={k} ex={ex} n={k + 1} />
                          ))}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </Table>
        <p className="border-t border-line px-3 py-2 text-xs text-muted">
          Up to three examples per error, with credentials and secrets redacted.
        </p>
      </Card>
    </>
  );
}
