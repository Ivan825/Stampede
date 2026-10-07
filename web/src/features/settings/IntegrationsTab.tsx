import { useIntegrations } from '@/api/queries';
import { Chip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Card, CardHeader, EmptyState, ErrorAlert, Loading, Table } from '@/components/ui';
import { cli } from '@/lib/cli';
import { dateTime } from '@/lib/format';

/** The organisation's integrations, read-only. Tokens are never shown. */
export function IntegrationsTab() {
  const list = useIntegrations();
  return (
    <Card>
      <CardHeader
        title="Integrations"
        description={
          <>
            Prometheus to chart the target&apos;s own metrics in reports, trace link templates for
            the slowest requests, and fault agents that break dependencies during a run. Reference
            them in a scenario with{' '}
            <code className="font-mono text-xs">
              observe: {'{'} prometheus: {'{'} integration: name, queries: … {'}'} {'}'}
            </code>{' '}
            or{' '}
            <code className="font-mono text-xs">
              faults: {'{'} agent: {'{'} integration: name {'}'}, timeline: … {'}'}
            </code>
            .
          </>
        }
        actions={<CliHint command={cli.integrationsCreate}>Add one</CliHint>}
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No integrations">
          A Prometheus server, a Jaeger or Tempo link template, or a fault agent.
        </EmptyState>
      ) : (
        <Table aria-label="Integrations">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>URL</th>
              <th>Token</th>
              <th>Added</th>
            </tr>
          </thead>
          <tbody>
            {list.data.map((i) => (
              <tr key={i.id}>
                <td className="font-mono text-xs font-medium">{i.name}</td>
                <td>
                  <Chip
                    tone={i.kind === 'prometheus' ? 'info' : i.kind === 'agent' ? 'warn' : 'accent'}
                  >
                    {i.kind}
                  </Chip>
                </td>
                <td className="max-w-80 truncate font-mono text-xs text-muted" title={i.url}>
                  {i.url}
                </td>
                <td className="text-xs text-muted">{i.hasToken ? 'stored' : '—'}</td>
                <td className="text-xs text-muted">{dateTime(i.createdAt)}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}
