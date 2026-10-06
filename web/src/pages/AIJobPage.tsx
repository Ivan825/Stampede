import * as Tabs from '@radix-ui/react-tabs';
import { Link, useParams } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { Check, Loader2 } from 'lucide-react';
import { useState } from 'react';
import { useAIJob, useMe } from '@/api/queries';
import type { AIJob } from '@/api/types';
import { isAIJobActive } from '@/api/types';
import { AIJobStatusChip } from '@/components/chips';
import { CopyButton } from '@/components/misc';
import {
  Card,
  EmptyState,
  ErrorAlert,
  Loading,
  Notice,
  PageHeader,
  SectionTitle,
  Stat,
} from '@/components/ui';
import { Approved, ApprovePanel } from '@/features/ai/ApprovePanel';
import { DiffView } from '@/features/ai/DiffView';
import { JourneyCard } from '@/features/ai/JourneyCard';
import { stageHelp, stageLabels, stages } from '@/features/ai/stages';
import { YamlEditor } from '@/features/scenarios/YamlEditor';
import { count, dateTime, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

function Progress({ job }: { job: AIJob }) {
  const current =
    job.status === 'queued' ? -1 : stages.indexOf(job.stage as (typeof stages)[number]);
  return (
    <Card className="px-4 py-3" role="status" aria-label="Progress">
      <ol className="flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]">
        {stages.slice(0, -1).map((s, i) => {
          const skipped = !job.dryRun && s === 'dry-run';
          const done = i < current;
          const active = i === current;
          return (
            <li
              key={s}
              aria-current={active ? 'step' : undefined}
              className={clsx(
                'flex items-center gap-1.5',
                active ? 'font-medium text-fg' : done ? 'text-fg' : 'text-muted',
                skipped && 'line-through',
              )}
            >
              {active ? (
                <Loader2 className="size-3.5 animate-spin text-primary" aria-hidden />
              ) : done ? (
                <Check className="size-3.5 text-pass" aria-hidden />
              ) : (
                <span className="size-3.5 rounded-full border border-line" aria-hidden />
              )}
              {stageLabels[s]}
              {s === 'repair' && job.round > 0 && (
                <span className="num text-xs text-muted">round {job.round}</span>
              )}
            </li>
          );
        })}
      </ol>
      <p className="mt-2 text-xs text-muted">
        {job.status === 'queued'
          ? 'Waiting for a free slot; the server runs two jobs at a time.'
          : (stageHelp[job.stage] ?? job.stage)}
        {job.round > 0 && job.status === 'running' && ` Repair round ${job.round}.`}
      </p>
    </Card>
  );
}

function Proposal({ job }: { job: AIJob }) {
  const [tab, setTab] = useState('yaml');
  const trigger =
    'h-9 border-b-2 border-transparent px-1 text-[13px] text-muted hover:text-fg data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg';
  return (
    <Card className="overflow-hidden">
      <Tabs.Root value={tab} onValueChange={setTab}>
        <div className="flex items-center gap-3 border-b border-line px-4">
          <Tabs.List className="flex flex-1 gap-4" aria-label="Proposal">
            <Tabs.Trigger value="yaml" className={trigger}>
              Scenario YAML
            </Tabs.Trigger>
            {job.scenarioId && (
              <Tabs.Trigger value="diff" className={trigger}>
                Diff
              </Tabs.Trigger>
            )}
          </Tabs.List>
          <CopyButton value={job.yaml ?? ''} label="Copy YAML" />
        </div>
        <Tabs.Content value="yaml" className="h-[460px]">
          <YamlEditor value={job.yaml ?? ''} resetKey={job.yaml ?? ''} readOnly />
        </Tabs.Content>
        {job.scenarioId && (
          <Tabs.Content value="diff">
            {job.diff ? (
              <DiffView diff={job.diff} />
            ) : (
              <EmptyState title="No differences">
                The proposal matches the latest version of the existing scenario.
              </EmptyState>
            )}
          </Tabs.Content>
        )}
      </Tabs.Root>
    </Card>
  );
}

export function AIJobPage() {
  const { projectId, jobId } = useParams({ from: '/app/projects/$projectId/ai/$jobId' });
  const me = useMe();
  const can = permissions(me.role);
  const q = useAIJob(jobId);

  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorAlert error={q.error} className="m-6" />;
  const job = q.data;
  const active = isAIJobActive(job.status);
  const finished = job.status === 'succeeded' || job.status === 'needs_review';
  const passed = job.journeys.filter((j) => j.status === 'passed').length;
  const fatal = job.problems.filter((p) => p.fatal);
  const other = job.problems.filter((p) => !p.fatal);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        breadcrumb={
          <Link to="/projects/$projectId/ai" params={{ projectId }} className="hover:underline">
            AI studio
          </Link>
        }
        title={
          <span className="flex items-center gap-3">
            Job <span className="font-mono">{job.id.slice(0, 8)}</span>
            <AIJobStatusChip status={job.status} />
          </span>
        }
        description={
          <>
            <span className="font-mono">
              {job.providerKind} / {job.model}
            </span>
            {' · '}
            {job.dryRun ? 'dry run' : 'no dry run'}
            {' · '}
            started by {job.createdBy ?? 'a deleted user'}{' '}
            <span title={dateTime(job.createdAt)}>{relativeTime(job.createdAt)}</span>
          </>
        }
      />

      <div className="flex flex-col gap-4">
        {active && <Progress job={job} />}
        {job.status === 'failed' && (
          <Notice tone="fail">
            <p className="font-medium">The job failed.</p>
            {job.error && <p className="mt-0.5 text-muted">{job.error}</p>}
          </Notice>
        )}
        {job.approvedAt ? (
          <Approved job={job} />
        ) : finished && can.generateJourneys ? (
          <ApprovePanel job={job} />
        ) : finished ? (
          <p className="text-[13px] text-muted">Editors can approve this proposal.</p>
        ) : null}

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat
            label="Journeys passed"
            value={job.journeys.length ? `${passed}/${job.journeys.length}` : '–'}
            tone={
              job.journeys.length && passed === job.journeys.length
                ? 'pass'
                : job.journeys.some((j) => j.status === 'flagged')
                  ? 'warn'
                  : undefined
            }
          />
          <Stat label="Repair rounds" value={job.round} />
          <Stat label="Input tokens" value={count(job.usage.inputTokens)} />
          <Stat label="Output tokens" value={count(job.usage.outputTokens)} />
        </div>
      </div>

      {job.problems.length > 0 && (
        <>
          <SectionTitle>Problems</SectionTitle>
          <Card className="px-4 py-3">
            <ul className="flex flex-col gap-1 text-[13px]">
              {[...fatal, ...other].map((p, i) => (
                <li key={i} className="flex gap-2">
                  <span className={clsx('font-medium', p.fatal ? 'text-fail' : 'text-warn')}>
                    {p.fatal ? 'fatal' : 'warning'}
                  </span>
                  {p.journey && <span className="font-mono text-xs leading-5">{p.journey}</span>}
                  <span>{p.message}</span>
                </li>
              ))}
            </ul>
          </Card>
        </>
      )}

      {job.journeys.length > 0 && (
        <>
          <SectionTitle>Journeys</SectionTitle>
          <div className="flex flex-col gap-3">
            {job.journeys.map((j) => (
              <JourneyCard key={j.name} journey={j} />
            ))}
          </div>
        </>
      )}

      {job.yaml ? (
        <>
          <SectionTitle>Proposal</SectionTitle>
          <Proposal job={job} />
        </>
      ) : (
        active && (
          <p className="mt-6 text-[13px] text-muted">
            The proposal appears here when the job finishes.
          </p>
        )
      )}
    </div>
  );
}
