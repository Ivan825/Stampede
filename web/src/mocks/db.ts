/**
 * In-memory state for the mock server. Every object is typed with the
 * OpenAPI-generated types so fixtures cannot drift from the API.
 */
import type {
  AuditEntry,
  Me,
  Project,
  Report,
  Role,
  Run,
  RunEvent,
  RunStatus,
  Scenario,
  ScenarioVersion,
  Secret,
  Target,
  Token,
  User,
  Worker,
} from '@/api/types';
import { isTerminal } from '@/api/types';
import {
  analyse,
  buildReport,
  parseDuration,
  rng,
  simulateTimeline,
  summaryOf,
  type SimParams,
} from './sim';
import {
  catalogBreakpoint,
  checkoutStress,
  paymentsBaseline,
  searchSoak,
  shopSmoke,
} from './yamls';

let seq = 1;
const rand = rng(42);
export function uuid(): string {
  const hex = (n: number) =>
    Array.from({ length: n }, () => Math.floor(rand() * 16).toString(16)).join('');
  return `${hex(8)}-${hex(4)}-4${hex(3)}-${'89ab'[Math.floor(rand() * 4)]}${hex(3)}-${hex(12)}`;
}
const nextId = () => seq++;

const NOW = Date.now();
const ago = (s: number) => new Date(NOW - s * 1000).toISOString();
const MIN = 60;
const HOUR = 3600;
const DAY = 86400;

export interface MockOptions {
  setupRequired: boolean;
  signedIn: boolean;
}

export interface Db {
  options: MockOptions;
  version: { version: string; commit: string };
  orgId: string;
  orgName: string;
  users: (User & { password: string })[];
  meId: string | null;
  tokens: Token[];
  projects: Project[];
  targets: Target[];
  secrets: Record<string, Secret[]>;
  scenarios: Scenario[];
  versions: Record<string, ScenarioVersion[]>;
  runs: Run[];
  sims: Record<string, SimParams & { targets: string[]; breakpoint: boolean }>;
  reports: Record<string, Report>;
  events: Record<string, RunEvent[]>;
  workers: Worker[];
  audit: AuditEntry[];
}

function scenarioFrom(
  projectId: string,
  yaml: string,
  versions: { message: string; ago: number; by: string }[],
): {
  scenario: Scenario;
  versions: ScenarioVersion[];
} {
  const a = analyse(yaml);
  const id = uuid();
  const vs: ScenarioVersion[] = versions.map((v, i) => ({
    scenarioId: id,
    version: versions.length - i,
    yaml: i === 0 ? yaml : yaml.replace(/duration: (\S+)/, 'duration: 1m'),
    message: v.message,
    createdBy: v.by,
    createdAt: ago(v.ago),
    ...(a.validation.plan ? { plan: a.validation.plan } : {}),
  }));
  const latest = vs[0]!;
  return {
    scenario: {
      id,
      projectId,
      name: a.name ?? 'scenario',
      ...(a.description ? { description: a.description } : {}),
      tags: a.tags,
      latestVersion: latest,
      createdAt: vs[vs.length - 1]!.createdAt,
      updatedAt: latest.createdAt,
    },
    versions: vs,
  };
}

const capacities: Record<string, Partial<SimParams>> = {
  'shop-smoke': { capacity: 400, baseLatency: 0.038, baseErrors: 0.0004 },
  'checkout-stress': { capacity: 430, baseLatency: 0.052, baseErrors: 0.0011 },
  'search-soak': { capacity: 200, baseLatency: 0.061, baseErrors: 0.0002 },
  'catalog-breakpoint': { capacity: 620, baseLatency: 0.031, baseErrors: 0.0003 },
  'payments-baseline': { capacity: 150, baseLatency: 0.089, baseErrors: 0.0008 },
};

export function simFor(
  s: Scenario,
  seed: number,
  overrides?: Run['overrides'],
): SimParams & { targets: string[]; breakpoint: boolean } {
  const a = analyse(s.latestVersion.yaml);
  const plan = a.validation.plan;
  const doc = s.latestVersion.yaml;
  const targets = [...doc.matchAll(/^\s+- ((?:http|errors|[\w-]+)\.?\S* *[<>=]+ *\S+)$/gm)].map(
    (m) => m[1]!,
  );
  const shape = overrides?.shape ?? plan?.shape;
  const mode = overrides?.mode ?? plan?.mode ?? 'rate';
  let peak = plan?.peak ?? 10;
  if (overrides?.rate) peak = Number.parseFloat(overrides.rate) || peak;
  if (overrides?.vus) peak = overrides.vus;
  if (overrides?.max) peak = Number.parseFloat(overrides.max) || peak;
  let duration = plan?.durationSeconds ?? 60;
  if (overrides?.duration) duration = parseDuration(overrides.duration) ?? duration;
  // Keep demos short: cap simulated runs at 10 minutes.
  duration = Math.min(duration, 600);
  return {
    seed,
    mode,
    shape,
    peak,
    duration,
    capacity: 300,
    baseLatency: 0.05,
    baseErrors: 0.001,
    ...capacities[s.name],
    targets,
    breakpoint: shape === 'breakpoint',
  };
}

