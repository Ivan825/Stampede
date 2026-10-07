import * as Menu from '@radix-ui/react-dropdown-menu';
import { useQueryClient } from '@tanstack/react-query';
import { Link, Outlet, useNavigate, useParams, useRouterState } from '@tanstack/react-router';
import { clsx } from 'clsx';
import {
  Activity,
  CalendarClock,
  Check,
  ChevronsUpDown,
  FolderKanban,
  FileCode2,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Package,
  Server,
  Settings,
  Sparkles,
  Target as TargetIcon,
} from 'lucide-react';
import { useEffect, type ReactNode } from 'react';
import * as Tooltip from '@radix-ui/react-tooltip';
import { onUnauthorized } from '@/api/client';
import { keys, useLogout, useMe, useProjects } from '@/api/queries';
import { Mark, ThemeToggle } from '@/components/misc';
import { RoleChip } from '@/components/chips';
import { lastProject } from '@/lib/lastProject';
import { KillSwitch } from './KillSwitch';

const menuContent =
  'z-50 min-w-56 rounded-md border border-line bg-surface p-1 text-[13px] shadow-lg';
const menuItem =
  'flex cursor-default items-center gap-2 rounded px-2 py-1.5 outline-none select-none data-[highlighted]:bg-surface-2';

function ProjectSwitcher({ currentId }: { currentId: string | null }) {
  const projects = useProjects();
  const navigate = useNavigate();
  const current = projects.data?.find((p) => p.id === currentId);
  return (
    <Menu.Root>
      <Menu.Trigger asChild>
        <button
          type="button"
          className="flex h-8 max-w-64 items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-[13px] hover:border-line-strong"
          aria-label="Switch project"
        >
          <FolderKanban className="size-3.5 text-muted" aria-hidden />
          <span className="truncate font-medium">{current?.name ?? 'Select a project'}</span>
          <ChevronsUpDown className="size-3.5 text-muted" aria-hidden />
        </button>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content className={menuContent} align="start" sideOffset={4}>
          <Menu.Label className="label-caps px-2 py-1">Projects</Menu.Label>
          {projects.data?.map((p) => (
            <Menu.Item
              key={p.id}
              className={menuItem}
              onSelect={() =>
                void navigate({ to: '/projects/$projectId', params: { projectId: p.id } })
              }
            >
              <Check
                className={clsx('size-3.5', p.id === currentId ? 'opacity-100' : 'opacity-0')}
                aria-hidden
              />
              <span className="truncate">{p.name}</span>
            </Menu.Item>
          ))}
          <Menu.Separator className="my-1 h-px bg-line" />
          <Menu.Item className={menuItem} onSelect={() => void navigate({ to: '/projects' })}>
            <span className="size-3.5" />
            All projects…
          </Menu.Item>
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>
  );
}

function UserMenu() {
  const me = useMe();
  const logout = useLogout();
  const navigate = useNavigate();
  const initials = me.name
    .split(/\s+/)
    .map((s) => s[0])
    .join('')
    .slice(0, 2)
    .toUpperCase();
  return (
    <Menu.Root>
      <Menu.Trigger asChild>
        <button
          type="button"
          className="flex size-8 items-center justify-center rounded-full border border-line bg-surface-2 text-xs font-semibold hover:border-line-strong"
          aria-label={`Account menu for ${me.name}`}
        >
          {initials || '?'}
        </button>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content className={menuContent} align="end" sideOffset={4}>
          <div className="px-2 py-1.5">
            <div className="font-medium">{me.name}</div>
            <div className="text-xs text-muted">{me.email}</div>
            <div className="mt-1.5 flex items-center gap-1.5 text-xs text-muted">
              {me.orgName} <RoleChip role={me.role} />
            </div>
          </div>
          <Menu.Separator className="my-1 h-px bg-line" />
          <Menu.Item className={menuItem} onSelect={() => void navigate({ to: '/settings' })}>
            <Settings className="size-3.5" aria-hidden /> Settings
          </Menu.Item>
          <Menu.Item
            className={menuItem}
            onSelect={() =>
              logout.mutate(undefined, { onSettled: () => void navigate({ to: '/login' }) })
            }
          >
            <LogOut className="size-3.5" aria-hidden /> Sign out
          </Menu.Item>
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>
  );
}

