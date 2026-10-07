import type { QueryClient } from '@tanstack/react-query';
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
  type RouterHistory,
} from '@tanstack/react-router';
import { meQuery, projectsQuery, versionQuery } from '@/api/queries';
import { Shell } from '@/app/Shell';
import { NotFound, RouteError } from '@/app/RouteError';
import { LoginPage } from '@/pages/Login';
import { SetupPage } from '@/pages/Setup';
import { lastProject } from '@/lib/lastProject';

export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  beforeLoad: async ({ context }) => {
    const v = await context.queryClient.query({ ...versionQuery, staleTime: 'static' });
    if (!v.setupRequired) throw redirect({ to: '/login' });
  },
  component: SetupPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  validateSearch: (s: Record<string, unknown>): { redirect?: string } =>
    typeof s.redirect === 'string' ? { redirect: s.redirect } : {},
  beforeLoad: async ({ context }) => {
    const v = await context.queryClient.query({ ...versionQuery, staleTime: 'static' });
    if (v.setupRequired) throw redirect({ to: '/setup' });
    const me = await context.queryClient.query({ ...meQuery, staleTime: 'static' });
    if (me) throw redirect({ to: '/' });
  },
  component: LoginPage,
});

/** Everything behind sign-in. */
const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  beforeLoad: async ({ context, location }) => {
    const v = await context.queryClient.query({ ...versionQuery, staleTime: 'static' });
    if (v.setupRequired) throw redirect({ to: '/setup' });
    const me = await context.queryClient.query({ ...meQuery, staleTime: 'static' });
    if (!me) {
      throw redirect({
        to: '/login',
        search: location.href !== '/' ? { redirect: location.href } : {},
      });
    }
  },
  component: Shell,
});

const indexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/',
  beforeLoad: async ({ context }) => {
    const projects = await context.queryClient.query({ ...projectsQuery, staleTime: 'static' });
    const remembered = lastProject.get();
    const p = projects.find((x) => x.id === remembered) ?? projects[0];
    if (p) throw redirect({ to: '/projects/$projectId', params: { projectId: p.id } });
    throw redirect({ to: '/projects' });
  },
});

const projectsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/projects',
  component: lazyRouteComponent(() => import('@/pages/Projects'), 'ProjectsPage'),
});

const projectRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/projects/$projectId',
  component: Outlet,
});

const projectIndexRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/',
  component: lazyRouteComponent(() => import('@/pages/ProjectOverview'), 'ProjectOverviewPage'),
});

export interface RunsSearch {
  scenarioId?: string;
  before?: string;
}

const runsRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/runs',
  validateSearch: (s: Record<string, unknown>): RunsSearch => ({
    ...(typeof s.scenarioId === 'string' && s.scenarioId ? { scenarioId: s.scenarioId } : {}),
    ...(typeof s.before === 'string' && s.before ? { before: s.before } : {}),
  }),
  component: lazyRouteComponent(() => import('@/pages/Runs'), 'RunsPage'),
});

const scenariosRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/scenarios',
  validateSearch: (s: Record<string, unknown>): { tag?: string } =>
    typeof s.tag === 'string' && s.tag ? { tag: s.tag } : {},
  component: lazyRouteComponent(() => import('@/pages/Scenarios'), 'ScenariosPage'),
});

const scenarioNewRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/scenarios/new',
  component: lazyRouteComponent(() => import('@/pages/ScenarioEditor'), 'NewScenarioPage'),
});

const scenarioRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/scenarios/$scenarioId',
  validateSearch: (s: Record<string, unknown>): { version?: number } => {
    const v = Number(s.version);
    return Number.isInteger(v) && v > 0 ? { version: v } : {};
  },
  component: lazyRouteComponent(() => import('@/pages/ScenarioEditor'), 'ScenarioEditorPage'),
});

const scenarioCoverageRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/scenarios/$scenarioId/coverage',
  validateSearch: (s: Record<string, unknown>): { tab?: 'coverage' | 'drift' } =>
    s.tab === 'coverage' || s.tab === 'drift' ? { tab: s.tab } : {},
  component: lazyRouteComponent(() => import('@/pages/ScenarioCoverage'), 'ScenarioCoveragePage'),
});

const schedulesRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/schedules',
  component: lazyRouteComponent(() => import('@/pages/Schedules'), 'SchedulesPage'),
});

const targetsRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/targets',
  component: lazyRouteComponent(() => import('@/pages/Targets'), 'TargetsPage'),
});

const secretsRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/secrets',
  component: lazyRouteComponent(() => import('@/pages/Secrets'), 'SecretsPage'),
});

const aiStudioRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/ai',
  component: lazyRouteComponent(() => import('@/pages/AIStudio'), 'AIStudioPage'),
});

const aiJobRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/ai/$jobId',
  component: lazyRouteComponent(() => import('@/pages/AIJobPage'), 'AIJobPage'),
});

/** Runs to compare, as comma-separated ids, and the names of both sides. */
export interface CompareSearch {
  a: string[];
  b: string[];
  labelA?: string;
  labelB?: string;
}

const uuidRe = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function idList(v: unknown): string[] {
  const parts = Array.isArray(v) ? v.map(String) : typeof v === 'string' ? v.split(',') : [];
  return parts.map((s) => s.trim()).filter((s) => uuidRe.test(s));
}

const compareRoute = createRoute({
  getParentRoute: () => projectRoute,
  path: '/compare',
  validateSearch: (s: Record<string, unknown>): CompareSearch => ({
    a: idList(s.a),
    b: idList(s.b),
    ...(typeof s.labelA === 'string' && s.labelA ? { labelA: s.labelA } : {}),
    ...(typeof s.labelB === 'string' && s.labelB ? { labelB: s.labelB } : {}),
  }),
  component: lazyRouteComponent(() => import('@/pages/Compare'), 'ComparePage'),
});

const runRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/runs/$runId',
  component: lazyRouteComponent(() => import('@/pages/RunPage'), 'RunPage'),
});

const libraryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/library',
  component: lazyRouteComponent(() => import('@/pages/Library'), 'LibraryPage'),
});

const packRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/library/$packName',
  component: lazyRouteComponent(() => import('@/pages/Library'), 'PackPage'),
});

const workersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/workers',
  component: lazyRouteComponent(() => import('@/pages/Workers'), 'WorkersPage'),
});

export type SettingsTab =
  | 'account'
  | 'tokens'
  | 'users'
  | 'audit'
  | 'integrations'
  | 'notifications'
  | 'ai'
  | 'sso'
  | 'limits';

const settingsTabs: readonly SettingsTab[] = [
  'account',
  'tokens',
  'users',
  'audit',
  'integrations',
  'notifications',
  'ai',
  'sso',
  'limits',
];

const settingsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/settings',
  validateSearch: (s: Record<string, unknown>): { tab?: SettingsTab } =>
    settingsTabs.includes(s.tab as SettingsTab) ? { tab: s.tab as SettingsTab } : {},
  component: lazyRouteComponent(() => import('@/pages/Settings'), 'SettingsPage'),
});

const routeTree = rootRoute.addChildren([
  setupRoute,
  loginRoute,
  appRoute.addChildren([
    indexRoute,
    projectsRoute,
    projectRoute.addChildren([
      projectIndexRoute,
      runsRoute,
      scenariosRoute,
      scenarioNewRoute,
      scenarioRoute,
      scenarioCoverageRoute,
      schedulesRoute,
      targetsRoute,
      secretsRoute,
      aiStudioRoute,
      aiJobRoute,
      compareRoute,
    ]),
    runRoute,
    libraryRoute,
    packRoute,
    workersRoute,
    settingsRoute,
  ]),
]);

export function makeRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    context: { queryClient },
    ...(history ? { history } : {}),
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
  });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
