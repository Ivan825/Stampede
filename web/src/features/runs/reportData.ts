import type { ErrorExample, Report } from '@/api/types';
import { cssVar } from '@/components/misc';

export type BandKind = 'fault' | 'saturated' | 'lost';

/** A shaded stretch of a timeline chart, in seconds since the start. */
export interface TimeBand {
  from: number;
  to: number;
  label: string;
  kind: BandKind;
}

export const bandColor: Record<BandKind, string> = {
  fault: '--warn',
  saturated: '--s3',
  lost: '--fail',
};
export const bandLabel: Record<BandKind, string> = {
  fault: 'injected fault',
  saturated: 'worker saturated',
  lost: 'worker lost',
};
export const bandOrder: BandKind[] = ['fault', 'saturated', 'lost'];

/**
 * The windows the timeline charts shade, as in the HTML report: injected
 * faults, windows in which a worker was saturated, and windows in which a
 * lost worker's share of the load was not generated.
 */
export function timeBandsOf(report: Pick<Report, 'faults' | 'workers'>): TimeBand[] {
  const out: TimeBand[] = [];
  for (const f of report.faults ?? []) {
    if (!f.error) out.push({ from: f.start, to: f.end, label: f.label, kind: 'fault' });
  }
  for (const w of report.workers ?? []) {
    const reasons = w.saturationReasons?.length ? ` (${w.saturationReasons.join(', ')})` : '';
    for (const s of w.saturated ?? []) {
      out.push({
        from: s.from,
        to: s.to,
        label: `${w.name} saturated${reasons}`,
        kind: 'saturated',
      });
    }
    if (w.lost) {
      out.push({ from: w.lost.from, to: w.lost.to, label: `${w.name} lost`, kind: 'lost' });
    }
  }
  return out;
}

type MarkArea = [
  { xAxis: number; name: string; itemStyle: { color: string; opacity: number } },
  { xAxis: number },
];

/** ECharts markArea data for the bands, clipped to [lo, hi]. */
export function markAreaOf(bands: TimeBand[], lo: number, hi: number): MarkArea[] {
  return bands
    .map((b) => ({ ...b, from: Math.max(lo, b.from), to: Math.min(hi, b.to) }))
    .filter((b) => b.to > b.from)
    .map((b): MarkArea => [
      {
        xAxis: b.from,
        name: b.label,
        itemStyle: { color: cssVar(bandColor[b.kind]), opacity: 0.14 },
      },
      { xAxis: b.to },
    ]);
}

function headerLines(h: Record<string, string> | undefined) {
  return Object.entries(h ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}: ${v}`);
}

/** Text of the request side of an error example, like the HTML report. */
export function requestText(ex: ErrorExample): string {
  const lines = [ex.request, ...headerLines(ex.requestHeaders)];
  return ex.requestBody ? `${lines.join('\n')}\n\n${ex.requestBody}` : lines.join('\n');
}

/** Text of the response side of an error example, like the HTML report. */
export function responseText(ex: ErrorExample): string {
  const first = `${ex.status ? String(ex.status) : 'no response'}${ex.detail ? ` · ${ex.detail}` : ''}`;
  const lines = [first, ...headerLines(ex.responseHeaders)];
  return ex.responseBody ? `${lines.join('\n')}\n\n${ex.responseBody}` : lines.join('\n');
}
