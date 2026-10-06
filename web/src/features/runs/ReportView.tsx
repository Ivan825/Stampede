import { clsx } from 'clsx';
import { Download } from 'lucide-react';
import { Fragment } from 'react';
import { reportUrl } from '@/api/client';
import type { Report } from '@/api/types';
import { verdictLabels } from '@/components/chips';
import { Card, Notice, SectionTitle, Stat, Table } from '@/components/ui';
import { bytes, count, dateTime, ms, num, pct, rate, secs } from '@/lib/format';
import { ReportCharts } from './ReportCharts';

const verdictStyle = {
  pass: 'border-pass/50 bg-pass-bg text-pass',
  fail: 'border-fail/50 bg-fail-bg text-fail',
  'generator-limited': 'border-warn/50 bg-warn-bg text-warn',
  'no-targets': 'border-line bg-surface-2 text-muted',
} as const;

const verdictText = {
  pass: 'Every target held.',
  fail: 'At least one target was missed.',
  'generator-limited':
    'The load generator could not keep the schedule, so results understate what the target would see.',
  'no-targets': 'The scenario defines no targets, so there is no pass or fail.',
} as const;

export function Downloads({ runId }: { runId: string }) {
  const formats = [
    ['html', 'HTML'],
    ['json', 'JSON'],
    ['junit', 'JUnit'],
    ['markdown', 'Markdown'],
  ] as const;
  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Download report">
      <Download className="size-3.5 text-muted" aria-hidden />
      {formats.map(([f, label]) => (
        <a
          key={f}
          href={reportUrl(runId, f)}
          download
          className="inline-flex h-7 items-center rounded-md border border-line bg-surface px-2.5 text-[13px] font-medium hover:border-line-strong hover:bg-surface-2"
        >
          {label}
        </a>
      ))}
    </div>
  );
}

const phases = ['dns', 'connect', 'tls', 'wait', 'download'] as const;

