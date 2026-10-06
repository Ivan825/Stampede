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

const runRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/runs/$runId',
  component: lazyRouteComponent(() => import('@/pages/RunPage'), 'RunPage'),
});

const workersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/workers',
  component: lazyRouteComponent(() => import('@/pages/Workers'), 'WorkersPage'),
});

export type SettingsTab =
  'account' | 'tokens' | 'users' | 'audit' | 'integrations' | 'notifications';

const settingsTabs: readonly SettingsTab[] = [
  'account',
  'tokens',
  'users',
  'audit',
  'integrations',
  'notifications',
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
      targetsRoute,
      secretsRoute,
    ]),
    runRoute,
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
