/**
 * Mock-only versions of the server's live worker health, pack library,
 * scenario coverage and drift, and read-only settings.
 */
import { parse } from 'yaml';
import type {
  DriftEndpoint,
  DriftJourneyCheck,
  LimitCaps,
  LimitSettings,
  PackDetail,
  PackEntry,
  RequestRef,
  Run,
  RunWorkerHealth,
  RunWorkers,
  Scenario,
  ScenarioCoverage,
  ScenarioDrift,
  SSOSettings,
} from '@/api/types';
import { isActive } from '@/api/types';
import { elapsedOf, type Db } from './db';

// ---------------------------------------------------------------- worker health

/** Health of an active run's workers, varying with the wall clock. */
export function runWorkerHealth(db: Db, r: Run): RunWorkers {
  if (!isActive(r.status) || r.status === 'scheduling') {
    return { live: isActive(r.status), workers: [] };
  }
  const attached = db.workers.filter((w) => w.runId === r.id);
  const gens = attached.length
    ? attached.map((w) => ({ id: w.id, name: w.name, region: w.region, status: w.status }))
    : Array.from({ length: r.workers ?? 1 }, (_, i) => ({
        id: `gen-${i + 1}`,
        name: `worker-${i + 1}`,
        region: 'eu-west-1',
        status: 'busy' as const,
      }));
  const el = elapsedOf(r);
  const now = Date.now();
  const workers = gens.map((g, i): RunWorkerHealth => {
    const saturated = g.status === 'saturated' && el > 25;
    const lost = g.status === 'lost';
    const cpu = saturated ? 92 + Math.sin(el / 3) * 3 : 38 + Math.sin(el / 7 + i) * 14 + i * 4;
    return {
      id: g.id,
      name: g.name,
      region: g.region,
      status: lost ? 'lost' : saturated ? 'saturated' : 'healthy',
      saturated,
      ...(saturated ? { reasons: ['cpu 92% above 85% for 10s'] } : {}),
      cpuPercent: Math.round(cpu * 10) / 10,
      schedLagP99: saturated ? 0.0141 : 0.0006 + i * 0.0002,
      lastHeartbeatAt: new Date(now - (lost ? 40_000 : (i % 2) * 1000)).toISOString(),
    };
  });
  return { live: true, workers };
}

// ---------------------------------------------------------------- packs

export const mockPacks: PackEntry[] = [
  {
    name: 'ecommerce',
    title: 'E-commerce, marketplaces',
    status: 'shipped',
    signature: 'Browse, search, cart, checkout; flash-sale spike; last-item contention',
    drivers: ['http'],
  },
  {
    name: 'llm-apps',
    title: 'AI and LLM apps',
    status: 'shipped',
    signature: 'Time to first token; tokens per second; long streaming requests',
    drivers: ['sse', 'http'],
  },
  {
    name: 'identity',
    title: 'Login and identity',
    status: 'shipped',
    signature: 'Login storm; token refresh waves',
    drivers: ['http'],
  },
  {
    name: 'gaming',
    title: 'Gaming backends',
    status: 'shipped',
    signature: 'Matchmaking, leaderboards, session servers',
    drivers: ['websocket', 'udp'],
  },
];

const shopMix = `# Everyday shop traffic: most visitors browse and search, some add to the
# cart, few check out.
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: ecommerce-shop-mix
  tags: [pack:ecommerce]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: browse
    weight: 6
    steps:
      - get: /api/products
        check: { status: 200 }
  - name: checkout
    weight: 1
    steps:
      - post: /api/cart
        json: { productId: 1, qty: 1 }
      - post: /api/checkout
load:
  mode: rate
  rate: 20/s
  duration: 2m
targets:
  - http.p95 < 300ms
`;

const flashSale = `# A flash sale: traffic jumps tenfold for a minute, then returns to normal.
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: ecommerce-flash-sale-spike
  tags: [pack:ecommerce, stress]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: rush
    steps:
      - get: /api/products/42
      - post: /api/cart
        json: { productId: 42, qty: 1 }
load:
  shape: spike
  mode: rate
  rate: 20/s
  max: 200/s
  duration: 3m
targets:
  - http.p95 < 500ms
`;

const ttft = `# A chat completion streamed token by token; measures time to first token.
apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: llm-apps-chat-stream
  tags: [pack:llm-apps]
target:
  baseURL: \${env.TARGET_URL}
journeys:
  - name: chat
    steps:
      - post: /v1/chat/completions
        sse: true
        json: { stream: true, messages: [{ role: user, content: hi }] }
load:
  mode: rate
  rate: 2/s
  duration: 2m
`;

