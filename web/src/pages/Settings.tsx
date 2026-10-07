import * as Tabs from '@radix-ui/react-tabs';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { Plus, Trash2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import {
  useAudit,
  useChangePassword,
  useCreateToken,
  useCreateUser,
  useDeleteToken,
  useDeleteUser,
  useMe,
  useTokens,
  useUpdateUser,
  useUsers,
} from '@/api/queries';
import type { Role, TokenCreated } from '@/api/types';
import { RoleChip } from '@/components/chips';
import { AIProvidersTab } from '@/features/settings/AIProvidersTab';
import { IntegrationsTab } from '@/features/settings/IntegrationsTab';
import { NotificationsTab } from '@/features/settings/NotificationsTab';
import { LimitsTab, SSOTab } from '@/features/settings/ServerSettingsTabs';
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
  PageHeader,
  Select,
  Table,
} from '@/components/ui';
import type { SettingsTab } from '@/router';
import { dateTime, relativeTime } from '@/lib/format';
import { grantableRoles, permissions, roleDescriptions } from '@/lib/roles';

// ---------------------------------------------------------------- Account

function AccountTab() {
  const me = useMe();
  const change = useChangePassword();
  const toast = useToast();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [touched, setTouched] = useState(false);
  const errors = {
    next: next.length >= 10 ? undefined : 'Use at least 10 characters.',
    confirm: confirm === next ? undefined : 'Passwords do not match.',
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!current || errors.next || errors.confirm) return;
    change.mutate(
      { current, new: next },
      {
        onSuccess: () => {
          toast.success('Password changed.');
          setCurrent('');
          setNext('');
          setConfirm('');
          setTouched(false);
        },
      },
    );
  };
  return (
    <div className="grid gap-5 lg:grid-cols-2">
      <Card>
        <CardHeader title="Profile" />
        <dl className="grid grid-cols-[8rem_1fr] gap-y-2 px-4 py-3 text-[13px]">
          <dt className="text-muted">Name</dt>
          <dd>{me.name}</dd>
          <dt className="text-muted">Email</dt>
          <dd>{me.email}</dd>
          <dt className="text-muted">Organisation</dt>
          <dd>{me.orgName}</dd>
          <dt className="text-muted">Role</dt>
          <dd className="flex items-center gap-2">
            <RoleChip role={me.role} />
            <span className="text-xs text-muted">{roleDescriptions[me.role]}</span>
          </dd>
        </dl>
      </Card>
      <Card>
        <CardHeader title="Change password" />
        <form onSubmit={submit} className="flex flex-col gap-3 px-4 py-3" noValidate>
          <Field label="Current password">
            {(p) => (
              <Input
                {...p}
                type="password"
                autoComplete="current-password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
              />
            )}
          </Field>
          <Field
            label="New password"
            hint="At least 10 characters."
            error={touched ? errors.next : undefined}
          >
            {(p) => (
              <Input
                {...p}
                type="password"
                autoComplete="new-password"
                value={next}
                onChange={(e) => setNext(e.target.value)}
              />
            )}
          </Field>
          <Field label="Confirm new password" error={touched ? errors.confirm : undefined}>
            {(p) => (
              <Input
                {...p}
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
            )}
          </Field>
          <ErrorAlert error={change.error} />
          <div>
            <Button type="submit" variant="primary" loading={change.isPending} disabled={!current}>
              Change password
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------- Tokens

function NewTokenDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const me = useMe();
  const create = useCreateToken();
  const [name, setName] = useState('');
  const [role, setRole] = useState<Role>(me.role === 'owner' ? 'admin' : me.role);
  const [days, setDays] = useState('90');
  const [created, setCreated] = useState<TokenCreated | null>(null);

  const close = (v: boolean) => {
    if (!v) {
      setCreated(null);
      setName('');
      create.reset();
    }
    onOpenChange(v);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), role, ...(days ? { expiresInDays: Number(days) } : {}) },
      { onSuccess: setCreated },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={close}
      title={created ? 'Token created' : 'New API token'}
      description={
        created ? undefined : 'For the CLI and CI. Send it as Authorization: Bearer stp_…'
      }
      footer={
        created ? (
          <Button variant="primary" onClick={() => close(false)}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={() => close(false)}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              form="token-form"
              loading={create.isPending}
              disabled={!name.trim()}
            >
              Create token
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="flex flex-col gap-3">
          <Notice tone="warn">
            Copy this token now. It is shown only once and cannot be recovered.
          </Notice>
          <div className="flex items-center gap-2">
            <code
              className="min-w-0 flex-1 rounded-md border border-line bg-surface-2 px-2.5 py-2 font-mono text-xs break-all"
              aria-label="Token secret"
            >
              {created.secret}
            </code>
            <CopyButton value={created.secret} />
          </div>
          <p className="text-xs text-muted">
            {created.name} · role {created.role}
            {created.expiresAt ? ` · expires ${dateTime(created.expiresAt)}` : ' · never expires'}
          </p>
        </div>
      ) : (
        <form id="token-form" onSubmit={submit} className="flex flex-col gap-4">
          <Field label="Name" hint="Where it is used, e.g. github-actions.">
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
          <div className="grid grid-cols-2 gap-3">
            <Field label="Role" hint="At most your own.">
              {(p) => (
                <Select {...p} value={role} onChange={(e) => setRole(e.target.value as Role)}>
                  {grantableRoles(me.role).map((r) => (
                    <option key={r} value={r}>
                      {r}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Expires after (days)" hint="Empty for never.">
              {(p) => (
                <Input
                  {...p}
                  inputMode="numeric"
                  value={days}
                  onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))}
                />
              )}
            </Field>
          </div>
          <ErrorAlert error={create.error} />
        </form>
      )}
    </Modal>
  );
}

