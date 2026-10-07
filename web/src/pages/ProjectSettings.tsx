import { useParams } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import {
  useDeleteProjectRole,
  useMe,
  useProject,
  useProjectRoles,
  useProjectSettings,
  usePutProjectRole,
  usePutProjectSettings,
  useUsers,
} from '@/api/queries';
import type { ProjectRole, ProjectSettings, Role } from '@/api/types';
import { Chip, RoleChip } from '@/components/chips';
import { useToast } from '@/components/toast';
import {
  Button,
  Card,
  CardHeader,
  ErrorAlert,
  Loading,
  PageHeader,
  Select,
  Table,
} from '@/components/ui';
import { CapsFields } from '@/features/settings/CapsFields';
import { capsErrors, capsForm, capsText, toCaps } from '@/features/settings/capsForm';
import { canAdminProject, overrideRoles, roleDescriptions } from '@/lib/roles';

const gateHelp =
  'Before load, each journey runs once with one user against the target. If any journey fails, the run fails and no load is started; the run page lists each journey’s result.';

function SettingsForm({ projectId, initial }: { projectId: string; initial: ProjectSettings }) {
  const put = usePutProjectSettings(projectId);
  const toast = useToast();
  const [form, setForm] = useState(() => capsForm(initial.caps));
  const [requireDryRun, setRequireDryRun] = useState(initial.requireDryRun);
  const [touched, setTouched] = useState(false);
  const errors = capsErrors(form);
  const valid = Object.values(errors).every((e) => !e);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    put.mutate(
      { caps: toCaps(form), requireDryRun },
      {
        onSuccess: (s) => {
          setForm(capsForm(s.caps));
          setRequireDryRun(s.requireDryRun);
          setTouched(false);
          toast.success('Project settings saved.');
        },
      },
    );
  };
  return (
    <form
      onSubmit={submit}
      className="flex flex-col gap-4 px-4 py-3"
      aria-label="Caps and dry run"
      noValidate
    >
      <CapsFields
        form={form}
        setForm={setForm}
        errors={touched ? errors : {}}
        legend="Caps on every run in this project"
        hint="Runs must also fit the server's, the organisation's and the target's caps. Leave a field empty for no cap."
      />
      <div className="flex flex-col gap-1">
        <label className="flex items-center gap-2 text-[13px] font-medium">
          <input
            type="checkbox"
            checked={requireDryRun}
            aria-describedby="dry-run-help"
            onChange={(e) => setRequireDryRun(e.target.checked)}
          />
          Require a passing dry run before load
        </label>
        <p id="dry-run-help" className="text-xs text-muted">
          {gateHelp}
        </p>
      </div>
      <ErrorAlert error={put.error} />
      <div>
        <Button type="submit" variant="primary" loading={put.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}

function SettingsCard({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  const settings = useProjectSettings(projectId);
  return (
    <Card role="region" aria-label="Caps and dry run">
      <CardHeader
        title="Caps and dry run"
        description={
          canEdit ? undefined : 'Read-only. Project and organisation admins change these.'
        }
      />
      {settings.isPending ? (
        <Loading />
      ) : settings.error ? (
        <ErrorAlert error={settings.error} className="m-4" />
      ) : canEdit ? (
        <SettingsForm projectId={projectId} initial={settings.data} />
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

function RoleCell({
  projectId,
  member,
  override,
  canEdit,
  choices,
}: {
  projectId: string;
  member: { id: string; name: string; role: Role };
  override: ProjectRole | undefined;
  canEdit: boolean;
  choices: Role[];
}) {
  const put = usePutProjectRole(projectId);
  const del = useDeleteProjectRole(projectId);
  const toast = useToast();
  if (member.role === 'owner') {
    return (
      <span className="flex items-center gap-2">
        <RoleChip role="owner" />
        <span className="text-xs text-muted">Owners are owners in every project</span>
      </span>
    );
  }
  // An override above the caller's own role can only be shown, not changed.
  const editable = canEdit && (!override || choices.includes(override.role));
  if (!editable) {
    return (
      <span className="flex items-center gap-2">
        <RoleChip role={override?.role ?? member.role} />
        <span className="text-xs text-muted">
          {override ? 'Project override' : 'Organisation role'}
        </span>
      </span>
    );
  }
  const busy = put.isPending || del.isPending;
  const set = (v: string) => {
    if (v === '') {
      del.mutate(member.id, {
        onSuccess: () =>
          toast.success(`${member.name} has their organisation role (${member.role}) here again.`),
        onError: (e) => toast.error(e),
      });
      return;
    }
    put.mutate(
      { userId: member.id, role: v as Role },
      {
        onSuccess: (r) => toast.success(`${member.name} is now ${r.role} in this project.`),
        onError: (e) => toast.error(e),
      },
    );
  };
  return (
    <div className="flex items-center gap-2">
      <Select
        aria-label={`Role for ${member.name} in this project`}
        className="!h-7 !w-56 text-[13px]"
        value={override?.role ?? ''}
        disabled={busy}
        onChange={(e) => set(e.target.value)}
      >
        <option value="">Organisation role ({member.role})</option>
        {choices.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </Select>
      {override && (
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          aria-label={`Remove the project override for ${member.name}`}
          onClick={() => set('')}
        >
          Remove override
        </Button>
      )}
    </div>
  );
}

function MembersCard({
  projectId,
  canEdit,
  choices,
}: {
  projectId: string;
  canEdit: boolean;
  choices: Role[];
}) {
  const me = useMe();
  const users = useUsers();
  const overrides = useProjectRoles(projectId);
  const err = users.error ?? overrides.error;
  return (
    <Card>
      <CardHeader
        title="Members and roles"
        description="Each member has their organisation role in every project, unless an override here gives them another role in this one, higher or lower."
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
                    <RoleCell
                      projectId={projectId}
                      member={u}
                      override={override}
                      canEdit={canEdit}
                      choices={choices}
                    />
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

export function ProjectSettingsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/settings' });
  const me = useMe();
  const project = useProject(projectId);
  if (project.isPending) return <Loading />;
  if (project.error) return <ErrorAlert error={project.error} className="m-6" />;
  const projectRole = project.data.role;
  const canEdit = canAdminProject(me.role, projectRole);
  const yours = projectRole ?? me.role;
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
      <SettingsCard projectId={projectId} canEdit={canEdit} />
      <MembersCard
        projectId={projectId}
        canEdit={canEdit}
        choices={overrideRoles(me.role, projectRole)}
      />
    </div>
  );
}
