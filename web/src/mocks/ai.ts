/**
 * AI providers and generation jobs for the mock API. A new job moves
 * through the pipeline stages on the wall clock (about 15 seconds) and
 * ends with a proposal, per-journey dry-run traces and a diff.
 */
import type {
  AIJob,
  AIJobStatus,
  AIJourney,
  AIProvider,
  AIStepTrace,
  AITrace,
  Scenario,
} from '@/api/types';

export interface MockAIJob extends AIJob {
  /** Repair rounds the job may use (not part of the API). */
  maxRepairs: number;
}

export const proposalYaml = `apiVersion: stampede.dev/v1
kind: Scenario
metadata:
  name: shop-generated
  description: Browse and search most of the time; some shoppers log in and buy.
  tags: [generated]
journeys:
  - name: browse
    weight: 6
    steps:
      - get: /api/products?page=\${rand(1, 20)}
        check: { status: 200 }
        extract: { productId: "$.items[0].id" }
      - think: 1s..3s
      - get: /api/products/\${productId}
        check: { status: 200 }
  - name: search
    weight: 3
    steps:
      - get: /api/search?q=\${pick("lamp", "chair", "desk")}
        check: { status: 200 }
  - name: checkout
    weight: 1
    steps:
      - post: /api/login
        json: { email: "user0001@shoplab.test", password: "\${secret.SHOP_PASSWORD}" }
        check: { status: 200 }
        extract: { token: "$.token" }
      - post: /api/cart
        headers: { Authorization: "Bearer \${token}" }
        json: { productId: 12, qty: 1 }
        check: { status: [200, 201] }
      - post: /api/checkout
        headers: { Authorization: "Bearer \${token}" }
        json: { shippingAddress: { line1: "1 Test Street", city: "Testville" } }
        check: { status: 201 }
load:
  mode: rate
  rate: 20/s
  duration: 2m
targets:
  - http.p95 < 300ms
  - errors < 1%
`;

const diffAgainstSmoke = `--- shop-smoke (latest)
+++ proposal
@@ -1,9 +1,9 @@
 apiVersion: stampede.dev/v1
 kind: Scenario
 metadata:
-  name: shop-smoke
-  description: Quick check that the shop answers.
-  tags: [smoke, ci]
+  name: shop-generated
+  description: Browse and search most of the time; some shoppers log in and buy.
+  tags: [generated]
 journeys:
   - name: browse
-    weight: 8
+    weight: 6
@@ -18,4 +18,22 @@
+  - name: checkout
+    weight: 1
+    steps:
+      - post: /api/login
+        json: { email: "user0001@shoplab.test", password: "\${secret.SHOP_PASSWORD}" }
+        check: { status: 200 }
+        extract: { token: "$.token" }
+      - post: /api/cart
+      - post: /api/checkout
 load:
   mode: rate
-  rate: 10/s
-  duration: 30s
+  rate: 20/s
+  duration: 2m
`;

function step(
  name: string,
  method: string,
  url: string,
  status: number,
  durationMs: number,
  extra: Partial<AIStepTrace> = {},
): AIStepTrace {
  const ok = extra.ok ?? status < 400;
  return {
    step: name,
    method,
    url,
    status,
    durationMs,
    requestHeaders: { 'User-Agent': 'stampede/0.9 (dry run)', Accept: 'application/json' },
    responseHeaders: { 'Content-Type': 'application/json', 'X-Request-Id': '[redacted]' },
    checks: [
      {
        name: `status ${status < 300 ? status : 200}`,
        ok,
        ...(ok ? {} : { detail: `got ${status}` }),
      },
    ],
    ...extra,
    ok,
  };
}

