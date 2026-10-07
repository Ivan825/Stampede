import { Plus, Send, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import {
  useCreateNotificationChannel,
  useDeleteNotificationChannel,
  useNotificationChannels,
  useNotificationDeliveries,
  useTestNotificationChannel,
} from '@/api/queries';
import {
  notificationEvents,
  type NotificationChannel,
  type NotificationChannelCreated,
  type NotificationDelivery,
  type NotificationEvent,
  type NotificationKind,
} from '@/api/types';
import { Chip } from '@/components/chips';
import { Confirm, Modal } from '@/components/dialog';
import { CopyButton } from '@/components/misc';
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
  Notice,
  Select,
  Table,
} from '@/components/ui';
import { dateTime, relativeTime } from '@/lib/format';

const nameRe = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$/;

const kindLabels: Record<NotificationKind, string> = {
  webhook: 'Webhook',
  slack: 'Slack',
  discord: 'Discord',
};

const urlHints: Record<NotificationKind, string> = {
  webhook: 'Any https endpoint. Bodies are signed with HMAC-SHA256 in X-Stampede-Signature.',
  slack: 'A Slack incoming webhook URL, https://hooks.slack.com/services/…',
  discord: 'A Discord webhook URL, https://discord.com/api/webhooks/…',
};

const eventLabels: Record<NotificationEvent, string> = {
  'run.finished': 'Run finished',
  'run.target_failed': 'A target failed',
  'run.killed': 'Run killed',
  'drift.detected': 'Drift detected',
};

/** A delivery attempt's outcome as a chip. */
export function DeliveryChip({ d }: { d: NotificationDelivery }) {
  return d.ok ? (
    <Chip tone="pass">{d.statusCode || 'ok'}</Chip>
  ) : (
    <Chip tone="fail" title={d.error}>
      {d.statusCode ? d.statusCode : 'failed'}
    </Chip>
  );
}

function NewChannelDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const create = useCreateNotificationChannel();
  const [form, setForm] = useState({
    name: '',
    kind: 'slack' as NotificationKind,
    url: '',
    events: [...notificationEvents] as NotificationEvent[],
    allowPrivate: false,
    secret: '',
  });
  const [touched, setTouched] = useState(false);
  const [created, setCreated] = useState<NotificationChannelCreated | null>(null);
  const errors = {
    name: nameRe.test(form.name.trim())
      ? undefined
      : "Start with a letter or digit; use letters, digits, '_', '.' or '-'.",
    url: /^https?:\/\/\S+$/.test(form.url.trim()) ? undefined : 'Enter an http(s) URL.',
    events: form.events.length ? undefined : 'Choose at least one event.',
    secret:
      form.secret && form.secret.length < 16
        ? 'At least 16 characters, or leave empty.'
        : undefined,
  };
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);
  const close = (v: boolean) => {
    if (!v) {
      setCreated(null);
      setTouched(false);
      setForm({
        name: '',
        kind: 'slack',
        url: '',
        events: [...notificationEvents],
        allowPrivate: false,
        secret: '',
      });
      create.reset();
    }
    onOpenChange(v);
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
        events: form.events,
        allowPrivate: form.allowPrivate,
        ...(form.kind === 'webhook' && form.secret ? { secret: form.secret } : {}),
      },
      {
        onSuccess: (c) => {
          if (c.secret) setCreated(c);
          else close(false);
        },
      },
    );
  };
  const toggle = (ev: NotificationEvent) =>
    setForm({
      ...form,
      events: form.events.includes(ev)
        ? form.events.filter((x) => x !== ev)
        : notificationEvents.filter((x) => x === ev || form.events.includes(x)),
    });
  return (
    <Modal
      open={open}
      onOpenChange={close}
      title={created ? 'Channel created' : 'Add notification channel'}
      description={created ? undefined : 'The URL is stored encrypted and never shown again.'}
      footer={
        created ? (
          <Button variant="primary" onClick={() => close(false)}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={() => close(false)}>Cancel</Button>
            <Button variant="primary" type="submit" form="channel-form" loading={create.isPending}>
              Add channel
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="flex flex-col gap-3">
          <Notice tone="warn">
            Copy the signing secret now. It is shown only once. Verify each delivery by comparing
            X-Stampede-Signature with sha256= and the hex HMAC-SHA256 of the body.
          </Notice>
          <div className="flex items-center gap-2">
            <code
              className="min-w-0 flex-1 rounded-md border border-line bg-surface-2 px-2.5 py-2 font-mono text-xs break-all"
              aria-label="Signing secret"
            >
              {created.secret}
            </code>
            <CopyButton value={created.secret ?? ''} />
          </div>
          <p className="text-xs text-muted">
            {created.channel.name} · {created.channel.urlHint}
          </p>
        </div>
      ) : (
        <form id="channel-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Name" error={show('name')} hint="e.g. perf-alerts">
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
                  onChange={(e) => setForm({ ...form, kind: e.target.value as NotificationKind })}
                >
                  {(Object.keys(kindLabels) as NotificationKind[]).map((k) => (
                    <option key={k} value={k}>
                      {kindLabels[k]}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          <Field label="URL" error={show('url')} hint={urlHints[form.kind]}>
            {(p) => (
              <Input
                {...p}
                className="font-mono text-xs"
                autoComplete="off"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
              />
            )}
          </Field>
          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-1 text-[13px] font-medium">Events</legend>
            {notificationEvents.map((ev) => (
              <label key={ev} className="flex items-center gap-2 text-[13px]">
                <input
                  type="checkbox"
                  className="accent-accent"
                  checked={form.events.includes(ev)}
                  onChange={() => toggle(ev)}
                />
                {eventLabels[ev]}
                <code className="font-mono text-xs text-muted">{ev}</code>
              </label>
            ))}
            {show('events') && <p className="text-xs text-fail">{errors.events}</p>}
          </fieldset>
          {form.kind === 'webhook' && (
            <Field
              label="Signing secret (optional)"
              error={show('secret')}
              hint="Generated when empty and shown once."
            >
              {(p) => (
                <Input
                  {...p}
                  type="password"
                  autoComplete="off"
                  value={form.secret}
                  onChange={(e) => setForm({ ...form, secret: e.target.value })}
                />
              )}
            </Field>
          )}
          <label className="flex items-start gap-2 text-[13px]">
            <input
              type="checkbox"
              className="mt-0.5 accent-accent"
              checked={form.allowPrivate}
              onChange={(e) => setForm({ ...form, allowPrivate: e.target.checked })}
            />
            <span>
              Allow private destinations
              <span className="block text-xs text-muted">
                Deliver to private, loopback or link-local addresses (an internal service). Off,
                such destinations are refused.
              </span>
            </span>
          </label>
          <ErrorAlert error={create.error} />
        </form>
      )}
    </Modal>
  );
}

function DeliveryLog({ channel, onClose }: { channel: NotificationChannel; onClose: () => void }) {
  const log = useNotificationDeliveries(channel.id);
  return (
    <Modal
      open
      onOpenChange={(v) => !v && onClose()}
      width="max-w-3xl"
      title={`Deliveries · ${channel.name}`}
      description="The most recent attempts, newest first. Failed deliveries are retried with backoff."
      footer={<Button onClick={onClose}>Close</Button>}
    >
      {log.isPending ? (
        <Loading />
      ) : log.error ? (
        <ErrorAlert error={log.error} />
      ) : log.data.length === 0 ? (
        <EmptyState title="Nothing delivered yet">Send a test to check the channel.</EmptyState>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>When</th>
              <th>Event</th>
              <th>Attempt</th>
              <th>Result</th>
              <th>Took</th>
              <th>Error</th>
            </tr>
          </thead>
          <tbody>
            {log.data.map((d) => (
              <tr key={d.id}>
                <td className="num text-xs whitespace-nowrap text-muted" title={d.at}>
                  {dateTime(d.at)}
                </td>
                <td className="font-mono text-xs">{d.event}</td>
                <td className="num">{d.attempt}</td>
                <td>
                  <DeliveryChip d={d} />
                </td>
                <td className="num text-xs text-muted">{d.durationMs} ms</td>
                <td className="max-w-64 truncate text-xs text-muted" title={d.error}>
                  {d.error}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Modal>
  );
}

export function NotificationsTab() {
  const list = useNotificationChannels();
  const del = useDeleteNotificationChannel();
  const test = useTestNotificationChannel();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [logFor, setLogFor] = useState<NotificationChannel | null>(null);
  const sendTest = (c: NotificationChannel) =>
    test.mutate(c.id, {
      onSuccess: (d) =>
        d.ok
          ? toast.success(`Test delivered to ${c.name} (HTTP ${d.statusCode}, ${d.durationMs} ms).`)
          : toast.error(
              new Error(`Test to ${c.name} failed: ${d.error || `HTTP ${d.statusCode}`}`),
            ),
      onError: (err) => toast.error(err),
    });
  return (
    <Card>
      <CardHeader
        title="Notifications"
        description="Tell a webhook, Slack or Discord when runs finish, miss a target or are killed."
        actions={
          <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
            <Plus className="size-3.5" aria-hidden /> Add channel
          </Button>
        }
      />
      {list.isPending ? (
        <Loading />
      ) : list.error ? (
        <ErrorAlert error={list.error} className="m-4" />
      ) : list.data.length === 0 ? (
        <EmptyState title="No notification channels" />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>Destination</th>
              <th>Events</th>
              <th>Last delivery</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.data.map((c) => (
              <tr key={c.id}>
                <td className="font-mono text-xs font-medium">{c.name}</td>
                <td>
                  <Chip tone="info">{kindLabels[c.kind]}</Chip>
                </td>
                <td className="font-mono text-xs text-muted">
                  {c.urlHint}
                  {c.allowPrivate && (
                    <Chip tone="warn" className="ml-2">
                      private allowed
                    </Chip>
                  )}
                  {c.hasSecret && <span className="ml-2 text-muted">signed</span>}
                </td>
                <td className="font-mono text-xs text-muted">{c.events.join(', ')}</td>
                <td>
                  {c.lastDelivery ? (
                    <button
                      type="button"
                      className="flex items-center gap-2 text-xs text-muted hover:text-fg"
                      onClick={() => setLogFor(c)}
                    >
                      <DeliveryChip d={c.lastDelivery} />
                      {relativeTime(c.lastDelivery.at)}
                    </button>
                  ) : (
                    <span className="text-xs text-muted">never</span>
                  )}
                </td>
                <td className="text-right whitespace-nowrap">
                  <div className="flex justify-end gap-1.5">
                    <Button
                      size="sm"
                      onClick={() => sendTest(c)}
                      loading={test.isPending && test.variables === c.id}
                    >
                      <Send className="size-3.5" aria-hidden /> Send test
                    </Button>
                    <Button size="sm" onClick={() => setLogFor(c)}>
                      Deliveries
                    </Button>
                    <Confirm
                      trigger={
                        <Button size="sm" variant="danger" aria-label={`Delete ${c.name}`}>
                          <Trash2 className="size-3.5" aria-hidden />
                        </Button>
                      }
                      title={`Delete ${c.name}?`}
                      description="Its delivery log is deleted too."
                      confirmLabel="Delete channel"
                      destructive
                      onConfirm={async () => {
                        await del.mutateAsync(c.id);
                        toast.success(`Deleted ${c.name}.`);
                      }}
                    />
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <NewChannelDialog open={open} onOpenChange={setOpen} />
      {logFor && <DeliveryLog channel={logFor} onClose={() => setLogFor(null)} />}
    </Card>
  );
}
