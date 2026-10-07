import { delay, http, HttpResponse, type JsonBodyType } from 'msw';
import type {
  AIJob,
  AIJobApprove,
  AIJobCreate,
  AIJobSummary,
  AIProvider,
  AIProviderPut,
  Caps,
  CompareRequest,
  CoverageRequest,
  DriftRepair,
  DriftRequest,
  ProjectRole,
  ProjectSettings,
  Scenario,
  ApiErrorBody,
  Integration,
  IntegrationCreate,
  NotificationChannelCreate,
  NotificationChannelCreated,
  NotificationDelivery,
  LoginRequest,
  ProjectCreate,
  RunCreate,
  Schedule,
  ScheduleCreate,
  ScheduleUpdate,
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
import { checkZone, nextTimes, parseCron } from './cron';
import { advanceAIJob, type MockAIJob } from './ai';
import { driftCheck, driftSummary } from './drift';
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

  /** The signed-in user's role in a project: an override, or their organisation role. */
  const roleIn = (projectId: string): Role => {
    const u = me(db)!;
    if (u.role === 'owner') return 'owner';
    return db.projectRoles[projectId]?.find((x) => x.userId === u.id)?.role ?? u.role;
  };
  /** The highest role the caller may grant in a project, as the server checks it. */
  const rankIn = (projectId: string) => Math.max(rank[role()], rank[roleIn(projectId)]);
  /** Organisation admins, or admins in this project through an override. */
  const projectAdmin = (projectId: string) => {
    if (!db.projects.some((p) => p.id === projectId)) return notFound('Project');
    return rankIn(projectId) >= rank.admin
      ? null
      : err(403, 'forbidden', 'This needs the admin role in this project.');
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
  /** Checks caps: every value set must be positive. */
  const capsBody = (c: Caps | undefined): Caps | Response => {
    const out: Caps = {};
    for (const k of ['maxRate', 'maxVUs', 'maxDurationSeconds'] as const) {
      const v = c?.[k];
      if (v == null) continue;
      if (typeof v !== 'number' || !(v > 0)) return err(422, 'invalid', 'caps must be positive');
      out[k] = v;
    }
    return out;
  };

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
  /** Checks a schedule body; returns an error response or the next run time. */
  const checkSchedule = (b: ScheduleCreate, projectId: string, selfId?: string) => {
    const details: string[] = [];
    if (!b.name?.trim()) return err(422, 'invalid', 'name is required');
    if (
      db.schedules.some(
        (x) => x.projectId === projectId && x.name === b.name.trim() && x.id !== selfId,
      )
    )
      return err(
        409,
        'conflict',
        `a schedule named "${b.name.trim()}" already exists in this project`,
      );
    const sc = db.scenarios.find((x) => x.id === b.scenarioId && x.projectId === projectId);
    const t = db.targets.find((x) => x.id === b.targetId && x.projectId === projectId);
    if (!sc) details.push('scenario not found in this project');
    if (!t) details.push('target not found in this project');
    if (details.length) return err(422, 'invalid', details[0]!);
    let next: string | undefined;
    try {
      checkZone(b.timezone || 'UTC');
      next = nextTimes(parseCron(b.cron), b.timezone || 'UTC', new Date(), 1)[0];
    } catch (e) {
      return err(422, 'invalid', `cron: ${(e as Error).message}`);
    }
    if (!next)
      return err(422, 'invalid', 'cron: the expression does not fire in the next nine years');
    if (b.overrides?.rate && !/^[0-9.]+(\/(s|m|h))?$/.test(b.overrides.rate))
      return err(422, 'invalid', 'overrides: rate is not valid', [
        `overrides.rate: "${b.overrides.rate}" is not a rate`,
      ]);
    const kind = b.kind ?? 'run';
    const specURL = b.specURL?.trim() ?? '';
    if (kind !== 'run' && kind !== 'drift') return err(422, 'invalid', 'kind must be run or drift');
    if (kind === 'run' && specURL)
      return err(422, 'invalid', 'specURL applies only to drift schedules');
    if (specURL) {
      let u: URL | undefined;
      try {
        u = new URL(specURL);
      } catch {
        /* checked below */
      }
      if (!u || (u.protocol !== 'http:' && u.protocol !== 'https:'))
        return err(422, 'invalid', 'specURL must be an absolute http(s) URL');
      const host = new URL(t!.baseURL).hostname;
      if (u.hostname !== host && !(t!.allowHosts ?? []).includes(u.hostname))
        return err(
          422,
          'invalid',
          `specURL must be on the target's host ${host} or one of its allowed hosts, not ${u.hostname}`,
        );
    }
    return { sc: sc!, t: t!, next, kind, specURL };
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
    http.get(
      `${B}/projects`,
      async () => (await auth()) ?? ok(db.projects.map((p) => ({ ...p, role: roleIn(p.id) }))),
    ),
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
      return p ? ok({ ...p, role: roleIn(p.id) }) : notFound('Project');
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

    // ------------------------------------------------------------ caps, gate, project roles
    http.get(`${B}/organisation/caps`, async () => (await auth()) ?? ok(db.orgCaps)),
    http.put(`${B}/organisation/caps`, async ({ request }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const c = capsBody((await request.json()) as Caps);
      if (c instanceof Response) return c;
      db.orgCaps = c;
      return ok(c);
    }),
    http.get(`${B}/projects/:id/settings`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const id = String(params.id);
      if (!db.projects.some((p) => p.id === id)) return notFound('Project');
      return ok(db.projectSettings[id] ?? { caps: {}, requireDryRun: false });
    }),
    http.put(`${B}/projects/:id/settings`, async ({ params, request }) => {
      const id = String(params.id);
      const a = (await auth()) ?? projectAdmin(id);
      if (a) return a;
      const b = (await request.json()) as ProjectSettings;
      const c = capsBody(b.caps);
      if (c instanceof Response) return c;
      db.projectSettings[id] = { caps: c, requireDryRun: b.requireDryRun };
      return ok(db.projectSettings[id]);
    }),
    http.get(`${B}/projects/:id/roles`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const id = String(params.id);
      if (!db.projects.some((p) => p.id === id)) return notFound('Project');
      return ok(projectRoles(id));
    }),
    http.put(`${B}/projects/:id/roles/:userId`, async ({ params, request }) => {
      const id = String(params.id);
      const a = (await auth()) ?? projectAdmin(id);
      if (a) return a;
      const { role: r } = (await request.json()) as { role: Role };
      if (!(r in rank)) return err(422, 'invalid', `unknown role ${r}`);
      if (r === 'owner')
        return err(
          422,
          'invalid',
          'owner is an organisation role; it cannot be given for one project',
        );
      const mine = rankIn(id);
      if (rank[r] > mine)
        return err(
          403,
          'forbidden',
          `you cannot give a role higher than your own (${roleIn(id)}) in this project`,
        );
      const u = db.users.find((x) => x.id === params.userId);
      if (!u) return notFound('member');
      if (u.role === 'owner')
        return err(
          422,
          'invalid',
          `${u.email} is an owner, and owners have the owner role in every project`,
        );
      const list = (db.projectRoles[id] ??= []);
      const cur = list.find((x) => x.userId === u.id);
      if (cur) cur.role = r;
      else list.push({ userId: u.id, role: r, createdAt: new Date().toISOString() });
      return ok(projectRoles(id).find((x) => x.userId === u.id));
    }),
    http.delete(`${B}/projects/:id/roles/:userId`, async ({ params }) => {
      const id = String(params.id);
      const a = (await auth()) ?? projectAdmin(id);
      if (a) return a;
      const list = db.projectRoles[id] ?? [];
      const i = list.findIndex((x) => x.userId === params.userId);
      if (i < 0) return notFound('project role');
      list.splice(i, 1);
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
    http.post(`${B}/runs/:id/narrative`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const r = db.runs.find((x) => x.id === params.id);
      if (!r) return notFound('Run');
      const rep = db.reports[r.id];
      if (!rep) return err(404, 'not_found', 'report (the run has not finished) not found.');
      await new Promise((res) => setTimeout(res, 900));
      const o = rep.overall;
      const msText = (s: number) => `${(s * 1000).toFixed(1)}ms`;
      const failed = (rep.thresholds ?? []).findIndex((c) => !c.pass);
      const facts = [
        {
          id: 'overall.latency',
          where: 'summary',
          text: `latency from scheduled send p50 ${msText(o.latency.p50)}, p95 ${msText(o.latency.p95)}, p99 ${msText(o.latency.p99)}`,
        },
        {
          id: 'overall.requests',
          where: 'summary',
          text: `${o.requests} requests at ${o.rps.toFixed(1)}/s, ${(o.errorRate * 100).toFixed(2)}% failed`,
        },
        ...(failed >= 0
          ? [
              {
                id: `target.${failed}`,
                where: 'targets',
                text: `target ${rep.thresholds![failed]!.source} failed (observed ${rep.thresholds![failed]!.observedText})`,
              },
            ]
          : []),
      ];
      const narrative = {
        model: 'mock-model',
        summary:
          failed >= 0
            ? `The run missed ${rep.thresholds![failed]!.source}; p95 latency reached ${msText(o.latency.p95)}.`
            : `Every target held; p95 latency was ${msText(o.latency.p95)}.`,
        claims: [
          {
            text: `p95 latency from the scheduled send time was ${msText(o.latency.p95)}.`,
            label: 'measured' as const,
            refs: ['overall.latency'],
          },
          ...(failed >= 0
            ? [
                {
                  text: `The target ${rep.thresholds![failed]!.source} failed.`,
                  label: 'measured' as const,
                  refs: [`target.${failed}`],
                },
              ]
            : []),
          {
            text: 'The gap between p95 and p99 suggests a slow minority of requests, likely waiting on a shared resource.',
            label: 'suspected' as const,
            refs: ['overall.latency', 'overall.requests'],
          },
        ],
        facts,
      };
      db.reports[r.id] = { ...rep, narrative };
      return ok({ narrative, usage: { inputTokens: 1840, outputTokens: 220 } });
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
    http.post(`${B}/projects/:id/schedules`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const body = (await request.json()) as ScheduleCreate;
      const pid = String(params.id);
      const c = checkSchedule(body, pid);
      if (c instanceof Response) return c;
      const u = me(db)!;
      const enabled = body.enabled ?? true;
      const sc: Schedule = {
        id: uuid(),
        projectId: pid,
        name: body.name.trim(),
        scenarioId: c.sc.id,
        scenarioName: c.sc.name,
        targetId: c.t.id,
        targetName: c.t.name,
        cron: body.cron.trim(),
        timezone: body.timezone || 'UTC',
        overrides: body.overrides ?? {},
        env: body.env ?? {},
        workers: body.workers ?? 0,
        enabled,
        note: body.note ?? '',
        ownerId: u.id,
        ownerEmail: u.email,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        nextRunAt: enabled ? c.next : null,
        lastFiredAt: null,
        lastRunId: null,
        lastSkipReason: '',
        kind: c.kind,
        ...(c.specURL ? { specURL: c.specURL } : {}),
      };
      db.schedules.push(sc);
      return ok(scheduleView(sc), 201);
    }),
    http.get(`${B}/schedules/preview`, async ({ request }) => {
      const a = await auth();
      if (a) return a;
      const q = new URL(request.url).searchParams;
      const tz = q.get('timezone') || 'UTC';
      try {
        checkZone(tz);
        const next = nextTimes(
          parseCron(q.get('cron') ?? ''),
          tz,
          new Date(),
          Number(q.get('count') ?? 3),
        );
        return ok({ timezone: tz, next });
      } catch (e) {
        return err(422, 'invalid', `cron: ${(e as Error).message}`);
      }
    }),
    http.get(`${B}/schedules/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const sc = db.schedules.find((x) => x.id === params.id);
      return sc ? ok(scheduleView(sc)) : notFound('Schedule');
    }),
    http.patch(`${B}/schedules/:id`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const sc = db.schedules.find((x) => x.id === params.id);
      if (!sc) return notFound('Schedule');
      const body = (await request.json()) as ScheduleUpdate;
      const merged: ScheduleCreate = {
        name: body.name ?? sc.name,
        scenarioId: body.scenarioId ?? sc.scenarioId,
        targetId: body.targetId ?? sc.targetId,
        cron: body.cron ?? sc.cron,
        timezone: body.timezone ?? sc.timezone,
        overrides: body.overrides ?? sc.overrides ?? {},
        env: body.env ?? sc.env ?? {},
        workers: body.workers ?? sc.workers,
        enabled: body.enabled ?? sc.enabled,
        note: body.note ?? sc.note ?? '',
        kind: sc.kind ?? 'run',
        specURL: body.specURL ?? sc.specURL ?? '',
      };
      const c = checkSchedule(merged, sc.projectId, sc.id);
      if (c instanceof Response) return c;
      const u = me(db)!;
      const enabled = merged.enabled ?? true;
      const timing = merged.cron !== sc.cron || merged.timezone !== sc.timezone || !sc.nextRunAt;
      const { specURL: _s, ...rest } = merged;
      if (c.specURL) sc.specURL = c.specURL;
      else delete sc.specURL;
      Object.assign(sc, {
        ...rest,
        name: merged.name.trim(),
        cron: merged.cron.trim(),
        timezone: merged.timezone || 'UTC',
        scenarioName: c.sc.name,
        targetName: c.t.name,
        ownerId: u.id,
        ownerEmail: u.email,
        updatedAt: new Date().toISOString(),
        nextRunAt: !enabled ? null : timing ? c.next : sc.nextRunAt,
      });
      return ok(scheduleView(sc));
    }),
    http.delete(`${B}/schedules/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const i = db.schedules.findIndex((x) => x.id === params.id);
      if (i < 0) return notFound('Schedule');
      db.schedules.splice(i, 1);
      return noContent();
    }),
    http.post(`${B}/schedules/:id/run`, async ({ params }) => {
      const a = (await auth()) ?? need('runner');
      if (a) return a;
      const sc = db.schedules.find((x) => x.id === params.id);
      if (!sc) return notFound('Schedule');
      const v = scheduleView(sc);
      if (v.lastRunStatus && activeStatuses.includes(v.lastRunStatus))
        return err(409, 'conflict', "the schedule's previous run is still active");
      const s = db.scenarios.find((x) => x.id === sc.scenarioId);
      const t = db.targets.find((x) => x.id === sc.targetId);
      if (!s || !t) return err(422, 'invalid', 'scenario or target not found in this project');
      if (sc.kind === 'drift') {
        // The API has not changed since the last check, so a check finds
        // what the last one found.
        const last = db.driftResults.find((r) => r.id === sc.lastDriftId);
        const res = driftCheck({
          id: uuid(),
          schedule: sc,
          scenario: s,
          target: t,
          drifted: last ? last.status === 'drifted' : true,
          at: new Date().toISOString(),
        });
        db.driftResults.unshift(res);
        Object.assign(sc, {
          lastFiredAt: res.createdAt,
          lastDriftId: res.id,
          lastDriftStatus: res.status,
          lastDriftBroken: res.broken,
          lastSkipReason: '',
        });
        return ok(res);
      }
      const run = startRun(db, s, t, {
        note: `scheduled: ${sc.name}`,
        ...(sc.workers ? { workers: sc.workers } : {}),
        ...(sc.overrides && Object.keys(sc.overrides).length ? { overrides: sc.overrides } : {}),
        createdBy: me(db)!.email,
      });
      sc.lastRunId = run.id;
      sc.lastFiredAt = run.createdAt;
      sc.lastSkipReason = '';
      return ok(run, 201);
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
    http.post(`${B}/drift-results/:id/repair`, async ({ params, request }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const r = db.driftResults.find((x) => x.id === params.id);
      if (!r) return notFound('drift result');
      if (r.status !== 'drifted' || r.broken.length === 0)
        return err(409, 'conflict', 'this drift check found no broken journeys to repair');
      const b = ((await request.json().catch(() => ({}))) ?? {}) as DriftRepair;
      const prov = b.providerId
        ? db.aiProviders.find((p) => p.id === b.providerId)
        : db.aiProviders.length === 1
          ? db.aiProviders[0]
          : db.aiProviders.find((p) => p.name === 'default');
      if (!prov)
        return err(
          409,
          'conflict',
          'no AI provider is configured; an admin can add one with POST /ai/providers',
        );
      const job: MockAIJob = {
        id: uuid(),
        projectId: r.projectId,
        status: 'queued',
        stage: '',
        providerKind: prov.kind,
        model: prov.model,
        usage: { inputTokens: 0, outputTokens: 0 },
        dryRun: true,
        createdBy: me(db)!.email,
        createdAt: new Date().toISOString(),
        finishedAt: null,
        approvedAt: null,
        startedAt: null,
        round: 0,
        targetId: r.targetId,
        scenarioId: r.scenarioId,
        problems: [],
        journeys: [],
        approvedScenarioId: null,
        approvedVersion: null,
        maxRepairs: b.maxRepairs ?? 3,
      };
      db.aiJobs.unshift(job);
      r.repairJobId = job.id;
      return ok(aiPublic(job), 202);
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
    http.post(`${B}/ai/providers`, async ({ request }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      const b = (await request.json()) as AIProviderPut;
      const name = (b.name ?? 'default').trim();
      if (!name || name.length > 100)
        return err(422, 'invalid', 'name must be 1 to 100 characters');
      const model = b.model?.trim() || (b.kind === 'anthropic' ? 'claude-sonnet-5-5' : '');
      if (!model) return err(422, 'invalid', `model is required for ${b.kind}`);
      const baseURL = b.baseURL?.trim().replace(/\/+$/, '') ?? '';
      if (baseURL && !/^https?:\/\/[^/\s]+/.test(baseURL))
        return err(422, 'invalid', 'baseURL must be an absolute http(s) URL');
      if (b.kind === 'openai-compatible' && !baseURL)
        return err(
          422,
          'invalid',
          'baseURL is required for openai-compatible providers, for example http://llm.internal:8000/v1',
        );
      if (b.monthlyTokenCap != null && b.monthlyTokenCap <= 0)
        return err(422, 'invalid', 'monthlyTokenCap must be positive');
      const existing = db.aiProviders.find((p) => p.name === name);
      const hasKey = !!b.apiKey?.trim() || !!existing?.hasKey;
      if (['anthropic', 'openai', 'gemini'].includes(b.kind) && !hasKey)
        return err(422, 'invalid', `apiKey is required for ${b.kind}`);
      const now = new Date().toISOString();
      const p: AIProvider = {
        id: existing?.id ?? uuid(),
        name,
        kind: b.kind,
        model,
        ...(baseURL ? { baseURL } : {}),
        hasKey,
        monthlyTokenCap: b.monthlyTokenCap ?? existing?.monthlyTokenCap ?? 2_000_000,
        usedTokensThisMonth: db.aiProviders[0]?.usedTokensThisMonth ?? 0,
        createdAt: existing?.createdAt ?? now,
        updatedAt: now,
      };
      if (existing) db.aiProviders = db.aiProviders.map((x) => (x.id === p.id ? p : x));
      else db.aiProviders.push(p);
      return ok(p, existing ? 200 : 201);
    }),
    http.delete(`${B}/ai/providers/:id`, async ({ params }) => {
      const a = (await auth()) ?? need('admin');
      if (a) return a;
      if (!db.aiProviders.some((p) => p.id === params.id)) return notFound('AI provider');
      db.aiProviders = db.aiProviders.filter((p) => p.id !== params.id);
      return noContent();
    }),
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
    http.post(`${B}/projects/:id/ai/jobs`, async ({ request, params }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const projectId = String(params.id);
      const b = (await request.json()) as AIJobCreate;
      const len = (s?: string) => new TextEncoder().encode(s ?? '').length;
      if (!b.description?.trim() && !b.openapi && !b.har && !b.accessLog)
        return err(
          422,
          'invalid',
          'give at least one input: description, openapi, har or accessLog',
        );
      if (len(b.description?.trim()) > 20_000)
        return err(422, 'invalid', 'description is longer than 20,000 characters');
      if (len(b.openapi) > 5 << 20) return err(422, 'invalid', 'openapi is larger than 5 MiB');
      if (len(b.har) > 20 << 20 || len(b.accessLog) > 20 << 20)
        return err(422, 'invalid', 'har and accessLog are limited to 20 MiB each');
      const dryRun = b.dryRun ?? true;
      const t = b.targetId
        ? db.targets.find((x) => x.id === b.targetId && x.projectId === projectId)
        : undefined;
      if (b.targetId && !t) return err(422, 'invalid', 'target not found in this project');
      if (!t && dryRun)
        return err(422, 'invalid', 'targetId is required for the dry run (or set dryRun to false)');
      if (
        b.scenarioId &&
        !db.scenarios.some((s) => s.id === b.scenarioId && s.projectId === projectId)
      )
        return err(422, 'invalid', 'scenario not found in this project');
      let prov = b.providerId ? db.aiProviders.find((p) => p.id === b.providerId) : undefined;
      if (b.providerId && !prov) return err(422, 'invalid', 'AI provider not found');
      if (!prov) {
        if (db.aiProviders.length === 0)
          return err(
            409,
            'conflict',
            'no AI provider is configured; an admin can add one with POST /ai/providers',
          );
        prov =
          db.aiProviders.length === 1
            ? db.aiProviders[0]
            : db.aiProviders.find((p) => p.name === 'default');
        if (!prov)
          return err(
            422,
            'invalid',
            'several AI providers are configured; choose one with providerId',
          );
      }
      if (prov.usedTokensThisMonth >= prov.monthlyTokenCap)
        return err(
          429,
          'ai_token_cap',
          `the organisation used ${prov.usedTokensThisMonth} AI tokens this month, which reaches the cap of ${prov.monthlyTokenCap} for provider ${prov.name}`,
        );
      const job: MockAIJob = {
        id: uuid(),
        projectId,
        status: 'queued',
        stage: '',
        providerKind: prov.kind,
        model: prov.model,
        usage: { inputTokens: 0, outputTokens: 0 },
        dryRun,
        createdBy: me(db)!.email,
        createdAt: new Date().toISOString(),
        finishedAt: null,
        approvedAt: null,
        startedAt: null,
        round: 0,
        targetId: t?.id ?? null,
        scenarioId: b.scenarioId ?? null,
        problems: [],
        journeys: [],
        approvedScenarioId: null,
        approvedVersion: null,
        maxRepairs: b.maxRepairs ?? 3,
      };
      db.aiJobs.unshift(job);
      return ok(aiPublic(job), 202);
    }),
    http.get(`${B}/ai/jobs/:id`, async ({ params }) => {
      const a = await auth();
      if (a) return a;
      const j = db.aiJobs.find((x) => x.id === params.id);
      if (!j) return notFound('AI job');
      advance(j);
      return ok(aiPublic(j));
    }),
    http.post(`${B}/ai/jobs/:id/approve`, async ({ params, request }) => {
      const a = (await auth()) ?? need('editor');
      if (a) return a;
      const j = db.aiJobs.find((x) => x.id === params.id);
      if (!j) return notFound('AI job');
      advance(j);
      const b = (await request.json()) as AIJobApprove;
      if (j.approvedAt) return err(409, 'conflict', 'this job was already approved');
      if (j.status === 'queued' || j.status === 'running')
        return err(409, 'conflict', 'the job has not finished yet');
      if (j.status === 'failed' || !j.yaml)
        return err(409, 'conflict', 'the job failed and has no usable proposal');
      if (j.status === 'needs_review' && !b.allowUnvalidated) {
        const flagged = j.journeys.filter((x) => x.status === 'flagged').map((x) => x.name);
        return err(
          409,
          'conflict',
          `some journeys did not pass their dry run (${flagged.join(', ')}); review them and approve with allowUnvalidated`,
        );
      }
      const targetId = b.scenarioId ?? j.scenarioId ?? undefined;
      const msg = b.message?.trim() || `generated by AI job ${j.id} (${j.providerKind}/${j.model})`;
      const an = analyse(j.yaml);
      const now = new Date().toISOString();
      let scenario: Scenario;
      let version: number;
      if (targetId) {
        const s = db.scenarios.find((x) => x.id === targetId && x.projectId === j.projectId);
        if (!s) return err(422, 'invalid', "scenario not found in the job's project");
        version = s.latestVersion.version + 1;
        const v = {
          scenarioId: s.id,
          version,
          yaml: j.yaml,
          message: msg,
          createdBy: me(db)!.email,
          createdAt: now,
          ...(an.validation.plan ? { plan: an.validation.plan } : {}),
        };
        db.versions[s.id]!.unshift(v);
        Object.assign(s, { latestVersion: v, updatedAt: now, tags: an.tags });
        scenario = s;
      } else {
        if (db.scenarios.some((s) => s.projectId === j.projectId && s.name === an.name))
          return err(
            409,
            'conflict',
            `A scenario named ${an.name} already exists in this project.`,
          );
        const id = uuid();
        version = 1;
        const v = {
          scenarioId: id,
          version,
          yaml: j.yaml,
          message: msg,
          createdBy: me(db)!.email,
          createdAt: now,
          ...(an.validation.plan ? { plan: an.validation.plan } : {}),
        };
        scenario = {
          id,
          projectId: j.projectId,
          name: an.name ?? 'shop-generated',
          ...(an.description ? { description: an.description } : {}),
          tags: an.tags,
          latestVersion: v,
          createdAt: now,
          updatedAt: now,
        };
        db.scenarios.push(scenario);
        db.versions[id] = [v];
      }
      Object.assign(j, {
        approvedAt: now,
        approvedScenarioId: scenario.id,
        approvedVersion: version,
      });
      return ok({ scenario, version }, 201);
    }),
  ];

  return { handlers, getDb: () => db, reset: (o: MockOptions) => (db = createDb(o)) };
}
