import { Link, useNavigate } from '@tanstack/react-router';
import { Plus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useCreateProject, useMe, useProjects } from '@/api/queries';
import { Modal } from '@/components/dialog';
import {
  Button,
  Card,
  EmptyState,
  ErrorAlert,
  Field,
  Input,
  Loading,
  PageHeader,
  Table,
  Textarea,
} from '@/components/ui';
import { dateTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

export function NewProjectDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const create = useCreateProject();
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), ...(description.trim() ? { description: description.trim() } : {}) },
      {
        onSuccess: (p) => {
          onOpenChange(false);
          setName('');
          setDescription('');
          void navigate({ to: '/projects/$projectId', params: { projectId: p.id } });
        },
      },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="New project"
      description="Projects group scenarios, targets, secrets and runs."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            form="new-project"
            loading={create.isPending}
            disabled={!name.trim()}
          >
            Create project
          </Button>
        </>
      }
    >
      <form id="new-project" onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name">
          {(p) => (
            <Input
              {...p}
              autoFocus
              maxLength={100}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          )}
        </Field>
        <Field label="Description" hint="Optional.">
          {(p) => (
            <Textarea
              {...p}
              rows={3}
              maxLength={1000}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          )}
        </Field>
        <ErrorAlert error={create.error} />
      </form>
    </Modal>
  );
}

export function ProjectsPage() {
  const me = useMe();
  const can = permissions(me.role);
  const projects = useProjects();
  const [open, setOpen] = useState(false);

  return (
    <div className="mx-auto max-w-5xl px-6 py-6">
      <PageHeader
        title="Projects"
        description={me.orgName}
        actions={
          can.editProjects && (
            <Button variant="primary" onClick={() => setOpen(true)}>
              <Plus className="size-4" aria-hidden /> New project
            </Button>
          )
        }
      />
      <Card>
        {projects.isPending ? (
          <Loading />
        ) : projects.error ? (
          <ErrorAlert error={projects.error} className="m-4" />
        ) : projects.data.length === 0 ? (
          <EmptyState
            title="No projects yet"
            action={
              can.editProjects && (
                <Button variant="primary" onClick={() => setOpen(true)}>
                  Create a project
                </Button>
              )
            }
          >
            A project holds the scenarios, targets and secrets for one product or service.
          </EmptyState>
        ) : (
          <Table>
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
      <NewProjectDialog open={open} onOpenChange={setOpen} />
    </div>
  );
}
