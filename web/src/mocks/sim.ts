/**
 * Mock-only simulation: derives a plan from scenario YAML and synthesises
 * believable timelines and reports, so the UI can be demoed without a server.
 */
import { parse } from 'yaml';
import type {
  Check,
  PlanSummary,
  Point,
  Report,
  ReportJourney,
  RunSummary,
  SlowRequest,
  Stats,
  TargetMetric,
  Validation,
  Verdict,
} from '@/api/types';
import { ms, pct } from '@/lib/format';

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);

export function parseDuration(v: unknown): number | null {
  if (typeof v === 'number') return v;
  if (typeof v !== 'string') return null;
  const units: Record<string, number> = { ms: 0.001, s: 1, m: 60, h: 3600, d: 86400 };
  let total = 0;
  let matched = false;
  for (const m of v.matchAll(/([0-9.]+)(ms|s|m|h|d)?/g)) {
    matched = true;
    total += Number(m[1]) * (units[m[2] ?? 's'] ?? 1);
  }
  return matched ? total : null;
}

export function parseRate(v: unknown): number | null {
  if (typeof v === 'number') return v;
  if (typeof v !== 'string') return null;
  const m = /^([0-9.]+)(?:\/(s|sec|second|m|min|minute|h|hour))?$/.exec(v.trim());
  if (!m) return null;
  const per = m[2]?.startsWith('h') ? 3600 : m[2]?.startsWith('m') ? 60 : 1;
  return Number(m[1]) / per;
}

export interface StepRef {
  journey: string;
  name: string;
  weight: number;
}

export interface ParsedScenario {
  validation: Validation;
  name?: string;
  description?: string;
  tags: string[];
  steps: StepRef[];
  journeys: { name: string; weight: number }[];
}

const methods = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options'];

function collectSteps(steps: unknown, journey: string, out: StepRef[], weight: number) {
  if (!Array.isArray(steps)) return;
  for (const s of steps) {
    if (!isObj(s)) continue;
    const m = methods.find((k) => typeof s[k] === 'string');
    if (m) {
      const name = typeof s.name === 'string' ? s.name : `${m.toUpperCase()} ${String(s[m])}`;
      out.push({ journey, name, weight });
    }
    if (Array.isArray(s.branch)) {
      for (const arm of s.branch)
        if (isObj(arm)) collectSteps(arm.steps, journey, out, weight * 0.5);
    }
    if (Array.isArray(s.steps))
      collectSteps(s.steps, journey, out, weight * (typeof s.loop === 'number' ? s.loop : 1));
  }
}

