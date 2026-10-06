import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import { cssVar, useIsDark } from '@/components/misc';
import { clock } from '@/lib/format';

export interface LiveSeries {
  label: string;
  /** CSS variable holding the colour, e.g. "--s1". */
  color: string;
  values: (number | null)[];
  dashed?: boolean;
  /** Plot on the right-hand axis. */
  right?: boolean;
  fmt: (v: number) => string;
}

/**
 * A small uPlot line chart for live data. Data is replaced in place with
 * setData so updates every second are cheap.
 */
export function LiveChart({
  title,
  xs,
  series,
  height = 180,
  leftFmt,
  rightFmt,
}: {
  title: string;
  xs: number[];
  series: LiveSeries[];
  height?: number;
  leftFmt: (v: number) => string;
  rightFmt?: (v: number) => string;
}) {
  const host = useRef<HTMLDivElement>(null);
  const plot = useRef<uPlot | null>(null);
  const dark = useIsDark();
  const shape = series.map((s) => `${s.label}:${s.right ? 1 : 0}`).join('|');
  const latest = useRef({ xs, series });
  useEffect(() => {
    latest.current = { xs, series };
  });

  // (Re)create when the series layout or theme changes.
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const muted = cssVar('--muted') || '#8a96a3';
    const grid = cssVar('--line') || '#262e36';
    const axis = (fmt: (v: number) => string, side?: number): uPlot.Axis => ({
      // eslint-disable-next-line @typescript-eslint/no-unsafe-enum-assignment -- uPlot's Side is an ambient const enum
      ...(side != null ? { side } : {}),
      stroke: muted,
      grid: side === 1 ? { show: false } : { stroke: grid, width: 1 },
      ticks: { show: false },
      font: '11px ui-monospace, Menlo, monospace',
      size: 56,
      values: (_u, vals) => vals.map((v) => (v == null ? '' : fmt(v))),
    });
    const { series: ss } = latest.current;
    const hasRight = ss.some((s) => s.right);
    const opts: uPlot.Options = {
      width: el.clientWidth || 600,
      height,
      padding: [8, 16, 0, 0],
      legend: { show: false },
      cursor: { points: { size: 6 }, drag: { x: false, y: false } },
      scales: {
        x: { time: false },
        y: { range: (_u, _min, max) => [0, max > 0 ? max * 1.1 : 1] },
        ...(hasRight ? { r: { range: (_u, _min, max) => [0, max > 0 ? max * 1.1 : 1] } } : {}),
      },
      axes: [
        {
          stroke: muted,
          grid: { show: false },
          ticks: { show: false },
          font: '11px ui-monospace, Menlo, monospace',
          values: (_u, vals) => vals.map((v) => (v == null ? '' : clock(v))),
        },
        axis(leftFmt),
        ...(hasRight && rightFmt ? [{ ...axis(rightFmt, 1), scale: 'r' }] : []),
      ],
      series: [
        {},
        ...ss.map((s): uPlot.Series => ({
          label: s.label,
          stroke: cssVar(s.color),
          width: 1.6,
          dash: s.dashed ? [4, 3] : undefined,
          scale: s.right ? 'r' : 'y',
          points: { show: false },
          spanGaps: false,
        })),
      ],
    };
    const data = [latest.current.xs, ...ss.map((s) => s.values)] as uPlot.AlignedData;
    const u = new uPlot(opts, data, el);
    plot.current = u;
    const ro = new ResizeObserver(() => {
      u.setSize({ width: el.clientWidth, height });
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      u.destroy();
      plot.current = null;
    };
  }, [shape, dark, height, leftFmt, rightFmt]);

  useEffect(() => {
    plot.current?.setData([xs, ...series.map((s) => s.values)] as uPlot.AlignedData);
  }, [xs, series]);

  const last = series.map((s) => {
    for (let i = s.values.length - 1; i >= 0; i--) {
      const v = s.values[i];
      if (v != null) return v;
    }
    return null;
  });

  return (
    <figure className="rounded-lg border border-line bg-surface px-3 pt-2.5 pb-2">
      <figcaption className="mb-1 flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="text-[13px] font-semibold">{title}</span>
        <span className="flex flex-wrap gap-x-3 gap-y-1">
          {series.map((s, i) => (
            <span key={s.label} className="flex items-center gap-1.5 text-xs text-muted">
              <span
                className="inline-block h-0.5 w-3 rounded"
                style={{
                  background: s.dashed
                    ? `repeating-linear-gradient(90deg, var(${s.color}) 0 4px, transparent 4px 7px)`
                    : `var(${s.color})`,
                }}
                aria-hidden
              />
              {s.label}
              <span className="num text-fg">{last[i] != null ? s.fmt(last[i]) : '–'}</span>
            </span>
          ))}
        </span>
      </figcaption>
      <div ref={host} className="w-full" role="img" aria-label={`${title} chart`} />
    </figure>
  );
}