function TokensTab() {
  const tokens = useTokens();
  const del = useDeleteToken();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <CardHeader
        title="API tokens"
        description="Your personal tokens. Secrets are never shown after creation."
        actions={
          <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
            <Plus className="size-3.5" aria-hidden /> New token
          </Button>
        }
      />
      {tokens.isPending ? (
        <Loading />
      ) : tokens.error ? (
        <ErrorAlert error={tokens.error} className="m-4" />
      ) : tokens.data.length === 0 ? (
        <EmptyState title="No tokens" />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Token</th>
              <th>Role</th>
              <th>Created</th>
              <th>Last used</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {tokens.data.map((t) => (
              <tr key={t.id}>
                <td className="font-medium">{t.name}</td>
                <td className="font-mono text-xs text-muted">{t.prefix}…</td>
                <td>
                  <RoleChip role={t.role} />
                </td>
                <td className="text-xs text-muted">{dateTime(t.createdAt)}</td>
                <td className="text-xs text-muted">
                  {t.lastUsedAt ? relativeTime(t.lastUsedAt) : 'never'}
                </td>
                <td className="text-xs text-muted">
                  {t.expiresAt ? dateTime(t.expiresAt) : 'never'}
                </td>
                <td className="text-right">
                  <Confirm
                    trigger={
                      <Button size="sm" variant="danger">
                        Revoke
                      </Button>
                    }
                    title={`Revoke ${t.name}?`}
                    description="Anything using this token stops working immediately."
                    confirmLabel="Revoke token"
                    destructive
                    onConfirm={async () => {
                      await del.mutateAsync(t.id);
                      toast.success(`Revoked ${t.name}.`);
                    }}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <NewTokenDialog open={open} onOpenChange={setOpen} />
    </Card>
  );
}

// ---------------------------------------------------------------- Users

function NewUserDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const me = useMe();
  const create = useCreateUser();
  const toast = useToast();
  const [form, setForm] = useState({ name: '', email: '', role: 'editor' as Role, password: '' });
  const [touched, setTouched] = useState(false);
  const errors = {
    name: form.name.trim() ? undefined : 'Enter a name.',
    email: /^[^\s@]+@[^\s@]+$/.test(form.email.trim()) ? undefined : 'Enter a valid email.',
    password: form.password.length >= 10 ? undefined : 'At least 10 characters.',
  };
  const show = (k: keyof typeof errors) => (touched ? errors[k] : undefined);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (Object.values(errors).some(Boolean)) return;
    create.mutate(
      { ...form, name: form.name.trim(), email: form.email.trim() },
      {
        onSuccess: (u) => {
          toast.success(`Added ${u.name}. Share the initial password with them securely.`);
          onOpenChange(false);
          setForm({ name: '', email: '', role: 'editor', password: '' });
          setTouched(false);
        },
      },
    );
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add user"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" type="submit" form="user-form" loading={create.isPending}>
            Add user
          </Button>
        </>
      }
    >
      <form id="user-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Field label="Name" error={show('name')}>
          {(p) => (
            <Input
              {...p}
              autoFocus
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          )}
        </Field>
        <Field label="Email" error={show('email')}>
          {(p) => (
            <Input
              {...p}
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          )}
        </Field>
        <Field label="Role" hint={roleDescriptions[form.role]}>
          {(p) => (
            <Select
              {...p}
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value as Role })}
            >
              {grantableRoles(me.role).map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Initial password" error={show('password')} hint="At least 10 characters.">
          {(p) => (
            <Input
              {...p}
              type="password"
              autoComplete="new-password"
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
          )}
        </Field>
        <ErrorAlert error={create.error} />
      </form>
    </Modal>
  );
}

function UsersTab() {
  const me = useMe();
  const can = permissions(me.role);
  const users = useUsers();
  const update = useUpdateUser();
  const del = useDeleteUser();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  return (
    <Card>
      <CardHeader
        title="Users"
        description={`Members of ${me.orgName}.`}
        actions={
          can.manageUsers && (
            <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-3.5" aria-hidden /> Add user
            </Button>
          )
        }
      />
      {users.isPending ? (
        <Loading />
      ) : users.error ? (
        <ErrorAlert error={users.error} className="m-4" />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Email</th>
              <th>Role</th>
              <th>Last sign-in</th>
              <th>Added</th>
              {can.manageUsers && <th />}
            </tr>
          </thead>
          <tbody>
            {users.data.map((u) => {
              const self = u.id === me.id;
              const editable = can.manageUsers && !self && grantableRoles(me.role).includes(u.role);
              return (
                <tr key={u.id}>
                  <td className="font-medium">
                    {u.name}
                    {self && <span className="ml-1.5 text-xs text-muted">(you)</span>}
                  </td>
                  <td className="text-muted">{u.email}</td>
                  <td>
                    {editable ? (
                      <Select
                        aria-label={`Role for ${u.name}`}
                        className="!h-7 !w-28 text-[13px]"
                        value={u.role}
                        disabled={update.isPending}
                        onChange={(e) =>
                          update.mutate(
                            { id: u.id, role: e.target.value as Role },
                            {
                              onSuccess: (x) => toast.success(`${x.name} is now ${x.role}.`),
                              onError: (err) => toast.error(err),
                            },
                          )
                        }
                      >
                        {grantableRoles(me.role).map((r) => (
                          <option key={r} value={r}>
                            {r}
                          </option>
                        ))}
                      </Select>
                    ) : (
                      <RoleChip role={u.role} />
                    )}
                  </td>
                  <td className="text-xs text-muted">
                    {u.lastLoginAt ? relativeTime(u.lastLoginAt) : 'never'}
                  </td>
                  <td className="text-xs text-muted">{dateTime(u.createdAt)}</td>
                  {can.manageUsers && (
                    <td className="text-right">
                      {editable && (
                        <Confirm
                          trigger={
                            <Button size="sm" variant="danger" aria-label={`Remove ${u.name}`}>
                              <Trash2 className="size-3.5" aria-hidden />
                            </Button>
                          }
                          title={`Remove ${u.name}?`}
                          description="They lose access immediately and their API tokens are revoked."
                          confirmLabel="Remove user"
                          destructive
                          onConfirm={async () => {
                            await del.mutateAsync(u.id);
                            toast.success(`Removed ${u.name}.`);
                          }}
                        />
                      )}
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}
      {can.manageUsers && <NewUserDialog open={open} onOpenChange={setOpen} />}
    </Card>
  );
}

// ---------------------------------------------------------------- Audit

function AuditTab() {
  const audit = useAudit(true);
  return (
    <Card>
      <CardHeader title="Audit log" description="Who did what, newest first." />
      {audit.isPending ? (
        <Loading />
      ) : audit.error ? (
        <ErrorAlert error={audit.error} className="m-4" />
      ) : audit.data.length === 0 ? (
        <EmptyState title="Nothing recorded yet" />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>When</th>
              <th>Actor</th>
              <th>Action</th>
              <th>Subject</th>
              <th>Details</th>
              <th>IP</th>
            </tr>
          </thead>
          <tbody>
            {audit.data.map((a) => (
              <tr key={a.id}>
                <td className="num text-xs whitespace-nowrap text-muted" title={a.at}>
                  {dateTime(a.at)}
                </td>
                <td>{a.actor}</td>
                <td className="font-mono text-xs">{a.action}</td>
                <td className="max-w-48 truncate font-mono text-xs text-muted">{a.subject}</td>
                <td className="max-w-72 truncate font-mono text-xs text-muted">
                  {a.details && Object.keys(a.details).length ? JSON.stringify(a.details) : ''}
                </td>
                <td className="font-mono text-xs text-muted">{a.ip}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------- Page

const tabTrigger =
  'h-9 border-b-2 border-transparent px-1 text-[13px] text-muted hover:text-fg data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg';

export function SettingsPage() {
  const me = useMe();
  const can = permissions(me.role);
  const search = useSearch({ from: '/app/settings' });
  const navigate = useNavigate({ from: '/settings' });
  let tab: SettingsTab = search.tab ?? 'account';
  if (tab === 'audit' && !can.viewAudit) tab = 'account';
  if ((tab === 'integrations' || tab === 'notifications') && !can.manageIntegrations) {
    tab = 'account';
  }
  if (tab === 'ai' && !can.manageAIProviders) tab = 'account';
  if ((tab === 'sso' || tab === 'limits') && !can.manageUsers) tab = 'account';

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <PageHeader title="Settings" />
      <Tabs.Root
        value={tab}
        onValueChange={(v) => void navigate({ search: { tab: v as SettingsTab } })}
      >
        <Tabs.List className="mb-5 flex gap-5 border-b border-line" aria-label="Settings sections">
          <Tabs.Trigger value="account" className={tabTrigger}>
            Account
          </Tabs.Trigger>
          <Tabs.Trigger value="tokens" className={tabTrigger}>
            API tokens
          </Tabs.Trigger>
          <Tabs.Trigger value="users" className={tabTrigger}>
            Users
          </Tabs.Trigger>
          {can.manageIntegrations && (
            <Tabs.Trigger value="integrations" className={tabTrigger}>
              Integrations
            </Tabs.Trigger>
          )}
          {can.manageIntegrations && (
            <Tabs.Trigger value="notifications" className={tabTrigger}>
              Notifications
            </Tabs.Trigger>
          )}
          {can.manageAIProviders && (
            <Tabs.Trigger value="ai" className={tabTrigger}>
              AI providers
            </Tabs.Trigger>
          )}
          {can.manageUsers && (
            <Tabs.Trigger value="sso" className={tabTrigger}>
              SSO
            </Tabs.Trigger>
          )}
          {can.manageUsers && (
            <Tabs.Trigger value="limits" className={tabTrigger}>
              Limits
            </Tabs.Trigger>
          )}
          {can.viewAudit && (
            <Tabs.Trigger value="audit" className={tabTrigger}>
              Audit log
            </Tabs.Trigger>
          )}
        </Tabs.List>
        <Tabs.Content value="account">
          <AccountTab />
        </Tabs.Content>
        <Tabs.Content value="tokens">
          <TokensTab />
        </Tabs.Content>
        <Tabs.Content value="users">
          <UsersTab />
        </Tabs.Content>
        {can.manageIntegrations && (
          <Tabs.Content value="integrations">
            <IntegrationsTab />
          </Tabs.Content>
        )}
        {can.manageIntegrations && (
          <Tabs.Content value="notifications">
            <NotificationsTab />
          </Tabs.Content>
        )}
        {can.manageAIProviders && (
          <Tabs.Content value="ai">
            <AIProvidersTab />
          </Tabs.Content>
        )}
        {can.manageUsers && (
          <Tabs.Content value="sso">
            <SSOTab />
          </Tabs.Content>
        )}
        {can.manageUsers && (
          <Tabs.Content value="limits">
            <LimitsTab />
          </Tabs.Content>
        )}
        {can.viewAudit && (
          <Tabs.Content value="audit">
            <AuditTab />
          </Tabs.Content>
        )}
      </Tabs.Root>
    </div>
  );
}
