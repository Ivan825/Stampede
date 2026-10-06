import { Link } from '@tanstack/react-router';
import { clsx } from 'clsx';
import type { Comparison, MetricDelta } from '@/api/types';
import { Chip, CompareVerdictChip } from '@/components/chips';
import { CopyButton } from '@/components/misc';
import { Card, CardHeader, Notice, SectionTitle, Table } from '@/components/ui';
import { compareValue, signedPct } from '@/lib/format';

const verdictText: Record<MetricDelta['verdict'], string> = {
  regression: 'regression',
  improvement: 'improvement',
  'no-change': 'no change',
  inconclusive: 'inconclusive',
};

const verdictClass: Record<MetricDelta['verdict'], string> = {
  regression: 'text-fail',
  improvement: 'text-pass',
  'no-change': 'text-muted',
  inconclusive: 'text-warn',
};

function names(ms: MetricDelta[], v: MetricDelta['verdict']): string {
  return ms
    .filter((m) => m.verdict === v)
    .map((m) => m.name)
    .join(', ');
}

/** One plain sentence explaining the overall verdict. */
function summary(c: Comparison): string {
  const a = c.a.label;
  const b = c.b.label;
  const few = c.a.runs.length < 2 || c.b.runs.length < 2;
  switch (c.verdict) {
    case 'regression':
      return `${b} is worse than ${a} on ${names(c.metrics, 'regression')}, beyond the noise between repeats.`;
    case 'improvement':
      return `${b} is better than ${a} on ${names(c.metrics, 'improvement')}, and nothing regressed.`;
    case 'no-change':
      return 'No metric changed by more than the noise between repeats.';
    default:
      if (!c.comparable) return 'The runs differ in setup, so a difference could come from that.';
      return few
        ? 'Some metrics moved, but with one run on a side a change cannot be told apart from noise. Add runs.'
        : 'Some metrics moved beyond the noise floor, but the 95% interval still includes no change. Add runs.';
  }
}

function values(name: string, vs: number[]): string {
  return vs.map((v) => compareValue(name, v)).join(', ');
}

function MetricCells({ m }: { m: MetricDelta }) {
  return (
    <>
      <td className="num text-right" title={`Each run: ${values(m.name, m.a)}`}>
        {compareValue(m.name, m.meanA)}
      </td>
      <td className="num text-right" title={`Each run: ${values(m.name, m.b)}`}>
        {compareValue(m.name, m.meanB)}
      </td>
      <td className={clsx('num text-right font-medium', verdictClass[m.verdict])}>
        {signedPct(m.change)}
      </td>
      <td className="num text-right text-xs whitespace-nowrap text-muted">
        {signedPct(m.ciLow)} … {signedPct(m.ciHigh)}
      </td>
      <td className="num text-right text-xs text-muted">±{(m.noiseFloor * 100).toFixed(0)}%</td>
      <td className={clsx('text-xs font-medium', verdictClass[m.verdict])}>
        {verdictText[m.verdict]}
      </td>
    </>
  );
}

function RunLinks({ label, runs }: { label: string; runs: string[] }) {
  return (
    <div className="min-w-0">
      <div className="text-[13px] font-medium">
        {label}{' '}
        <span className="text-xs font-normal text-muted">
          ({runs.length} run{runs.length === 1 ? '' : 's'})
        </span>
      </div>
      <div className="mt-1 flex flex-wrap gap-x-2 gap-y-0.5">
        {runs.map((id) => (
          <Link
            key={id}
            to="/runs/$runId"
            params={{ runId: id }}
            className="font-mono text-xs text-muted hover:text-fg hover:underline"
          >
            {id.slice(0, 8)}
          </Link>
        ))}
      </div>
    </div>
  );
}

export function ComparisonView({ c }: { c: Comparison }) {
  const pct95 = `${Math.round(c.confidence * 100)}%`;
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap items-start gap-4 px-4 py-3.5">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2" aria-label="Verdict">
              {c.comparable ? (
                <CompareVerdictChip verdict={c.verdict} />
              ) : (
                <Chip tone="warn">NOT COMPARABLE</Chip>
              )}
              <span className="text-[13px] text-muted">
                {c.a.label} → {c.b.label}
              </span>
            </div>
            <p className="mt-2 text-[13px]">{summary(c)}</p>
          </div>
          <CopyButton value={c.markdown} label="Copy Markdown" />
        </div>
        <div className="grid gap-4 border-t border-line px-4 py-3 sm:grid-cols-2">
          <RunLinks label={`A · ${c.a.label}`} runs={c.a.runs} />
          <RunLinks label={`B · ${c.b.label}`} runs={c.b.runs} />
        </div>
      </Card>

      {(c.problems.length > 0 || c.a.runs.length < 3 || c.b.runs.length < 3) && (
        <div className="flex flex-col gap-2" aria-label="Warnings">
          {c.problems.map((p) => (
            <Notice key={p} tone="warn">
              Not comparable: {p}.
            </Notice>
          ))}
          {(c.a.runs.length < 3 || c.b.runs.length < 3) && (
            <Notice tone="info">
              Use at least 3 runs per version; with fewer, changes are reported as inconclusive.
            </Notice>
          )}
        </div>
      )}

      <Card>
        <CardHeader
          title="Metrics"
          description={`Means of each side, the relative change, its bootstrap ${pct95} interval and the noise floor: the largest spread between repeats of one version (at least 2%).`}
        />
        <Table>
          <thead>
            <tr>
              <th>Metric</th>
              <th className="!text-right">{c.a.label}</th>
              <th className="!text-right">{c.b.label}</th>
              <th className="!text-right">Change</th>
              <th className="!text-right">{pct95} interval</th>
              <th className="!text-right">Noise</th>
              <th>Verdict</th>
            </tr>
          </thead>
          <tbody>
            {c.metrics.map((m) => (
              <tr key={m.name}>
                <td className="font-medium">{m.name}</td>
                <MetricCells m={m} />
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>

      {c.steps.length > 0 && (
        <>
          <SectionTitle>Per step</SectionTitle>
          <Card>
            <p className="border-b border-line px-4 py-2 text-xs text-muted">
              With many steps, some differ by chance, so steps do not change the verdict. Use them
              to find where a change comes from.
            </p>
            <Table>
              <thead>
                <tr>
                  <th>Step</th>
                  <th>Metric</th>
                  <th className="!text-right">{c.a.label}</th>
                  <th className="!text-right">{c.b.label}</th>
                  <th className="!text-right">Change</th>
                  <th className="!text-right">{pct95} interval</th>
                  <th className="!text-right">Noise</th>
                  <th>Verdict</th>
                </tr>
              </thead>
              <tbody>
                {c.steps.map((s) =>
                  s.metrics.map((m, i) => (
                    <tr key={`${s.journey}/${s.step}/${m.name}`}>
                      {i === 0 && (
                        <td rowSpan={s.metrics.length} className="max-w-72 align-top">
                          <div className="truncate text-xs text-muted">{s.journey}</div>
                          <div className="truncate font-mono text-xs">{s.step}</div>
                        </td>
                      )}
                      <td className="text-xs">{m.name}</td>
                      <MetricCells m={m} />
                    </tr>
                  )),
                )}
              </tbody>
            </Table>
          </Card>
        </>
      )}
      <p className="text-xs text-muted">
        A change counts as a regression or an improvement only when the {pct95} interval excludes
        zero and the change is larger than the noise floor; for latency it must also exceed 1 ms.
        The CLI gives the same result with <span className="font-mono">stampede compare</span>.
      </p>
    </div>
  );
}
