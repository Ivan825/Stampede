import { useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, Play } from 'lucide-react';
import { useState } from 'react';
import { useMe, useRuns, useScenarios } from '@/api/queries';
import { Button, Card, EmptyState, ErrorAlert, Loading, PageHeader, Select } from '@/components/ui';
import { NewRunDialog } from '@/features/runs/NewRunDialog';
import { RunsTable } from '@/features/runs/RunsTable';
import { dateTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

const PAGE = 25;

export function RunsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/runs' });
  const search = useSearch({ from: '/app/projects/$projectId/runs' });
  const navigate = useNavigate({ from: '/projects/$projectId/runs' });
  const me = useMe();
  const can = permissions(me.role);
  const scenarios = useScenarios(projectId);
  const runs = useRuns(
    projectId,
    { scenarioId: search.scenarioId, before: search.before, limit: PAGE },
    search.before ? undefined : 5_000,
  );
  const [newRun, setNewRun] = useState(false);
  const last = runs.data?.[runs.data.length - 1];

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Runs"
        actions={
          can.startRuns && (
            <Button variant="primary" onClick={() => setNewRun(true)}>
              <Play className="size-3.5" aria-hidden /> New run
            </Button>
          )
        }
      />
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <label htmlFor="runs-scenario" className="text-[13px] text-muted">
          Scenario
        </label>
        <Select
          id="runs-scenario"
          className="!w-64"
          value={search.scenarioId ?? ''}
          onChange={(e) =>
            void navigate({
              search: e.target.value ? { scenarioId: e.target.value } : {},
            })
          }
        >
          <option value="">All scenarios</option>
          {scenarios.data?.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        {search.before && (
          <span className="text-xs text-muted">Showing runs before {dateTime(search.before)}</span>
        )}
      </div>
      <Card>
        {runs.isPending ? (
          <Loading />
        ) : runs.error ? (
          <ErrorAlert error={runs.error} className="m-4" />
        ) : runs.data.length === 0 ? (
          <EmptyState title={search.before ? 'No older runs' : 'No runs match'} />
        ) : (
          <RunsTable runs={runs.data} />
        )}
      </Card>
      <nav className="mt-3 flex justify-end gap-2" aria-label="Pagination">
        <Button
          size="sm"
          disabled={!search.before}
          onClick={() => void navigate({ search: (s) => ({ scenarioId: s.scenarioId }) })}
        >
          <ChevronLeft className="size-3.5" aria-hidden /> Newest
        </Button>
        <Button
          size="sm"
          disabled={!last || (runs.data?.length ?? 0) < PAGE}
          onClick={() =>
            last && void navigate({ search: (s) => ({ ...s, before: last.createdAt }) })
          }
        >
          Older <ChevronRight className="size-3.5" aria-hidden />
        </Button>
      </nav>
      {can.startRuns && (
        <NewRunDialog
          projectId={projectId}
          open={newRun}
          onOpenChange={setNewRun}
          scenarioId={search.scenarioId}
        />
      )}
    </div>
  );
}
