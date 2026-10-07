import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
  type UseQueryOptions,
} from '@tanstack/react-query';
import { api, ApiError, unwrap } from './client';
import type {
  AIJob,
  AIJobApprove,
  AIJobCreate,
  AIProviderPut,
  CompareRequest,
  CoverageRequest,
  DriftRequest,
  IntegrationCreate,
  LoginRequest,
  NotificationChannelCreate,
  Me,
  ProjectCreate,
  Report,
  Role,
  Run,
  RunCreate,
  ScenarioVersionCreate,
  ScheduleCreate,
  ScheduleUpdate,
  SecretPut,
  SetupRequest,
  TargetCreate,
  TokenCreate,
  UserCreate,
} from './types';
import { activeStatuses, isAIJobActive } from './types';

export const keys = {
  version: ['version'] as const,
  me: ['me'] as const,
  projects: ['projects'] as const,
  project: (id: string) => ['projects', id] as const,
  targets: (projectId: string) => ['projects', projectId, 'targets'] as const,
  target: (id: string) => ['targets', id] as const,
  secrets: (projectId: string) => ['projects', projectId, 'secrets'] as const,
  scenarios: (projectId: string, tag?: string) =>
    ['projects', projectId, 'scenarios', tag ?? ''] as const,
  scenario: (id: string) => ['scenarios', id] as const,
  versions: (scenarioId: string) => ['scenarios', scenarioId, 'versions'] as const,
  version_: (scenarioId: string, v: number) => ['scenarios', scenarioId, 'versions', v] as const,
  runs: (projectId: string, filters: object = {}) =>
    ['projects', projectId, 'runs', filters] as const,
  allRuns: (projectId: string) => ['projects', projectId, 'runs'] as const,
  run: (id: string) => ['runs', id] as const,
  schedules: (projectId: string) => ['projects', projectId, 'schedules'] as const,
  schedulePreview: (cron: string, timezone: string) =>
    ['schedule-preview', cron, timezone] as const,
  timeline: (id: string) => ['runs', id, 'timeline'] as const,
  report: (id: string) => ['runs', id, 'report'] as const,
  activeRuns: ['active-runs'] as const,
  workers: ['workers'] as const,
  users: ['users'] as const,
  tokens: ['tokens'] as const,
  audit: ['audit'] as const,
  integrations: ['integrations'] as const,
  channels: ['notification-channels'] as const,
  deliveries: (channelId: string) => ['notification-channels', channelId, 'deliveries'] as const,
  aiProviders: ['ai-providers'] as const,
  aiJobs: (projectId: string) => ['projects', projectId, 'ai-jobs'] as const,
  aiJob: (id: string) => ['ai-jobs', id] as const,
  compare: (body: CompareRequest) => ['compare', body] as const,
  runWorkers: (runId: string) => ['runs', runId, 'workers'] as const,
  packs: ['packs'] as const,
  pack: (name: string) => ['packs', name] as const,
  ssoSettings: ['settings', 'sso'] as const,
  limitSettings: ['settings', 'limits'] as const,
};

// ---------------------------------------------------------------- system/auth

export const versionQuery = queryOptions({
  queryKey: keys.version,
  queryFn: () => unwrap(api.GET('/version')),
  staleTime: 60_000,
});

/** The signed-in user, or null when signed out. */
export const meQuery = queryOptions({
  queryKey: keys.me,
  queryFn: async (): Promise<Me | null> => {
    try {
      return await unwrap(api.GET('/me'));
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return null;
      throw err;
    }
  },
  staleTime: 5 * 60_000,
});

export function useVersion() {
  return useQuery(versionQuery);
}

/** The signed-in user. Only use inside the authenticated app shell. */
export function useMe(): Me {
  const { data } = useQuery(meQuery);
  if (!data) throw new Error('useMe used outside an authenticated route');
  return data;
}

/** How people can sign in; public, so the sign-in page can ask. */
export function useAuthConfig() {
  return useQuery({
    queryKey: ['auth', 'config'],
    queryFn: () => unwrap(api.GET('/auth/config')),
    staleTime: 5 * 60_000,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: LoginRequest) => unwrap(api.POST('/auth/login', { body })),
    onSuccess: (session) => {
      qc.setQueryData(keys.me, session.user);
    },
  });
}

export function useSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SetupRequest) => unwrap(api.POST('/setup', { body })),
    onSuccess: (session) => {
      qc.setQueryData(keys.me, session.user);
      qc.setQueryData(keys.version, (v: { setupRequired: boolean } | undefined) =>
        v ? { ...v, setupRequired: false } : v,
      );
    },
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST('/auth/logout')),
    onSettled: () => {
      qc.clear();
      qc.setQueryData(keys.me, null);
    },
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { current: string; new: string }) =>
      unwrap(api.PUT('/me/password', { body })),
  });
}

