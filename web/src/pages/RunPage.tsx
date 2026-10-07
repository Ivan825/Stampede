import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { OctagonX, ShieldAlert, Square } from 'lucide-react';
import { useCallback, useEffect, useMemo } from 'react';
import { ApiError } from '@/api/client';
import {
  keys,
  useKillRun,
  useMe,
  useProject,
  useReport,
  useRun,
  useRunEvents,
  useStopRun,
  useTargets,
  useTimeline,
} from '@/api/queries';
import type { Run } from '@/api/types';
import { isTerminal } from '@/api/types';
import { StatusChip, VerdictChip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Confirm } from '@/components/dialog';
import { useToast } from '@/components/toast';
import { Button, ErrorAlert, Loading, Notice } from '@/components/ui';
import { LiveView } from '@/features/runs/LiveView';
import { Downloads, ReportView } from '@/features/runs/ReportView';
import { DryRunGatePanel, EventFeed } from '@/features/runs/RunEvents';
import { dryRunGate, mergeEvents } from '@/features/runs/eventLog';
import { cli } from '@/lib/cli';
import { dateTime } from '@/lib/format';
import { permissions } from '@/lib/roles';
import { useRunStream } from '@/lib/useRunStream';

function RunHeader({ run }: { run: Run }) {
  const me = useMe();
  const can = permissions(me.role);
  const stop = useStopRun();
  const kill = useKillRun();
  const toast = useToast();
  const project = useProject(run.projectId);
  const target = useTargets(run.projectId).data?.find((t) => t.id === run.targetId);
  const active = !isTerminal(run.status);
  return (
    <div className="mb-5 flex flex-wrap items-start gap-3">
      <div className="min-w-0 flex-1">
        <div className="mb-1 text-xs text-muted">
          <Link
            to="/projects/$projectId/runs"
            params={{ projectId: run.projectId }}
            className="hover:underline"
          >
            Runs
          </Link>{' '}
          / <span className="font-mono">{run.id.slice(0, 8)}</span>
        </div>
        <h1 className="flex flex-wrap items-center gap-2 text-lg font-semibold tracking-tight">
          <Link
            to="/projects/$projectId/scenarios/$scenarioId"
            params={{ projectId: run.projectId, scenarioId: run.scenarioId }}
            className="hover:underline"
          >
            {run.scenarioName ?? 'Scenario'}
          </Link>
          <span className="num text-sm font-normal text-muted">v{run.scenarioVersion}</span>
          <StatusChip status={run.status} />
          {run.verdict && <VerdictChip verdict={run.verdict} />}
        </h1>
        <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-muted">
          <span className="font-mono">{run.targetURL}</span>
          <span>started {dateTime(run.startedAt ?? run.createdAt)}</span>
          {run.createdBy && <span>by {run.createdBy}</span>}
          {run.workers != null && (
            <span>
              {run.workers} worker{run.workers === 1 ? '' : 's'}
            </span>
          )}
          {run.overrides?.shape && <span>shape {run.overrides.shape}</span>}
          {run.stopReason && <span>stop: {run.stopReason}</span>}
        </p>
        {run.note && <p className="mt-1 text-[13px]">{run.note}</p>}
      </div>
      <div className="flex flex-col items-end gap-2">
        {active && can.stopRuns && (
          <div
            role="group"
            aria-label="Safety controls"
            className="flex items-center gap-2 rounded-md border border-fail/40 bg-fail-bg/50 py-1 pr-1 pl-2.5"
          >
            <span className="flex items-center gap-1.5 text-xs font-medium text-fail">
              <ShieldAlert className="size-3.5" aria-hidden /> Safety
            </span>
            <Button
              size="sm"
              onClick={() =>
                stop.mutate(run.id, {
                  onSuccess: () => toast.success('Stopping. In-flight iterations may finish.'),
                  onError: (e) => toast.error(e),
                })
              }
              loading={stop.isPending}
              disabled={run.status === 'stopping' || run.status === 'analyzing'}
            >
              <Square className="size-3.5" aria-hidden /> Stop
            </Button>
            <Confirm
              trigger={
                <Button size="sm" variant="danger-solid" aria-label="Kill switch: kill this run">
                  <OctagonX className="size-4" aria-hidden /> Kill
                </Button>
              }
              title="Kill this run?"
              description="All load stops immediately. In-flight requests are abandoned and the run ends as aborted."
              confirmLabel="Kill run"
              destructive
              onConfirm={async () => {
                await kill.mutateAsync(run.id);
                toast.success('Run killed.');
              }}
            />
          </div>
        )}
        {!active && <Downloads runId={run.id} />}
        {!active && (
          <CliHint
            command={cli.start({
              project: project.data?.slug,
              scenario: run.scenarioName,
              target: target?.name,
            })}
          >
            Run it again
          </CliHint>
        )}
      </div>
    </div>
  );
}

export function RunPage() {
  const { runId } = useParams({ from: '/app/runs/$runId' });
  const qc = useQueryClient();
  const runQ = useRun(runId, (q) =>
    q.state.data && !isTerminal(q.state.data.status) ? 5000 : false,
  );
  const initialActive = !!runQ.data && !isTerminal(runQ.data.status);
  const timeline = useTimeline(runId, initialActive);

  const onFinished = useCallback(
    (r: Run) => {
      qc.setQueryData(keys.run(runId), r);
      void qc.invalidateQueries({ queryKey: keys.run(runId) });
      void qc.invalidateQueries({ queryKey: keys.activeRuns });
    },
    [qc, runId],
  );
  const stream = useRunStream(runId, {
    enabled: initialActive,
    seed: timeline.data,
    onFinished,
  });

  // The stream's status is fresher than the polled run while live.
  const run =
    stream.run && runQ.data && stream.run.id === runQ.data.id
      ? { ...runQ.data, ...stream.run }
      : runQ.data;
  const finished = !!run && isTerminal(run.status);
  const report = useReport(runId, finished);
  const recorded = useRunEvents(runId, run && !finished ? 5000 : false);
  const events = useMemo(
    () => mergeEvents(recorded.data ?? [], stream.events),
    [recorded.data, stream.events],
  );
  // Fetch the events once more when the run ends, for its last ones.
  useEffect(() => {
    if (finished) void qc.invalidateQueries({ queryKey: keys.runEvents(runId) });
  }, [finished, qc, runId]);

  if (runQ.isPending) return <Loading />;
  if (runQ.error) return <ErrorAlert error={runQ.error} className="m-6" />;
  if (!run) return null;

  return (
    <div className="mx-auto max-w-7xl px-6 py-6">
      <RunHeader run={run} />
      {run.error && <ErrorAlert error={new Error(run.error)} className="mb-4" />}
      {finished && dryRunGate(events) && (
        <div className="mb-4">
          <DryRunGatePanel events={events} />
        </div>
      )}
      {!finished ? (
        <LiveView run={run} points={stream.points} events={events} state={stream.state} />
      ) : report.isPending ? (
        <Loading label="Loading report…" />
      ) : report.error ? (
        report.error instanceof ApiError && report.error.status === 409 ? (
          <Notice tone="warn">
            No report for this run: it ended as <b>{run.status}</b> before producing results.
          </Notice>
        ) : (
          <ErrorAlert error={report.error} />
        )
      ) : (
        <ReportView report={report.data} runId={run.id} />
      )}
      {finished && (
        <div className="mt-4">
          {recorded.error ? <ErrorAlert error={recorded.error} /> : <EventFeed events={events} />}
        </div>
      )}
    </div>
  );
}
