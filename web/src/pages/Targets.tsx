import { useParams } from '@tanstack/react-router';
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import {
  useCreateTarget,
  useDeleteTarget,
  useMe,
  useTargets,
  useUpdateTarget,
  useVerifyTarget,
} from '@/api/queries';
import type { Caps, Target, TargetCreate } from '@/api/types';
import { Confirm, Modal } from '@/components/dialog';
import { CopyButton } from '@/components/misc';
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
  Textarea,
} from '@/components/ui';
import { TargetBadge } from '@/features/targets/TargetBadge';
import { dateTime, humanDuration, num } from '@/lib/format';
import { permissions } from '@/lib/roles';

function capsText(c: Caps | undefined): string {
  if (!c) return 'none';
  const parts = [
    c.maxRate != null && `≤ ${num(c.maxRate)}/s`,
    c.maxVUs != null && `≤ ${c.maxVUs} VUs`,
    c.maxDurationSeconds != null && `≤ ${humanDuration(c.maxDurationSeconds)}`,
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : 'none';
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function TargetDialog({
  projectId,
  target,
  open,
  onOpenChange,
}: {
  projectId: string;
  target?: Target;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const create = useCreateTarget(projectId);
  const update = useUpdateTarget(projectId);
  const m = target ? update : create;
  const [name, setName] = useState(target?.name ?? '');
  const [baseURL, setBaseURL] = useState(target?.baseURL ?? '');
  const [allowHosts, setAllowHosts] = useState((target?.allowHosts ?? []).join('\n'));
  const [maxRate, setMaxRate] = useState(target?.caps.maxRate?.toString() ?? '');
  const [maxVUs, setMaxVUs] = useState(target?.caps.maxVUs?.toString() ?? '');
  const [maxDur, setMaxDur] = useState(target?.caps.maxDurationSeconds?.toString() ?? '');
  const [touched, setTouched] = useState(false);

  const isNum = (v: string) => v === '' || (Number.isFinite(Number(v)) && Number(v) > 0);
  const isInt = (v: string) => v === '' || (Number.isInteger(Number(v)) && Number(v) > 0);
  const errors = {
    name: name.trim() ? undefined : 'Enter a name.',
    baseURL: /^https?:\/\/[^\s/]+/.test(baseURL.trim())
      ? undefined
      : 'Enter an http:// or https:// URL.',
    maxRate: isNum(maxRate) ? undefined : 'A positive number.',
    maxVUs: isInt(maxVUs) ? undefined : 'A positive whole number.',
    maxDur: isInt(maxDur) ? undefined : 'Seconds, a positive whole number.',
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!valid) return;
    const caps: Caps = {};
    if (maxRate) caps.maxRate = Number(maxRate);
    if (maxVUs) caps.maxVUs = Number(maxVUs);
    if (maxDur) caps.maxDurationSeconds = Number(maxDur);
    const body: TargetCreate = {
      name: name.trim(),
      baseURL: baseURL.trim(),
      allowHosts: allowHosts
        .split(/[\s,]+/)
        .map((h) => h.trim())
        .filter(Boolean),
      caps,
    };
    const opts = { onSuccess: () => onOpenChange(false) };
    if (target) update.mutate({ id: target.id, ...body }, opts);
    else create.mutate(body, opts);
  };

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={target ? `Edit ${target.name}` : 'New target'}
      description="The system under test. Private and loopback addresses need no verification; public hosts run under low caps until you verify ownership."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="target-form" loading={m.isPending}>
            {target ? 'Save' : 'Create target'}
          </Button>
        </>
      }
    >
      <form id="target-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Name" error={show('name')}>
          {(p) => <Input {...p} autoFocus value={name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        <Field label="Base URL" error={show('baseURL')}>
          {(p) => (
            <Input
              {...p}
              className="font-mono"
              placeholder="https://staging.example.com"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
          )}
        </Field>
        <Field
          label="Other allowed hosts"
          hint="Requests may only reach the target host unless listed here. One per line."
        >
          {(p) => (
            <Textarea
              {...p}
              rows={2}
              className="font-mono"
              placeholder="auth.example.com"
              value={allowHosts}
              onChange={(e) => setAllowHosts(e.target.value)}
            />
          )}
        </Field>
        <fieldset>
          <legend className="mb-2 text-[13px] font-medium">Caps</legend>
          <div className="grid grid-cols-3 gap-3">
            <Field label="Max rate (/s)" error={show('maxRate')}>
              {(p) => (
                <Input
                  {...p}
                  inputMode="decimal"
                  value={maxRate}
                  onChange={(e) => setMaxRate(e.target.value)}
                />
              )}
            </Field>
            <Field label="Max VUs" error={show('maxVUs')}>
              {(p) => (
                <Input
                  {...p}
                  inputMode="numeric"
                  value={maxVUs}
                  onChange={(e) => setMaxVUs(e.target.value)}
                />
              )}
            </Field>
            <Field label="Max duration (s)" error={show('maxDur')}>
              {(p) => (
                <Input
                  {...p}
                  inputMode="numeric"
                  value={maxDur}
                  onChange={(e) => setMaxDur(e.target.value)}
                />
              )}
            </Field>
          </div>
        </fieldset>
        <ErrorAlert error={m.error} />
      </form>
    </Modal>
  );
}

function Verification({ target, canEdit }: { target: Target; canEdit: boolean }) {
  const verify = useVerifyTarget(target.projectId);
  const toast = useToast();
  const host = hostOf(target.baseURL);
  const record = `stampede-verify=${target.verificationToken}`;
  let origin = target.baseURL;
  try {
    origin = new URL(target.baseURL).origin;
  } catch {
    /* keep as typed */
  }
  return (
    <div className="flex flex-col gap-3 border-t border-line bg-surface-2/40 px-4 py-3 text-[13px]">
      <p>
        Prove you control <span className="font-mono">{host}</span> with either method, then check:
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        <div className="rounded-md border border-line bg-surface p-3">
          <p className="label-caps mb-1.5">DNS TXT record</p>
          <p className="mb-1 text-xs text-muted">
            Add a TXT record on <span className="font-mono">{host.split(':')[0]}</span>:
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
              {record}
            </code>
            <CopyButton value={record} />
          </div>
        </div>
        <div className="rounded-md border border-line bg-surface p-3">
          <p className="label-caps mb-1.5">Well-known file</p>
          <p className="mb-1 text-xs text-muted">
            Serve this token at{' '}
            <span className="font-mono">{origin}/.well-known/stampede-verify.txt</span>:
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
              {target.verificationToken}
            </code>
            <CopyButton value={target.verificationToken} />
          </div>
        </div>
      </div>
      {canEdit && (
        <div>
          <Button
            size="sm"
            variant="primary"
            loading={verify.isPending}
            onClick={() =>
              verify.mutate(target.id, {
                onSuccess: (t) =>
                  t.verified
                    ? toast.success(`${t.name} is verified.`)
                    : toast.error(
                        new Error(
                          'Token not found yet. DNS changes can take a few minutes to appear.',
                        ),
                      ),
                onError: (e) => toast.error(e),
              })
            }
          >
            <RefreshCw className="size-3.5" aria-hidden /> Check now
          </Button>
        </div>
      )}
    </div>
  );
}

export function TargetsPage() {
  const { projectId } = useParams({ from: '/app/projects/$projectId/targets' });
  const me = useMe();
  const can = permissions(me.role);
  const targets = useTargets(projectId);
  const del = useDeleteTarget(projectId);
  const toast = useToast();
  const [editing, setEditing] = useState<Target | 'new' | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader
        title="Targets"
        description="Where load goes. Each run is limited to its target's host and caps."
        actions={
          can.editTargets && (
            <Button variant="primary" onClick={() => setEditing('new')}>
              <Plus className="size-4" aria-hidden /> New target
            </Button>
          )
        }
      />
      {targets.isPending ? (
        <Loading />
      ) : targets.error ? (
        <ErrorAlert error={targets.error} />
      ) : targets.data.length === 0 ? (
        <Card>
          <EmptyState title="No targets">
            Add a target for each environment you test, such as local, staging or production.
          </EmptyState>
        </Card>
      ) : (
        <div className="flex flex-col gap-3">
          {targets.data.map((t) => {
            const needsVerify = !t.private && !t.verified;
            const open = expanded === t.id || (needsVerify && expanded === null);
            return (
              <Card key={t.id}>
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{t.name}</span>
                      <TargetBadge target={t} />
                    </div>
                    <div className="mt-0.5 truncate font-mono text-xs text-muted">{t.baseURL}</div>
                  </div>
                  <dl className="grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5 text-xs">
                    <dt className="text-muted">Caps</dt>
                    <dd className="num">{capsText(t.caps)}</dd>
                    {t.verified && t.verifiedAt && (
                      <>
                        <dt className="text-muted">Verified</dt>
                        <dd>
                          {dateTime(t.verifiedAt)}
                          {t.verificationMethod ? ` via ${t.verificationMethod}` : ''}
                        </dd>
                      </>
                    )}
                    {t.allowHosts && t.allowHosts.length > 0 && (
                      <>
                        <dt className="text-muted">Also allows</dt>
                        <dd className="font-mono">{t.allowHosts.join(', ')}</dd>
                      </>
                    )}
                  </dl>
                  <div className="flex items-center gap-2">
                    {!t.private && (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-expanded={open}
                        onClick={() => setExpanded(open ? '' : t.id)}
                      >
                        {open ? 'Hide verification' : 'Verification'}
                      </Button>
                    )}
                    {can.editTargets && (
                      <>
                        <Button
                          size="sm"
                          onClick={() => setEditing(t)}
                          aria-label={`Edit ${t.name}`}
                        >
                          <Pencil className="size-3.5" aria-hidden />
                        </Button>
                        <Confirm
                          trigger={
                            <Button size="sm" variant="danger" aria-label={`Delete ${t.name}`}>
                              <Trash2 className="size-3.5" aria-hidden />
                            </Button>
                          }
                          title={`Delete ${t.name}?`}
                          description="Runs already recorded keep their results. New runs can no longer use this target."
                          confirmLabel="Delete target"
                          destructive
                          onConfirm={async () => {
                            await del.mutateAsync(t.id);
                            toast.success(`Deleted ${t.name}.`);
                          }}
                        />
                      </>
                    )}
                  </div>
                </div>
                {!t.private && open && <Verification target={t} canEdit={can.editTargets} />}
              </Card>
            );
          })}
        </div>
      )}
      {editing && (
        <TargetDialog
          key={editing === 'new' ? 'new' : editing.id}
          projectId={projectId}
          target={editing === 'new' ? undefined : editing}
          open
          onOpenChange={(v) => !v && setEditing(null)}
        />
      )}
    </div>
  );
}