export function createDb(options: MockOptions): Db {
  const orgId = uuid();
  const mk = (
    name: string,
    email: string,
    role: Role,
    lastLogin: number | null,
    created: number,
  ) => ({
    id: uuid(),
    name,
    email,
    role,
    createdAt: ago(created),
    lastLoginAt: lastLogin == null ? null : ago(lastLogin),
    password: 'correct-horse-battery',
  });
  const users = [
    mk('Priya Shah', 'priya@acme.dev', 'owner', 3 * MIN, 90 * DAY),
    mk('Tom Becker', 'tom@acme.dev', 'admin', 2 * HOUR, 80 * DAY),
    mk('Ana Lima', 'ana@acme.dev', 'editor', 1 * DAY, 60 * DAY),
    mk('Kenji Mori', 'kenji@acme.dev', 'runner', 5 * HOUR, 40 * DAY),
    mk('Sam Okafor', 'sam@acme.dev', 'viewer', null, 3 * DAY),
  ];

  const p1: Project = {
    id: uuid(),
    name: 'Storefront',
    slug: 'storefront',
    description: 'Customer-facing shop: catalogue, search, cart and checkout.',
    createdAt: ago(85 * DAY),
  };
  const p2: Project = {
    id: uuid(),
    name: 'Payments API',
    slug: 'payments-api',
    description: 'Authorisation and capture service.',
    createdAt: ago(50 * DAY),
  };
  const p3: Project = {
    id: uuid(),
    name: 'Internal tools',
    slug: 'internal-tools',
    createdAt: ago(9 * DAY),
  };

  const tgt = (
    projectId: string,
    name: string,
    baseURL: string,
    priv: boolean,
    verified: boolean,
    caps: Target['caps'],
    verifiedAgo?: number,
    method?: string,
  ): Target => ({
    id: uuid(),
    projectId,
    name,
    baseURL,
    private: priv,
    verified,
    verifiedAt: verifiedAgo != null ? ago(verifiedAgo) : null,
    verificationMethod: method ?? null,
    verificationToken: `stp-v-${uuid().replace(/-/g, '').slice(0, 24)}`,
    allowHosts: [],
    caps,
    createdAt: ago(70 * DAY),
  });
  const targets: Target[] = [
    tgt(p1.id, 'local', 'http://localhost:8090', true, true, {}),
    {
      ...tgt(
        p1.id,
        'staging',
        'https://staging.shop.acme.dev',
        false,
        true,
        { maxRate: 2000, maxVUs: 5000, maxDurationSeconds: 4 * HOUR },
        30 * DAY,
        'dns-txt',
      ),
      allowHosts: ['auth.staging.acme.dev'],
    },
    tgt(p1.id, 'production', 'https://shop.acme.dev', false, false, {
      maxRate: 5,
      maxVUs: 10,
      maxDurationSeconds: 60,
    }),
    tgt(
      p2.id,
      'sandbox',
      'https://sandbox.pay.acme.dev',
      false,
      true,
      { maxRate: 500, maxVUs: 1000 },
      12 * DAY,
      'well-known',
    ),
    tgt(p2.id, 'docker', 'http://10.0.4.12:9000', true, true, {}),
  ];

  const scenarios: Scenario[] = [];
  const versions: Record<string, ScenarioVersion[]> = {};
  const addScenario = (
    projectId: string,
    yaml: string,
    vs: { message: string; ago: number; by: string }[],
  ) => {
    const r = scenarioFrom(projectId, yaml, vs);
    scenarios.push(r.scenario);
    versions[r.scenario.id] = r.versions;
    return r.scenario;
  };
  const sSmoke = addScenario(p1.id, shopSmoke, [
    { message: 'Add login journey', ago: 2 * DAY, by: 'ana@acme.dev' },
    { message: 'Initial version', ago: 20 * DAY, by: 'priya@acme.dev' },
  ]);
  const sStress = addScenario(p1.id, checkoutStress, [
    { message: 'Raise max to 400/s', ago: 5 * HOUR, by: 'ana@acme.dev' },
    { message: 'Branch into buy / browse more', ago: 3 * DAY, by: 'ana@acme.dev' },
    { message: 'Use STRIPE_TEST_KEY secret', ago: 9 * DAY, by: 'tom@acme.dev' },
    { message: 'Initial version', ago: 15 * DAY, by: 'ana@acme.dev' },
  ]);
  const sSoak = addScenario(p1.id, searchSoak, [
    { message: 'Initial version', ago: 12 * DAY, by: 'priya@acme.dev' },
  ]);
  const sBreak = addScenario(p1.id, catalogBreakpoint, [
    { message: 'Ten levels of 30s', ago: 6 * DAY, by: 'tom@acme.dev' },
    { message: 'Initial version', ago: 7 * DAY, by: 'tom@acme.dev' },
  ]);
  const sPay = addScenario(p2.id, paymentsBaseline, [
    { message: 'Initial version', ago: 10 * DAY, by: 'tom@acme.dev' },
  ]);

  const db: Db = {
    options,
    version: { version: 'v0.9.0', commit: '03f60ea' },
    orgId,
    orgName: 'Acme Retail',
    users,
    meId: options.signedIn && !options.setupRequired ? users[0]!.id : null,
    tokens: [
      {
        id: uuid(),
        name: 'github-actions',
        prefix: 'stp_9fK2',
        role: 'runner',
        createdAt: ago(30 * DAY),
        lastUsedAt: ago(4 * HOUR),
        expiresAt: ago(-60 * DAY),
      },
      {
        id: uuid(),
        name: 'laptop cli',
        prefix: 'stp_a81Q',
        role: 'owner',
        createdAt: ago(60 * DAY),
        lastUsedAt: ago(3 * DAY),
        expiresAt: null,
      },
    ],
    projects: [p1, p2, p3],
    targets,
    secrets: {
      [p1.id]: [
        { name: 'API_KEY', updatedAt: ago(14 * DAY) },
        { name: 'ADMIN_PASSWORD', updatedAt: ago(40 * DAY) },
        { name: 'STRIPE_TEST_KEY', updatedAt: ago(9 * DAY) },
      ],
      [p2.id]: [{ name: 'MERCHANT_SECRET', updatedAt: ago(10 * DAY) }],
      [p3.id]: [],
    },
    scenarios,
    versions,
    runs: [],
    sims: {},
    reports: {},
    events: {},
    workers: [],
    audit: [],
  };
  if (options.setupRequired) {
    db.users = [];
    db.projects = [];
  }

  // Run history.
  const staging = targets[1]!;
  const local = targets[0]!;
  const sandbox = targets[3]!;
  const history: [Scenario, Target, number, RunStatus, string?, Partial<Run>?][] = [
    [sStress, staging, 26 * MIN, 'completed'],
    [sSmoke, staging, 3 * HOUR, 'completed', 'release 2026.10.2 smoke'],
    [sBreak, staging, 6 * HOUR, 'completed'],
    [sStress, staging, 1 * DAY, 'completed', 'before cache change'],
    [
      sSmoke,
      local,
      1 * DAY + 2 * HOUR,
      'aborted',
      undefined,
      { stopReason: 'killed by priya@acme.dev' },
    ],
    [sSoak, staging, 2 * DAY, 'completed', 'overnight soak'],
    [sSmoke, staging, 3 * DAY, 'completed'],
    [
      sStress,
      staging,
      4 * DAY,
      'failed',
      undefined,
      { error: 'no workers connected in region eu-west-1' },
    ],
    [sSmoke, local, 5 * DAY, 'completed'],
    [sPay, sandbox, 2 * HOUR, 'completed'],
    [sPay, sandbox, 2 * DAY, 'completed'],
  ];
  for (let i = 9; i < 40; i++)
    history.push([i % 3 ? sSmoke : sStress, i % 4 ? staging : local, (6 + i) * DAY, 'completed']);

  history.forEach(([s, t, startAgo, status, note, extra], i) => {
    const sim = simFor(s, 1000 + i);
    if (s === sStress && i === 3) sim.capacity = 300; // a failing run
    const created = ago(startAgo + 4);
    const started = ago(startAgo);
    const run: Run = {
      id: uuid(),
      projectId: s.projectId,
      scenarioId: s.id,
      scenarioName: s.name,
      scenarioVersion: s.latestVersion.version,
      targetId: t.id,
      targetURL: t.baseURL,
      status,
      verdict: null,
      stopReason: status === 'completed' ? 'completed' : null,
      error: null,
      overrides: {},
      ...(s.latestVersion.plan ? { plan: s.latestVersion.plan } : {}),
      workers: 2,
      ...(note ? { note } : {}),
      createdBy: i % 3 === 0 ? 'ana@acme.dev' : 'github-actions',
      createdAt: created,
      startedAt: status === 'failed' ? null : started,
      endedAt: ago(Math.max(0, startAgo - sim.duration)),
      summary: null,
      ...extra,
    };
    db.runs.push(run);
    db.sims[run.id] = sim;
    if (status === 'completed' || status === 'aborted') {
      const tl = simulateTimeline(
        sim,
        status === 'aborted' ? Math.floor(sim.duration / 3) : sim.duration,
      );
      const rep = buildReport({
        runId: run.id,
        scenario: analyse(s.latestVersion.yaml),
        targetURL: t.baseURL,
        targets: sim.targets,
        timeline: tl,
        started,
        stopReason: run.stopReason ?? 'completed',
        workers: 2,
        breakpoint: sim.breakpoint,
      });
      db.reports[run.id] = rep;
      run.verdict = rep.verdict;
      run.summary = summaryOf(rep);
    }
  });

  // One run in progress, 40s in.
  const live = startRun(db, sStress, staging, { note: 'cache warm-up check', workers: 2 }, 40);
  live.createdBy = 'kenji@acme.dev';

  db.workers = [
    {
      id: 'w-eu-1',
      name: 'worker-eu-1',
      region: 'eu-west-1',
      version: 'v0.9.0',
      labels: { pool: 'default', arch: 'amd64' },
      cpus: 8,
      memoryBytes: 16 * 2 ** 30,
      status: 'busy',
      runId: live.id,
      connectedAt: ago(3 * DAY),
      lastSeenAt: ago(1),
    },
    {
      id: 'w-eu-2',
      name: 'worker-eu-2',
      region: 'eu-west-1',
      version: 'v0.9.0',
      labels: { pool: 'default', arch: 'amd64' },
      cpus: 8,
      memoryBytes: 16 * 2 ** 30,
      status: 'saturated',
      runId: live.id,
      connectedAt: ago(3 * DAY),
      lastSeenAt: ago(2),
    },
    {
      id: 'w-us-1',
      name: 'worker-us-1',
      region: 'us-east-1',
      version: 'v0.9.0',
      labels: { pool: 'default', arch: 'arm64' },
      cpus: 16,
      memoryBytes: 32 * 2 ** 30,
      status: 'idle',
      runId: null,
      connectedAt: ago(1 * DAY),
      lastSeenAt: ago(2),
    },
    {
      id: 'w-ap-1',
      name: 'worker-ap-1',
      region: 'ap-south-1',
      version: 'v0.8.2',
      labels: { pool: 'canary' },
      cpus: 4,
      memoryBytes: 8 * 2 ** 30,
      status: 'lost',
      runId: null,
      connectedAt: ago(2 * DAY),
      lastSeenAt: ago(17 * MIN),
    },
  ];

  const actions: [string, string, string, Record<string, unknown>?][] = [
    [
      'kenji@acme.dev',
      'run.start',
      `run ${live.id.slice(0, 8)}`,
      { scenario: 'checkout-stress', target: 'staging' },
    ],
    ['ana@acme.dev', 'scenario.version', 'checkout-stress v4', { message: 'Raise max to 400/s' }],
    ['github-actions', 'run.start', 'shop-smoke', { target: 'staging' }],
    ['priya@acme.dev', 'run.kill', 'shop-smoke', { reason: 'wrong target' }],
    ['tom@acme.dev', 'target.verify', 'staging', { method: 'dns-txt', result: 'verified' }],
    ['priya@acme.dev', 'user.create', 'sam@acme.dev', { role: 'viewer' }],
    ['tom@acme.dev', 'secret.put', 'STRIPE_TEST_KEY'],
    ['priya@acme.dev', 'token.create', 'laptop cli', { role: 'owner' }],
    ['tom@acme.dev', 'target.create', 'production'],
    ['priya@acme.dev', 'auth.login', 'priya@acme.dev'],
  ];
  db.audit = actions.map(([actor, action, subject, details], i) => ({
    id: 5000 - i,
    at: ago(i === 0 ? 45 : (i * i + 1) * 37 * MIN),
    actor,
    action,
    subject,
    ...(details ? { details } : {}),
    ip: actor === 'github-actions' ? '140.82.112.4' : '10.0.1.23',
  }));
  return db;
}