function NavItem({
  to,
  params,
  icon,
  children,
  exact,
  indent,
}: {
  to: string;
  params?: Record<string, string>;
  icon: ReactNode;
  children: ReactNode;
  exact?: boolean;
  indent?: boolean;
}) {
  return (
    <Link
      to={to}
      params={params}
      activeOptions={{ exact: !!exact, includeSearch: false }}
      className={clsx(
        'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] text-muted hover:bg-surface-2 hover:text-fg',
        'data-[status=active]:bg-surface-2 data-[status=active]:font-medium data-[status=active]:text-fg',
        indent && 'ml-3.5',
      )}
    >
      <span className="text-current [&>svg]:size-4" aria-hidden>
        {icon}
      </span>
      {children}
    </Link>
  );
}

export function Shell() {
  const params: { projectId?: string } = useParams({ strict: false });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useRouterState({ select: (s) => s.location.href });
  const projects = useProjects();

  useEffect(() => {
    if (params.projectId) lastProject.set(params.projectId);
  }, [params.projectId]);

  useEffect(
    () =>
      onUnauthorized(() => {
        qc.setQueryData(keys.me, null);
        void navigate({ to: '/login', search: { redirect: location } });
      }),
    [qc, navigate, location],
  );

  const remembered = lastProject.get();
  const projectId =
    params.projectId ??
    (projects.data?.some((p) => p.id === remembered) ? remembered : null) ??
    projects.data?.[0]?.id ??
    null;
  const project = projects.data?.find((p) => p.id === projectId);

  return (
    <Tooltip.Provider>
      <div className="flex h-full min-h-0">
        <a
          href="#main"
          className="sr-only z-50 rounded bg-surface px-3 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
        >
          Skip to content
        </a>
        <aside className="flex w-56 shrink-0 flex-col border-r border-line bg-surface">
          <Link to="/" className="flex h-12 items-center gap-2 border-b border-line px-4">
            <Mark className="h-5 w-auto" />
            <span className="text-[15px] font-semibold tracking-tight">Stampede</span>
          </Link>
          <nav className="flex flex-1 flex-col gap-0.5 overflow-y-auto p-2" aria-label="Main">
            <NavItem to="/projects" icon={<FolderKanban />} exact>
              Projects
            </NavItem>
            {project && (
              <div className="mt-0.5 mb-1 flex flex-col gap-0.5">
                <div
                  className="ml-3.5 truncate px-2.5 pt-1 pb-0.5 text-[11px] font-medium text-muted"
                  title={project.name}
                >
                  {project.name}
                </div>
                <NavItem
                  to="/projects/$projectId"
                  params={{ projectId: project.id }}
                  icon={<LayoutDashboard />}
                  exact
                  indent
                >
                  Overview
                </NavItem>
                <NavItem
                  to="/projects/$projectId/runs"
                  params={{ projectId: project.id }}
                  icon={<Activity />}
                  indent
                >
                  Runs
                </NavItem>
                <NavItem
                  to="/projects/$projectId/schedules"
                  params={{ projectId: project.id }}
                  icon={<CalendarClock />}
                  indent
                >
                  Schedules
                </NavItem>
                <NavItem
                  to="/projects/$projectId/scenarios"
                  params={{ projectId: project.id }}
                  icon={<FileCode2 />}
                  indent
                >
                  Scenarios
                </NavItem>
                <NavItem
                  to="/projects/$projectId/ai"
                  params={{ projectId: project.id }}
                  icon={<Sparkles />}
                  indent
                >
                  AI studio
                </NavItem>
                <NavItem
                  to="/projects/$projectId/targets"
                  params={{ projectId: project.id }}
                  icon={<TargetIcon />}
                  indent
                >
                  Targets
                </NavItem>
                <NavItem
                  to="/projects/$projectId/secrets"
                  params={{ projectId: project.id }}
                  icon={<KeyRound />}
                  indent
                >
                  Secrets
                </NavItem>
              </div>
            )}
            <NavItem to="/library" icon={<Package />}>
              Library
            </NavItem>
            <NavItem to="/workers" icon={<Server />}>
              Workers
            </NavItem>
            <NavItem to="/settings" icon={<Settings />}>
              Settings
            </NavItem>
          </nav>
        </aside>
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-4">
            <ProjectSwitcher currentId={projectId} />
            <div className="flex-1" />
            <KillSwitch />
            <ThemeToggle />
            <UserMenu />
          </header>
          <main id="main" className="min-h-0 flex-1 overflow-y-auto" tabIndex={-1}>
            <Outlet />
          </main>
        </div>
      </div>
    </Tooltip.Provider>
  );
}
