import { Plus, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useCreateIntegration, useDeleteIntegration, useIntegrations } from '@/api/queries';
import type { IntegrationKind } from '@/api/types';
import { Chip } from '@/components/chips';
import { Confirm, Modal } from '@/components/dialog';
import { useToast } from '@/components/toast';
import {
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorAlert,
  Field,
  Input,
  Loading,
  Select,
  Table,
} from '@/components/ui';
import { dateTime } from '@/lib/format';

const nameRe = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$/;

const urlHints: Record<IntegrationKind, string> = {
  prometheus: 'The Prometheus base URL, e.g. http://prometheus:9090.',
  traces: 'A link template containing {traceId}, e.g. https://jaeger.example.com/trace/{traceId}.',
  agent: "A stampede agent's control API, e.g. http://agent.shop.svc:7070.",
};

function NewIntegrationDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const create = useCreateIntegration();
  const toast = useToast();
  const [form, setForm] = useState({
    name: '',
    kind: 'prometheus' as IntegrationKind,
    url: '',
    token: '',
  });
  const [touched, setTouched] = useState(false);
  const errors = {
    name: nameRe.test(form.name.trim())
      ? undefined
      : "Start with a letter or digit; use letters, digits, '_', '.' or '-'.",
    url: !/^https?:\/\/\S+$/.test(form.url.trim())
      ? 'Enter an absolute http(s) URL.'
      : form.kind === 'traces' && !form.url.includes('{traceId}')
        ? 'The template must contain {traceId}.'
        : undefined,
    token:
      form.kind === 'agent' && !form.token.trim()
        ? "Enter the agent's token (its --token or STAMPEDE_AGENT_TOKEN)."
        : undefined,
  };
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);
  const reset = () => {
    setForm({ name: '', kind: 'prometheus', url: '', token: '' });
    setTouched(false);
    create.reset();
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (Object.values(errors).some(Boolean)) return;
    create.mutate(
      {
        name: form.name.trim(),
        kind: form.kind,
        url: form.url.trim(),
        ...(form.kind !== 'traces' && form.token.trim() ? { bearerToken: form.token.trim() } : {}),
      },
      {
        onSuccess: (i) => {
          toast.success(`Added ${i.name}.`);
          onOpenChange(false);
          reset();
        },
      },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={(v) => {
        if (!v) reset();
        onOpenChange(v);
      }}
      title="Add integration"
      description="Scenarios run on this server refer to integrations by name, so the server only contacts URLs an admin configured."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            form="integration-form"
            loading={create.isPending}
          >
            Add integration
          </Button>
        </>
      }
    >
      <form id="integration-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name" error={show('name')} hint="e.g. prod-prometheus">
            {(p) => (
              <Input
                {...p}
                autoFocus
                maxLength={100}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            )}
          </Field>
          <Field label="Kind">
            {(p) => (
              <Select
                {...p}
                value={form.kind}
                onChange={(e) => setForm({ ...form, kind: e.target.value as IntegrationKind })}
              >
                <option value="prometheus">Prometheus</option>
                <option value="traces">Traces (link template)</option>
                <option value="agent">Fault agent (stampede agent)</option>
              </Select>
            )}
          </Field>
        </div>
        <Field label="URL" error={show('url')} hint={urlHints[form.kind]}>
          {(p) => (
            <Input
              {...p}
              className="font-mono text-xs"
              value={form.url}
              onChange={(e) => setForm({ ...form, url: e.target.value })}
            />
          )}
        </Field>
        {form.kind !== 'traces' && (
          <Field
            label={form.kind === 'agent' ? 'Agent token' : 'Bearer token (optional)'}
            error={show('token')}
            hint="Stored encrypted and never shown again."
          >
            {(p) => (
              <Input
                {...p}
                type="password"
                autoComplete="off"
                value={form.token}
                onChange={(e) => setForm({ ...form, token: e.target.value })}
              />
            )}
          </Field>
        )}
        <ErrorAlert error={create.error} />
      </form>
    </Modal>
  );
}

export function IntegrationsTab() {
  const list = useIntegrations();
  const del = useDeleteIntegration();
  const toast = useToast();
  const [open, setOpen] = useState(false);
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
        actions={
          <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
            <Plus className="size-3.5" aria-hidden /> Add integration
          </Button>
        }
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No integrations">
          Add a Prometheus server or a Jaeger or Tempo link template.
        </EmptyState>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>URL</th>
              <th>Token</th>
              <th>Added</th>
              <th />
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
                <td className="text-right">
                  <Confirm
                    trigger={
                      <Button size="sm" variant="danger" aria-label={`Delete ${i.name}`}>
                        <Trash2 className="size-3.5" aria-hidden />
                      </Button>
                    }
                    title={`Delete ${i.name}?`}
                    description="Runs of scenarios that name it will be refused until it is added again."
                    confirmLabel="Delete integration"
                    destructive
                    onConfirm={async () => {
                      await del.mutateAsync(i.id);
                      toast.success(`Deleted ${i.name}.`);
                    }}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <NewIntegrationDialog open={open} onOpenChange={setOpen} />
    </Card>
  );
}
