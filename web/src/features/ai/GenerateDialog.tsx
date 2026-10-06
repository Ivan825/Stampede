import { useNavigate } from '@tanstack/react-router';
import { clsx } from 'clsx';
import { FileUp, X } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useCreateAIJob, useScenarios, useTargets } from '@/api/queries';
import type { AIJobCreate, AIProvider } from '@/api/types';
import { Modal } from '@/components/dialog';
import { Button, ErrorAlert, Field, Notice, Select, Textarea } from '@/components/ui';
import { bytes } from '@/lib/format';

/** The server's input limits (internal/server/handlers_ai.go). */
const limits = {
  description: 20_000,
  openapi: 5 * 1024 * 1024,
  har: 20 * 1024 * 1024,
  accessLog: 20 * 1024 * 1024,
};

type SourceKey = 'openapi' | 'har' | 'accessLog';

interface Source {
  text: string;
  /** Set when the text came from a file. */
  fileName?: string;
}

const utf8 = new TextEncoder();
const byteLength = (s: string) => utf8.encode(s).length;

/** A document given as a file upload or pasted text. */
function SourceField({
  label,
  hint,
  accept,
  limit,
  value,
  error,
  onChange,
  onError,
}: {
  label: string;
  hint: string;
  accept: string;
  limit: number;
  value: Source;
  error?: string;
  onChange: (v: Source) => void;
  onError: (msg: string | undefined) => void;
}) {
  const [mode, setMode] = useState<'upload' | 'paste'>(
    value.text && !value.fileName ? 'paste' : 'upload',
  );
  const tab = (m: 'upload' | 'paste', text: string) => (
    <button
      type="button"
      aria-pressed={mode === m}
      onClick={() => setMode(m)}
      className={clsx(
        'rounded px-1.5 py-0.5 text-xs',
        mode === m ? 'bg-surface-2 font-medium text-fg' : 'text-muted hover:text-fg',
      )}
    >
      {text}
    </button>
  );
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <span className="flex-1 text-[13px] font-medium">{label}</span>
        <div className="flex gap-0.5" role="group" aria-label={`${label} input`}>
          {tab('upload', 'Upload')}
          {tab('paste', 'Paste')}
        </div>
      </div>
      {mode === 'upload' ? (
        value.fileName ? (
          <div className="flex h-8 items-center gap-2 rounded-md border border-line bg-surface-2 px-2.5 text-[13px]">
            <FileUp className="size-3.5 text-muted" aria-hidden />
            <span className="min-w-0 flex-1 truncate font-mono text-xs">{value.fileName}</span>
            <span className="num text-xs text-muted">{bytes(byteLength(value.text))}</span>
            <Button
              size="sm"
              variant="ghost"
              className="-mr-1.5 px-1"
              aria-label={`Remove ${value.fileName}`}
              onClick={() => {
                onChange({ text: '' });
                onError(undefined);
              }}
            >
              <X className="size-3.5" aria-hidden />
            </Button>
          </div>
        ) : (
          <input
            type="file"
            aria-label={`${label} file`}
            accept={accept}
            className="block w-full text-[13px] text-muted file:mr-3 file:h-8 file:cursor-pointer file:rounded-md file:border file:border-line file:bg-surface file:px-3 file:text-[13px] file:font-medium file:text-fg hover:file:bg-surface-2"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = '';
              if (!f) return;
              if (f.size > limit) {
                onError(
                  `${f.name} is ${bytes(f.size)}; the server accepts at most ${bytes(limit)}.`,
                );
                return;
              }
              onError(undefined);
              onChange({ text: await f.text(), fileName: f.name });
            }}
          />
        )
      ) : (
        <Textarea
          aria-label={`${label} text`}
          rows={4}
          className="font-mono text-xs"
          spellCheck={false}
          value={value.fileName ? '' : value.text}
          onChange={(e) => {
            onChange({ text: e.target.value });
            onError(
              byteLength(e.target.value) > limit
                ? `The pasted text is larger than ${bytes(limit)}.`
                : undefined,
            );
          }}
        />
      )}
      {error ? (
        <p className="text-xs text-fail">{error}</p>
      ) : (
        <p className="text-xs text-muted">{hint}</p>
      )}
    </div>
  );
}

