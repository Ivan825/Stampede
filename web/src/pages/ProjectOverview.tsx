import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { Pencil, Play, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import {
  useDeleteProject,
  useMe,
  useProject,
  useRuns,
  useScenarios,
  useTargets,
  useUpdateProject,
} from '@/api/queries';
import type { Project } from '@/api/types';
import { Chip } from '@/components/chips';
import { Confirm, Modal } from '@/components/dialog';
import {
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorAlert,
  Field,
  Input,
  Loading,
  PageHeader,
  Textarea,
} from '@/components/ui';
import { NewRunDialog } from '@/features/runs/NewRunDialog';
import { RunsTable } from '@/features/runs/RunsTable';
import { TargetBadge } from '@/features/targets/TargetBadge';
import { load, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';
import { useToast } from '@/components/toast';

function EditProjectDialog({
  project,
  open,
  onOpenChange,
}: {
  project: Project;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const update = useUpdateProject();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description ?? '');
  const submit = (e: FormEvent) => {
    e.preventDefault();
    update.mutate(
      { id: project.id, name: name.trim(), description: description.trim() },
      { onSuccess: () => onOpenChange(false) },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Edit project"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            form="edit-project"
            loading={update.isPending}
            disabled={!name.trim()}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="edit-project" onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name">
          {(p) => <Input {...p} value={name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        <Field label="Description">
          {(p) => (
            <Textarea
              {...p}
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          )}
        </Field>
        <ErrorAlert error={update.error} />
      </form>
    </Modal>
  );
}

export function ProjectOverviewPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/' });
  const me = useMe();
  const can = permissions(me.role);
  const project = useProject(projectId);
  const runs = useRuns(projectId, { limit: 10 }, 5_000);
  const scenarios = useScenarios(projectId);
  const targets = useTargets(projectId);
  const del = useDeleteProject();
  const navigate = useNavigate();
  const toast = useToast();
  const [newRun, setNewRun] = useState(false);
  const [edit, setEdit] = useState(false);

  if (project.isPending) return <Loading />;
  if (project.error) return <ErrorAlert error={project.error} className="m-6" />;
  const p = project.data;

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title={p.name}
        description={p.description || undefined}
        actions={
          <>
            {can.editProjects && (
              <Button onClick={() => setEdit(true)}>
                <Pencil className="size-3.5" aria-hidden /> Edit
              </Button>
            )}
            {can.deleteProjects && (
              <Confirm
                trigger={
                  <Button variant="danger">
                    <Trash2 className="size-3.5" aria-hidden /> Delete
                  </Button>
                }
                title={`Delete ${p.name}?`}
                description="This deletes the project with all of its scenarios, targets, secrets and run history. It cannot be undone."
                confirmLabel="Delete project"
                destructive
                onConfirm={async () => {
                  await del.mutateAsync(p.id);
                  toast.success(`Deleted ${p.name}.`);
                  void navigate({ to: '/projects' });
                }}
              />
            )}
            {can.startRuns && (
              <Button variant="primary" onClick={() => setNewRun(true)}>
                <Play className="size-3.5" aria-hidden /> New run
              </Button>
            )}
          </>
        }
      />

      <Card>
        <CardHeader
          title="Recent runs"
          actions={
            <Link
              to="/projects/$projectId/runs"
              params={{ projectId }}
              className="text-[13px] text-info hover:underline"
            >
              All runs
            </Link>
          }
        />
        {runs.isPending ? (
          <Loading />
        ) : runs.error ? (
          <ErrorAlert error={runs.error} className="m-4" />
        ) : runs.data.length === 0 ? (
          <EmptyState title="No runs yet">
            {can.startRuns
              ? 'Start a run to see throughput, latency and a verdict here.'
              : 'Runs started by your team will appear here.'}
          </EmptyState>
        ) : (
          <RunsTable runs={runs.data} />
        )}
      </Card>

      <div className="mt-5 grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader
            title="Scenarios"
            actions={
              <Link
                to="/projects/$projectId/scenarios"
                params={{ projectId }}
                className="text-[13px] text-info hover:underline"
              >
                Manage
              </Link>
            }
          />
          {scenarios.isPending ? (
            <Loading />
          ) : scenarios.error ? (
            <ErrorAlert error={scenarios.error} className="m-4" />
          ) : scenarios.data.length === 0 ? (
            <EmptyState title="No scenarios">Describe how your users behave in YAML.</EmptyState>
          ) : (
            <ul className="divide-y divide-line">
              {scenarios.data.slice(0, 8).map((s) => {
                const plan = s.latestVersion.plan;
                return (
                  <li key={s.id} className="flex items-center gap-3 px-4 py-2.5">
                    <div className="min-w-0 flex-1">
                      <Link
                        to="/projects/$projectId/scenarios/$scenarioId"
                        params={{ projectId, scenarioId: s.id }}
                        className="font-medium hover:underline"
                      >
                        {s.name}
                      </Link>
                      <span className="num ml-1.5 text-xs text-muted">
                        v{s.latestVersion.version}
                      </span>
                      <div className="mt-0.5 flex flex-wrap gap-1">
                        {s.tags.map((t) => (
                          <Chip key={t}>{t}</Chip>
                        ))}
                      </div>
                    </div>
                    {plan && (
                      <span className="num text-xs text-muted">
                        {plan.shape ?? plan.executor} · {load(plan.peak, plan.mode)}
                      </span>
                    )}
                    <span className="w-24 text-right text-xs text-muted">
                      {relativeTime(s.updatedAt)}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>

        <Card>
          <CardHeader
            title="Targets"
            actions={
              <Link
                to="/projects/$projectId/targets"
                params={{ projectId }}
                className="text-[13px] text-info hover:underline"
              >
                Manage
              </Link>
            }
          />
          {targets.isPending ? (
            <Loading />
          ) : targets.error ? (
            <ErrorAlert error={targets.error} className="m-4" />
          ) : targets.data.length === 0 ? (
            <EmptyState title="No targets">Add the base URL of the system you test.</EmptyState>
          ) : (
            <ul className="divide-y divide-line">
              {targets.data.map((t) => (
                <li key={t.id} className="flex items-center gap-3 px-4 py-2.5">
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{t.name}</div>
                    <div className="truncate font-mono text-xs text-muted">{t.baseURL}</div>
                  </div>
                  <TargetBadge target={t} />
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      {can.startRuns && (
        <NewRunDialog projectId={projectId} open={newRun} onOpenChange={setNewRun} />
      )}
      {edit && <EditProjectDialog project={p} open={edit} onOpenChange={setEdit} />}
    </div>
  );
}
