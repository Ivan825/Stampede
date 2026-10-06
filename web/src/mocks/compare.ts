/**
 * A port of internal/report/compare.go for the mock API, so the compare
 * view shows the same statistics the server computes: means, the relative
 * change, a bootstrap 95% interval, the noise floor and the verdicts.
 */
import type {
  Comparison,
  CompareVerdict,
  MetricDelta,
  Report,
  Stats,
  StepDelta,
} from '@/api/types';
import { compareValue, signedPct } from '@/lib/format';
import { rng } from './sim';

const minNoise = 0.02;
const minLatencyChange = 0.001;

const metricDefs: { name: string; higher: boolean; get: (r: Report) => number | null }[] = [
  {
    name: 'p50',
    higher: false,
    get: (r) => (r.overall.requests > 0 ? r.overall.latency.p50 : null),
  },
  {
    name: 'p95',
    higher: false,
    get: (r) => (r.overall.requests > 0 ? r.overall.latency.p95 : null),
  },
  {
    name: 'p99',
    higher: false,
    get: (r) => (r.overall.requests > 0 ? r.overall.latency.p99 : null),
  },
  {
    name: 'error rate',
    higher: false,
    get: (r) => (r.overall.requests > 0 ? r.overall.errorRate : null),
  },
  { name: 'throughput', higher: true, get: (r) => (r.overall.requests > 0 ? r.overall.rps : null) },
  { name: 'max sustainable load', higher: true, get: (r) => r.breakpoint?.lastPass ?? null },
];

const mean = (v: number[]) => v.reduce((s, x) => s + x, 0) / v.length;

function spread(v: number[]): number {
  const m = mean(v);
  if (v.length < 2 || m === 0) return 0;
  return Math.max(...v.map((x) => Math.abs(x - m) / Math.abs(m)));
}

function relChange(a: number, b: number): number {
  if (a === 0) return b === 0 ? 0 : Infinity;
  return (b - a) / Math.abs(a);
}

function bootstrapCI(rand: () => number, a: number[], b: number[], n = 2000): [number, number] {
  if (a.length < 2 || b.length < 2) {
    const c = relChange(mean(a), mean(b));
    return [c, c];
  }
  const draws: number[] = [];
  const pick = (v: number[]) => v.map(() => v[Math.floor(rand() * v.length)]!);
  for (let i = 0; i < n; i++) {
    const c = relChange(mean(pick(a)), mean(pick(b)));
    if (Number.isFinite(c)) draws.push(c);
  }
  if (draws.length === 0) return [0, 0];
  draws.sort((x, y) => x - y);
  const lo = draws[Math.floor(0.025 * draws.length)]!;
  const hi = draws[Math.min(draws.length - 1, Math.ceil(0.975 * draws.length) - 1)]!;
  return [lo, hi];
}

function judge(d: MetricDelta, change: number, repeated: boolean): CompareVerdict {
  if (d.meanA === 0 && d.meanB === 0) return 'no-change';
  const worse = d.higherIsBetter ? change < 0 : change > 0;
  let big = Math.abs(change) > d.noiseFloor;
  if (['p50', 'p95', 'p99'].includes(d.name) && Math.abs(d.meanB - d.meanA) < minLatencyChange)
    big = false;
  const excludesZero = (d.ciLow ?? Infinity) > 0 || (d.ciHigh ?? Infinity) < 0;
  if (!big) return 'no-change';
  if (!repeated || !excludesZero) return 'inconclusive';
  return worse ? 'regression' : 'improvement';
}

const finite = (f: number) => (Number.isFinite(f) ? f : null);

function delta(rand: () => number, name: string, higher: boolean, a: number[], b: number[]) {
  const change = relChange(mean(a), mean(b));
  const [lo, hi] = bootstrapCI(rand, a, b);
  const d: MetricDelta = {
    name,
    higherIsBetter: higher,
    a,
    b,
    meanA: mean(a),
    meanB: mean(b),
    change: finite(change),
    ciLow: finite(lo),
    ciHigh: finite(hi),
    noiseFloor: Math.max(minNoise, spread(a), spread(b)),
    verdict: 'no-change',
  };
  d.verdict = judge(d, change, a.length >= 2 && b.length >= 2);
  return d;
}

