import { useParams } from '@tanstack/react-router';
import { Plus, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useDeleteSecret, useMe, usePutSecret, useSecrets } from '@/api/queries';
import { Confirm, Modal } from '@/components/dialog';
import { useToast } from '@/components/toast';
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
import { dateTime, relativeTime } from '@/lib/format';
import { permissions } from '@/lib/roles';

const namePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

function SecretDialog({
  projectId,
  name: existing,
  open,
  onOpenChange,
}: {
  projectId: string;
  name?: string;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const put = usePutSecret(projectId);
  const toast = useToast();
  const [name, setName] = useState(existing ?? '');
  const [value, setValue] = useState('');
  const [touched, setTouched] = useState(false);
  const nameError = namePattern.test(name)
    ? undefined
    : 'Start with a letter or underscore; letters, digits and underscores only.';
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (nameError || !value) return;
    put.mutate(
      { name, value },
      {
        onSuccess: () => {
          toast.success(existing ? `Updated ${name}.` : `Stored ${name}.`);
          onOpenChange(false);
        },
      },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={existing ? `Update ${existing}` : 'New secret'}
      description="Values are encrypted at rest and never shown again. Use them in scenarios as ${secret.NAME}."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            form="secret-form"
            loading={put.isPending}
            disabled={!value}
          >
            {existing ? 'Replace value' : 'Store secret'}
          </Button>
        </>
      }
    >
      <form id="secret-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Name" error={touched ? nameError : undefined}>
          {(p) => (
            <Input
              {...p}
              className="font-mono"
              autoFocus={!existing}
              readOnly={!!existing}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          )}
        </Field>
        <Field label="Value" hint="Write-only.">
          {(p) => (
            <Textarea
              {...p}
              rows={3}
              className="font-mono"
              autoFocus={!!existing}
              autoComplete="off"
              spellCheck={false}
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          )}
        </Field>
        <ErrorAlert error={put.error} />
      </form>
    </Modal>
  );
}

export function SecretsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/secrets' });
  const me = useMe();
  const can = permissions(me.role);
  const secrets = useSecrets(projectId);
  const del = useDeleteSecret(projectId);
  const toast = useToast();
  const [editing, setEditing] = useState<string | null>(null);

  return (
    <div className="mx-auto max-w-4xl px-6 py-6">
      <PageHeader
        title="Secrets"
        description={
          <>
            Credentials for scenarios, available as{' '}
            <code className="font-mono text-fg">{'${secret.NAME}'}</code>. Values are write-only.
          </>
        }
        actions={
          can.editSecrets && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus className="size-4" aria-hidden /> New secret
            </Button>
          )
        }
      />
      <Card>
        {secrets.isPending ? (
          <Loading />
        ) : secrets.error ? (
          <ErrorAlert error={secrets.error} className="m-4" />
        ) : secrets.data.length === 0 ? (
          <EmptyState title="No secrets">
            Store API keys and passwords here instead of in scenario files.
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Value</th>
                <th>Updated</th>
                {can.editSecrets && <th className="w-40" />}
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
                  {can.editSecrets && (
                    <td>
                      <div className="flex justify-end gap-2">
                        <Button size="sm" onClick={() => setEditing(s.name)}>
                          Update
                        </Button>
                        <Confirm
                          trigger={
                            <Button size="sm" variant="danger" aria-label={`Delete ${s.name}`}>
                              <Trash2 className="size-3.5" aria-hidden />
                            </Button>
                          }
                          title={`Delete ${s.name}?`}
                          description="Scenarios that use this secret will fail until it is added again."
                          confirmLabel="Delete secret"
                          destructive
                          onConfirm={async () => {
                            await del.mutateAsync(s.name);
                            toast.success(`Deleted ${s.name}.`);
                          }}
                        />
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      {editing && (
        <SecretDialog
          key={editing}
          projectId={projectId}
          name={editing === 'new' ? undefined : editing}
          open
          onOpenChange={(v) => !v && setEditing(null)}
        />
      )}
    </div>
  );
}