// ---------------------------------------------------------------- users/tokens

export function useUsers(enabled = true) {
  return useQuery({
    queryKey: keys.users,
    queryFn: () => unwrap(api.GET('/users')),
    enabled,
  });
}

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: UserCreate) => unwrap(api.POST('/users', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.users }),
  });
}

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; role?: Role; name?: string }) =>
      unwrap(api.PATCH('/users/{userId}', { params: { path: { userId: id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.users }),
  });
}

export function useDeleteUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/users/{userId}', { params: { path: { userId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.users }),
  });
}

export function useTokens() {
  return useQuery({ queryKey: keys.tokens, queryFn: () => unwrap(api.GET('/tokens')) });
}

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: TokenCreate) => unwrap(api.POST('/tokens', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens }),
  });
}

export function useDeleteToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/tokens/{tokenId}', { params: { path: { tokenId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens }),
  });
}

export function useAudit(enabled: boolean) {
  return useQuery({
    queryKey: keys.audit,
    queryFn: () => unwrap(api.GET('/audit', { params: { query: { limit: 200 } } })),
    enabled,
  });
}

// ---------------------------------------------------------------- integrations

export function useIntegrations() {
  return useQuery({
    queryKey: keys.integrations,
    queryFn: () => unwrap(api.GET('/integrations')),
  });
}

export function useCreateIntegration() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: IntegrationCreate) => unwrap(api.POST('/integrations', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.integrations }),
  });
}

export function useDeleteIntegration() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.DELETE('/integrations/{integrationId}', { params: { path: { integrationId: id } } }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.integrations }),
  });
}

export function useNotificationChannels() {
  return useQuery({
    queryKey: keys.channels,
    queryFn: () => unwrap(api.GET('/notifications/channels')),
  });
}

export function useCreateNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: NotificationChannelCreate) =>
      unwrap(api.POST('/notifications/channels', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.channels }),
  });
}

export function useDeleteNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.DELETE('/notifications/channels/{channelId}', {
          params: { path: { channelId: id } },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.channels }),
  });
}

export function useTestNotificationChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST('/notifications/channels/{channelId}/test', {
          params: { path: { channelId: id } },
        }),
      ),
    onSuccess: (_d, id) => {
      void qc.invalidateQueries({ queryKey: keys.channels });
      void qc.invalidateQueries({ queryKey: keys.deliveries(id) });
    },
  });
}

export function useNotificationDeliveries(channelId: string, enabled = true) {
  return useQuery({
    queryKey: keys.deliveries(channelId),
    queryFn: () =>
      unwrap(
        api.GET('/notifications/channels/{channelId}/deliveries', {
          params: { path: { channelId }, query: { limit: 50 } },
        }),
      ),
    enabled,
  });
}

// ---------------------------------------------------------------- projects

export const projectsQuery = queryOptions({
  queryKey: keys.projects,
  queryFn: () => unwrap(api.GET('/projects')),
  staleTime: 30_000,
});

export function useProjects() {
  return useQuery(projectsQuery);
}

export function useProject(id: string) {
  return useQuery({
    queryKey: keys.project(id),
    queryFn: () =>
      unwrap(api.GET('/projects/{projectId}', { params: { path: { projectId: id } } })),
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProjectCreate) => unwrap(api.POST('/projects', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.projects }),
  });
}

export function useUpdateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ProjectCreate & { id: string }) =>
      unwrap(api.PATCH('/projects/{projectId}', { params: { path: { projectId: id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.projects }),
  });
}

export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/projects/{projectId}', { params: { path: { projectId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.projects }),
  });
}

// ---------------------------------------------------------------- targets

export function useTargets(projectId: string) {
  return useQuery({
    queryKey: keys.targets(projectId),
    queryFn: () =>
      unwrap(api.GET('/projects/{projectId}/targets', { params: { path: { projectId } } })),
  });
}

export function useCreateTarget(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: TargetCreate) =>
      unwrap(api.POST('/projects/{projectId}/targets', { params: { path: { projectId } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.targets(projectId) }),
  });
}

export function useUpdateTarget(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: TargetCreate & { id: string }) =>
      unwrap(api.PATCH('/targets/{targetId}', { params: { path: { targetId: id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.targets(projectId) }),
  });
}

export function useDeleteTarget(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/targets/{targetId}', { params: { path: { targetId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.targets(projectId) }),
  });
}

