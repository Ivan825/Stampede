import { LineChart } from 'echarts/charts';
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkAreaComponent,
  MarkLineComponent,
  TooltipComponent,
} from 'echarts/components';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';
import { useEffect, useMemo, useRef, type ReactNode } from 'react';
import type { CurvePoint, Knee, Point, TargetMetric } from '@/api/types';
import { cssVar, useIsDark } from '@/components/misc';
import { axisMs, axisPct, clock, metricValue, ms, num, secs } from '@/lib/format';
import { bandColor, bandLabel, bandOrder, markAreaOf, type TimeBand } from './reportData';

echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  MarkAreaComponent,
  MarkLineComponent,
  CanvasRenderer,
]);

interface S {
  name: string;
  color: string;
  data: (number | null)[];
  dashed?: boolean;
  right?: boolean;
  fmt: (v: number) => string;
}

/** Legend entries for the kinds of band a chart shades. */
export function BandLegend({ bands }: { bands: TimeBand[] }) {
  const kinds = bandOrder.filter((k) => bands.some((b) => b.kind === k));
  if (kinds.length === 0) return null;
  return (
    <ul
      className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted"
      aria-label="Shaded windows"
    >
      {kinds.map((k) => (
        <li key={k} className="flex items-center gap-1.5">
          <span
            className="inline-block h-2.5 w-3 rounded-sm opacity-50"
            style={{ background: `var(${bandColor[k]})` }}
            aria-hidden
          />
          {bandLabel[k]}
        </li>
      ))}
    </ul>
  );
}

const group = 'stampede-report';

function TimelineChart({
  title,
  xs,
  series,
  leftFmt,
  rightFmt,
  height = 220,
  synced = true,
  extra,
  bands = [],
}: {
  title: ReactNode;
  xs: number[];
  series: S[];
  leftFmt: (v: number) => string;
  rightFmt?: (v: number) => string;
  height?: number;
  /** Share the cursor with the other timeline charts (same x axis). */
  synced?: boolean;
  extra?: ReactNode;
  bands?: TimeBand[];
}) {
  const el = useRef<HTMLDivElement>(null);
  const dark = useIsDark();

  useEffect(() => {
    if (!el.current) return;
    const chart = echarts.init(el.current, undefined, { renderer: 'canvas' });
    if (synced) {
      chart.group = group;
      echarts.connect(group);
    }
    const muted = cssVar('--muted');
    const line = cssVar('--line');
    const fg = cssVar('--fg');
    const surface = cssVar('--surface');
    const mono = 'ui-monospace, Menlo, monospace';
    const hasRight = series.some((s) => s.right);
    const lo = xs[0] ?? 0;
    const hi = xs[xs.length - 1] ?? 1;
    const areas = markAreaOf(bands, lo, hi);
    chart.setOption({
      animation: false,
      textStyle: { fontFamily: mono },
      grid: { left: 64, right: hasRight ? 56 : 16, top: 34, bottom: 28 },
      legend: {
        top: 0,
        left: 0,
        icon: 'roundRect',
        itemWidth: 12,
        itemHeight: 3,
        textStyle: { color: muted, fontSize: 11 },
      },
      tooltip: {
        trigger: 'axis',
        backgroundColor: surface,
        borderColor: line,
        textStyle: { color: fg, fontSize: 12, fontFamily: mono },
        axisPointer: { lineStyle: { color: muted, type: 'dashed' } },
        formatter: (
          params: {
            seriesIndex: number;
            axisValue: number;
            value: [number, number | null];
            marker: string;
            seriesName: string;
          }[],
        ) => {
          const t = params[0]?.axisValue ?? 0;
          const rows = params
            .map((p) => {
              const v = p.value[1];
              return `${p.marker}${p.seriesName}: <b>${v == null ? '–' : series[p.seriesIndex]!.fmt(v)}</b>`;
            })
            .join('<br/>');
          const inBands = bands.filter((b) => t >= b.from && t < b.to).map((b) => b.label);
          return `t = ${secs(t)}<br/>${rows}${inBands.length ? `<br/><i>${inBands.join('<br/>')}</i>` : ''}`;
        },
      },
      xAxis: {
        type: 'value',
        min: lo,
        max: hi,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { color: muted, fontSize: 11, formatter: (v: number) => clock(v) },
      },
      yAxis: [
        {
          type: 'value',
          min: 0,
          splitLine: { lineStyle: { color: line } },
          axisLabel: { color: muted, fontSize: 11, formatter: (v: number) => leftFmt(v) },
        },
        ...(hasRight
          ? [
              {
                type: 'value',
                min: 0,
                splitLine: { show: false },
                axisLabel: {
                  color: muted,
                  fontSize: 11,
                  formatter: (v: number) => (rightFmt ?? leftFmt)(v),
                },
              },
            ]
          : []),
      ],
      dataZoom: [{ type: 'inside', throttle: 50 }],
      series: series.map((s, i) => ({
        name: s.name,
        type: 'line',
        data: s.data.map((y, k) => [xs[k], y]),
        yAxisIndex: s.right ? 1 : 0,
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: 1.8, color: cssVar(s.color), type: s.dashed ? 'dashed' : 'solid' },
        itemStyle: { color: cssVar(s.color) },
        ...(i === 0 && areas.length
          ? { markArea: { silent: true, label: { show: false }, data: areas } }
          : {}),
      })),
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el.current);
    return () => {
      ro.disconnect();
      chart.dispose();
    };
  }, [xs, series, dark, leftFmt, rightFmt, synced, bands]);

  return (
    <figure className="rounded-lg border border-line bg-surface px-3 pt-2.5 pb-1">
      <figcaption className="mb-1 text-[13px] font-semibold">{title}</figcaption>
      {extra}
      <div
        ref={el}
        style={{ height }}
        role="img"
        aria-label={`${typeof title === 'string' ? title : 'Metric'} chart`}
      />
      {bands.length > 0 && (
        <div className="pb-1.5">
          <BandLegend bands={bands} />
        </div>
      )}
    </figure>
  );
}

