import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import { useSchedules } from '@/api/queries';
import type { Schedule, Verdict } from '@/api/types';
import { isActive } from '@/api/types';
import { Chip, StatusChip, VerdictChip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Card, EmptyState, ErrorAlert, Loading, PageHeader, Table } from '@/components/ui';
import {
  DriftResultDialog,
  DriftResultsCard,
  DriftStatusChip,
} from '@/features/schedules/DriftResults';
import { cli } from '@/lib/cli';
import { dateTime, relativeTime } from '@/lib/format';

/** A firing time in the schedule's own zone: "Thu 8 Oct, 02:00 BST". */
function zoned(iso: string, timeZone: string): string {
  try {
    return new Intl.DateTimeFormat('en-GB', {
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit',
      timeZone,
      timeZoneName: 'short',
    }).format(new Date(iso));
  } catch {
    return dateTime(iso);
  }
}

function LastDrift({ s, onOpen }: { s: Schedule; onOpen: (id: string) => void }) {
  if (!s.lastDriftStatus) return <span className="text-muted">Never</span>;
  const broken = s.lastDriftBroken ?? [];
  const summary = (
    <>
      <DriftStatusChip status={s.lastDriftStatus} />
      <span className="text-xs text-muted">{relativeTime(s.lastFiredAt)}</span>
    </>
  );
  return (
    <div className="flex flex-col gap-0.5">
      {s.lastDriftId ? (
        <button
          type="button"
          className="flex items-center gap-2 text-left hover:underline"
          aria-label={`View the last drift check of ${s.name}`}
          onClick={() => onOpen(s.lastDriftId!)}
        >
          {summary}
        </button>
      ) : (
        <div className="flex items-center gap-2">{summary}</div>
      )}
      {broken.length > 0 && (
        <span className="max-w-64 text-xs text-muted">Broken: {broken.join(', ')}</span>
      )}
    </div>
  );
}

function LastRun({ s, onOpenDrift }: { s: Schedule; onOpenDrift: (id: string) => void }) {
  if (s.kind === 'drift' && !s.lastSkipReason) return <LastDrift s={s} onOpen={onOpenDrift} />;
  if (s.lastSkipReason) {
    return (
      <div className="flex flex-col gap-0.5">
        <Chip tone="warn" title={s.lastSkipReason}>
          SKIPPED
        </Chip>
        <span className="max-w-64 text-xs text-muted">{s.lastSkipReason}</span>
      </div>
    );
  }
  if (!s.lastRunId) return <span className="text-muted">Never</span>;
  return (
    <Link
      to="/runs/$runId"
      params={{ runId: s.lastRunId }}
      className="flex items-center gap-2 hover:underline"
    >
      {s.lastRunStatus && isActive(s.lastRunStatus) ? (
        <StatusChip status={s.lastRunStatus} />
      ) : s.lastRunVerdict ? (
        <VerdictChip verdict={s.lastRunVerdict as Verdict} />
      ) : (
        s.lastRunStatus && <StatusChip status={s.lastRunStatus} />
      )}
      <span className="text-xs text-muted">{relativeTime(s.lastRunAt)}</span>
    </Link>
  );
}

export function SchedulesPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/schedules' });
  const schedules = useSchedules(projectId);
  const navigate = useNavigate();
  const search = useSearch({ from: '/app/projects/$projectId/schedules' });
  const openDrift = (id: string | undefined) =>
    void navigate({
      to: '/projects/$projectId/schedules',
      params: { projectId },
      search: id ? { drift: id } : {},
    });

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-4 px-6 py-6">
      <PageHeader
        title="Schedules"
        description="Runs that start on a cron schedule, such as a nightly regression check, and checks of a scenario for drift from the API it tests. Times are UTC unless a schedule names a time zone."
        actions={<CliHint command={cli.schedulesCreate}>Add a schedule</CliHint>}
      />
      <Card>
        {schedules.isPending ? (
          <Loading />
        ) : schedules.error ? (
          <ErrorAlert error={schedules.error} className="m-4" />
        ) : schedules.data.length === 0 ? (
          <EmptyState title="No schedules">
            A schedule runs a scenario against a target at set times, for example every night at
            02:00.
          </EmptyState>
        ) : (
          <>
            <Table aria-label="Schedules">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>When</th>
                  <th>Next run</th>
                  <th>Last run</th>
                  <th>Enabled</th>
                </tr>
              </thead>
              <tbody>
                {schedules.data.map((s) => (
                  <tr key={s.id}>
                    <td className="max-w-72">
                      <div className="font-medium">
                        {s.name}
                        {s.kind === 'drift' && (
                          <span className="ml-2 text-xs font-normal text-muted">drift check</span>
                        )}
                      </div>
                      <div className="truncate text-xs text-muted">
                        {s.scenarioName} → {s.targetName}
                      </div>
                      {s.note && <div className="truncate text-xs text-muted">{s.note}</div>}
                    </td>
                    <td>
                      <div className="font-mono text-xs">{s.cron}</div>
                      <div className="text-xs text-muted">{s.timezone}</div>
                    </td>
                    <td>
                      {s.nextRunAt ? (
                        <>
                          <div className="num">{zoned(s.nextRunAt, s.timezone)}</div>
                          <div className="text-xs text-muted">{relativeTime(s.nextRunAt)}</div>
                        </>
                      ) : (
                        <span className="text-muted">Disabled</span>
                      )}
                    </td>
                    <td>
                      <LastRun s={s} onOpenDrift={openDrift} />
                    </td>
                    <td>
                      {s.enabled ? <Chip tone="pass">ON</Chip> : <Chip>OFF</Chip>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <div className="border-t border-line px-4 py-2.5">
              <CliHint
                command={[cli.schedulesRun('<name>'), cli.schedulesUpdate('<name>')]}
                className="border-none px-0 py-0"
              >
                Run one now, change, enable or disable it from the terminal
              </CliHint>
            </div>
          </>
        )}
      </Card>
      {schedules.data?.some((s) => s.kind === 'drift') && (
        <DriftResultsCard projectId={projectId} onOpen={openDrift} />
      )}
      <DriftResultDialog
        id={search.drift ?? null}
        projectId={projectId}
        onClose={() => openDrift(undefined)}
      />
    </div>
  );
}
