import { Link } from '@tanstack/react-router';
import { useMe, useProjects } from '@/api/queries';
import { CliHint } from '@/components/cliHint';
import { Card, EmptyState, ErrorAlert, Loading, PageHeader, Table } from '@/components/ui';
import { cli } from '@/lib/cli';
import { dateTime } from '@/lib/format';

export function ProjectsPage() {
  const me = useMe();
  const projects = useProjects();

  return (
    <div className="mx-auto max-w-5xl px-6 py-6">
      <PageHeader
        title="Projects"
        description={me.orgName}
        actions={<CliHint command={cli.projectsCreate}>Create a project</CliHint>}
      />
      <Card>
        {projects.isPending ? (
          <Loading />
        ) : projects.error ? (
          <ErrorAlert error={projects.error} className="m-4" />
        ) : projects.data.length === 0 ? (
          <EmptyState title="No projects yet">
            A project holds the scenarios, targets and secrets for one product or service. Create
            one with <code className="font-mono text-xs">stampede projects create</code>.
          </EmptyState>
        ) : (
          <Table aria-label="Projects">
            <thead>
              <tr>
                <th>Name</th>
                <th>Slug</th>
                <th>Description</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {projects.data.map((p) => (
                <tr key={p.id} className="hover:bg-surface-2/60">
                  <td>
                    <Link
                      to="/projects/$projectId"
                      params={{ projectId: p.id }}
                      className="font-medium hover:underline"
                    >
                      {p.name}
                    </Link>
                  </td>
                  <td className="font-mono text-xs text-muted">{p.slug}</td>
                  <td className="max-w-md truncate text-muted">{p.description}</td>
                  <td className="num text-xs whitespace-nowrap text-muted">
                    {dateTime(p.createdAt)}
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
