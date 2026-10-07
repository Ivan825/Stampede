import { useParams } from '@tanstack/react-router';
import { useMe, useProject, useProjectRoles, useProjectSettings, useUsers } from '@/api/queries';
import { Chip, RoleChip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import { Card, CardHeader, ErrorAlert, Loading, PageHeader, Table } from '@/components/ui';
import { capsText } from '@/features/settings/capsText';
import { cli } from '@/lib/cli';
import { roleDescriptions } from '@/lib/roles';

const gateHelp =
  'Before load, each journey runs once with one user against the target. If any journey fails, the run fails and no load is started; the run page lists each journey’s result.';

function SettingsCard({ projectId, slug }: { projectId: string; slug: string }) {
  const settings = useProjectSettings(projectId);
  return (
    <Card role="region" aria-label="Caps and dry run">
      <CardHeader
        title="Caps and dry run"
        description="Runs must also fit the server's, the organisation's and the target's caps."
        actions={<CliHint command={cli.projectSettings(slug)}>Change them</CliHint>}
      />
      {settings.isPending ? (
        <Loading />
      ) : settings.error ? (
        <ErrorAlert error={settings.error} className="m-4" />
      ) : (
        <dl className="grid grid-cols-[12rem_1fr] gap-y-2 px-4 py-3 text-[13px]">
          <dt className="text-muted">Caps</dt>
          <dd className="num">{capsText(settings.data.caps)}</dd>
          <dt className="text-muted">Dry run before load</dt>
          <dd>
            {settings.data.requireDryRun ? (
              <>
                <Chip tone="info">required</Chip>
                <p className="mt-1 text-xs text-muted">{gateHelp}</p>
              </>
            ) : (
              'Not required'
            )}
          </dd>
        </dl>
      )}
    </Card>
  );
}

function MembersCard({ projectId, slug }: { projectId: string; slug: string }) {
  const me = useMe();
  const users = useUsers();
  const overrides = useProjectRoles(projectId);
  const err = users.error ?? overrides.error;
  return (
    <Card>
      <CardHeader
        title="Members and roles"
        description="Each member has their organisation role in every project, unless an override gives them another role in this one, higher or lower."
        actions={<CliHint command={cli.projectRoles(slug)}>Set an override</CliHint>}
      />
      {users.isPending || overrides.isPending ? (
        <Loading />
      ) : err ? (
        <ErrorAlert error={err} className="m-4" />
      ) : (
        <Table aria-label="Project members">
          <thead>
            <tr>
              <th>Member</th>
              <th>Organisation role</th>
              <th>Role in this project</th>
            </tr>
          </thead>
          <tbody>
            {users.data!.map((u) => {
              const override = overrides.data!.find((o) => o.userId === u.id);
              return (
                <tr key={u.id}>
                  <td>
                    <div className="font-medium">
                      {u.name}
                      {u.id === me.id && <span className="ml-1.5 text-xs text-muted">(you)</span>}
                    </div>
                    <div className="text-xs text-muted">{u.email}</div>
                  </td>
                  <td>
                    <RoleChip role={u.role} />
                  </td>
                  <td>
                    <span className="flex items-center gap-2">
                      <RoleChip role={u.role === 'owner' ? 'owner' : (override?.role ?? u.role)} />
                      <span className="text-xs text-muted">
                        {u.role === 'owner'
                          ? 'Owners are owners in every project'
                          : override
                            ? 'Project override'
                            : 'Organisation role'}
                      </span>
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

/** A project's caps, dry-run gate and role overrides, read-only. */
export function ProjectSettingsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/settings' });
  const me = useMe();
  const project = useProject(projectId);
  if (project.isPending) return <Loading />;
  if (project.error) return <ErrorAlert error={project.error} className="m-6" />;
  const yours = project.data.role ?? me.role;
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-4 px-6 py-6">
      <PageHeader
        title="Project settings"
        description={
          <>
            {project.data.name}. Your role here: <b>{yours}</b> ({roleDescriptions[yours]}).
          </>
        }
      />
      <SettingsCard projectId={projectId} slug={project.data.slug} />
      <MembersCard projectId={projectId} slug={project.data.slug} />
    </div>
  );
}