/** Creates a run that started `elapsed` seconds ago and is still active. */
export function startRun(
  db: Db,
  s: Scenario,
  t: Target,
  opts: {
    note?: string;
    workers?: number;
    overrides?: Run['overrides'];
    version?: number;
    createdBy?: string;
  },
  elapsed = 0,
): Run {
  const id = uuid();
  const sim = simFor(s, nextId() * 7919, opts.overrides);
  const plan = s.latestVersion.plan
    ? {
        ...s.latestVersion.plan,
        durationSeconds: sim.duration,
        ...(sim.shape ? { shape: sim.shape } : {}),
        mode: sim.mode,
        peak: sim.peak,
      }
    : undefined;
  const run: Run = {
    id,
    projectId: s.projectId,
    scenarioId: s.id,
    scenarioName: s.name,
    scenarioVersion: opts.version ?? s.latestVersion.version,
    targetId: t.id,
    targetURL: t.baseURL,
    status: elapsed > 0 ? 'running' : 'scheduling',
    verdict: null,
    stopReason: null,
    error: null,
    overrides: opts.overrides ?? {},
    ...(plan ? { plan } : {}),
    workers: opts.workers || 2,
    ...(opts.note ? { note: opts.note } : {}),
    createdBy: opts.createdBy ?? 'priya@acme.dev',
    createdAt: new Date(Date.now() - (elapsed + 2) * 1000).toISOString(),
    startedAt: elapsed > 0 ? new Date(Date.now() - elapsed * 1000).toISOString() : null,
    endedAt: null,
    summary: null,
  };
  db.runs.unshift(run);
  db.sims[id] = sim;
  db.events[id] = [
    {
      type: 'worker.assigned',
      message: `${run.workers} workers assigned in eu-west-1`,
      at: run.createdAt,
    },
  ];
  return run;
}

