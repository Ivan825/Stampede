import { delay, http, HttpResponse, type JsonBodyType } from 'msw';
import type {
  AIJob,
  AIJobSummary,
  CompareRequest,
  CoverageRequest,
  DriftRequest,
  ProjectRole,
  ApiErrorBody,
  LoginRequest,
  Schedule,
  Session,
  VersionInfo,
  Role,
} from '@/api/types';
import { isTerminal, activeStatuses } from '@/api/types';
import { createDb, elapsedOf, finish, me, publicUser, tick, type Db, type MockOptions } from './db';
import { advanceAIJob, type MockAIJob } from './ai';
import { driftSummary } from './drift';
import { compareReports } from './compare';
import { analyse, simulatePoint, simulateTimeline } from './sim';
import {
  coverageOf,
  driftOf,
  endpointsOf,
  limitSettings,
  mockPacks,
  mockShopSpec,
  mockSSO,
  packDetail,
  runWorkerHealth,
  scenarioYaml,
  type Endpoint,
} from './library';

const B = '*/api/v1';

function err(status: number, code: string, message: string, details?: string[]) {
  const body: ApiErrorBody = { error: { code, message, ...(details ? { details } : {}) } };
  return HttpResponse.json(body, { status });
}

const notFound = (what: string) => err(404, 'not_found', `${what} not found.`);
const ok = (body: JsonBodyType, status = 200) => HttpResponse.json(body, { status });
const noContent = () => new HttpResponse(null, { status: 204 });

const rank: Record<Role, number> = { viewer: 0, runner: 1, editor: 2, admin: 3, owner: 4 };

const sessionFor = (db: Db): Session => ({
  user: me(db)!,
  expiresAt: new Date(Date.now() + 7 * 86400_000).toISOString(),
});

export interface MockHandlerOptions {
  /** Artificial latency in ms for every request (browser demo only). */
  latency?: number;
  /** Interval between live points in ms. */
  liveIntervalMs?: number;
}