const details: Record<string, PackDetail> = {
  ecommerce: {
    name: 'ecommerce',
    title: 'E-commerce and marketplaces',
    description:
      'Browsing, search, cart and checkout journeys with a flash-sale spike and a last-item contention test. Built for JSON shop APIs shaped like ShopLab.',
    status: 'shipped',
    protocols: ['http'],
    referenceApp: 'examples/shoplab',
    variables: [{ name: 'TARGET_URL', description: 'Base URL of the shop API' }],
    files: [
      {
        path: 'journeys/shop-mix.yaml',
        kind: 'journey',
        scenario: 'ecommerce-shop-mix',
        description:
          'Everyday shop traffic: most visitors browse and search, some add to the cart, few check out.',
        journeys: ['browse', 'checkout'],
        yaml: shopMix,
      },
      {
        path: 'stresses/flash-sale-spike.yaml',
        kind: 'stress',
        scenario: 'ecommerce-flash-sale-spike',
        description: 'A flash sale: traffic jumps tenfold for a minute, then returns to normal.',
        journeys: ['rush'],
        shape: 'spike',
        yaml: flashSale,
      },
    ],
  },
  'llm-apps': {
    name: 'llm-apps',
    title: 'AI and LLM apps',
    description: 'Streaming chat completions: time to first token and tokens per second.',
    status: 'shipped',
    protocols: ['sse', 'http'],
    variables: [{ name: 'TARGET_URL', description: 'Base URL of the model API' }],
    files: [
      {
        path: 'journeys/chat-stream.yaml',
        kind: 'journey',
        scenario: 'llm-apps-chat-stream',
        description: 'A chat completion streamed token by token; measures time to first token.',
        journeys: ['chat'],
        yaml: ttft,
      },
    ],
  },
};

export function packDetail(name: string): PackDetail | undefined {
  return details[name];
}

// ---------------------------------------------------------------- coverage and drift

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);
const methods = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options'];

/** The shop API the mock targets serve at /openapi.json. */
export const mockShopSpec = `openapi: 3.0.3
info: { title: Shop, version: "2" }
paths:
  /:
    get: { summary: Home page }
  /api/search:
    get: { summary: Search products }
  /api/products:
    get: { summary: List products }
  /api/products/{id}:
    get: { summary: One product }
  /api/cart:
    post: { summary: Add to cart }
  /api/checkout:
    post: { summary: Check out }
  /api/checkout/shipping:
    post: { summary: Set shipping }
  /api/checkout/pay:
    post: { summary: Pay }
  /api/login:
    post: { summary: Sign in }
  /api/me:
    get: { summary: The signed-in user }
  /api/orders:
    get: { summary: List orders }
`;

export interface Endpoint {
  method: string;
  path: string;
  summary?: string;
}

/** Endpoints of an OpenAPI document, or an error message. */
export function endpointsOf(spec: string): Endpoint[] | string {
  let doc: unknown;
  try {
    doc = parse(spec);
  } catch (e) {
    return `openapi: ${(e as Error).message.split('\n')[0]}`;
  }
  if (!isObj(doc) || !isObj(doc.paths)) return 'openapi: not an OpenAPI 3 document';
  const out: Endpoint[] = [];
  for (const [path, item] of Object.entries(doc.paths)) {
    if (!isObj(item)) continue;
    for (const m of methods) {
      const op = item[m];
      if (!isObj(op)) continue;
      out.push({
        method: m.toUpperCase(),
        path,
        ...(typeof op.summary === 'string' ? { summary: op.summary } : {}),
      });
    }
  }
  return out.length ? out : 'openapi has no endpoints';
}

/** Every request step of a scenario, by journey. */
function requestsOf(yaml: string): RequestRef[] {
  let doc: unknown;
  try {
    doc = parse(yaml);
  } catch {
    return [];
  }
  const out: RequestRef[] = [];
  const walk = (steps: unknown, journey: string) => {
    if (!Array.isArray(steps)) return;
    for (const s of steps) {
      if (!isObj(s)) continue;
      const m = methods.find((k) => typeof s[k] === 'string');
      if (m) out.push({ journey, method: m.toUpperCase(), url: String(s[m]) });
      if (Array.isArray(s.branch))
        for (const arm of s.branch) if (isObj(arm)) walk(arm.steps, journey);
      walk(s.steps, journey);
    }
  };
  if (isObj(doc) && Array.isArray(doc.journeys)) {
    for (const j of doc.journeys)
      if (isObj(j)) walk(j.steps, typeof j.name === 'string' ? j.name : 'journey');
  }
  return out;
}

function matches(e: Endpoint, r: RequestRef): boolean {
  if (e.method !== r.method) return false;
  const path = r.url.replace(/^https?:\/\/[^/]+/, '').split('?')[0]!;
  const re = new RegExp(`^${e.path.replace(/\{[^}/]*\}/g, '[^/]+')}/?$`);
  return re.test(path.replace(/\$\{[^}]*\}/g, 'x'));
}

const isTemplated = (r: RequestRef) => r.url.trim().startsWith('${');

