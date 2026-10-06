import { delay, http, HttpResponse, type JsonBodyType } from 'msw';
import type {
  ApiErrorBody,
  Integration,
  IntegrationCreate,
  NotificationChannelCreate,
  NotificationChannelCreated,
  NotificationDelivery,
  LoginRequest,
  ProjectCreate,
  RunCreate,
  ScenarioVersionCreate,
  SecretPut,
  Session,
  SetupRequest,
  TargetCreate,
  TokenCreate,
  TokenCreated,
  UserCreate,
  VersionInfo,
  Role,
} from '@/api/types';
import { isTerminal, activeStatuses } from '@/api/types';
import {
  createDb,
  elapsedOf,
  finish,
  me,
  publicUser,
  startRun,
  tick,
  uuid,
  type Db,
  type MockOptions,
} from './db';
import { analyse, simulatePoint, simulateTimeline } from './sim';

const B = '*/api/v1';

function err(status: number, code: string, message: string, details?: string[]) {
  const body: ApiErrorBody = { error: { code, message, ...(details ? { details } : {}) } };
  return HttpResponse.json(body, { status });
}

const notFound = (what: string) => err(404, 'not_found', `${what} not found.`);
const ok = (body: JsonBodyType, status = 200) => HttpResponse.json(body, { status });
const noContent = () => new HttpResponse(null, { status: 204 });

const nameRe = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$/;

const rank: Record<Role, number> = { viewer: 0, runner: 1, editor: 2, admin: 3, owner: 4 };