function journeys(base: string, checkoutPassed: boolean, attempts: number): AIJourney[] {
  const browse: AITrace = {
    pass: 1,
    ok: true,
    steps: [
      step('GET /api/products', 'GET', `${base}/api/products?page=7`, 200, 38.2, {
        responseBody:
          '{"items":[{"id":12,"name":"Desk lamp","price":39.0},{"id":13,"name":"Chair","price":129.0}],"page":7}',
        extracted: { productId: '12' },
      }),
      step('GET /api/products/{id}', 'GET', `${base}/api/products/12`, 200, 21.7, {
        responseBody: '{"id":12,"name":"Desk lamp","price":39.0,"stock":41}',
      }),
    ],
  };
  const search: AITrace = {
    pass: 1,
    ok: true,
    steps: [
      step('GET /api/search', 'GET', `${base}/api/search?q=lamp`, 200, 64.9, {
        responseBody: '{"results":[{"id":12,"name":"Desk lamp"}],"total":1}',
      }),
    ],
  };
  const login = step('POST /api/login', 'POST', `${base}/api/login`, 200, 88.4, {
    requestHeaders: { 'Content-Type': 'application/json' },
    requestBody: '{"email":"user0001@shoplab.test","password":"[redacted]"}',
    responseBody: '{"token":"[redacted jwt]","user":{"id":1,"email":"[redacted email]"}}',
    extracted: { token: '[redacted]' },
  });
  const cart = step('POST /api/cart', 'POST', `${base}/api/cart`, 201, 41.3, {
    requestHeaders: { 'Content-Type': 'application/json', Authorization: '[redacted]' },
    requestBody: '{"productId":12,"qty":1}',
    responseBody: '{"cartId":"c_81","items":1}',
  });
  const checkout = checkoutPassed
    ? step('POST /api/checkout', 'POST', `${base}/api/checkout`, 201, 132.6, {
        requestHeaders: { 'Content-Type': 'application/json', Authorization: '[redacted]' },
        requestBody: '{"shippingAddress":{"line1":"1 Test Street","city":"Testville"}}',
        responseBody: '{"orderId":"o_5521","status":"placed"}',
      })
    : step('POST /api/checkout', 'POST', `${base}/api/checkout`, 422, 19.8, {
        requestHeaders: { 'Content-Type': 'application/json', Authorization: '[redacted]' },
        requestBody: '{"items":[12]}',
        responseBody: '{"error":"shippingAddress is required"}',
        error: 'check failed: status 201 expected, got 422',
      });
  return [
    { name: 'browse', status: 'passed', attempts: 1, traces: [browse] },
    { name: 'search', status: 'passed', attempts: 1, traces: [search] },
    {
      name: 'checkout',
      status: checkoutPassed ? 'passed' : 'flagged',
      attempts,
      ...(checkoutPassed
        ? {}
        : {
            problems: [
              'POST /api/checkout returned 422: shippingAddress is required (the spec marks it optional)',
            ],
          }),
      traces: [
        {
          pass: 1,
          ok: checkoutPassed,
          ...(checkoutPassed ? {} : { error: 'step 3 (POST /api/checkout) failed' }),
          steps: [login, cart, checkout],
        },
      ],
    },
  ];
}

const notRun = (): AIJourney[] =>
  ['browse', 'search', 'checkout'].map((name) => ({
    name,
    status: 'not-run' as const,
    attempts: 0,
    traces: [],
  }));

const iso = (ms: number) => new Date(ms).toISOString();

