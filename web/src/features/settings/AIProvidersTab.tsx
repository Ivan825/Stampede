import { Pencil, Plus, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useAIProviders, useDeleteAIProvider, usePutAIProvider } from '@/api/queries';
import type { AIProvider, AIProviderKind, AIProviderPut } from '@/api/types';
import { aiProviderKinds } from '@/api/types';
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
import { count, dateTime } from '@/lib/format';

const DEFAULT_CAP = 2_000_000;

const kindLabels: Record<AIProviderKind, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  gemini: 'Google Gemini',
  ollama: 'Ollama',
  'openai-compatible': 'OpenAI-compatible (vLLM, LM Studio, LiteLLM…)',
};

/** Kinds the server refuses without an API key (provider.NeedsKey). */
const needsKey = (k: AIProviderKind) => k === 'anthropic' || k === 'openai' || k === 'gemini';

function ProviderDialog({
  provider,
  open,
  onOpenChange,
}: {
  provider?: AIProvider;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const put = usePutAIProvider();
  const toast = useToast();
  const [form, setForm] = useState({
    name: provider?.name ?? 'default',
    kind: provider?.kind ?? 'anthropic',
    model: provider?.model ?? '',
    baseURL: provider?.baseURL ?? '',
    apiKey: '',
    cap: String(provider?.monthlyTokenCap ?? DEFAULT_CAP),
  });
  const [touched, setTouched] = useState(false);
  const keyStored = !!provider?.hasKey;
  const errors = {
    name:
      form.name.trim().length >= 1 && form.name.trim().length <= 100
        ? undefined
        : 'Enter a name of 1 to 100 characters.',
    model:
      form.kind !== 'anthropic' && !form.model.trim()
        ? `Enter the model name; ${kindLabels[form.kind]} has no default.`
        : undefined,
    baseURL:
      form.baseURL.trim() && !/^https?:\/\/\S+$/.test(form.baseURL.trim())
        ? 'Enter an absolute http(s) URL.'
        : form.kind === 'openai-compatible' && !form.baseURL.trim()
          ? 'Enter the server URL, for example http://llm.internal:8000/v1.'
          : undefined,
    apiKey:
      needsKey(form.kind) && !keyStored && !form.apiKey.trim()
        ? `${kindLabels[form.kind]} needs an API key.`
        : undefined,
    cap:
      Number.isInteger(Number(form.cap)) && Number(form.cap) >= 1
        ? undefined
        : 'A whole number of tokens, 1 or more.',
  };
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (Object.values(errors).some(Boolean)) return;
    const body: AIProviderPut = {
      name: form.name.trim(),
      kind: form.kind,
      monthlyTokenCap: Number(form.cap),
      ...(form.model.trim() ? { model: form.model.trim() } : {}),
      ...(form.baseURL.trim() ? { baseURL: form.baseURL.trim() } : {}),
      ...(form.apiKey.trim() ? { apiKey: form.apiKey.trim() } : {}),
    };
    put.mutate(body, {
      onSuccess: (p) => {
        toast.success(provider ? `Saved ${p.name}.` : `Added ${p.name}.`);
        onOpenChange(false);
      },
    });
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={provider ? `Replace ${provider.name}` : 'Add AI provider'}
      description="AI journey generation is optional and uses your own key. The key is stored encrypted and never shown again. Prompts contain redacted inputs and go to this provider."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="ai-provider-form" loading={put.isPending}>
            {provider ? 'Save provider' : 'Add provider'}
          </Button>
        </>
      }
    >
      <form id="ai-provider-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <div className="grid grid-cols-2 gap-3">
          <Field
            label="Name"
            error={show('name')}
            hint={
              provider
                ? 'Saving replaces the provider with this name.'
                : 'Jobs use "default" unless one is chosen.'
            }
          >
            {(p) => (
              <Input
                {...p}
                maxLength={100}
                disabled={!!provider}
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
                onChange={(e) => setForm({ ...form, kind: e.target.value as AIProviderKind })}
              >
                {aiProviderKinds.map((k) => (
                  <option key={k} value={k}>
                    {kindLabels[k]}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <Field
          label="Model"
          error={show('model')}
          hint={form.kind === 'anthropic' ? 'Optional; defaults to claude-sonnet-5-5.' : undefined}
        >
          {(p) => (
            <Input
              {...p}
              className="font-mono text-xs"
              placeholder={form.kind === 'anthropic' ? 'claude-sonnet-5-5' : ''}
              value={form.model}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
            />
          )}
        </Field>
        <Field
          label={form.kind === 'openai-compatible' ? 'Base URL' : 'Base URL (optional)'}
          error={show('baseURL')}
          hint={
            form.kind === 'ollama'
              ? 'A remote Ollama, e.g. http://gpu-box:11434. Empty uses the default endpoint.'
              : form.kind === 'openai-compatible'
                ? 'The OpenAI-compatible endpoint, e.g. http://localhost:8000/v1.'
                : "Empty uses the provider's public API."
          }
        >
          {(p) => (
            <Input
              {...p}
              className="font-mono text-xs"
              value={form.baseURL}
              onChange={(e) => setForm({ ...form, baseURL: e.target.value })}
            />
          )}
        </Field>
        <Field
          label={needsKey(form.kind) && !keyStored ? 'API key' : 'API key (optional)'}
          error={show('apiKey')}
          hint={
            keyStored
              ? 'A key is stored. Leave this empty to keep it.'
              : 'Stored encrypted and never shown again.'
          }
        >
          {(p) => (
            <Input
              {...p}
              type="password"
              autoComplete="off"
              value={form.apiKey}
              onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
            />
          )}
        </Field>
        <Field
          label="Monthly token cap"
          error={show('cap')}
          hint="New jobs are refused once the organisation's AI token use this calendar month (UTC) reaches the cap."
        >
          {(p) => (
            <Input
              {...p}
              inputMode="numeric"
              value={form.cap}
              onChange={(e) => setForm({ ...form, cap: e.target.value })}
            />
          )}
        </Field>
        <ErrorAlert error={put.error} />
      </form>
    </Modal>
  );
}

export function AIProvidersTab() {
  const list = useAIProviders();
  const del = useDeleteAIProvider();
  const toast = useToast();
  const [editing, setEditing] = useState<AIProvider | 'new' | null>(null);
  return (
    <Card>
      <CardHeader
        title="AI providers"
        description="Models used to draft scenarios in the AI studio and to summarise reports. Nothing in Stampede needs one, and no model is called while load runs."
        actions={
          <Button variant="primary" size="sm" onClick={() => setEditing('new')}>
            <Plus className="size-3.5" aria-hidden /> Add provider
          </Button>
        }
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No AI providers">
          Add Anthropic, OpenAI, Gemini, a local Ollama or any OpenAI-compatible server to generate
          journeys in the AI studio.
        </EmptyState>
      ) : (
        <Table>
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
              <th>
                <span className="sr-only">Actions</span>
              </th>
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
                <td>
                  <div className="flex items-center justify-end gap-2">
                    <Button
                      size="sm"
                      aria-label={`Replace ${p.name}`}
                      onClick={() => setEditing(p)}
                    >
                      <Pencil className="size-3.5" aria-hidden />
                    </Button>
                    <Confirm
                      trigger={
                        <Button size="sm" variant="danger" aria-label={`Delete ${p.name}`}>
                          <Trash2 className="size-3.5" aria-hidden />
                        </Button>
                      }
                      title={`Delete ${p.name}?`}
                      description="Its key is deleted. Past jobs stay readable; new jobs need another provider."
                      confirmLabel="Delete provider"
                      destructive
                      onConfirm={async () => {
                        await del.mutateAsync(p.id);
                        toast.success(`Deleted ${p.name}.`);
                      }}
                    />
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {editing && (
        <ProviderDialog
          key={editing === 'new' ? 'new' : editing.id}
          provider={editing === 'new' ? undefined : editing}
          open
          onOpenChange={(v) => !v && setEditing(null)}
        />
      )}
    </Card>
  );
}