function slugify(s: string) {
  return (
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '') || 'project'
  );
}

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

  const handlers = [
    // ------------------------------------------------------------ system/auth
    http.get(`${B}/version`, () => {
      const v: VersionInfo = { ...db.version, setupRequired: db.users.length === 0 };
      return ok(v);
    }),
    http.post(`${B}/setup`, async ({ request }) => {
      if (db.users.length > 0) return err(409, 'conflict', 'Stampede is already set up.');
      const body = (await request.json()) as SetupRequest;
      const details: string[] = [];
      if (!body.organisation?.trim()) details.push('organisation: required');
      if (!body.name?.trim()) details.push('name: required');
      if (!/^[^\s@]+@[^\s@]+$/.test(body.email ?? '')) details.push('email: not a valid address');
      if ((body.password ?? '').length < 10) details.push('password: at least 10 characters');
      if (details.length) return err(422, 'invalid', 'The setup details are not valid.', details);
      const fresh = createDb({ setupRequired: false, signedIn: true });
      const owner = fresh.users[0]!;
      owner.name = body.name;
      owner.email = body.email;
      owner.password = body.password;
      fresh.orgName = body.organisation;
      fresh.meId = owner.id;
      db = fresh;
      return ok(sessionFor(db), 201);
    }),
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
    http.put(`${B}/me/password`, async ({ request }) => {
      const a = await auth();
      if (a) return a;
      const body = (await request.json()) as { current: string; new: string };
      const u = db.users.find((x) => x.id === db.meId)!;
      if (body.current !== u.password)
        return err(401, 'invalid_credentials', 'The current password is incorrect.');
      if (body.new.length < 10)
        return err(422, 'invalid', 'The new password is too short.', [
          'new: at least 10 characters',
        ]);
      u.password = body.new;
      return noContent();
    }),

    // ------------------------------------------------------------ users
    http.get(`${B}/users`, async () => (await auth()) ?? ok(db.users.map(publicUser))),
    http.post(`${B}/users`, async ({ request }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const body = (await request.json()) as UserCreate;
      if (db.users.some((u) => u.email === body.email))
        return err(409, 'conflict', `${body.email} is already a member.`);
      if (rank[body.role] > rank[role()])
        return err(403, 'forbidden', 'You cannot grant a role above your own.');
      const u = {
        id: uuid(),
        name: body.name,
        email: body.email,
        role: body.role,
        createdAt: new Date().toISOString(),
        lastLoginAt: null,
        password: body.password,
      };
      db.users.push(u);
      return ok(publicUser(u), 201);
    }),
    http.patch(`${B}/users/:id`, async ({ request, params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const u = db.users.find((x) => x.id === params.id);
      if (!u) return notFound('User');
      const body = (await request.json()) as { name?: string; role?: Role };
      if (
        u.role === 'owner' &&
        body.role &&
        body.role !== 'owner' &&
        db.users.filter((x) => x.role === 'owner').length === 1
      )
        return err(409, 'conflict', 'The organisation needs at least one owner.');
      Object.assign(u, body);
      return ok(publicUser(u));
    }),
    http.delete(`${B}/users/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.users.some((x) => x.id === params.id)) return notFound('User');
      db.users = db.users.filter((x) => x.id !== params.id);
      return noContent();
    }),

    // ------------------------------------------------------------ tokens
    http.get(`${B}/tokens`, async () => (await auth()) ?? ok(db.tokens)),
    http.post(`${B}/tokens`, async ({ request }) => {
      const a = await auth();
      if (a) return a;
      const body = (await request.json()) as TokenCreate;
      const secret = `stp_${uuid().replace(/-/g, '')}${uuid().replace(/-/g, '').slice(0, 12)}`;
      const t: TokenCreated = {
        id: uuid(),
        name: body.name,
        prefix: secret.slice(0, 8),
        role: body.role ?? role(),
        createdAt: new Date().toISOString(),
        lastUsedAt: null,
        expiresAt: body.expiresInDays
          ? new Date(Date.now() + body.expiresInDays * 86400_000).toISOString()
          : null,
        secret,
      };
      const { secret: _s, ...stored } = t;
      db.tokens.unshift(stored);
      return ok(t, 201);
    }),
    http.delete(`${B}/tokens/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      if (!db.tokens.some((t) => t.id === params.id)) return notFound('Token');
      db.tokens = db.tokens.filter((t) => t.id !== params.id);
      return noContent();
    }),

    // ------------------------------------------------------------ projects
    http.get(`${B}/projects`, async () => (await auth()) ?? ok(db.projects)),
    http.post(`${B}/projects`, async ({ request }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const body = (await request.json()) as ProjectCreate;
      const slug = slugify(body.name);
      if (db.projects.some((p) => p.slug === slug))
        return err(409, 'conflict', `A project called ${body.name} already exists.`);
      const p = {
        id: uuid(),
        name: body.name,
        slug,
        ...(body.description ? { description: body.description } : {}),
        createdAt: new Date().toISOString(),
      };
      db.projects.push(p);
      db.secrets[p.id] = [];
      return ok(p, 201);
    }),
    http.get(`${B}/projects/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const p = db.projects.find((x) => x.id === params.id);
      return p ? ok(p) : notFound('Project');
    }),
    http.patch(`${B}/projects/:id`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const p = db.projects.find((x) => x.id === params.id);
      if (!p) return notFound('Project');
      Object.assign(p, (await request.json()) as ProjectCreate);
      return ok(p);
    }),
    http.delete(`${B}/projects/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      db.projects = db.projects.filter((x) => x.id !== params.id);
      return noContent();
    }),

    // ------------------------------------------------------------ targets
    http.get(
      `${B}/projects/:id/targets`,
      async ({ params }) =>
        (await auth()) ?? ok(db.targets.filter((t) => t.projectId === params.id)),
    ),
    http.post(`${B}/projects/:id/targets`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const body = (await request.json()) as TargetCreate;
      let host = '';
      try {
        host = new URL(body.baseURL).hostname;
      } catch {
        return err(422, 'invalid', 'The target is not valid.', ['baseURL: not an absolute URL']);
      }
      const priv = /^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host);
      const t = {
        id: uuid(),
        projectId: String(params.id),
        name: body.name,
        baseURL: body.baseURL,
        private: priv,
        verified: priv,
        verifiedAt: null,
        verificationMethod: null,
        verificationToken: `stp-v-${uuid().replace(/-/g, '').slice(0, 24)}`,
        allowHosts: body.allowHosts ?? [],
        caps: priv
          ? (body.caps ?? {})
          : { maxRate: 5, maxVUs: 10, maxDurationSeconds: 60, ...body.caps },
        createdAt: new Date().toISOString(),
      };
      db.targets.push(t);
      return ok(t, 201);
    }),
    http.get(`${B}/targets/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const t = db.targets.find((x) => x.id === params.id);
      return t ? ok(t) : notFound('Target');
    }),
    http.patch(`${B}/targets/:id`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const t = db.targets.find((x) => x.id === params.id);
      if (!t) return notFound('Target');
      const body = (await request.json()) as TargetCreate;
      Object.assign(t, body, { caps: body.caps ?? t.caps });
      return ok(t);
    }),
    http.delete(`${B}/targets/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      db.targets = db.targets.filter((x) => x.id !== params.id);
      return noContent();
    }),
    http.post(`${B}/targets/:id/verify`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const t = db.targets.find((x) => x.id === params.id);
      if (!t) return notFound('Target');
      await delay(600);
      // The mock never finds the token, except for hosts ending in .test.
      if (new URL(t.baseURL).hostname.endsWith('.test')) {
        Object.assign(t, {
          verified: true,
          verifiedAt: new Date().toISOString(),
          verificationMethod: 'dns-txt',
        });
      }
      return ok(t);
    }),

    // ------------------------------------------------------------ secrets
    http.get(
      `${B}/projects/:id/secrets`,
      async ({ params }) => (await auth()) ?? ok(db.secrets[String(params.id)] ?? []),
    ),
    http.put(`${B}/projects/:id/secrets`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const body = (await request.json()) as SecretPut;
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(body.name))
        return err(422, 'invalid', 'The secret is not valid.', [
          'name: letters, digits and underscores only',
        ]);
      const list = (db.secrets[String(params.id)] ??= []);
      const s = { name: body.name, updatedAt: new Date().toISOString() };
      const i = list.findIndex((x) => x.name === body.name);
      if (i >= 0) list[i] = s;
      else list.push(s);
      return ok(s);
    }),
    http.delete(`${B}/projects/:id/secrets/:name`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const list = db.secrets[String(params.id)] ?? [];
      if (!list.some((s) => s.name === params.name)) return notFound('Secret');
      db.secrets[String(params.id)] = list.filter((s) => s.name !== params.name);
      return noContent();
    }),

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
    http.post(`${B}/projects/:id/scenarios`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const body = (await request.json()) as ScenarioVersionCreate;
      const an = analyse(body.yaml);
      if (!an.validation.valid)
        return err(422, 'invalid', 'The scenario has problems.', an.validation.problems);
      if (db.scenarios.some((s) => s.projectId === params.id && s.name === an.name))
        return err(409, 'conflict', `A scenario named ${an.name} already exists in this project.`);
      const id = uuid();
      const now = new Date().toISOString();
      const v = {
        scenarioId: id,
        version: 1,
        yaml: body.yaml,
        message: body.message ?? 'Initial version',
        createdBy: me(db)!.email,
        createdAt: now,
        ...(an.validation.plan ? { plan: an.validation.plan } : {}),
      };
      const s = {
        id,
        projectId: String(params.id),
        name: an.name!,
        ...(an.description ? { description: an.description } : {}),
        tags: an.tags,
        latestVersion: v,
        createdAt: now,
        updatedAt: now,
      };
      db.scenarios.push(s);
      db.versions[id] = [v];
      return ok(s, 201);
    }),
    http.get(`${B}/scenarios/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const s = db.scenarios.find((x) => x.id === params.id);
      return s ? ok(s) : notFound('Scenario');
    }),
    http.delete(`${B}/scenarios/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      db.scenarios = db.scenarios.filter((x) => x.id !== params.id);
      return noContent();
    }),
    http.get(`${B}/scenarios/:id/versions`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const vs = db.versions[String(params.id)];
      return vs ? ok(vs) : notFound('Scenario');
    }),
    http.post(`${B}/scenarios/:id/versions`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const s = db.scenarios.find((x) => x.id === params.id);
      if (!s) return notFound('Scenario');
      const body = (await request.json()) as ScenarioVersionCreate;
      const an = analyse(body.yaml);
      if (!an.validation.valid)
        return err(422, 'invalid', 'The scenario has problems.', an.validation.problems);
      const v = {
        scenarioId: s.id,
        version: s.latestVersion.version + 1,
        yaml: body.yaml,
        ...(body.message ? { message: body.message } : {}),
        createdBy: me(db)!.email,
        createdAt: new Date().toISOString(),
        ...(an.validation.plan ? { plan: an.validation.plan } : {}),
      };
      db.versions[s.id]!.unshift(v);
      Object.assign(s, {
        latestVersion: v,
        updatedAt: v.createdAt,
        tags: an.tags,
        ...(an.description ? { description: an.description } : {}),
      });
      return ok(v, 201);
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
    http.post(`${B}/projects/:id/runs`, async ({ request }) => {
      const a = (await auth()) ?? need('runner');
      if (a) return a;
      const body = (await request.json()) as RunCreate;
      const s = db.scenarios.find((x) => x.id === body.scenarioId);
      const t = db.targets.find((x) => x.id === body.targetId);
      if (!s || !t)
        return err(422, 'invalid', 'The run is not valid.', [
          !s ? 'scenarioId: unknown scenario' : 'targetId: unknown target',
        ]);
      if (body.overrides?.rate && !/^[0-9.]+(\/(s|m|h))?$/.test(body.overrides.rate))
        return err(422, 'invalid', 'The run is not valid.', [
          `overrides.rate: "${body.overrides.rate}" is not a rate`,
        ]);
      const run = startRun(db, s, t, {
        ...(body.note ? { note: body.note } : {}),
        ...(body.workers ? { workers: body.workers } : {}),
        ...(body.overrides ? { overrides: body.overrides } : {}),
        ...(body.version ? { version: body.version } : {}),
        createdBy: me(db)!.email,
      });
      for (const w of db.workers)
        if (w.status === 'idle') {
          w.status = 'busy';
          w.runId = run.id;
        }
      return ok(run, 201);
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
      };
      const [ct, ext] = types[format] ?? ['text/plain', 'txt'];
      const text =
        format === 'html'
          ? `<!doctype html><title>${rep.scenario}</title><h1>${rep.scenario}: ${rep.verdict}</h1><p>Mock report.</p>`
          : format === 'junit'
            ? `<?xml version="1.0"?><testsuite name="${rep.scenario}" tests="${rep.thresholds?.length ?? 0}"></testsuite>`
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
    http.get(`${B}/workers`, async () => {
      const a = await auth();
      if (a) return a;
      const now = new Date().toISOString();
      for (const w of db.workers) if (w.status !== 'lost') w.lastSeenAt = now;
      return ok(db.workers);
    }),
    http.get(`${B}/audit`, async () => (await auth()) ?? need('admin') ?? ok(db.audit)),

    // ------------------------------------------------------------ integrations
    http.get(`${B}/integrations`, async () => (await auth()) ?? ok(db.integrations)),
    http.post(`${B}/integrations`, async ({ request }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const body = (await request.json()) as IntegrationCreate;
      if (!nameRe.test(body.name ?? '')) return err(422, 'invalid', 'name is not valid');
      if (db.integrations.some((i) => i.name === body.name)) {
        return err(409, 'conflict', `an integration named "${body.name}" already exists`);
      }
      if (body.kind === 'traces' && !body.url.includes('{traceId}')) {
        return err(422, 'invalid', 'url must contain {traceId}');
      }
      const now = new Date().toISOString();
      const i: Integration = {
        id: uuid(),
        name: body.name,
        kind: body.kind,
        url: body.url.replace(/\/+$/, ''),
        hasToken: !!body.bearerToken,
        createdAt: now,
        updatedAt: now,
      };
      db.integrations = [...db.integrations, i].sort((x, y) => x.name.localeCompare(y.name));
      return ok(i, 201);
    }),
    http.delete(`${B}/integrations/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.integrations.some((i) => i.id === params.id)) return notFound('Integration');
      db.integrations = db.integrations.filter((i) => i.id !== params.id);
      return noContent();
    }),
    http.get(`${B}/notifications/channels`, async () => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      return ok(db.channels.map(({ url: _u, ...c }) => c));
    }),
    http.post(`${B}/notifications/channels`, async ({ request }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const body = (await request.json()) as NotificationChannelCreate;
      if (!nameRe.test(body.name ?? '')) return err(422, 'invalid', 'name is not valid');
      if (db.channels.some((c) => c.name === body.name)) {
        return err(409, 'conflict', `a channel named "${body.name}" already exists`);
      }
      let u: URL;
      try {
        u = new URL(body.url);
      } catch {
        return err(422, 'invalid', 'url: invalid URL');
      }
      if (!body.allowPrivate && /^(localhost|127\.|10\.|192\.168\.|169\.254\.)/.test(u.hostname)) {
        return err(
          422,
          'invalid',
          'the URL points to a private, loopback or link-local address; set allowPrivate to deliver there',
        );
      }
      const secret =
        body.kind === 'webhook'
          ? body.secret ||
            `whsec_${uuid().replace(/-/g, '')}${uuid().replace(/-/g, '').slice(0, 11)}`
          : undefined;
      const channel = {
        id: uuid(),
        name: body.name,
        kind: body.kind,
        events: body.events ?? ['run.finished', 'run.target_failed', 'run.killed'],
        allowPrivate: body.allowPrivate ?? false,
        urlHint: `${u.protocol}//${u.host}`,
        hasSecret: !!secret,
        createdAt: new Date().toISOString(),
      };
      db.channels = [...db.channels, { ...channel, url: body.url }].sort((x, y) =>
        x.name.localeCompare(y.name),
      );
      db.deliveries[channel.id] = [];
      const out: NotificationChannelCreated = { channel, ...(secret ? { secret } : {}) };
      return ok(out, 201);
    }),
    http.delete(`${B}/notifications/channels/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.channels.some((c) => c.id === params.id)) return notFound('Notification channel');
      db.channels = db.channels.filter((c) => c.id !== params.id);
      db.deliveries = Object.fromEntries(
        Object.entries(db.deliveries).filter(([k]) => k !== params.id),
      );
      return noContent();
    }),
    http.post(`${B}/notifications/channels/:id/test`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const c = db.channels.find((x) => x.id === params.id);
      if (!c) return notFound('Notification channel');
      const log = db.deliveries[c.id] ?? [];
      // A channel whose URL mentions "fail" demonstrates a failed delivery.
      const failed = c.url.includes('fail');
      const d: NotificationDelivery = {
        id: (log[0]?.id ?? 0) + 1,
        deliveryId: uuid(),
        event: 'test',
        runId: null,
        attempt: 1,
        ok: !failed,
        statusCode: failed ? 404 : 200,
        error: failed ? 'HTTP 404: no_service' : '',
        durationMs: 140 + Math.round(Math.random() * 120),
        at: new Date().toISOString(),
      };
      db.deliveries[c.id] = [d, ...log].slice(0, 50);
      c.lastDelivery = d;
      return ok(d);
    }),
    http.get(`${B}/notifications/channels/:id/deliveries`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.channels.some((c) => c.id === params.id)) return notFound('Notification channel');
      return ok(db.deliveries[params.id as string] ?? []);
    }),
  ];

  return { handlers, getDb: () => db, reset: (o: MockOptions) => (db = createDb(o)) };
}