/** Brings a job up to date with the wall clock. */
export function advanceAIJob(job: MockAIJob, targetURL: string, now = Date.now()) {
  if (job.status !== 'queued' && job.status !== 'running') return;
  const t = (now - Date.parse(job.createdAt)) / 1000;
  const repairs = job.dryRun ? Math.min(job.maxRepairs, 1) : 0;
  const plan: [number, string, number][] = [
    [1.5, 'understand', 0],
    [3, 'draft', 0],
    [6, 'static-check', 0],
  ];
  if (job.dryRun) {
    plan.push([7, 'dry-run', 0]);
    if (repairs) plan.push([10, 'repair', 1], [12, 'static-check', 1], [13, 'dry-run', 1]);
  }
  const end = job.dryRun ? (repairs ? 15 : 10) : 7;
  if (t < 1.5) return;
  if (!job.startedAt) job.startedAt = iso(Date.parse(job.createdAt) + 1500);
  job.status = 'running';
  for (const [at, stage, round] of plan)
    if (t >= at) {
      job.stage = stage;
      job.round = round;
    }
  const calls = (t >= 3 ? 1 : 0) + (repairs && t >= 10 ? 1 : 0);
  job.usage = { inputTokens: calls * 9_412, outputTokens: calls * 2_318 };
  if (t < end) return;
  const passed = !job.dryRun || repairs > 0;
  job.status = (passed ? 'succeeded' : 'needs_review') satisfies AIJobStatus;
  job.stage = 'done';
  job.finishedAt = iso(Date.parse(job.createdAt) + end * 1000);
  job.yaml = proposalYaml;
  job.journeys = job.dryRun ? journeys(targetURL, passed, 1 + repairs) : notRun();
  if (job.scenarioId) job.diff = diffAgainstSmoke;
}

export function seedAI(args: {
  uuid: () => string;
  ago: (s: number) => string;
  projectId: string;
  targetId: string;
  targetURL: string;
  smoke: Scenario;
  ana: string;
  tom: string;
}): { providers: AIProvider[]; jobs: MockAIJob[] } {
  const { uuid, ago, projectId, targetId, targetURL, smoke } = args;
  const providers: AIProvider[] = [
    {
      id: uuid(),
      name: 'default',
      kind: 'anthropic',
      model: 'claude-sonnet-5-5',
      hasKey: true,
      monthlyTokenCap: 2_000_000,
      usedTokensThisMonth: 184_220,
      createdAt: ago(20 * 86400),
      updatedAt: ago(20 * 86400),
    },
  ];
  const base = (over: Partial<MockAIJob>, agoS: number): MockAIJob => ({
    id: uuid(),
    projectId,
    status: 'succeeded',
    stage: 'done',
    providerKind: 'anthropic',
    model: 'claude-sonnet-5-5',
    usage: { inputTokens: 18_824, outputTokens: 4_636 },
    dryRun: true,
    createdBy: args.ana,
    createdAt: ago(agoS),
    startedAt: ago(agoS - 2),
    finishedAt: ago(agoS - 95),
    approvedAt: null,
    round: 1,
    targetId,
    scenarioId: null,
    problems: [],
    journeys: [],
    approvedScenarioId: null,
    approvedVersion: null,
    maxRepairs: 3,
    ...over,
  });
  const jobs: MockAIJob[] = [
    base(
      {
        status: 'needs_review',
        round: 3,
        usage: { inputTokens: 41_206, outputTokens: 9_870 },
        scenarioId: smoke.id,
        yaml: proposalYaml,
        diff: diffAgainstSmoke,
        journeys: journeys(targetURL, false, 4),
        problems: [
          {
            journey: 'checkout',
            message:
              'dry run failed after 3 repair rounds: POST /api/checkout returned 422 (shippingAddress is required)',
          },
        ],
      },
      2 * 3600,
    ),
    base(
      {
        approvedAt: ago(86400 - 600),
        approvedScenarioId: smoke.id,
        approvedVersion: smoke.latestVersion.version,
        yaml: proposalYaml,
        scenarioId: smoke.id,
        diff: diffAgainstSmoke,
        journeys: journeys(targetURL, true, 2),
        createdBy: args.tom,
      },
      86400,
    ),
    base(
      {
        status: 'failed',
        stage: 'draft',
        round: 0,
        usage: { inputTokens: 6_102, outputTokens: 0 },
        error: 'AI provider anthropic: 401 Unauthorized: invalid x-api-key',
        finishedAt: ago(3 * 86400 - 4),
        createdBy: args.tom,
      },
      3 * 86400,
    ),
  ];
  return { providers, jobs };
}
