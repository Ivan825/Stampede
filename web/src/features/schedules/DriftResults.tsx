import { Link, useNavigate } from '@tanstack/react-router';
import { Sparkles } from 'lucide-react';
import type { ReactNode } from 'react';
import { useDriftResult, useDriftResults, useRepairDrift } from '@/api/queries';
import type { DriftResult } from '@/api/types';
import { Chip } from '@/components/chips';
import { Modal } from '@/components/dialog';
import { useToast } from '@/components/toast';
import {
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorAlert,
  Loading,
  Notice,
  Table,
} from '@/components/ui';
import { TraceView } from '@/features/ai/JourneyCard';
import { dateTime, relativeTime } from '@/lib/format';

export function DriftStatusChip({ status }: { status: string }) {
  if (status === 'ok') return <Chip tone="pass">NO DRIFT</Chip>;
  if (status === 'drifted') return <Chip tone="fail">DRIFTED</Chip>;
  return <Chip tone="warn">CHECK FAILED</Chip>;
}

const specChanges = (r: DriftResult) =>
  (r.removedEndpoints?.length ?? 0) + (r.addedEndpoints?.length ?? 0) + (r.unmatched?.length ?? 0);

/** Results of the project's scheduled drift checks, newest first. */
export function DriftResultsCard({
  projectId,
  onOpen,
}: {
  projectId: string;
  onOpen: (id: string) => void;
}) {
  const results = useDriftResults(projectId);
  return (
    <Card role="region" aria-label="Drift checks">
      <CardHeader
        title="Drift checks"
        description="Each check of a drift schedule runs every journey once with one user against the target and, when the schedule names a spec, compares the scenario with it. No load is generated."
      />
      {results.isPending ? (
        <Loading />
      ) : results.error ? (
        <ErrorAlert error={results.error} className="m-4" />
      ) : results.data.length === 0 ? (
        <EmptyState title="No drift checks yet">
          Create a schedule that checks for drift, or run one now with Check now.
        </EmptyState>
      ) : (
        <Table aria-label="Drift check results">
          <thead>
            <tr>
              <th>When</th>
              <th>Schedule</th>
              <th>Scenario</th>
              <th>Result</th>
              <th>Broken journeys</th>
              <th className="sr-only">Actions</th>
            </tr>
          </thead>
          <tbody>
            {results.data.map((r) => (
              <tr key={r.id}>
                <td className="text-xs whitespace-nowrap text-muted" title={dateTime(r.createdAt)}>
                  {relativeTime(r.createdAt)}
                </td>
                <td>{r.scheduleName || <span className="text-muted">deleted</span>}</td>
                <td>
                  <div>
                    {r.scenarioName}{' '}
                    <span className="num text-xs text-muted">v{r.scenarioVersion}</span>
                  </div>
                  <div className="font-mono text-xs text-muted">{r.targetURL}</div>
                </td>
                <td>
                  <DriftStatusChip status={r.status} />
                </td>
                <td className="max-w-64 text-xs">
                  {r.broken.length ? (
                    r.broken.join(', ')
                  ) : r.error ? (
                    <span className="text-muted">{r.error}</span>
                  ) : (
                    <span className="text-muted">None</span>
                  )}
                  {specChanges(r) > 0 && (
                    <div className="text-muted">
                      {specChanges(r)} spec change{specChanges(r) === 1 ? '' : 's'}
                    </div>
                  )}
                </td>
                <td className="text-right">
                  <Button
                    size="sm"
                    onClick={() => onOpen(r.id)}
                    aria-label={`View the drift check of ${relativeTime(r.createdAt)}${r.scheduleName ? ` by ${r.scheduleName}` : ''}`}
                  >
                    View
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function EndpointList({ title, items, help }: { title: string; items?: string[]; help: string }) {
  if (!items?.length) return null;
  return (
    <div>
      <h4 className="label-caps">{title}</h4>
      <p className="mb-1 text-xs text-muted">{help}</p>
      <ul className="flex flex-col gap-0.5 font-mono text-xs" aria-label={title}>
        {items.map((e) => (
          <li key={e}>{e}</li>
        ))}
      </ul>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  );
}

function DriftDetail({
  r,
  canRepair,
  projectId,
}: {
  r: DriftResult;
  canRepair: boolean;
  projectId: string;
}) {
  const repair = useRepairDrift(projectId);
  const navigate = useNavigate();
  const toast = useToast();
  const journeys = r.journeys ?? [];
  const repairable = r.status === 'drifted' && r.broken.length > 0;
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
        <DriftStatusChip status={r.status} />
        <span>
          {r.scenarioName} <span className="num text-muted">v{r.scenarioVersion}</span>
        </span>
        <span className="font-mono text-xs text-muted">{r.targetURL}</span>
        <span className="text-xs text-muted">{dateTime(r.createdAt)}</span>
      </div>
      {r.error && <ErrorAlert error={new Error(r.error)} />}

      {repairable && (
        <Notice tone="fail">
          <p className="font-medium">
            {r.broken.length === 1
              ? `The journey ${r.broken[0]} no longer works against the API.`
              : `The journeys ${r.broken.join(', ')} no longer work against the API.`}
          </p>
          <p className="mt-1 text-muted">
            Propose a fix to have the AI generator repair {r.broken.length === 1 ? 'it' : 'them'}{' '}
            from this evidence. Nothing is saved until you review and approve the proposed version.
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {canRepair && (
              <Button
                variant="primary"
                size="sm"
                loading={repair.isPending}
                onClick={() =>
                  repair.mutate(
                    { id: r.id },
                    {
                      onSuccess: (job) =>
                        void navigate({
                          to: '/projects/$projectId/ai/$jobId',
                          params: { projectId, jobId: job.id },
                        }),
                      onError: (e) => toast.error(e),
                    },
                  )
                }
              >
                <Sparkles className="size-3.5" aria-hidden /> Propose a fix
              </Button>
            )}
            {r.repairJobId && (
              <Link
                to="/projects/$projectId/ai/$jobId"
                params={{ projectId, jobId: r.repairJobId }}
                className="text-[13px] text-info hover:underline"
              >
                Review the proposed fix
              </Link>
            )}
          </div>
        </Notice>
      )}

      <Section title="Journeys">
        {journeys.length === 0 ? (
          <p className="text-[13px] text-muted">No journeys were dry-run.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {journeys.map((j) => (
              <div
                key={j.journey}
                className="rounded-md border border-line"
                role="group"
                aria-label={`Journey ${j.journey}`}
              >
                <div className="flex items-center gap-2 px-3 py-2">
                  <span className="min-w-0 flex-1 truncate text-[13px] font-medium">
                    {j.journey}
                  </span>
                  {j.ok ? <Chip tone="pass">PASSED</Chip> : <Chip tone="fail">BROKEN</Chip>}
                </div>
                {j.problem && (
                  <p className="border-t border-line px-3 py-1.5 font-mono text-xs text-fail">
                    {j.problem}
                  </p>
                )}
                {j.traces?.length ? (
                  <div className="flex flex-col gap-1.5 border-t border-line p-2">
                    {j.traces.map((t, i) => (
                      <TraceView key={i} trace={t} />
                    ))}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Section>

      {specChanges(r) > 0 && (
        <Section title="Spec changes">
          <EndpointList
            title="Removed endpoints"
            items={r.removedEndpoints}
            help="In the previous check's spec, but not in this one."
          />
          <EndpointList
            title="Requests with no endpoint"
            items={r.unmatched}
            help="Requests in the scenario that match no endpoint of the current spec."
          />
          <EndpointList
            title="Added endpoints"
            items={r.addedEndpoints}
            help="New since the previous check; no journey uses them yet."
          />
        </Section>
      )}
    </div>
  );
}

/** One drift check with its dry-run traces and spec changes. */
export function DriftResultDialog({
  id,
  projectId,
  canRepair,
  onClose,
}: {
  id: string | null;
  projectId: string;
  canRepair: boolean;
  onClose: () => void;
}) {
  const result = useDriftResult(id);
  return (
    <Modal
      open={!!id}
      onOpenChange={(v) => !v && onClose()}
      title={result.data?.scheduleName ? `Drift check: ${result.data.scheduleName}` : 'Drift check'}
      width="max-w-3xl"
      footer={<Button onClick={onClose}>Close</Button>}
    >
      {result.isPending ? (
        <Loading />
      ) : result.error ? (
        <ErrorAlert error={result.error} />
      ) : (
        <DriftDetail r={result.data} canRepair={canRepair} projectId={projectId} />
      )}
    </Modal>
  );
}
