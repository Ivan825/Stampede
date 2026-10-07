import { useParams } from '@tanstack/react-router';
import { useProject, useSecrets } from '@/api/queries';
import { CliHint } from '@/components/cliHint';
import { Card, EmptyState, ErrorAlert, Loading, PageHeader, Table } from '@/components/ui';
import { cli } from '@/lib/cli';
import { dateTime, relativeTime } from '@/lib/format';

export function SecretsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/secrets' });
  const project = useProject(projectId);
  const secrets = useSecrets(projectId);

  return (
    <div className="mx-auto max-w-4xl px-6 py-6">
      <PageHeader
        title="Secrets"
        description={
          <>
            Credentials for scenarios, available as{' '}
            <code className="font-mono text-fg">{'${secret.NAME}'}</code>. Values are write-only and
            never shown.
          </>
        }
        actions={
          <CliHint command={cli.secretsSet(project.data?.slug ?? '<project>')}>
            Store or replace a secret
          </CliHint>
        }
      />
      <Card>
        {secrets.isPending ? (
          <Loading />
        ) : secrets.error ? (
          <ErrorAlert error={secrets.error} className="m-4" />
        ) : secrets.data.length === 0 ? (
          <EmptyState title="No secrets">
            Store API keys and passwords as secrets instead of in scenario files.
          </EmptyState>
        ) : (
          <Table aria-label="Secrets">
            <thead>
              <tr>
                <th>Name</th>
                <th>Value</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {secrets.data.map((s) => (
                <tr key={s.name}>
                  <td className="font-mono">{s.name}</td>
                  <td className="font-mono text-muted" aria-label="hidden value">
                    ••••••••
                  </td>
                  <td className="text-xs text-muted" title={dateTime(s.updatedAt)}>
                    {relativeTime(s.updatedAt)}
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