function problemsOf(rs: Report[]): string[] {
  const out = new Set<string>();
  const first = rs[0]!;
  for (const r of rs.slice(1)) {
    if (r.scenario !== first.scenario)
      out.add(`different scenarios (${first.scenario} and ${r.scenario})`);
    if (
      r.load.executor !== first.load.executor ||
      r.load.mode !== first.load.mode ||
      Math.abs(r.load.peak - first.load.peak) > 0.01 * Math.max(r.load.peak, first.load.peak)
    )
      out.add('different load plans');
    if (Math.abs(r.duration - first.duration) > 0.1 * first.duration + 1 && !r.breakpoint)
      out.add('different durations');
    if (r.load.workers !== first.load.workers)
      out.add(`different worker counts (${first.load.workers} and ${r.load.workers})`);
    if (r.verdict === 'generator-limited' || first.verdict === 'generator-limited')
      out.add('a run was generator-limited');
  }
  return [...out];
}

function steps(rand: () => number, a: Report[], b: Report[]): StepDelta[] {
  const order: string[] = [];
  const collect = (rs: Report[]) => {
    const m = new Map<string, Stats[]>();
    for (const r of rs)
      for (const j of r.journeys ?? [])
        for (const s of j.steps ?? []) {
          if (s.stats.requests === 0) continue;
          const k = `${j.name}\u0000${s.name}`;
          if (!order.includes(k)) order.push(k);
          m.set(k, [...(m.get(k) ?? []), s.stats]);
        }
    return m;
  };
  const sa = collect(a);
  const sb = collect(b);
  const out: StepDelta[] = [];
  for (const k of order) {
    const xa = sa.get(k);
    const xb = sb.get(k);
    if (!xa || !xb) continue;
    const [journey, step] = k.split('\u0000') as [string, string];
    out.push({
      journey,
      step,
      metrics: [
        delta(
          rand,
          'p95',
          false,
          xa.map((s) => s.latency.p95),
          xb.map((s) => s.latency.p95),
        ),
        delta(
          rand,
          'error rate',
          false,
          xa.map((s) => s.errorRate),
          xb.map((s) => s.errorRate),
        ),
      ],
    });
  }
  return out;
}

function markdown(c: Omit<Comparison, 'markdown'>): string {
  const icon = { regression: '❌', improvement: '✅', 'no-change': '➖', inconclusive: '⚠️' }[
    c.verdict
  ];
  let s = `### ${icon} Stampede comparison: ${c.verdict}\n\n${c.a.label} (${c.a.runs.length} runs) → ${c.b.label} (${c.b.runs.length} runs)\n\n`;
  s += `| Metric | ${c.a.label} | ${c.b.label} | Change | 95% interval | Noise | Verdict |\n|---|---:|---:|---:|---:|---:|---|\n`;
  for (const m of c.metrics)
    s += `| ${m.name} | ${compareValue(m.name, m.meanA)} | ${compareValue(m.name, m.meanB)} | ${signedPct(m.change)} | ${signedPct(m.ciLow)} … ${signedPct(m.ciHigh)} | ±${(m.noiseFloor * 100).toFixed(0)}% | ${m.verdict} |\n`;
  for (const p of c.problems) s += `\n> Not comparable: ${p}`;
  return `${s}\n`;
}

export function compareReports(
  a: Report[],
  b: Report[],
  labelA: string,
  labelB: string,
): Comparison {
  const rand = rng(12);
  const problems = problemsOf([...a, ...b]);
  const metrics: MetricDelta[] = [];
  let worst: CompareVerdict = 'no-change';
  let anyImprove = false;
  for (const m of metricDefs) {
    const va = a.map(m.get).filter((x): x is number => x != null);
    const vb = b.map(m.get).filter((x): x is number => x != null);
    if (!va.length || !vb.length) continue;
    const d = delta(rand, m.name, m.higher, va, vb);
    if (d.verdict === 'regression') worst = 'regression';
    if (d.verdict === 'improvement') anyImprove = true;
    if (d.verdict === 'inconclusive' && worst === 'no-change') worst = 'inconclusive';
    metrics.push(d);
  }
  let verdict: CompareVerdict = worst;
  if (worst === 'no-change' && anyImprove) verdict = 'improvement';
  const comparable = problems.length === 0;
  if (!comparable && verdict !== 'no-change') verdict = 'inconclusive';
  const c = {
    a: { label: labelA, runs: a.map((r) => r.runId ?? r.started) },
    b: { label: labelB, runs: b.map((r) => r.runId ?? r.started) },
    comparable,
    problems,
    metrics,
    steps: steps(rand, a, b),
    verdict,
    confidence: 0.95,
  };
  return { ...c, markdown: markdown(c) };
}