export function GenerateDialog({
  projectId,
  providers,
  open,
  onOpenChange,
}: {
  projectId: string;
  providers: AIProvider[];
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const targets = useTargets(projectId);
  const scenarios = useScenarios(projectId);
  const create = useCreateAIJob(projectId);
  const navigate = useNavigate();

  const [description, setDescription] = useState('');
  const [sources, setSources] = useState<Record<SourceKey, Source>>({
    openapi: { text: '' },
    har: { text: '' },
    accessLog: { text: '' },
  });
  const [sourceErrors, setSourceErrors] = useState<Record<SourceKey, string | undefined>>({
    openapi: undefined,
    har: undefined,
    accessLog: undefined,
  });
  const [targetId, setTargetId] = useState('');
  const [scenarioId, setScenarioId] = useState('');
  const [providerId, setProviderId] = useState(
    () => providers.find((p) => p.name === 'default')?.id ?? '',
  );
  const [dryRun, setDryRun] = useState(true);
  const [maxRepairs, setMaxRepairs] = useState(3);
  const [touched, setTouched] = useState(false);

  const hasInput =
    description.trim() !== '' || Object.values(sources).some((s) => s.text.trim() !== '');
  const errors = {
    inputs: hasInput
      ? undefined
      : 'Give at least one input: a description, an OpenAPI spec, a HAR recording or an access log.',
    // The server counts UTF-8 bytes.
    description:
      byteLength(description.trim()) > limits.description
        ? `At most ${limits.description.toLocaleString('en-US')} characters (fewer for non-ASCII text).`
        : undefined,
    target: dryRun && !targetId ? 'Pick the target to dry-run against.' : undefined,
    provider: providers.length > 1 && !providerId ? 'Choose a provider.' : undefined,
    sources: Object.values(sourceErrors).find(Boolean),
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const setSource = (k: SourceKey) => (v: Source) => setSources((s) => ({ ...s, [k]: v }));
  const setSourceError = (k: SourceKey) => (m: string | undefined) =>
    setSourceErrors((s) => ({ ...s, [k]: m }));

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    const body: AIJobCreate = {
      dryRun,
      maxRepairs,
      ...(description.trim() ? { description: description.trim() } : {}),
      ...(sources.openapi.text.trim() ? { openapi: sources.openapi.text } : {}),
      ...(sources.har.text.trim() ? { har: sources.har.text } : {}),
      ...(sources.accessLog.text.trim() ? { accessLog: sources.accessLog.text } : {}),
      ...(targetId ? { targetId } : {}),
      ...(scenarioId ? { scenarioId } : {}),
      ...(providers.length > 1 && providerId ? { providerId } : {}),
    };
    create.mutate(body, {
      onSuccess: (job) => {
        onOpenChange(false);
        void navigate({
          to: '/projects/$projectId/ai/$jobId',
          params: { projectId, jobId: job.id },
        });
      },
    });
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Generate journeys"
      description="A model drafts a scenario from your inputs. Each journey is dry-run once with one user and repaired if it fails. Nothing is saved until you approve the proposal."
      width="max-w-2xl"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="ai-job-form" loading={create.isPending}>
            Generate
          </Button>
        </>
      }
    >
      <form id="ai-job-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field
          label="Description"
          error={show('description')}
          hint={`What your users do, in plain words. ${description.length.toLocaleString('en-US')} / ${limits.description.toLocaleString('en-US')} characters.`}
        >
          {(p) => (
            <Textarea
              {...p}
              autoFocus
              rows={4}
              placeholder="Most shoppers browse and search. Some log in to see their orders. A few buy."
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          )}
        </Field>
        <SourceField
          label="OpenAPI spec"
          hint={`OpenAPI 3.x, YAML or JSON. At most ${bytes(limits.openapi)}.`}
          accept=".yaml,.yml,.json,application/json,application/yaml"
          limit={limits.openapi}
          value={sources.openapi}
          error={sourceErrors.openapi}
          onChange={setSource('openapi')}
          onError={setSourceError('openapi')}
        />
        <div className="grid grid-cols-2 gap-3">
          <SourceField
            label="HAR recording"
            hint={`From browser devtools or a proxy. Redacted before it is sent. At most ${bytes(limits.har)}.`}
            accept=".har,.json,application/json"
            limit={limits.har}
            value={sources.har}
            error={sourceErrors.har}
            onChange={setSource('har')}
            onError={setSourceError('har')}
          />
          <SourceField
            label="Access log"
            hint={`Common or combined format; sets journey weights. At most ${bytes(limits.accessLog)}.`}
            accept=".log,.txt,text/plain"
            limit={limits.accessLog}
            value={sources.accessLog}
            error={sourceErrors.accessLog}
            onChange={setSource('accessLog')}
            onError={setSourceError('accessLog')}
          />
        </div>
        {show('inputs') && <p className="-mt-2 text-xs text-fail">{show('inputs')}</p>}

        <div className="grid grid-cols-2 gap-3">
          <Field
            label="Target"
            error={show('target')}
            hint={dryRun ? 'Each journey runs once against it.' : 'Optional without a dry run.'}
          >
            {(p) => (
              <Select {...p} value={targetId} onChange={(e) => setTargetId(e.target.value)}>
                <option value="">{dryRun ? 'Choose a target…' : 'None'}</option>
                {targets.data?.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name} — {t.baseURL}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Compare with" hint="Optional. Shows a diff and approves as a new version.">
            {(p) => (
              <Select {...p} value={scenarioId} onChange={(e) => setScenarioId(e.target.value)}>
                <option value="">No existing scenario</option>
                {scenarios.data?.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name} (v{s.latestVersion.version})
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3">
          {providers.length > 1 && (
            <Field label="Provider" error={show('provider')}>
              {(p) => (
                <Select {...p} value={providerId} onChange={(e) => setProviderId(e.target.value)}>
                  <option value="">Choose a provider…</option>
                  {providers.map((pr) => (
                    <option key={pr.id} value={pr.id}>
                      {pr.name} — {pr.kind} / {pr.model}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          )}
          <Field
            label="Repair rounds"
            hint="Failed journeys go back to the model with the evidence."
          >
            {(p) => (
              <Select
                {...p}
                value={maxRepairs}
                onChange={(e) => setMaxRepairs(Number(e.target.value))}
              >
                {[0, 1, 2, 3].map((n) => (
                  <option key={n} value={n}>
                    {n === 0 ? 'None' : n}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
          Dry-run each journey against the target
        </label>
        {dryRun ? (
          <Notice tone="warn" className="-mt-2">
            The dry run sends real requests: a checkout journey places a real order. Use a test
            environment. Project secrets are available to it.
          </Notice>
        ) : (
          <Notice tone="info" className="-mt-2">
            Without a dry run the proposal is only checked statically, so no journey is proven to
            work.
          </Notice>
        )}
        <ErrorAlert error={create.error} />
      </form>
    </Modal>
  );
}
