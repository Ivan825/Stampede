import { useAIProviders } from '@/api/queries';
import { Chip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Card, CardHeader, EmptyState, ErrorAlert, Loading, Table } from '@/components/ui';
import { cli } from '@/lib/cli';
import { count, dateTime } from '@/lib/format';

/** The organisation's AI providers, read-only. Keys are never shown. */
export function AIProvidersTab() {
  const list = useAIProviders();
  return (
    <Card>
      <CardHeader
        title="AI providers"
        description="Models used to draft scenarios and to summarise reports. Nothing in Stampede needs one, and no model is called while load runs."
        actions={<CliHint command={cli.aiProvidersSet}>Add or replace one</CliHint>}
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No AI providers">
          Anthropic, OpenAI, Gemini, a local Ollama or any OpenAI-compatible server can generate
          journeys and summarise reports.
        </EmptyState>
      ) : (
        <Table aria-label="AI providers">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>Model</th>
              <th>Key</th>
              <th
                className="!text-right"
                title="The organisation's AI token use this month against this provider's cap"
              >
                Used / cap this month
              </th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {list.data.map((p) => (
              <tr key={p.id}>
                <td className="font-mono text-xs font-medium">{p.name}</td>
                <td>
                  <Chip tone="info">{p.kind}</Chip>
                </td>
                <td className="max-w-64">
                  <div className="truncate font-mono text-xs">{p.model}</div>
                  {p.baseURL && (
                    <div className="truncate font-mono text-xs text-muted" title={p.baseURL}>
                      {p.baseURL}
                    </div>
                  )}
                </td>
                <td className="text-xs text-muted">{p.hasKey ? 'stored' : '—'}</td>
                <td className="num text-right text-xs">
                  {count(p.usedTokensThisMonth)}
                  <span className="text-muted"> / {count(p.monthlyTokenCap)}</span>
                </td>
                <td className="text-xs text-muted">{dateTime(p.updatedAt)}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}