export function ReportView({ report }: { report: Report }) {
  const o = report.overall;
  const unit = report.load.mode === 'rate' ? '/s' : ' VUs';
  const thresholds = report.thresholds ?? [];
  const journeys = report.journeys ?? [];
  const errors = report.errors ?? [];
  const timeline = report.timeline ?? [];
  const bp = report.breakpoint;

  return (
    <div className="flex flex-col">
      <div
        className={clsx(
          'flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg border px-4 py-3',
          verdictStyle[report.verdict],
        )}
        role="status"
      >
        <span className="font-mono text-lg font-bold tracking-wider">
          {verdictLabels[report.verdict]}
        </span>
        <span className="text-[13px] text-fg">{verdictText[report.verdict]}</span>
        <span className="num ml-auto text-xs text-muted">
          {dateTime(report.started)} · {secs(report.duration)} load · {report.load.executor}
          {report.load.shape ? ` · ${report.load.shape}` : ''} · peak {num(report.load.peak)}
          {unit} · {report.load.workers} worker{report.load.workers === 1 ? '' : 's'} · stop:{' '}
          {report.stopReason}
        </span>
      </div>

      {report.notes?.map((n, i) => (
        <Notice key={i} className="mt-3">
          {n}
        </Notice>
      ))}

      {thresholds.length > 0 && (
        <>
          <SectionTitle>Targets</SectionTitle>
          <Card>
            <Table>
              <thead>
                <tr>
                  <th>Target</th>
                  <th>Scope</th>
                  <th className="!text-right">Observed</th>
                  <th className="!text-right">Result</th>
                </tr>
              </thead>
              <tbody>
                {thresholds.map((c, i) => (
                  <tr key={i}>
                    <td className="font-mono text-xs">{c.source}</td>
                    <td className="text-xs text-muted">{c.scope}</td>
                    <td className="num text-right">{c.observedText}</td>
                    <td
                      className={clsx(
                        'text-right font-mono text-xs font-semibold',
                        c.pass ? 'text-pass' : 'text-fail',
                      )}
                    >
                      {c.pass ? 'pass' : 'fail'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </Card>
        </>
      )}

      {bp && (
        <>
          <SectionTitle>Breakpoint</SectionTitle>
          <Card className="px-4 py-3 text-[13px]">
            {bp.found ? (
              <p>
                Targets held up to{' '}
                <b className="num">
                  {num(bp.lastPass)}
                  {bp.unit}
                </b>{' '}
                and failed at{' '}
                <b className="num text-fail">
                  {num(bp.firstFail ?? 0)}
                  {bp.unit}
                </b>
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
                .
              </p>
            ) : (
              <p>
                Not reached: every target held at each level up to{' '}
                <b className="num">
                  {num(bp.lastPass)}
                  {bp.unit}
                </b>
                .
              </p>
            )}
          </Card>
        </>
      )}

      <SectionTitle>Summary</SectionTitle>
      <div className="grid grid-cols-2 gap-2.5 md:grid-cols-4">
        <Stat label="requests" value={count(o.requests)} sub={`${rate(o.rps)}/s`} />
        <Stat
          label={`failed requests (${count(o.failed)})`}
          value={pct(o.errorRate)}
          tone={o.errorRate > 0 ? 'fail' : undefined}
        />
        <Stat label="p50 latency" value={ms(o.latency.p50)} />
        <Stat label="p95 latency" value={ms(o.latency.p95)} />
        <Stat label="p99 latency" value={ms(o.latency.p99)} />
        <Stat
          label="iterations"
          value={count(o.iterations ?? 0)}
          sub={`${count(o.dropped ?? 0)} dropped`}
          tone={(o.dropped ?? 0) > 0 ? 'warn' : undefined}
        />
        <Stat label="peak virtual users" value={count(report.load.peakVUs)} />
        <Stat label="received" value={bytes(o.bytesIn)} sub={`${bytes(o.bytesOut)} sent`} />
      </div>
      {o.latency.p99 !== o.service.p99 && o.service.count > 0 && (
        <p className="num mt-2 text-xs text-muted">
          Service time (from actual send): p50 {ms(o.service.p50)} · p95 {ms(o.service.p95)} · p99{' '}
          {ms(o.service.p99)}. Latency above counts from the scheduled send, so generator queueing
          is never hidden.
        </p>
      )}

      {timeline.length > 0 && (
        <>
          <SectionTitle>Over time</SectionTitle>
          <ReportCharts timeline={timeline} mode={report.load.mode} />
        </>
      )}

      <SectionTitle>Journeys and steps</SectionTitle>
      <Card>
        <Table className="[&_td]:text-right [&_td:first-child]:text-left [&_th]:text-right [&_th:first-child]:text-left">
          <thead>
            <tr>
              <th>Step</th>
              <th>Requests</th>
              <th>Errors</th>
              <th>p50</th>
              <th>p95</th>
              <th>p99</th>
              <th>Max</th>
              {phases.map((p) => (
                <th key={p} title={`Mean ${p} time`}>
                  {p}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="num">
            {journeys.map((j) => (
              <Fragment key={j.name}>
                <tr className="bg-surface-2/70 font-semibold">
                  <td className="font-sans">{j.name}</td>
                  <td>{count(j.stats.requests)}</td>
                  <td className={j.stats.errorRate > 0 ? 'text-fail' : undefined}>
                    {pct(j.stats.errorRate)}
                  </td>
                  <td>{ms(j.stats.latency.p50)}</td>
                  <td>{ms(j.stats.latency.p95)}</td>
                  <td>{ms(j.stats.latency.p99)}</td>
                  <td>{ms(j.stats.latency.max)}</td>
                  <td
                    colSpan={phases.length}
                    className="!text-left font-sans text-xs font-normal text-muted"
                  >
                    {count(j.stats.iterations ?? 0)} iterations ·{' '}
                    {count(j.stats.iterationsFailed ?? 0)} failed
                    {j.stats.iterationTime?.p95
                      ? ` · p95 iteration ${ms(j.stats.iterationTime.p95)}`
                      : ''}
                  </td>
                </tr>
                {(j.steps ?? []).map((s) => (
                  <tr key={s.id}>
                    <td className="pl-6 font-mono text-xs">{s.name}</td>
                    <td>{count(s.stats.requests)}</td>
                    <td className={s.stats.errorRate > 0 ? 'text-fail' : undefined}>
                      {pct(s.stats.errorRate)}
                    </td>
                    <td>{ms(s.stats.latency.p50)}</td>
                    <td>{ms(s.stats.latency.p95)}</td>
                    <td>{ms(s.stats.latency.p99)}</td>
                    <td>{ms(s.stats.latency.max)}</td>
                    {phases.map((p) => (
                      <td
                        key={p}
                        className="text-xs text-muted"
                        title={
                          s.phases[p]
                            ? `mean ${ms(s.phases[p].mean)} · p95 ${ms(s.phases[p].p95)}`
                            : undefined
                        }
                      >
                        {ms(s.phases[p]?.mean)}
                      </td>
                    ))}
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </Table>
      </Card>

      {errors.length > 0 && (
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
                </tr>
              </thead>
              <tbody>
                {errors.map((e, i) => (
                  <tr key={i}>
                    <td className="max-w-md font-mono text-xs break-words">{e.error}</td>
                    <td>{e.journey}</td>
                    <td className="font-mono text-xs">{e.step}</td>
                    <td className="num text-right">{count(e.count)}</td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </Card>
        </>
      )}

      <p className="mt-8 border-t border-line pt-3 text-xs text-muted">
        Generated by Stampede {report.stampede}. Latency is measured from each request&apos;s
        scheduled send time; percentiles come from merged histograms, never averaged.
      </p>
    </div>
  );
}
