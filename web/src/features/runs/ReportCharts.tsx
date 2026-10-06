import { LineChart } from 'echarts/charts';
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';
import { useEffect, useMemo, useRef } from 'react';
import type { Point } from '@/api/types';
import { cssVar, useIsDark } from '@/components/misc';
import { ms, secs } from '@/lib/format';

echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
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

const group = 'stampede-report';

function TimelineChart({
  title,
  xs,
  series,
  leftFmt,
  rightFmt,
  height = 220,
}: {
  title: string;
  xs: number[];
  series: S[];
  leftFmt: (v: number) => string;
  rightFmt?: (v: number) => string;
  height?: number;
}) {
  const el = useRef<HTMLDivElement>(null);
  const dark = useIsDark();

  useEffect(() => {
    if (!el.current) return;
    const chart = echarts.init(el.current, undefined, { renderer: 'canvas' });
    chart.group = group;
    echarts.connect(group);
    const muted = cssVar('--muted');
    const line = cssVar('--line');
    const fg = cssVar('--fg');
    const surface = cssVar('--surface');
    const mono = 'ui-monospace, Menlo, monospace';
    const hasRight = series.some((s) => s.right);
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
        valueFormatter: undefined,
        formatter: (
          params: {
            seriesIndex: number;
            axisValue: number;
            value: number | null;
            marker: string;
            seriesName: string;
          }[],
        ) => {
          const t = params[0]?.axisValue ?? 0;
          const rows = params
            .map(
              (p) =>
                `${p.marker}${p.seriesName}: <b>${p.value == null ? '–' : series[p.seriesIndex]!.fmt(p.value)}</b>`,
            )
            .join('<br/>');
          return `t = ${secs(t)}<br/>${rows}`;
        },
      },
      xAxis: {
        type: 'category',
        data: xs,
        boundaryGap: false,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: muted, fontSize: 11, formatter: (v: string) => secs(Number(v)) },
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
      series: series.map((s) => ({
        name: s.name,
        type: 'line',
        data: s.data,
        yAxisIndex: s.right ? 1 : 0,
        showSymbol: false,
        connectNulls: false,
        lineStyle: { width: 1.8, color: cssVar(s.color), type: s.dashed ? 'dashed' : 'solid' },
        itemStyle: { color: cssVar(s.color) },
      })),
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el.current);
    return () => {
      ro.disconnect();
      chart.dispose();
    };
  }, [xs, series, dark, leftFmt, rightFmt]);

  return (
    <figure className="rounded-lg border border-line bg-surface px-3 pt-2.5 pb-1">
      <figcaption className="mb-1 text-[13px] font-semibold">{title}</figcaption>
      <div ref={el} style={{ height }} role="img" aria-label={`${title} chart`} />
    </figure>
  );
}

const count = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0));
const perSec = (v: number) => `${count(v)}/s`;
const percent = (v: number) => `${(v * 100).toFixed(1)}%`;

/** The report's three timeline charts, sharing a synced cursor. */
export function ReportCharts({ timeline, mode }: { timeline: Point[]; mode: string }) {
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
      />
      <TimelineChart title="Latency (from scheduled send)" xs={xs} series={latency} leftFmt={ms} />
      <TimelineChart title="Error rate" xs={xs} series={errors} leftFmt={percent} height={160} />
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