export function useVerifyTarget(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST('/targets/{targetId}/verify', { params: { path: { targetId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.targets(projectId) }),
  });
}

// ---------------------------------------------------------------- secrets

export function useSecrets(projectId: string) {
  return useQuery({
    queryKey: keys.secrets(projectId),
    queryFn: () =>
      unwrap(api.GET('/projects/{projectId}/secrets', { params: { path: { projectId } } })),
  });
}

export function usePutSecret(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SecretPut) =>
      unwrap(api.PUT('/projects/{projectId}/secrets', { params: { path: { projectId } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.secrets(projectId) }),
  });
}

export function useDeleteSecret(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      unwrap(
        api.DELETE('/projects/{projectId}/secrets/{name}', {
          params: { path: { projectId, name } },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.secrets(projectId) }),
  });
}

// ---------------------------------------------------------------- scenarios

export function useScenarios(projectId: string, tag?: string) {
  return useQuery({
    queryKey: keys.scenarios(projectId, tag),
    queryFn: () =>
      unwrap(
        api.GET('/projects/{projectId}/scenarios', {
          params: { path: { projectId }, query: tag ? { tag } : {} },
        }),
      ),
  });
}

export function useScenario(id: string) {
  return useQuery({
    queryKey: keys.scenario(id),
    queryFn: () =>
      unwrap(api.GET('/scenarios/{scenarioId}', { params: { path: { scenarioId: id } } })),
  });
}

export function useScenarioVersions(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.versions(id),
    queryFn: () =>
      unwrap(api.GET('/scenarios/{scenarioId}/versions', { params: { path: { scenarioId: id } } })),
    enabled,
  });
}

export function useScenarioVersion(id: string, version: number | undefined) {
  return useQuery({
    queryKey: keys.version_(id, version ?? 0),
    queryFn: () =>
      unwrap(
        api.GET('/scenarios/{scenarioId}/versions/{version}', {
          params: { path: { scenarioId: id, version: version ?? 1 } },
        }),
      ),
    enabled: version != null,
  });
}

export function useCreateScenario(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScenarioVersionCreate) =>
      unwrap(
        api.POST('/projects/{projectId}/scenarios', { params: { path: { projectId } }, body }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['projects', projectId, 'scenarios'] }),
  });
}

export function useCreateScenarioVersion(projectId: string, scenarioId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScenarioVersionCreate) =>
      unwrap(
        api.POST('/scenarios/{scenarioId}/versions', {
          params: { path: { scenarioId } },
          body,
        }),
      ),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.scenario(scenarioId) }),
        qc.invalidateQueries({ queryKey: ['projects', projectId, 'scenarios'] }),
      ]);
    },
  });
}

export function useDeleteScenario(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/scenarios/{scenarioId}', { params: { path: { scenarioId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['projects', projectId, 'scenarios'] }),
  });
}

export function validateScenario(yaml: string, signal?: AbortSignal) {
  return unwrap(api.POST('/scenarios/validate', { body: { yaml }, signal }));
}

// ---------------------------------------------------------------- runs

export interface RunFilters {
  scenarioId?: string;
  before?: string;
  limit?: number;
}

export function listRuns(projectId: string, f: RunFilters = {}) {
  return unwrap(
    api.GET('/projects/{projectId}/runs', {
      params: {
        path: { projectId },
        query: {
          ...(f.scenarioId ? { scenarioId: f.scenarioId } : {}),
          ...(f.before ? { before: f.before } : {}),
          limit: f.limit ?? 50,
        },
      },
    }),
  );
}

export function useRuns(projectId: string, f: RunFilters = {}, refetchInterval?: number) {
  return useQuery({
    queryKey: keys.runs(projectId, f),
    queryFn: () => listRuns(projectId, f),
    refetchInterval,
  });
}

export function useRun(id: string, refetchInterval?: UseQueryOptions<Run>['refetchInterval']) {
  return useQuery({
    queryKey: keys.run(id),
    queryFn: () => unwrap(api.GET('/runs/{runId}', { params: { path: { runId: id } } })),
    refetchInterval,
  });
}

export function useTimeline(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.timeline(id),
    queryFn: () => unwrap(api.GET('/runs/{runId}/timeline', { params: { path: { runId: id } } })),
    enabled,
    staleTime: Infinity,
  });
}

export function useReport(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.report(id),
    queryFn: async () =>
      (await unwrap(
        api.GET('/runs/{runId}/report', {
          params: { path: { runId: id }, query: { format: 'json' } },
        }),
      )) as unknown as Report,
    enabled,
    staleTime: Infinity,
    retry: (n, err) => !(err instanceof ApiError && err.status === 409) && n < 2,
  });
}

