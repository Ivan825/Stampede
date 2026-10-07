import { Link, useParams } from '@tanstack/react-router';
import { useProject, useRuns, useScenarios, useTargets } from '@/api/queries';
import { Chip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Card, CardHeader, EmptyState, ErrorAlert, Loading, PageHeader } from '@/components/ui';
import { RunsTable } from '@/features/runs/RunsTable';
import { TargetBadge } from '@/features/targets/TargetBadge';
import { cli } from '@/lib/cli';
import { load, relativeTime } from '@/lib/format';

export function ProjectOverviewPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/' });
  const project = useProject(projectId);
  const runs = useRuns(projectId, { limit: 10 }, 5_000);
  const scenarios = useScenarios(projectId);
  const targets = useTargets(projectId);

  if (project.isPending) return <Loading />;
  if (project.error) return <ErrorAlert error={project.error} className="m-6" />;
  const p = project.data;

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title={p.name}
        description={p.description || undefined}
        actions={<CliHint command={cli.start({ project: p.slug })}>Start a run from the terminal</CliHint>}
      />

      <Card>
        <CardHeader
          title="Recent runs"
          actions={
            <Link
              to="/projects/$projectId/runs"
              params={{ projectId }}
              className="text-[13px] text-info hover:underline"
            >
              All runs
            </Link>
          }
        />
        {runs.isPending ? (
          <Loading />
        ) : runs.error ? (
          <ErrorAlert error={runs.error} className="m-4" />
        ) : runs.data.length === 0 ? (
          <EmptyState title="No runs yet">
            Runs started with <code className="font-mono text-xs">stampede start</code> appear here
            with throughput, latency and a verdict.
          </EmptyState>
        ) : (
          <RunsTable runs={runs.data} />
        )}
      </Card>

      <div className="mt-5 grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader
            title="Scenarios"
            actions={
              <Link
                to="/projects/$projectId/scenarios"
                params={{ projectId }}
                className="text-[13px] text-info hover:underline"
              >
                All scenarios
              </Link>
            }
          />
          {scenarios.isPending ? (
            <Loading />
          ) : scenarios.error ? (
            <ErrorAlert error={scenarios.error} className="m-4" />
          ) : scenarios.data.length === 0 ? (
            <EmptyState title="No scenarios">
              Describe how your users behave in YAML and save it with{' '}
              <code className="font-mono text-xs">stampede push</code>.
            </EmptyState>
          ) : (
            <ul className="divide-y divide-line">
              {scenarios.data.slice(0, 8).map((s) => {
                const plan = s.latestVersion.plan;
                return (
                  <li key={s.id} className="flex items-center gap-3 px-4 py-2.5">
                    <div className="min-w-0 flex-1">
                      <Link
                        to="/projects/$projectId/scenarios/$scenarioId"
                        params={{ projectId, scenarioId: s.id }}
                        className="font-medium hover:underline"
                      >
                        {s.name}
                      </Link>
                      <span className="num ml-1.5 text-xs text-muted">
                        v{s.latestVersion.version}
                      </span>
                      <div className="mt-0.5 flex flex-wrap gap-1">
                        {s.tags.map((t) => (
                          <Chip key={t}>{t}</Chip>
                        ))}
                      </div>
                    </div>
                    {plan && (
                      <span className="num text-xs text-muted">
                        {plan.shape ?? plan.executor} · {load(plan.peak, plan.mode)}
                      </span>
                    )}
                    <span className="w-24 text-right text-xs text-muted">
                      {relativeTime(s.updatedAt)}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>

        <Card>
          <CardHeader
            title="Targets"
            actions={
              <Link
                to="/projects/$projectId/targets"
                params={{ projectId }}
                className="text-[13px] text-info hover:underline"
              >
                All targets
              </Link>
            }
          />
          {targets.isPending ? (
            <Loading />
          ) : targets.error ? (
            <ErrorAlert error={targets.error} className="m-4" />
          ) : targets.data.length === 0 ? (
            <EmptyState title="No targets">
              Add the base URL of the system you test with{' '}
              <code className="font-mono text-xs">stampede targets create</code>.
            </EmptyState>
          ) : (
            <ul className="divide-y divide-line">
              {targets.data.map((t) => (
                <li key={t.id} className="flex items-center gap-3 px-4 py-2.5">
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{t.name}</div>
                    <div className="truncate font-mono text-xs text-muted">{t.baseURL}</div>
                  </div>
                  <TargetBadge target={t} />
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