/** A rough stand-in for the server's validator. */
export function analyse(yamlText: string): ParsedScenario {
  const problems: string[] = [];
  let doc: unknown;
  try {
    doc = parse(yamlText);
  } catch (err) {
    const msg = err instanceof Error ? err.message.split('\n')[0]! : String(err);
    return {
      validation: { valid: false, problems: [`yaml: ${msg}`] },
      tags: [],
      steps: [],
      journeys: [],
    };
  }
  if (!isObj(doc)) {
    return {
      validation: { valid: false, problems: ['scenario must be a mapping'] },
      tags: [],
      steps: [],
      journeys: [],
    };
  }
  const meta = isObj(doc.metadata) ? doc.metadata : {};
  const name = typeof meta.name === 'string' ? meta.name : undefined;
  if (!name) problems.push('metadata.name: required');
  else if (!/^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$/.test(name))
    problems.push(`metadata.name: "${name}" must be lowercase letters, digits, '.', '_' or '-'`);
  const journeysRaw = Array.isArray(doc.journeys) ? doc.journeys.filter(isObj) : [];
  if (journeysRaw.length === 0) problems.push('journeys: at least one journey is required');
  const steps: StepRef[] = [];
  const journeys: { name: string; weight: number }[] = [];
  journeysRaw.forEach((j, i) => {
    const jn = typeof j.name === 'string' ? j.name : `journey ${i + 1}`;
    if (typeof j.name !== 'string') problems.push(`journeys[${i}].name: required`);
    const w = typeof j.weight === 'number' ? j.weight : 1;
    journeys.push({ name: jn, weight: w });
    if (!Array.isArray(j.steps) || j.steps.length === 0)
      problems.push(`journeys[${i}] (${jn}): steps: at least one step is required`);
    collectSteps(j.steps, jn, steps, w);
  });
  const load = isObj(doc.load) ? doc.load : null;
  if (!load) problems.push('load: required');
  const targets = Array.isArray(doc.targets) ? doc.targets : [];
  targets.forEach((t, i) => {
    if (typeof t !== 'string' || !/^\s*\S+\s*(<=|>=|==|<|>)\s*\S+\s*$/.test(t))
      problems.push(`targets[${i}]: expected "metric < value", got ${JSON.stringify(t)}`);
  });

  let plan: PlanSummary | undefined;
  if (load) {
    const shape = typeof load.shape === 'string' ? load.shape : undefined;
    const mode =
      load.mode === 'vus' || load.mode === 'rate' ? load.mode : load.rate != null ? 'rate' : 'vus';
    const level = (v: unknown) =>
      mode === 'rate' ? parseRate(v) : typeof v === 'number' ? v : Number(v);
    let peak = level(load.max ?? (mode === 'rate' ? load.rate : load.vus)) ?? 1;
    if (!Number.isFinite(peak)) peak = 1;
    let duration = parseDuration(load.duration) ?? 0;
    if ((shape === 'breakpoint' || shape === 'steps') && typeof load.steps === 'number') {
      duration = load.steps * (parseDuration(load.stepDuration) ?? 30);
    }
    if (Array.isArray(load.stages)) {
      duration = load.stages.reduce(
        (s: number, st) => s + (isObj(st) ? (parseDuration(st.duration) ?? 0) : 0),
        0,
      );
      peak = Math.max(...load.stages.map((st) => (isObj(st) ? (level(st.target) ?? 0) : 0)), 0);
    }
    if (!duration && !load.iterations)
      problems.push('load.duration: required unless iterations or stages are set');
    if (mode === 'rate' && load.rate == null && load.max == null && !load.stages)
      problems.push('load.rate: required in rate mode');
    const executor = load.iterations
      ? 'shared-iterations'
      : shape || load.stages
        ? mode === 'rate'
          ? 'ramping-arrival-rate'
          : 'ramping-vus'
        : mode === 'rate'
          ? 'constant-arrival-rate'
          : 'constant-vus';
    plan = {
      executor,
      mode,
      ...(shape ? { shape } : {}),
      peak: Math.round(peak * 100) / 100,
      durationSeconds: duration,
      journeys: journeys.length,
      steps: steps.length,
    };
  }
  return {
    validation: { valid: problems.length === 0, problems, ...(plan ? { plan } : {}) },
    name,
    description: typeof meta.description === 'string' ? meta.description : undefined,
    tags: Array.isArray(meta.tags)
      ? meta.tags.filter((t): t is string => typeof t === 'string')
      : [],
    steps,
    journeys,
  };
}

// ---------------------------------------------------------------- timeline

/** Deterministic PRNG (mulberry32). */
export function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Planned load at second t for a shape, between `start` and `peak`. */
export function plannedAt(shape: string | undefined, t: number, dur: number, peak: number): number {
  const f = dur > 0 ? Math.min(1, t / dur) : 1;
  const start = peak * 0.1;
  switch (shape) {
    case 'stress':
      return start + (peak - start) * Math.min(1, f * 1.15);
    case 'spike':
      return f > 0.4 && f < 0.6 ? peak : peak * 0.15;
    case 'breakpoint':
    case 'steps':
      return start + (peak - start) * (Math.floor(f * 10) / 9);
    case 'recovery':
      return f < 0.3 ? peak * 0.3 : f < 0.6 ? peak : peak * 0.3;
    case 'wave':
      return peak * (0.55 + 0.45 * Math.sin(f * Math.PI * 6 - Math.PI / 2));
    case 'smoke':
      return Math.min(peak, 2);
    default:
      return peak * Math.min(1, f / 0.08);
  }
}

export interface SimParams {
  seed: number;
  mode: string;
  shape?: string;
  peak: number;
  duration: number;
  /** Throughput (rate) at which the target saturates. */
  capacity: number;
  baseLatency: number;
  baseErrors: number;
}