/** Elapsed load seconds for an active run. */
export function elapsedOf(run: Run): number {
  return run.startedAt ? (Date.now() - Date.parse(run.startedAt)) / 1000 : 0;
}

/** Advances active runs according to the wall clock. */
export function tick(db: Db) {
  for (const run of db.runs) {
    if (isTerminal(run.status)) continue;
    const sim = db.sims[run.id]!;
    if (run.status === 'scheduling' && Date.now() - Date.parse(run.createdAt) > 1500) {
      run.status = 'running';
      run.startedAt = new Date().toISOString();
    }
    if (run.status === 'running' || run.status === 'stopping') {
      const el = elapsedOf(run);
      const evs = (db.events[run.id] ??= []);
      if (el > 25 && !evs.some((e) => e.type === 'worker.saturated')) {
        evs.unshift({
          type: 'worker.saturated',
          message: 'worker-eu-2 CPU above 90% for 10s; schedule lag p99 4.1ms',
          worker: 'worker-eu-2',
          at: new Date(Date.parse(run.startedAt!) + 25_000).toISOString(),
        });
      }
      if (el >= sim.duration || run.status === 'stopping')
        finish(db, run, run.status === 'stopping' ? 'stopped by user' : 'completed', 'completed');
    }
  }
}

export function finish(db: Db, run: Run, stopReason: string, status: RunStatus) {
  const sim = db.sims[run.id]!;
  const s = db.scenarios.find((x) => x.id === run.scenarioId);
  const upTo = Math.max(1, Math.floor(Math.min(elapsedOf(run), sim.duration)));
  run.status = status;
  run.stopReason = stopReason;
  run.endedAt = new Date().toISOString();
  if (s && run.startedAt) {
    const rep = buildReport({
      runId: run.id,
      scenario: analyse(s.latestVersion.yaml),
      targetURL: run.targetURL ?? '',
      targets: sim.targets,
      timeline: simulateTimeline(sim, upTo),
      started: run.startedAt,
      stopReason,
      workers: run.workers ?? 1,
      breakpoint: sim.breakpoint,
    });
    db.reports[run.id] = rep;
    run.verdict = rep.verdict;
    run.summary = summaryOf(rep);
  }
  for (const w of db.workers)
    if (w.runId === run.id) {
      w.runId = null;
      if (w.status !== 'lost') w.status = 'idle';
    }
}

export function me(db: Db): Me | null {
  const u = db.users.find((x) => x.id === db.meId);
  if (!u) return null;
  return {
    id: u.id,
    email: u.email,
    name: u.name,
    role: u.role,
    orgId: db.orgId,
    orgName: db.orgName,
  };
}

export function publicUser(u: Db['users'][number]): User {
  const { password: _pw, ...rest } = u;
  return rest;
}

export { nextId };
