import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { useAIJobs, useAIProviders } from '@/api/queries';
import type { AIProvider } from '@/api/types';
import { AIJobStatusChip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import {
  Card,
  EmptyState,
  ErrorAlert,
  Loading,
  Notice,
  PageHeader,
  Table,
} from '@/components/ui';
import { stageLabels } from '@/features/ai/stages';
import { cli } from '@/lib/cli';
import { count, dateTime, relativeTime } from '@/lib/format';

function ProviderNotice({ providers }: { providers: AIProvider[] }) {
  if (providers.length === 0) {
    return (
      <Notice tone="info" className="mb-4 flex flex-col gap-2">
        <div>
          <p className="font-medium">No AI provider is configured.</p>
          <p className="mt-0.5 text-muted">
            AI generation is optional and uses your organisation&apos;s own key: Anthropic, OpenAI,
            Gemini, Ollama or an OpenAI-compatible server.
          </p>
        </div>
        <CliHint command={cli.aiProvidersSet} className="self-start">
          An admin adds one from the terminal
        </CliHint>
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

/** AI generation jobs of a project and their proposals, read-only. */
export function AIJobsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/ai' });
  const providers = useAIProviders();
  const jobs = useAIJobs(projectId);
  const navigate = useNavigate();

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="AI jobs"
        description="Scenarios drafted from a description, an OpenAPI spec, a HAR recording or an access log. Every journey is dry-run against a target before a person approves it."
        actions={<CliHint command={cli.aiJobsCreate}>Generate journeys</CliHint>}
      />
      {providers.error ? (
        <ErrorAlert error={providers.error} className="mb-4" />
      ) : (
        providers.data && <ProviderNotice providers={providers.data} />
      )}
      <Card>
        {jobs.isPending ? (
          <Loading />
        ) : jobs.error ? (
          <ErrorAlert error={jobs.error} className="m-4" />
        ) : jobs.data.length === 0 ? (
          <EmptyState title="No generation jobs yet">
            Jobs started with <code className="font-mono text-xs">stampede ai jobs create</code>{' '}
            appear here with a dry-run trace for every journey and the proposed scenario.
          </EmptyState>
        ) : (
          <Table aria-label="AI jobs">
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
    </div>
  );
}