export function simulatePoint(p: SimParams, t: number): Point {
  const r = rng(p.seed * 100_003 + t);
  const noise = () => 1 + (r() - 0.5) * 0.12;
  const planned = plannedAt(p.shape, t, p.duration, p.peak);
  // Convert VUs to an arrival rate assuming ~2s per iteration.
  const offered = p.mode === 'rate' ? planned : planned / 2;
  const u = offered / p.capacity;
  const slow = 1 + 2.5 * u ** 4 + (u > 1 ? (u - 1) * 12 : 0);
  const p50 = p.baseLatency * slow * noise();
  const p95 = p50 * (2.3 + r() * 0.5) * (u > 0.9 ? 1.4 : 1);
  const p99 = p95 * (1.5 + r() * 0.4);
  const served = Math.min(offered, p.capacity * 1.05) * noise();
  const errorRate = Math.min(0.6, p.baseErrors * noise() + (u > 1 ? (u - 1) * 0.25 : 0));
  const vus = Math.max(1, Math.round(p.mode === 'rate' ? served * (p50 + 1.8) : planned));
  return {
    t,
    rps: Math.round(served * 10) / 10,
    errorRate: Math.round(errorRate * 1e5) / 1e5,
    p50: round6(p50),
    p95: round6(p95),
    p99: round6(p99),
    vus,
    planned: Math.round(planned * 10) / 10,
    dropped: u > 1.15 ? Math.round((u - 1.15) * 20 * r()) : 0,
    iterations: Math.round(served / 2.4),
    schedLagP99: round6(0.0004 + r() * 0.0006),
  };
}

const round6 = (v: number) => Math.round(v * 1e6) / 1e6;

export function simulateTimeline(p: SimParams, upTo = p.duration): Point[] {
  const out: Point[] = [];
  for (let t = 0; t < Math.min(upTo, p.duration); t++) out.push(simulatePoint(p, t));
  return out;
}

// ---------------------------------------------------------------- report

function percentiles(base: number, count: number) {
  return {
    count,
    min: base * 0.25,
    mean: base * 1.15,
    p50: base,
    p90: base * 2.0,
    p95: base * 2.5,
    p99: base * 3.9,
    p999: base * 6.2,
    max: base * 9.5,
  };
}

function statsFor(requests: number, errorRate: number, p50: number, duration: number): Stats {
  const failed = Math.round(requests * errorRate);
  return {
    requests,
    failed,
    errorRate: requests ? failed / requests : 0,
    rps: duration ? requests / duration : 0,
    latency: percentiles(p50, requests),
    service: percentiles(p50 * 0.97, requests),
    bytesIn: requests * 4180,
    bytesOut: requests * 610,
    checksPassed: requests - failed,
    checksFailed: failed,
    status: { 200: requests - failed, ...(failed ? { 503: failed } : {}) },
  };
}

function evaluate(source: string, overall: Stats): Check {
  const m = /^\s*(\S+)\s*(<=|>=|==|<|>)\s*(\S+)\s*$/.exec(source);
  const metric = m?.[1] ?? source;
  const op = m?.[2] ?? '<';
  const raw = m?.[3] ?? '0';
  const isErr = metric.startsWith('errors') || metric.endsWith('.errors');
  const target = isErr
    ? raw.endsWith('%')
      ? Number(raw.slice(0, -1)) / 100
      : Number(raw)
    : (parseDuration(raw) ?? 0);
  const scope = metric.includes('.') ? metric.split('.')[0]! : 'overall';
  const which = metric.split('.').pop() ?? 'p95';
  const lat = overall.latency as unknown as Record<string, number>;
  let observed = isErr ? overall.errorRate : (lat[which] ?? overall.latency.p95);
  if (scope !== 'http' && scope !== 'overall' && !isErr) observed *= 1.35;
  const pass =
    op === '<'
      ? observed < target
      : op === '<='
        ? observed <= target
        : op === '>'
          ? observed > target
          : op === '>='
            ? observed >= target
            : observed === target;
  return {
    source,
    scope: scope === 'http' ? 'overall' : scope,
    metric: isErr ? 'errors' : which,
    op,
    target,
    observed,
    pass,
    targetText: isErr ? pct(target) : ms(target),
    observedText: isErr ? pct(observed) : ms(observed),
  };
}

