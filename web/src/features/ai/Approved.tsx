import { Link } from '@tanstack/react-router';
import { CheckCircle2 } from 'lucide-react';
import { useScenario } from '@/api/queries';
import type { AIJob } from '@/api/types';
import { CliHint } from '@/components/cliHint';
import { Notice } from '@/components/ui';
import { cli } from '@/lib/cli';
import { relativeTime } from '@/lib/format';

/** Where an approved proposal was saved, with a link to the scenario. */
export function Approved({ job }: { job: AIJob }) {
  if (!job.approvedScenarioId) return null;
  return <ApprovedLink job={job} scenarioId={job.approvedScenarioId} />;
}

function ApprovedLink({ job, scenarioId }: { job: AIJob; scenarioId: string }) {
  const scenario = useScenario(scenarioId);
  const name = scenario.data?.name ?? 'the scenario';
  return (
    <Notice tone="pass" className="flex items-center gap-2">
      <CheckCircle2 className="size-4 shrink-0 text-pass" aria-hidden />
      <span>
        Approved {relativeTime(job.approvedAt)}: saved as{' '}
        <Link
          to="/projects/$projectId/scenarios/$scenarioId"
          params={{ projectId: job.projectId, scenarioId }}
          search={job.approvedVersion ? { version: job.approvedVersion } : {}}
          className="font-medium underline"
        >
          {name}
          {job.approvedVersion ? ` v${job.approvedVersion}` : ''}
        </Link>
        .
      </span>
    </Notice>
  );
}

/** A finished proposal waiting for review: approving it is done from the terminal. */
export function AwaitingApproval({ job }: { job: AIJob }) {
  const flagged = job.journeys.filter((j) => j.status === 'flagged').map((j) => j.name);
  return (
    <Notice tone={flagged.length ? 'warn' : 'info'} className="flex flex-col gap-2">
      <p>
        {flagged.length > 0 ? (
          <>
            {flagged.length === 1 ? 'Journey' : 'Journeys'}{' '}
            <span className="font-medium">{flagged.join(', ')}</span> did not pass the dry run.
            Review the evidence below before you approve.
          </>
        ) : (
          'Every journey passed its dry run. Review the proposal below, then approve it to save it as a scenario.'
        )}
      </p>
      <CliHint command={cli.aiJobsApprove(job.id)} className="self-start bg-surface">
        Approve from the terminal
      </CliHint>
    </Notice>
  );
}