/** Asks the server's AI provider for a narrative and puts it in the cached report. */
export function useCreateNarrative(runId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      unwrap(api.POST('/runs/{runId}/narrative', { params: { path: { runId } }, body: {} })),
    onSuccess: (res) => {
      qc.setQueryData<Report>(keys.report(runId), (r) =>
        r ? { ...r, narrative: res.narrative } : r,
      );
    },
  });
}

export function useCreateRun(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: RunCreate) =>
      unwrap(api.POST('/projects/{projectId}/runs', { params: { path: { projectId } }, body })),
    onSuccess: (run) => {
      qc.setQueryData(keys.run(run.id), run);
      void qc.invalidateQueries({ queryKey: keys.allRuns(projectId) });
      void qc.invalidateQueries({ queryKey: keys.activeRuns });
    },
  });
}

function invalidateRun(qc: QueryClient, id: string) {
  void qc.invalidateQueries({ queryKey: keys.run(id) });
  void qc.invalidateQueries({ queryKey: keys.activeRuns });
}

export function useStopRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST('/runs/{runId}/stop', { params: { path: { runId: id } } })),
    onSuccess: (_d, id) => invalidateRun(qc, id),
  });
}

export function useKillRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST('/runs/{runId}/kill', { params: { path: { runId: id } } })),
    onSuccess: (_d, id) => invalidateRun(qc, id),
  });
}

export function useKillAll() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST('/runs/kill-all')),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.activeRuns });
      void qc.invalidateQueries({ queryKey: ['runs'] });
      void qc.invalidateQueries({ queryKey: keys.projects });
    },
  });
}

/**
 * Active runs across every project. The API has no organisation-wide run
 * list, so this asks each project for its newest runs and keeps the active
 * ones. Polled so the kill switch appears as soon as a run starts.
 */
export function useActiveRuns() {
  const projects = useProjects();
  const ids = (projects.data ?? []).map((p) => p.id);
  return useQuery({
    queryKey: [...keys.activeRuns, ids],
    queryFn: async (): Promise<Run[]> => {
      const lists = await Promise.all(ids.map((id) => listRuns(id, { limit: 50 })));
      return lists.flat().filter((r) => activeStatuses.includes(r.status));
    },
    enabled: projects.isSuccess,
    refetchInterval: 5_000,
  });
}

// ---------------------------------------------------------------- schedules

export function useSchedules(projectId: string) {
  return useQuery({
    queryKey: keys.schedules(projectId),
    queryFn: () =>
      unwrap(api.GET('/projects/{projectId}/schedules', { params: { path: { projectId } } })),
    refetchInterval: 15_000,
  });
}

export function useCreateSchedule(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScheduleCreate) =>
      unwrap(
        api.POST('/projects/{projectId}/schedules', { params: { path: { projectId } }, body }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.schedules(projectId) }),
  });
}

export function useUpdateSchedule(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: ScheduleUpdate & { id: string }) =>
      unwrap(api.PATCH('/schedules/{scheduleId}', { params: { path: { scheduleId: id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.schedules(projectId) }),
  });
}

export function useDeleteSchedule(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/schedules/{scheduleId}', { params: { path: { scheduleId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.schedules(projectId) }),
  });
}

/** Starts a schedule's run now, as the signed-in user. */
export function useRunSchedule(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST('/schedules/{scheduleId}/run', { params: { path: { scheduleId: id } } })),
    onSuccess: (res) => {
      // A drift schedule's check returns a drift result, not a run.
      if (!('broken' in res)) qc.setQueryData(keys.run(res.id), res);
      void qc.invalidateQueries({ queryKey: keys.schedules(projectId) });
      void qc.invalidateQueries({ queryKey: keys.allRuns(projectId) });
      void qc.invalidateQueries({ queryKey: keys.activeRuns });
    },
  });
}

/** The next times a cron expression fires, checked by the server. */
export function useSchedulePreview(cron: string, timezone: string) {
  return useQuery({
    queryKey: keys.schedulePreview(cron, timezone),
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/schedules/preview', {
          params: { query: { cron, ...(timezone ? { timezone } : {}), count: 3 } },
          signal,
        }),
      ),
    enabled: cron.trim() !== '',
    retry: false,
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------- workers

export function useWorkers() {
  return useQuery({
    queryKey: keys.workers,
    queryFn: () => unwrap(api.GET('/workers')),
    refetchInterval: 3_000,
  });
}

/** Health of a run's load generators, polled while the run is active. */
export function useRunWorkers(runId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.runWorkers(runId),
    queryFn: () => unwrap(api.GET('/runs/{runId}/workers', { params: { path: { runId } } })),
    enabled,
    refetchInterval: enabled ? 2_000 : false,
  });
}