export function buildReport(args: {
  runId: string;
  scenario: ParsedScenario;
  targetURL: string;
  targets: string[];
  timeline: Point[];
  started: string;
  stopReason: string;
  workers: number;
  breakpoint?: boolean;
}): Report {
  const { timeline: tl, scenario } = args;
  const duration = tl.length;
  const requests = Math.round(tl.reduce((s, p) => s + p.rps, 0));
  const failed = tl.reduce((s, p) => s + p.rps * p.errorRate, 0);
  const errorRate = requests ? failed / requests : 0;
  const busy = tl.filter((p) => p.rps > 0);
  const p50 = busy.length ? busy.reduce((s, p) => s + p.p50 * p.rps, 0) / Math.max(1, requests) : 0;
  const overall = statsFor(requests, errorRate, p50, duration);
  const p95s = busy.map((p) => p.p95).sort((a, b) => a - b);
  const p99s = busy.map((p) => p.p99).sort((a, b) => a - b);
  overall.latency.p95 = p95s[Math.floor(p95s.length * 0.9)] ?? overall.latency.p95;
  overall.latency.p99 = p99s[Math.floor(p99s.length * 0.95)] ?? overall.latency.p99;
  overall.service.p99 = overall.latency.p99 * 0.93;
  const dropped = tl.reduce((s, p) => s + p.dropped, 0);
  overall.iterations = tl.reduce((s, p) => s + (p.iterations ?? 0), 0);
  overall.iterationsFailed = Math.round((overall.iterations ?? 0) * errorRate);
  overall.dropped = dropped;
  overall.iterationTime = percentiles(p50 * 6 + 2, overall.iterations ?? 0);

  const totalW = scenario.steps.reduce((s, x) => s + x.weight, 0) || 1;
  const journeys: ReportJourney[] = scenario.journeys.map((j, ji) => {
    let id = ji * 100;
    const steps = scenario.steps
      .filter((s) => s.journey === j.name)
      .map((s, si) => {
        const n = Math.round((requests * s.weight) / totalW);
        const factor = 0.7 + ((si * 37 + ji * 11) % 9) / 10;
        const st = statsFor(n, errorRate * factor, p50 * factor, duration);
        return {
          id: id++,
          name: s.name,
          stats: st,
          phases: {
            dns: { mean: 0.00003, p95: 0.0002 },
            connect: { mean: 0.0004 * factor, p95: 0.0021 * factor },
            tls: {
              mean: args.targetURL.startsWith('https') ? 0.0011 : 0,
              p95: args.targetURL.startsWith('https') ? 0.0062 : 0,
            },
            wait: { mean: p50 * factor * 0.92, p95: p50 * factor * 2.3 },
            download: { mean: 0.0006 * factor, p95: 0.0024 * factor },
          },
          slowest: slowestFor(id, n, st.latency.max, args.started, duration, errorRate * factor),
        };
      });
    const jr = steps.reduce((s, x) => s + x.stats.requests, 0);
    const jstats = statsFor(jr, errorRate, p50, duration);
    const share = j.weight / (scenario.journeys.reduce((s, x) => s + x.weight, 0) || 1);
    jstats.iterations = Math.round((overall.iterations ?? 0) * share);
    jstats.iterationsFailed = Math.round(jstats.iterations * errorRate);
    jstats.iterationTime = percentiles(p50 * steps.length + 1.5, jstats.iterations);
    return { name: j.name, stats: jstats, steps };
  });

  const errors =
    failed > 0
      ? scenario.steps.slice(0, 3).map((s, i) => ({
          journey: s.journey,
          step: s.name,
          error: [
            'status 503: Service Unavailable',
            'timeout after 30s',
            'check failed: status 200 expected, got 500',
          ][i]!,
          count: Math.max(1, Math.round(failed * [0.62, 0.27, 0.11][i]!)),
        }))
      : [];

  const thresholds = args.targets.map((t) => evaluate(t, overall));
  let verdict: Verdict = thresholds.length
    ? thresholds.every((c) => c.pass)
      ? 'pass'
      : 'fail'
    : 'no-targets';
  if (dropped > requests * 0.02) verdict = 'generator-limited';

  const notes: string[] = [];
  if (args.stopReason !== 'completed') notes.push(`Run ended early: ${args.stopReason}.`);
  if (dropped > 0)
    notes.push(
      'Some iterations were dropped because no virtual user was free. Raise maxVUs or reduce the rate; dropped iterations show the generator could not keep the schedule.',
    );

  const peakPlanned = Math.max(...tl.map((p) => p.planned), 0);
  const report: Report = {
    formatVersion: 1,
    stampede: 'v0.9.0-mock',
    runId: args.runId,
    scenario: scenario.name ?? 'scenario',
    target: args.targetURL,
    started: args.started,
    ended: new Date(Date.parse(args.started) + duration * 1000).toISOString(),
    duration,
    stopReason: args.stopReason,
    load: {
      ...(scenario.validation.plan?.shape ? { shape: scenario.validation.plan.shape } : {}),
      mode: scenario.validation.plan?.mode ?? 'rate',
      executor: scenario.validation.plan?.executor ?? 'constant-arrival-rate',
      peak: scenario.validation.plan?.peak ?? peakPlanned,
      peakVUs: Math.max(...tl.map((p) => p.vus), 0),
      workers: args.workers,
    },
    verdict,
    thresholds,
    overall,
    journeys,
    errors,
    timeline: tl,
    ...(notes.length ? { notes } : {}),
    targetMetrics: targetMetricsFor(tl),
  };
  if (args.breakpoint) {
    const failing = tl.find((p) => p.p95 > 0.25 || p.errorRate > 0.01);
    const unit = report.load.mode === 'rate' ? '/s' : ' VUs';
    report.breakpoint = failing
      ? {
          found: true,
          lastPass: Math.round(
            Math.max(...tl.filter((p) => p.t < failing.t).map((p) => p.planned), 0),
          ),
          firstFail: Math.round(failing.planned),
          unit,
          failedOn: ['http.p95 < 250ms'],
        }
      : { found: false, lastPass: Math.round(peakPlanned), unit };
  }
  return report;
}