/** One chart per Prometheus query of the target's own metrics. */
export function TargetMetricCharts({
  metrics,
  bands = [],
}: {
  metrics: TargetMetric[];
  bands?: TimeBand[];
}) {
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      {metrics.map((m) => (
        <TargetMetricChart key={m.name} metric={m} bands={bands} />
      ))}
    </div>
  );
}

function TargetMetricChart({ metric, bands }: { metric: TargetMetric; bands: TimeBand[] }) {
  const { xs, series, range } = useMemo(() => {
    const pts = metric.points ?? [];
    const values = pts.map((p) => p.value);
    return {
      xs: pts.map((p) => p.t),
      series: [{ name: metric.name, color: '--s2', data: values, fmt: metricValue }] as S[],
      range: values.length
        ? { min: Math.min(...values), max: Math.max(...values), last: values[values.length - 1]! }
        : null,
    };
  }, [metric]);
  const header = (
    <span className="flex flex-wrap items-baseline gap-x-3">
      <span className="font-mono">{metric.name}</span>
      {range && (
        <span className="num text-xs font-normal text-muted">
          min {metricValue(range.min)} · max {metricValue(range.max)} · last{' '}
          {metricValue(range.last)}
        </span>
      )}
    </span>
  );
  const detail = (
    <>
      <p className="mb-1 font-mono text-[11px] break-all text-muted">{metric.query}</p>
      {metric.error && (
        <p className="mb-1 text-xs text-warn" role="note">
          {metric.error}
        </p>
      )}
    </>
  );
  if (!range) {
    return (
      <figure className="rounded-lg border border-line bg-surface px-3 py-2.5">
        <figcaption className="mb-1 text-[13px] font-semibold">{header}</figcaption>
        {detail}
        <p className="text-xs text-muted">No data returned.</p>
      </figure>
    );
  }
  return (
    <TimelineChart
      title={header}
      extra={detail}
      xs={xs}
      series={series}
      leftFmt={metricValue}
      height={160}
      synced={false}
      bands={bands}
    />
  );
}

const count = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0));
const perSec = (v: number) => `${count(v)}/s`;
const percent = (v: number) => `${(v * 100).toFixed(1)}%`;

/** The report's three timeline charts, sharing a synced cursor. */
export function ReportCharts({
  timeline,
  mode,
  bands = [],
}: {
  timeline: Point[];
  mode: string;
  bands?: TimeBand[];
}) {
  const { xs, load, latency, errors } = useMemo(
    () => buildSeries(timeline, mode),
    [timeline, mode],
  );
  return (
    <div className="flex flex-col gap-3">
      <TimelineChart
        title="Throughput and users"
        xs={xs}
        series={load}
        leftFmt={perSec}
        rightFmt={count}
        bands={bands}
      />
      <TimelineChart
        title="Latency (from scheduled send)"
        xs={xs}
        series={latency}
        leftFmt={axisMs}
        bands={bands}
      />
      <TimelineChart
        title="Error rate"
        xs={xs}
        series={errors}
        leftFmt={axisPct}
        height={160}
        bands={bands}
      />
    </div>
  );
}

