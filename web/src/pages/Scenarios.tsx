import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { Plus, X } from 'lucide-react';
import { useMe, useScenarios } from '@/api/queries';
import { Chip } from '@/components/chips';
import { Button, Card, EmptyState, ErrorAlert, Loading, PageHeader, Table } from '@/components/ui';
import { dateTime, humanDuration, load, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

export function ScenariosPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/scenarios' });
  const { tag } = useSearch({ from: '/app/projects/$projectId/scenarios' });
  const navigate = useNavigate({ from: '/projects/$projectId/scenarios' });
  const me = useMe();
  const can = permissions(me.role);
  const scenarios = useScenarios(projectId, tag);
  // All tags come from the unfiltered list.
  const all = useScenarios(projectId);
  const tags = [...new Set((all.data ?? []).flatMap((s) => s.tags))].sort();

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Scenarios"
        description="How your users behave, as versioned YAML."
        actions={
          can.editScenarios && (
            <Button
              variant="primary"
              onClick={() =>
                void navigate({ to: '/projects/$projectId/scenarios/new', params: { projectId } })
              }
            >
              <Plus className="size-4" aria-hidden /> New scenario
            </Button>
          )
        }
      />
      {tags.length > 0 && (
        <div
          className="mb-3 flex flex-wrap items-center gap-1.5"
          role="group"
          aria-label="Filter by tag"
        >
          <span className="mr-1 text-[13px] text-muted">Tags</span>
          {tags.map((t) => (
            <button
              key={t}
              type="button"
              aria-pressed={tag === t}
              onClick={() => void navigate({ search: tag === t ? {} : { tag: t } })}
              className="rounded-full"
            >
              <Chip tone={tag === t ? 'info' : 'neutral'}>{t}</Chip>
            </button>
          ))}
          {tag && (
            <Button size="sm" variant="ghost" onClick={() => void navigate({ search: {} })}>
              <X className="size-3.5" aria-hidden /> Clear
            </Button>
          )}
        </div>
      )}
      <Card>
        {scenarios.isPending ? (
          <Loading />
        ) : scenarios.error ? (
          <ErrorAlert error={scenarios.error} className="m-4" />
        ) : scenarios.data.length === 0 ? (
          <EmptyState title={tag ? `No scenarios tagged ${tag}` : 'No scenarios yet'}>
            {!tag && 'A scenario describes journeys, load and pass/fail targets.'}
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Tags</th>
                <th>Load</th>
                <th className="!text-right">Duration</th>
                <th className="!text-right">Version</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {scenarios.data.map((s) => {
                const plan = s.latestVersion.plan;
                return (
                  <tr key={s.id} className="hover:bg-surface-2/60">
                    <td>
                      <Link
                        to="/projects/$projectId/scenarios/$scenarioId"
                        params={{ projectId, scenarioId: s.id }}
                        className="font-medium hover:underline"
                      >
                        {s.name}
                      </Link>
                      {s.description && (
                        <div className="max-w-md truncate text-xs text-muted">{s.description}</div>
                      )}
                    </td>
                    <td>
                      <div className="flex flex-wrap gap-1">
                        {s.tags.map((t) => (
                          <Chip key={t}>{t}</Chip>
                        ))}
                      </div>
                    </td>
                    <td className="num text-xs">
                      {plan ? (
                        <>
                          {plan.shape ?? plan.executor} · {load(plan.peak, plan.mode)}
                        </>
                      ) : (
                        <span className="text-muted">–</span>
                      )}
                    </td>
                    <td className="num text-right text-xs">
                      {plan ? humanDuration(plan.durationSeconds) : '–'}
                    </td>
                    <td className="num text-right">v{s.latestVersion.version}</td>
                    <td
                      className="text-xs whitespace-nowrap text-muted"
                      title={dateTime(s.updatedAt)}
                    >
                      {relativeTime(s.updatedAt)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}