export function coverageOf(yaml: string, eps: Endpoint[], version: number): ScenarioCoverage {
  const reqs = requestsOf(yaml);
  const endpoints = eps.map((e) => ({
    ...e,
    journeys: [
      ...new Set(reqs.filter((r) => !isTemplated(r) && matches(e, r)).map((r) => r.journey)),
    ].sort(),
  }));
  return {
    version,
    endpoints,
    unmatched: reqs.filter((r) => !isTemplated(r) && !eps.some((e) => matches(e, r))),
    templated: reqs.filter(isTemplated).length,
    covered: endpoints.filter((e) => e.journeys.length > 0).length,
    total: eps.length,
  };
}

const key = (e: Endpoint) => `${e.method} ${e.path.replace(/\{[^}/]*\}/g, '{}')}`;

export function driftOf(
  yaml: string,
  cur: Endpoint[],
  prev: Endpoint[] | null,
  version: number,
  dryRun: boolean,
): ScenarioDrift {
  const reqs = requestsOf(yaml);
  const unmatched = reqs.filter((r) => !isTemplated(r) && !cur.some((e) => matches(e, r)));
  const out: ScenarioDrift = { version, drifted: unmatched.length > 0, unmatched };
  if (prev) {
    const have = new Set(cur.map(key));
    const had = new Set(prev.map(key));
    const removed: DriftEndpoint[] = prev
      .filter((e) => !have.has(key(e)))
      .map(({ method, path }) => ({ method, path }));
    out.removed = removed;
    out.added = cur.filter((e) => !had.has(key(e))).map(({ method, path }) => ({ method, path }));
    const broken = new Map<string, DriftEndpoint[]>();
    for (const e of removed) {
      for (const r of reqs) {
        if (matches(e, r)) {
          const list = broken.get(r.journey) ?? [];
          if (!list.some((x) => x.path === e.path && x.method === e.method)) list.push(e);
          broken.set(r.journey, list);
        }
      }
    }
    out.broken = [...broken.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([journey, endpoints]) => ({ journey, endpoints }));
    out.drifted = out.drifted || out.broken.length > 0;
  }
  if (dryRun) {
    const journeys = [...new Set(reqs.map((r) => r.journey))];
    out.dryRun = journeys.map((journey): DriftJourneyCheck => {
      const bad = unmatched.find((r) => r.journey === journey);
      return bad
        ? { journey, ok: false, step: `${bad.method} ${bad.url}`, status: 404, error: 'status 404' }
        : { journey, ok: true };
    });
    out.drifted = out.drifted || out.dryRun.some((c) => !c.ok);
  }
  return out;
}

/** The YAML of a scenario version. */
export function scenarioYaml(
  db: Db,
  sc: Scenario,
  version?: number,
): { yaml: string; version: number } | null {
  if (version == null) return { yaml: sc.latestVersion.yaml, version: sc.latestVersion.version };
  const v = (db.versions[sc.id] ?? []).find((x) => x.version === version);
  return v ? { yaml: v.yaml, version: v.version } : null;
}

// ---------------------------------------------------------------- settings

export const mockSSO: SSOSettings = {
  enabled: true,
  passwordLogin: true,
  name: 'Okta',
  issuer: 'https://acme.okta.com/oauth2/default',
  redirectURL: 'https://stampede.acme.dev/api/v1/auth/oidc/callback',
  allowedDomains: ['acme.dev'],
  defaultRole: 'viewer',
  scopes: ['openid', 'email', 'profile'],
};

const serverCaps: LimitCaps = { maxRate: 5000, maxVUs: 5000, maxDurationSeconds: 4 * 3600 };
const unverified: LimitCaps = { maxRate: 50, maxVUs: 50, maxDurationSeconds: 600 };

function tightest(...cs: LimitCaps[]): LimitCaps {
  const lo = (k: keyof LimitCaps) => {
    const vs = cs.map((c) => c[k]).filter((v): v is number => typeof v === 'number' && v > 0);
    return vs.length ? Math.min(...vs) : undefined;
  };
  const out: LimitCaps = {};
  const rate = lo('maxRate');
  const vus = lo('maxVUs');
  const dur = lo('maxDurationSeconds');
  if (rate != null) out.maxRate = rate;
  if (vus != null) out.maxVUs = vus;
  if (dur != null) out.maxDurationSeconds = dur;
  return out;
}

export function limitSettings(db: Db): LimitSettings {
  return {
    server: serverCaps,
    unverifiedPublic: unverified,
    abortFloor: { errorRate: 0.9, forSeconds: 30 },
    targets: db.targets.map((t) => {
      const verified = t.private || t.verified;
      return {
        id: t.id,
        name: t.name,
        projectId: t.projectId,
        projectName: db.projects.find((p) => p.id === t.projectId)?.name ?? '',
        baseURL: t.baseURL,
        private: t.private,
        verified,
        caps: t.caps,
        effective: verified
          ? tightest(serverCaps, t.caps)
          : tightest(serverCaps, t.caps, unverified),
      };
    }),
  };
}