export function createHandlers(initial: MockOptions, opts: MockHandlerOptions = {}) {
  let db = createDb(initial);
  const latency = opts.latency ?? 0;
  const liveMs = opts.liveIntervalMs ?? 1000;

  /** Guards every authenticated endpoint. */
  const auth = async (): Promise<Response | null> => {
    if (latency) await delay(latency);
    tick(db);
    if (!me(db)) return err(401, 'unauthorized', 'Sign in to continue.');
    return null;
  };
  const role = () => me(db)!.role;
  const need = (min: Role) =>
    rank[role()] >= rank[min]
      ? null
      : err(403, 'forbidden', `Your role (${role()}) cannot do this; it needs ${min} or above.`);

  /** The signed-in user's role in a project: an override, or their organisation role. */
  const roleIn = (projectId: string): Role => {
    const u = me(db)!;
    if (u.role === 'owner') return 'owner';
    return db.projectRoles[projectId]?.find((x) => x.userId === u.id)?.role ?? u.role;
  };
  const projectRoles = (projectId: string): ProjectRole[] =>
    (db.projectRoles[projectId] ?? []).flatMap((o) => {
      const u = db.users.find((x) => x.id === o.userId);
      return u
        ? [
            {
              userId: u.id,
              email: u.email,
              name: u.name,
              role: o.role,
              orgRole: u.role,
              createdAt: o.createdAt,
            },
          ]
        : [];
    });
  /** Moves an AI job along the pipeline according to the wall clock. */
  const advance = (j: MockAIJob) =>
    advanceAIJob(
      j,
      db.targets.find((t) => t.id === j.targetId)?.baseURL ?? 'http://localhost:8090',
    );
  const aiPublic = (j: MockAIJob): AIJob => {
    const { maxRepairs: _m, ...rest } = j;
    return rest;
  };
  const aiSummary = (j: MockAIJob): AIJobSummary => ({
    id: j.id,
    projectId: j.projectId,
    status: j.status,
    stage: j.stage,
    providerKind: j.providerKind,
    model: j.model,
    usage: j.usage,
    dryRun: j.dryRun,
    ...(j.error ? { error: j.error } : {}),
    createdBy: j.createdBy ?? null,
    createdAt: j.createdAt,
    finishedAt: j.finishedAt ?? null,
    approvedAt: j.approvedAt ?? null,
  });

  /** A schedule with its last run's state filled in from the runs. */
  const scheduleView = (sc: Schedule): Schedule => {
    const r = sc.lastRunId ? db.runs.find((x) => x.id === sc.lastRunId) : undefined;
    return {
      ...sc,
      lastRunStatus: r?.status,
      lastRunVerdict: r?.verdict ?? null,
      lastRunAt: r?.createdAt ?? null,
    };
  };
  /**
   * An OpenAPI document given inline or by URL on a project target's host
   * (where every mock target serves the shop API); an error response, or
   * null when neither is given.
   */
  const specFor = (
    projectId: string,
    inline: string | undefined,
    url: string | undefined,
    what: string,
  ): Endpoint[] | Response | null => {
    let text: string | undefined;
    if (inline?.trim()) text = inline;
    else if (url?.trim()) {
      const r = need('editor');
      if (r) return r;
      let host = '';
      try {
        host = new URL(url).hostname;
      } catch {
        return err(422, 'invalid', `${what} URL must be an absolute http(s) URL`);
      }
      const hosts = db.targets
        .filter((t) => t.projectId === projectId)
        .flatMap((t) => [new URL(t.baseURL).hostname, ...(t.allowHosts ?? [])]);
      if (!hosts.includes(host))
        return err(
          422,
          'invalid',
          `${what} URL: ${host} is not the host of a target in this project; add the target first or paste the document`,
        );
      text = mockShopSpec;
    }
    if (!text) return null;
    const eps = endpointsOf(text);
    return typeof eps === 'string' ? err(422, 'invalid', eps.replace('openapi', what)) : eps;
  };

  const handlers = [
    // ------------------------------------------------------------ system/auth
    http.get(`${B}/version`, () => {
      const v: VersionInfo = { ...db.version, setupRequired: db.users.length === 0 };
      return ok(v);
    }),
    http.get(`${B}/auth/config`, () =>
      ok(
        new URL(location.href).searchParams.get('mock') === 'sso'
          ? { password: true, sso: { name: 'Acme SSO', loginURL: '/api/v1/auth/oidc/login' } }
          : { password: true },
      ),
    ),
    http.post(`${B}/auth/login`, async ({ request }) => {
      if (latency) await delay(latency);
      const body = (await request.json()) as LoginRequest;
      const u = db.users.find((x) => x.email.toLowerCase() === body.email?.toLowerCase());
      if (!u || body.password !== u.password) {
        return err(401, 'invalid_credentials', 'The email or password is incorrect.');
      }
      db.meId = u.id;
      u.lastLoginAt = new Date().toISOString();
      return ok(sessionFor(db));
    }),
    http.post(`${B}/auth/logout`, () => {
      db.meId = null;
      return noContent();
    }),
    http.get(`${B}/me`, async () => (await auth()) ?? ok(me(db))),

    // ------------------------------------------------------------ users
    http.get(`${B}/users`, async () => (await auth()) ?? ok(db.users.map(publicUser))),

    // ------------------------------------------------------------ tokens
    http.get(`${B}/tokens`, async () => (await auth()) ?? ok(db.tokens)),

    // ------------------------------------------------------------ projects
    http.get(
      `${B}/projects`,
      async () => (await auth()) ?? ok(db.projects.map((p) => ({ ...p, role: roleIn(p.id) }))),
    ),
    http.get(`${B}/projects/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const p = db.projects.find((x) => x.id === params.id);
      return p ? ok({ ...p, role: roleIn(p.id) }) : notFound('Project');
    }),

    // ------------------------------------------------------------ caps, gate, project roles
    http.get(`${B}/organisation/caps`, async () => (await auth()) ?? ok(db.orgCaps)),
    http.get(`${B}/projects/:id/settings`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const id = String(params.id);
      if (!db.projects.some((p) => p.id === id)) return notFound('Project');
      return ok(db.projectSettings[id] ?? { caps: {}, requireDryRun: false });
    }),
    http.get(`${B}/projects/:id/roles`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const id = String(params.id);
      if (!db.projects.some((p) => p.id === id)) return notFound('Project');
      return ok(projectRoles(id));
    }),

    // ------------------------------------------------------------ targets
    http.get(
      `${B}/projects/:id/targets`,
      async ({ params }) =>
        (await auth()) ?? ok(db.targets.filter((t) => t.projectId === params.id)),
    ),
    http.get(`${B}/targets/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const t = db.targets.find((x) => x.id === params.id);
      return t ? ok(t) : notFound('Target');
    }),

    // ------------------------------------------------------------ secrets
    http.get(
      `${B}/projects/:id/secrets`,
      async ({ params }) => (await auth()) ?? ok(db.secrets[String(params.id)] ?? []),
    ),

    // ------------------------------------------------------------ scenarios
    http.post(`${B}/scenarios/validate`, async ({ request }) => {
      const a = await auth();
      if (a) return a;
      const body = (await request.json()) as { yaml: string };
      return ok(analyse(body.yaml).validation);
    }),
    http.get(`${B}/projects/:id/scenarios`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const tag = new URL(request.url).searchParams.get('tag');
      return ok(
        db.scenarios.filter((s) => s.projectId === params.id && (!tag || s.tags.includes(tag))),
      );
    }),
    http.get(`${B}/scenarios/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const s = db.scenarios.find((x) => x.id === params.id);
      return s ? ok(s) : notFound('Scenario');
    }),
    http.get(`${B}/scenarios/:id/versions`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const vs = db.versions[String(params.id)];
      return vs ? ok(vs) : notFound('Scenario');
    }),
    http.get(`${B}/scenarios/:id/versions/:v`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const v = db.versions[String(params.id)]?.find((x) => x.version === Number(params.v));
      return v ? ok(v) : notFound('Version');
    }),

    // ------------------------------------------------------------ runs
    http.get(`${B}/projects/:id/runs`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const q = new URL(request.url).searchParams;
      const limit = Number(q.get('limit') ?? 50);
      const before = q.get('before');
      const scenarioId = q.get('scenarioId');
      const runs = db.runs
        .filter((r) => r.projectId === params.id)
        .filter((r) => !scenarioId || r.scenarioId === scenarioId)
        .filter((r) => !before || Date.parse(r.createdAt) < Date.parse(before))
        .sort((x, y) => Date.parse(y.createdAt) - Date.parse(x.createdAt))
        .slice(0, limit);
      return ok(runs);
    }),
    http.get(`${B}/runs/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      return r ? ok(r) : notFound('Run');
    }),
    http.post(`${B}/runs/kill-all`, async () => {
      const a = (await auth()) ?? need('runner');
      if (a) return a;
      const killed: string[] = [];
      for (const r of db.runs)
        if (activeStatuses.includes(r.status)) {
          finish(db, r, `killed by ${me(db)!.email}`, 'aborted');
          killed.push(r.id);
        }
      return ok({ killed });
    }),
    http.post(`${B}/runs/:id/stop`, async ({ params }) => {
      const a = (await auth()) ?? need('runner');
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      if (isTerminal(r.status)) return err(409, 'conflict', `The run is already ${r.status}.`);
      r.status = 'stopping';
      return new HttpResponse(null, { status: 202 });
    }),
    http.post(`${B}/runs/:id/kill`, async ({ params }) => {
      const a = (await auth()) ?? need('runner');
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      if (!isTerminal(r.status)) finish(db, r, `killed by ${me(db)!.email}`, 'aborted');
      return new HttpResponse(null, { status: 202 });
    }),
    http.get(`${B}/runs/:id/events`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      // Stored newest first; the API lists them oldest first.
      return ok([...(db.events[r.id] ?? [])].reverse());
    }),
    http.get(`${B}/runs/:id/timeline`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      const sim = db.sims[r.id]!;
      if (isTerminal(r.status)) return ok(db.reports[r.id]?.timeline ?? []);
      return ok(simulateTimeline(sim, Math.floor(elapsedOf(r))));
    }),
    http.get(`${B}/runs/:id/report`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      const rep = db.reports[r.id];
      if (!rep) return err(409, 'conflict', `The run is ${r.status}; there is no report.`);
      const format = new URL(request.url).searchParams.get('format') ?? 'json';
      const file = `stampede-${rep.scenario}-${r.id.slice(0, 8)}`;
      if (format === 'json') return ok(rep);
      const types: Record<string, [string, string]> = {
        html: ['text/html', 'html'],
        junit: ['application/xml', 'xml'],
        markdown: ['text/markdown', 'md'],
        csv: ['text/csv', 'csv'],
        'timeline-csv': ['text/csv', 'csv'],
      };
      const [ct, ext] = types[format] ?? ['text/plain', 'txt'];
      const text =
        format === 'html'
          ? `<!doctype html><title>${rep.scenario}</title><h1>${rep.scenario}: ${rep.verdict}</h1><p>Mock report.</p>`
          : format === 'junit'
            ? `<?xml version="1.0"?><testsuite name="${rep.scenario}" tests="${rep.thresholds?.length ?? 0}"></testsuite>`
            : format === 'csv'
              ? `journey,step,requests\n,,${rep.overall.requests}\n`
              : format === 'timeline-csv'
                ? `t_s,rps\n`
                : `### Stampede: ${rep.scenario} — ${rep.verdict}\n`;
      return new HttpResponse(text, {
        headers: {
          'Content-Type': ct,
          'Content-Disposition': `attachment; filename="${file}.${ext}"`,
        },
      });
    }),
    http.get(`${B}/runs/:id/live`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      const enc = new TextEncoder();
      const sim = db.sims[r.id]!;
      let timer: ReturnType<typeof setInterval> | undefined;
      const sentEvents = new Set<string>();
      const stream = new ReadableStream<Uint8Array>({
        start(ctrl) {
          const send = (event: string, data: unknown) => {
            try {
              ctrl.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
            } catch {
              clearInterval(timer);
            }
          };
          send('status', r);
          let lastT = Math.floor(elapsedOf(r)) - 1;
          const step = () => {
            tick(db);
            for (const e of [...(db.events[r.id] ?? [])].reverse()) {
              const k = `${e.at}${e.type}`;
              if (!sentEvents.has(k)) {
                sentEvents.add(k);
                send('event', e);
              }
            }
            if (isTerminal(r.status)) {
              send('status', r);
              clearInterval(timer);
              try {
                ctrl.close();
              } catch {
                /* already closed */
              }
              return;
            }
            if (r.status === 'running') {
              const t = Math.floor(elapsedOf(r));
              for (let x = Math.max(0, lastT + 1); x <= t && x < sim.duration; x++)
                send('point', simulatePoint(sim, x));
              lastT = t;
            }
          };
          step();
          timer = setInterval(step, liveMs);
          request.signal.addEventListener('abort', () => clearInterval(timer));
        },
        cancel() {
          clearInterval(timer);
        },
      });
      return new HttpResponse(stream, {
        headers: {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
      });
    }),

    // ------------------------------------------------------------ workers/audit
    // ------------------------------------------------------------ schedules
    http.get(`${B}/projects/:id/schedules`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      return ok(
        db.schedules
          .filter((x) => x.projectId === params.id)
          .sort((x, y) => x.name.localeCompare(y.name))
          .map(scheduleView),
      );
    }),
    http.get(`${B}/schedules/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const sc = db.schedules.find((x) => x.id === params.id);
      return sc ? ok(scheduleView(sc)) : notFound('Schedule');
    }),

    http.get(`${B}/projects/:id/drift-results`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const q = new URL(request.url).searchParams;
      const scheduleId = q.get('scheduleId');
      return ok(
        db.driftResults
          .filter((r) => r.projectId === params.id)
          .filter((r) => !scheduleId || r.scheduleId === scheduleId)
          .slice(0, Number(q.get('limit') ?? 50))
          .map(driftSummary),
      );
    }),
    http.get(`${B}/drift-results/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const r = db.driftResults.find((x) => x.id === params.id);
      return r ? ok(r) : notFound('drift result');
    }),

    http.get(`${B}/workers`, async () => {
      const a = await auth();
      if (a) return a;
      const now = new Date().toISOString();
      for (const w of db.workers) if (w.status !== 'lost') w.lastSeenAt = now;
      return ok(db.workers);
    }),
    // ------------------------------------------------------------ packs
    http.get(`${B}/packs`, async () => (await auth()) ?? ok(mockPacks)),
    http.get(`${B}/packs/:name`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const p = packDetail(String(params.name));
      return p ? ok(p) : notFound('pack');
    }),

    // ------------------------------------------------------------ coverage and drift
    http.post(`${B}/scenarios/:id/coverage`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const sc = db.scenarios.find((x) => x.id === params.id);
      if (!sc) return notFound('scenario');
      const b = (await request.json()) as CoverageRequest;
      const ver = scenarioYaml(db, sc, b.version);
      if (!ver) return notFound('scenario version');
      const spec = specFor(sc.projectId, b.openapi, b.specURL, 'openapi');
      if (spec instanceof Response) return spec;
      if (!spec) return err(422, 'invalid', 'give the API as openapi or specURL');
      return ok(coverageOf(ver.yaml, spec, ver.version));
    }),
    http.post(`${B}/scenarios/:id/drift`, async ({ params, request }) => {
      const a = await auth();
      if (a) return a;
      const sc = db.scenarios.find((x) => x.id === params.id);
      if (!sc) return notFound('scenario');
      const b = (await request.json()) as DriftRequest;
      if (b.targetId) {
        const r = need('runner');
        if (r) return r;
        if (!db.targets.some((t) => t.id === b.targetId && t.projectId === sc.projectId))
          return err(422, 'invalid', 'target not found in this project');
      }
      const ver = scenarioYaml(db, sc, b.version);
      if (!ver) return notFound('scenario version');
      const cur = specFor(sc.projectId, b.openapi, b.specURL, 'openapi');
      if (cur instanceof Response) return cur;
      if (!cur) return err(422, 'invalid', 'give the current API as openapi or specURL');
      const prev = specFor(sc.projectId, b.previousOpenapi, b.previousSpecURL, 'previousOpenapi');
      if (prev instanceof Response) return prev;
      if (b.targetId) await delay(latency ? 1200 : 0);
      return ok(driftOf(ver.yaml, cur, prev, ver.version, !!b.targetId));
    }),

    // ------------------------------------------------------------ settings
    http.get(`${B}/settings/sso`, async () => (await auth()) ?? need('admin') ?? ok(mockSSO)),
    http.get(
      `${B}/settings/limits`,
      async () => (await auth()) ?? need('admin') ?? ok(limitSettings(db)),
    ),

    http.get(`${B}/runs/:id/workers`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      return ok(runWorkerHealth(db, r));
    }),
    http.get(`${B}/audit`, async () => (await auth()) ?? need('admin') ?? ok(db.audit)),

    // ------------------------------------------------------------ integrations
    http.get(`${B}/integrations`, async () => (await auth()) ?? ok(db.integrations)),
    http.get(`${B}/notifications/channels`, async () => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      return ok(db.channels.map(({ url: _u, ...c }) => c));
    }),
    http.get(`${B}/notifications/channels/:id/deliveries`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.channels.some((c) => c.id === params.id)) return notFound('Notification channel');
      return ok(db.deliveries[params.id as string] ?? []);
    }),

    // ------------------------------------------------------------ compare
    http.post(`${B}/compare`, async ({ request }) => {
      const a = await auth();
      if (a) return a;
      const body = (await request.json()) as CompareRequest;
      for (const [side, ids] of [
        ['a', body.a],
        ['b', body.b],
      ] as const)
        if (!Array.isArray(ids) || ids.length < 1 || ids.length > 20)
          return err(422, 'invalid', `${side} must list 1 to 20 runs`);
      const seen = new Set<string>();
      const problems: string[] = [];
      const load = (ids: string[]) =>
        ids.flatMap((id) => {
          if (seen.has(id)) {
            problems.push(`run ${id} is listed more than once`);
            return [];
          }
          seen.add(id);
          const r = db.runs.find((x) => x.id === id);
          const rep = db.reports[id];
          if (!r) problems.push(`run ${id} not found`);
          else if (r.status === 'failed')
            problems.push(`run ${id.slice(0, 8)} failed before producing a report`);
          else if (!isTerminal(r.status))
            problems.push(`run ${id.slice(0, 8)} has not finished (${r.status})`);
          else if (!rep) problems.push(`run ${id.slice(0, 8)} has no report`);
          return r && rep && isTerminal(r.status) ? [rep] : [];
        });
      const ra = load(body.a);
      const rb = load(body.b);
      if (problems.length)
        return err(
          422,
          'invalid',
          'every run must be in your organisation and have finished with a report',
          problems,
        );
      return ok(compareReports(ra, rb, body.labelA?.trim() || 'A', body.labelB?.trim() || 'B'));
    }),

    // ------------------------------------------------------------ AI
    http.get(`${B}/ai/providers`, async () => (await auth()) ?? ok(db.aiProviders)),
    http.get(`${B}/projects/:id/ai/jobs`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      return ok(
        db.aiJobs
          .filter((j) => j.projectId === params.id)
          .map((j) => {
            advance(j);
            return aiSummary(j);
          })
          .sort((x, y) => Date.parse(y.createdAt) - Date.parse(x.createdAt)),
      );
    }),
    http.get(`${B}/ai/jobs/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const j = db.aiJobs.find((x) => x.id === params.id);
      if (!j) return notFound('AI job');
      advance(j);
      return ok(aiPublic(j));
    }),
  ];

  return { handlers, getDb: () => db, reset: (o: MockOptions) => (db = createDb(o)) };
}