/** A deterministic 32-hex-digit trace ID. */
function traceId(seed: number): string {
  let x = (seed * 2654435761) >>> 0;
  let out = '';
  for (let i = 0; i < 4; i++) {
    x = (x * 1664525 + 1013904223) >>> 0;
    out += x.toString(16).padStart(8, '0');
  }
  return out;
}

/** The five slowest requests of a step, as the server keeps them. */
function slowestFor(
  stepId: number,
  requests: number,
  max: number,
  started: string,
  duration: number,
  errorRate: number,
): SlowRequest[] {
  if (requests === 0 || duration === 0) return [];
  return Array.from({ length: Math.min(5, requests) }, (_, i) => {
    const t = ((stepId * 7 + i * 13) % Math.max(1, duration)) + ((i * 37) % 10) / 10;
    const id = traceId(stepId * 10 + i + 1);
    const failedOne = i === 0 && errorRate > 0.005;
    return {
      latency: max * (1 - i * 0.08),
      at: new Date(Date.parse(started) + t * 1000).toISOString(),
      t,
      traceId: id,
      traceUrl: `https://jaeger.example.com/trace/${id}`,
      ...(failedOne ? { error: 'timeout after 30s' } : { status: 200 }),
    };
  });
}

/** The target's own metrics, as observe.prometheus would chart them. */
function targetMetricsFor(tl: Point[]): TargetMetric[] {
  if (tl.length === 0) return [];
  const peak = Math.max(...tl.map((p) => p.rps), 1);
  return [
    {
      name: 'cpu',
      query: 'rate(process_cpu_seconds_total{job="shop"}[30s])',
      points: tl.map((p) => ({ t: p.t, value: 0.08 + (0.85 * p.rps) / peak })),
    },
    {
      name: 'memory',
      query: 'process_resident_memory_bytes{job="shop"}',
      points: tl.map((p, i) => ({ t: p.t, value: 118e6 + i * 0.35e6 + (p.rps / peak) * 40e6 })),
    },
    {
      name: 'db_connections',
      query: 'pg_stat_activity_count{datname="shop"}',
      points: tl.map((p) => ({ t: p.t, value: Math.round(4 + (p.rps / peak) * 46) })),
      error:
        'the query returned 2 series; the first is shown. Aggregate it to one series, for example sum(...) or max(...)',
    },
  ];
}

export function summaryOf(r: Report): RunSummary {
  return {
    requests: r.overall.requests,
    errorRate: r.overall.errorRate,
    rps: r.overall.rps,
    p95: r.overall.latency.p95,
    p99: r.overall.latency.p99,
  };
}
