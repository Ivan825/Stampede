import { Link, useParams, useSearch } from '@tanstack/react-router';
import { useCompare } from '@/api/queries';
import { Card, EmptyState, ErrorAlert, Loading, PageHeader } from '@/components/ui';
import { ComparisonView } from '@/features/runs/ComparisonView';

export function ComparePage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/compare' });
  const search = useSearch({ from: '/app/projects/$projectId/compare' });
  const body = {
    a: search.a,
    b: search.b,
    ...(search.labelA ? { labelA: search.labelA } : {}),
    ...(search.labelB ? { labelB: search.labelB } : {}),
  };
  const ready = body.a.length > 0 && body.b.length > 0;
  const cmp = useCompare(body, ready);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        breadcrumb={
          <Link to="/projects/$projectId/runs" params={{ projectId }} className="hover:underline">
            Runs
          </Link>
        }
        title="Compare runs"
        description="Did version B change performance compared with version A, beyond the noise between repeated runs?"
      />
      {!ready ? (
        <Card>
          <EmptyState title="Choose runs to compare">
            Select finished runs on the{' '}
            <Link
              to="/projects/$projectId/runs"
              params={{ projectId }}
              className="text-fg underline"
            >
              Runs
            </Link>{' '}
            page and choose Compare.
          </EmptyState>
        </Card>
      ) : cmp.isPending ? (
        <Loading label="Comparing…" />
      ) : cmp.error ? (
        <ErrorAlert error={cmp.error} />
      ) : (
        <ComparisonView c={cmp.data} />
      )}
    </div>
  );
}
