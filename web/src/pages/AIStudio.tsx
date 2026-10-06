import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { Sparkles } from 'lucide-react';
import { useState } from 'react';
import { useAIJobs, useAIProviders, useMe } from '@/api/queries';
import type { AIProvider } from '@/api/types';
import { AIJobStatusChip } from '@/components/chips';
import {
  Button,
  Card,
  EmptyState,
  ErrorAlert,
  Loading,
  Notice,
  PageHeader,
  Table,
} from '@/components/ui';
import { GenerateDialog } from '@/features/ai/GenerateDialog';
import { count, dateTime, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';
import { stageLabels } from '@/features/ai/stages';

function ProviderNotice({ providers, canManage }: { providers: AIProvider[]; canManage: boolean }) {
  if (providers.length === 0) {
    return (
      <Notice tone="info" className="mb-4">
        <p className="font-medium">No AI provider is configured.</p>
        <p className="mt-0.5 text-muted">
          AI generation is optional and uses your organisation&apos;s own key.{' '}
          {canManage ? (
            <>
              Add Anthropic, OpenAI, Gemini, Ollama or an OpenAI-compatible server in{' '}
              <Link to="/settings" search={{ tab: 'ai' }} className="text-fg underline">
                Settings → AI providers
              </Link>
              .
            </>
          ) : (
            <>An admin adds one in Settings → AI providers.</>
          )}
        </p>
      </Notice>
    );
  }
  const p = providers.find((x) => x.name === 'default') ?? providers[0]!;
  const left = p.monthlyTokenCap - p.usedTokensThisMonth;
  return (
    <p className="mb-3 text-xs text-muted">
      {providers.length === 1 ? 'Provider' : `${providers.length} providers; default`}{' '}
      <span className="font-mono text-fg">
        {p.kind} / {p.model}
      </span>{' '}
      · <span className="num">{count(p.usedTokensThisMonth)}</span> of{' '}
      <span className="num">{count(p.monthlyTokenCap)}</span> tokens used this month
      {left <= 0 && <span className="text-fail"> · cap reached, new jobs are refused</span>}
    </p>
  );
}

export function AIStudioPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/ai' });
  const me = useMe();
  const can = permissions(me.role);
  const providers = useAIProviders();
  const jobs = useAIJobs(projectId);
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const noProvider = providers.data?.length === 0;

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="AI studio"
        description="Draft a scenario from a description, an OpenAPI spec, a HAR recording or an access log. Every journey is dry-run against a target before a person approves it."
        actions={
          can.generateJourneys && (
            <Button
              variant="primary"
              disabled={!providers.data || noProvider}
              onClick={() => setOpen(true)}
            >
              <Sparkles className="size-3.5" aria-hidden /> Generate journeys
            </Button>
          )
        }
      />
      {providers.error ? (
        <ErrorAlert error={providers.error} className="mb-4" />
      ) : (
        providers.data && (
          <ProviderNotice providers={providers.data} canManage={can.manageAIProviders} />
        )
      )}
      <Card>
        {jobs.isPending ? (
          <Loading />
        ) : jobs.error ? (
          <ErrorAlert error={jobs.error} className="m-4" />
        ) : jobs.data.length === 0 ? (
          <EmptyState title="No generation jobs yet">
            {can.generateJourneys
              ? 'Generate journeys to get a proposed scenario with a dry-run trace for every journey.'
              : 'Editors can generate journeys here. You can read every job and its proposal.'}
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Status</th>
                <th>Job</th>
                <th>Stage</th>
                <th className="!text-right">Tokens</th>
                <th>Created by</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {jobs.data.map((j) => (
                <tr
                  key={j.id}
                  className="cursor-pointer hover:bg-surface-2/60"
                  onClick={(e) => {
                    if ((e.target as HTMLElement).closest('a')) return;
                    void navigate({
                      to: '/projects/$projectId/ai/$jobId',
                      params: { projectId, jobId: j.id },
                    });
                  }}
                >
                  <td>
                    <AIJobStatusChip status={j.status} />
                  </td>
                  <td className="max-w-80">
                    <Link
                      to="/projects/$projectId/ai/$jobId"
                      params={{ projectId, jobId: j.id }}
                      className="font-mono text-xs font-medium hover:underline"
                    >
                      {j.id.slice(0, 8)}
                    </Link>
                    <span className="ml-2 font-mono text-xs text-muted">
                      {j.providerKind} / {j.model}
                    </span>
                    {!j.dryRun && <span className="ml-2 text-xs text-muted">no dry run</span>}
                    {j.approvedAt && (
                      <span className="ml-2 text-xs text-pass">
                        approved {relativeTime(j.approvedAt)}
                      </span>
                    )}
                    {j.error && <div className="truncate text-xs text-fail">{j.error}</div>}
                  </td>
                  <td className="text-xs">{stageLabels[j.stage] ?? j.stage}</td>
                  <td
                    className="num text-right text-xs"
                    title={`${count(j.usage.inputTokens)} in, ${count(j.usage.outputTokens)} out`}
                  >
                    {count(j.usage.inputTokens + j.usage.outputTokens)}
                  </td>
                  <td className="text-xs text-muted">{j.createdBy ?? '—'}</td>
                  <td
                    className="text-xs whitespace-nowrap text-muted"
                    title={dateTime(j.createdAt)}
                  >
                    {relativeTime(j.createdAt)}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {can.generateJourneys && providers.data && providers.data.length > 0 && open && (
        <GenerateDialog
          projectId={projectId}
          providers={providers.data}
          open
          onOpenChange={setOpen}
        />
      )}
    </div>
  );
}