// ---------------------------------------------------------------- packs

export function usePacks() {
  return useQuery({
    queryKey: keys.packs,
    queryFn: () => unwrap(api.GET('/packs')),
    staleTime: Infinity,
  });
}

export function usePack(name: string) {
  return useQuery({
    queryKey: keys.pack(name),
    queryFn: () => unwrap(api.GET('/packs/{packName}', { params: { path: { packName: name } } })),
    staleTime: Infinity,
  });
}

// ---------------------------------------------------------------- coverage and drift

/** Maps a scenario's requests onto an API's endpoints. */
export function useScenarioCoverage(scenarioId: string) {
  return useMutation({
    mutationFn: (body: CoverageRequest) =>
      unwrap(
        api.POST('/scenarios/{scenarioId}/coverage', {
          params: { path: { scenarioId } },
          body,
        }),
      ),
  });
}

/** Finds journeys an API change broke. */
export function useScenarioDrift(scenarioId: string) {
  return useMutation({
    mutationFn: (body: DriftRequest) =>
      unwrap(
        api.POST('/scenarios/{scenarioId}/drift', {
          params: { path: { scenarioId } },
          body,
        }),
      ),
  });
}

// ---------------------------------------------------------------- server settings

export function useSSOSettings() {
  return useQuery({
    queryKey: keys.ssoSettings,
    queryFn: () => unwrap(api.GET('/settings/sso')),
  });
}

export function useLimitSettings() {
  return useQuery({
    queryKey: keys.limitSettings,
    queryFn: () => unwrap(api.GET('/settings/limits')),
  });
}

// ---------------------------------------------------------------- compare

/** Compares finished runs of version A with runs of version B. */
export function useCompare(body: CompareRequest, enabled = true) {
  return useQuery({
    queryKey: keys.compare(body),
    queryFn: () => unwrap(api.POST('/compare', { body })),
    enabled: enabled && body.a.length > 0 && body.b.length > 0,
    staleTime: Infinity,
    retry: false,
  });
}

// ---------------------------------------------------------------- AI

export function useAIProviders() {
  return useQuery({
    queryKey: keys.aiProviders,
    queryFn: () => unwrap(api.GET('/ai/providers')),
  });
}

export function usePutAIProvider() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AIProviderPut) => unwrap(api.POST('/ai/providers', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.aiProviders }),
  });
}

export function useDeleteAIProvider() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE('/ai/providers/{providerId}', { params: { path: { providerId: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.aiProviders }),
  });
}

/** Generation jobs, polled while any of them is queued or running. */
export function useAIJobs(projectId: string) {
  return useQuery({
    queryKey: keys.aiJobs(projectId),
    queryFn: () =>
      unwrap(
        api.GET('/projects/{projectId}/ai/jobs', {
          params: { path: { projectId }, query: { limit: 100 } },
        }),
      ),
    refetchInterval: (q) => (q.state.data?.some((j) => isAIJobActive(j.status)) ? 3_000 : false),
  });
}

/** One job, polled every 1.5s while it is queued or running. */
export function useAIJob(id: string) {
  return useQuery({
    queryKey: keys.aiJob(id),
    queryFn: () => unwrap(api.GET('/ai/jobs/{jobId}', { params: { path: { jobId: id } } })),
    refetchInterval: (q) => (q.state.data && isAIJobActive(q.state.data.status) ? 1_500 : false),
  });
}

export function useCreateAIJob(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AIJobCreate) =>
      unwrap(api.POST('/projects/{projectId}/ai/jobs', { params: { path: { projectId } }, body })),
    onSuccess: (job) => {
      qc.setQueryData(keys.aiJob(job.id), job);
      void qc.invalidateQueries({ queryKey: keys.aiJobs(projectId) });
      void qc.invalidateQueries({ queryKey: keys.aiProviders });
    },
  });
}

export function useApproveAIJob(job: AIJob) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AIJobApprove) =>
      unwrap(api.POST('/ai/jobs/{jobId}/approve', { params: { path: { jobId: job.id } }, body })),
    onSuccess: (res) => {
      qc.setQueryData(keys.scenario(res.scenario.id), res.scenario);
      void qc.invalidateQueries({ queryKey: keys.aiJob(job.id) });
      void qc.invalidateQueries({ queryKey: keys.aiJobs(job.projectId) });
      void qc.invalidateQueries({ queryKey: ['projects', job.projectId, 'scenarios'] });
      void qc.invalidateQueries({ queryKey: keys.versions(res.scenario.id) });
    },
  });
}
