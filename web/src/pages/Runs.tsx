import { useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { ChevronLeft, ChevronRight, GitCompare } from 'lucide-react';
import { useState } from 'react';
import { useProject, useRuns, useScenarios } from '@/api/queries';
import type { Run } from '@/api/types';
import { hasReport } from '@/api/types';
import { CliHint } from '@/components/cliHint';
import { Button, Card, EmptyState, ErrorAlert, Loading, PageHeader, Select } from '@/components/ui';
import { CompareDialog } from '@/features/runs/CompareDialog';
import { MAX_PER_SIDE } from '@/features/runs/compareSides';
import { RunsTable, type RunSelection } from '@/features/runs/RunsTable';
import { cli } from '@/lib/cli';
import { dateTime } from '@/lib/format';

const PAGE = 25;

export function RunsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/runs' });
  const search = useSearch({ from: '/app/projects/$projectId/runs' });
  const navigate = useNavigate({ from: '/projects/$projectId/runs' });
  const project = useProject(projectId);
  const scenarios = useScenarios(projectId);
  const runs = useRuns(
    projectId,
    { scenarioId: search.scenarioId, before: search.before, limit: PAGE },
    search.before ? undefined : 5_000,
  );
  const [comparing, setComparing] = useState(false);
  // Selected runs by id; kept across pages so runs from several pages can be compared.
  const [selected, setSelected] = useState<Map<string, Run>>(new Map());
  const last = runs.data?.[runs.data.length - 1];
  const selection: RunSelection = {
    selected: new Set(selected.keys()),
    toggle: (r) =>
      setSelected((m) => {
        const n = new Map(m);
        if (n.has(r.id)) n.delete(r.id);
        else n.set(r.id, r);
        return n;
      }),
    blocked: (r) =>
      !hasReport(r)
        ? 'Only finished runs with a report can be compared.'
        : selected.size >= 2 * MAX_PER_SIDE
          ? `At most ${2 * MAX_PER_SIDE} runs can be compared.`
          : undefined,
  };

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Runs"
        actions={
          <CliHint
            command={cli.start({
              project: project.data?.slug,
              scenario: scenarios.data?.find((s) => s.id === search.scenarioId)?.name,
            })}
          >
            Start a run from the terminal
          </CliHint>
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
        <div className="flex-1" />
        {selected.size === 0 ? (
          <span className="text-xs text-muted">Select finished runs to compare them.</span>
        ) : (
          <>
            <span className="text-xs text-muted" role="status">
              {selected.size} run{selected.size === 1 ? '' : 's'} selected
            </span>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Map())}>
              Clear
            </Button>
            <Button
              size="sm"
              variant="primary"
              disabled={selected.size < 2}
              onClick={() => setComparing(true)}
            >
              <GitCompare className="size-3.5" aria-hidden /> Compare…
            </Button>
          </>
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
          <RunsTable runs={runs.data} selection={selection} />
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
      {comparing && (
        <CompareDialog
          projectId={projectId}
          runs={[...selected.values()]}
          open
          onOpenChange={setComparing}
        />
      )}
    </div>
  );
}