function buildSeries(timeline: Point[], mode: string) {
  // Same conventions as the HTML report: x = t + 1; latency and errors are
  // blank for intervals without requests.
  const xs = timeline.map((p) => p.t + 1);
  const empty = (p: Point) => p.rps === 0;
  const load: S[] = [
    { name: 'requests/s', color: '--s1', data: timeline.map((p) => p.rps), fmt: perSec },
    ...(mode === 'rate'
      ? [
          {
            name: 'planned iterations/s',
            color: '--s4',
            dashed: true,
            data: timeline.map((p) => p.planned),
            fmt: perSec,
          },
        ]
      : []),
    {
      name: 'active VUs',
      color: '--s2',
      right: true,
      data: timeline.map((p) => p.vus),
      fmt: count,
    },
  ];
  const latency: S[] = [
    { name: 'p50', color: '--s3', data: timeline.map((p) => (empty(p) ? null : p.p50)), fmt: ms },
    { name: 'p95', color: '--s1', data: timeline.map((p) => (empty(p) ? null : p.p95)), fmt: ms },
    { name: 'p99', color: '--s5', data: timeline.map((p) => (empty(p) ? null : p.p99)), fmt: ms },
  ];
  const errors: S[] = [
    {
      name: 'error rate',
      color: '--s5',
      data: timeline.map((p) => (empty(p) ? null : p.errorRate)),
      fmt: percent,
    },
  ];
  return { xs, load, latency, errors };
}

/**
 * Completed iterations per second and p95 latency at each load level, with
 * the knee marked where adding load stopped adding throughput.
 */
export function CurveChart({
  curve,
  knee,
  unit,
}: {
  curve: CurvePoint[];
  knee?: Knee;
  unit: string;
}) {
  const el = useRef<HTMLDivElement>(null);
  const dark = useIsDark();
  useEffect(() => {
    if (!el.current) return;
    const chart = echarts.init(el.current, undefined, { renderer: 'canvas' });
    const muted = cssVar('--muted');
    const line = cssVar('--line');
    const fg = cssVar('--fg');
    const surface = cssVar('--surface');
    const mono = 'ui-monospace, Menlo, monospace';
    const level = (v: number) => `${num(Math.round(v))}${unit}`;
    const marks =
      knee?.found && knee.next
        ? [
            // Each label sits on the outer side of its line, so the two never
            // overlap however close the levels are.
            {
              xAxis: knee.at.offered,
              name: 'scaled',
              label: { align: 'right', padding: [0, 4, 0, 0] },
            },
            {
              xAxis: knee.next.offered,
              name: 'knee',
              label: { align: 'left', padding: [0, 0, 0, 4] },
            },
          ]
        : [];
    chart.setOption({
      animation: false,
      textStyle: { fontFamily: mono },
      grid: { left: 64, right: 56, top: 34, bottom: 28 },
      legend: {
        top: 0,
        left: 0,
        icon: 'roundRect',
        itemWidth: 12,
        itemHeight: 3,
        textStyle: { color: muted, fontSize: 11 },
      },
      tooltip: {
        trigger: 'axis',
        backgroundColor: surface,
        borderColor: line,
        textStyle: { color: fg, fontSize: 12, fontFamily: mono },
        formatter: (
          params: { axisValue: number; dataIndex: number; marker: string; seriesName: string }[],
        ) => {
          const c = curve[params[0]?.dataIndex ?? 0];
          if (!c) return '';
          return [
            `offered ${level(c.offered)}`,
            `${params[0]?.marker ?? ''}completed: <b>${c.throughput.toFixed(1)} it/s</b>`,
            `${params[1]?.marker ?? ''}p95: <b>${ms(c.p95)}</b>`,
            `errors ${percent(c.errorRate)} · ${c.seconds}s at this level`,
          ].join('<br/>');
        },
      },
      xAxis: {
        type: 'value',
        min: curve[0]?.offered ?? 0,
        max: curve[curve.length - 1]?.offered ?? 1,
        axisLine: { lineStyle: { color: line } },
        splitLine: { show: false },
        axisLabel: { color: muted, fontSize: 11, formatter: level },
      },
      yAxis: [
        {
          type: 'value',
          min: 0,
          splitLine: { lineStyle: { color: line } },
          axisLabel: { color: muted, fontSize: 11, formatter: perSec },
        },
        {
          type: 'value',
          min: 0,
          splitLine: { show: false },
          axisLabel: { color: muted, fontSize: 11, formatter: axisMs },
        },
      ],
      series: [
        {
          name: 'completed iterations/s',
          type: 'line',
          data: curve.map((c) => [c.offered, c.throughput]),
          symbolSize: 5,
          lineStyle: { width: 1.8, color: cssVar('--s1') },
          itemStyle: { color: cssVar('--s1') },
          ...(marks.length
            ? {
                markLine: {
                  silent: true,
                  symbol: 'none',
                  lineStyle: { color: cssVar('--warn'), type: 'dashed' },
                  label: { color: muted, fontSize: 10, formatter: '{b}' },
                  data: marks,
                },
              }
            : {}),
        },
        {
          name: 'p95 latency',
          type: 'line',
          yAxisIndex: 1,
          data: curve.map((c) => [c.offered, c.p95]),
          symbolSize: 5,
          lineStyle: { width: 1.8, color: cssVar('--s5') },
          itemStyle: { color: cssVar('--s5') },
        },
      ],
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el.current);
    return () => {
      ro.disconnect();
      chart.dispose();
    };
  }, [curve, knee, unit, dark]);
  return (
    <div ref={el} style={{ height: 240 }} role="img" aria-label="Throughput against load chart" />
  );
}
