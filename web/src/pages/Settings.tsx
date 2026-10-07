import * as Tabs from '@radix-ui/react-tabs';
import { useNavigate, useSearch } from '@tanstack/react-router';
import { useAudit, useMe, useTokens, useUsers } from '@/api/queries';
import { RoleChip } from '@/components/chips';
import { CliHint } from '@/components/cliHint';
import {
  Card,
  CardHeader,
  EmptyState,
  ErrorAlert,
  Loading,
  PageHeader,
  Table,
} from '@/components/ui';
import { AIProvidersTab } from '@/features/settings/AIProvidersTab';
import { IntegrationsTab } from '@/features/settings/IntegrationsTab';
import { NotificationsTab } from '@/features/settings/NotificationsTab';
import { LimitsTab, SSOTab } from '@/features/settings/ServerSettingsTabs';
import { cli } from '@/lib/cli';
import { dateTime, relativeTime } from '@/lib/format';
import { permissions, roleDescriptions } from '@/lib/roles';
import type { SettingsTab } from '@/router';

// ---------------------------------------------------------------- Account

function AccountTab() {
  const me = useMe();
  return (
    <div className="grid gap-5 lg:grid-cols-2">
      <Card>
        <CardHeader title="Profile" description="Who you are signed in as." />
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
        <CardHeader
          title="The CLI"
          description="This web UI is for analysis and reporting. Runs, scenarios, schedules and settings are changed with the stampede CLI."
        />
        <div className="flex flex-col gap-2 px-4 py-3">
          <CliHint command={cli.login} className="self-start">
            Sign the CLI in to this server
          </CliHint>
          <CliHint command={cli.password} className="self-start">
            Change your password
          </CliHint>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------- Tokens

function TokensTab() {
  const tokens = useTokens();
  return (
    <Card>
      <CardHeader
        title="API tokens"
        description="Your personal tokens. Secrets are never shown after creation."
        actions={<CliHint command={cli.tokensCreate}>Create or revoke</CliHint>}
      />
      {tokens.isPending ? (
        <Loading />
      ) : tokens.error ? (
        <ErrorAlert error={tokens.error} className="m-4" />
      ) : tokens.data.length === 0 ? (
        <EmptyState title="No tokens" />
      ) : (
        <Table aria-label="API tokens">
          <thead>
            <tr>
              <th>Name</th>
              <th>Token</th>
              <th>Role</th>
              <th>Created</th>
              <th>Last used</th>
              <th>Expires</th>
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
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------- Users

function UsersTab() {
  const me = useMe();
  const can = permissions(me.role);
  const users = useUsers();
  return (
    <Card>
      <CardHeader
        title="Users"
        description={`Members of ${me.orgName}.`}
        actions={
          can.manageUsers && <CliHint command={cli.usersCreate}>Add or change a user</CliHint>
        }
      />
      {users.isPending ? (
        <Loading />
      ) : users.error ? (
        <ErrorAlert error={users.error} className="m-4" />
      ) : (
        <Table aria-label="Users">
          <thead>
            <tr>
              <th>Name</th>
              <th>Email</th>
              <th>Role</th>
              <th>Last sign-in</th>
              <th>Added</th>
            </tr>
          </thead>
          <tbody>
            {users.data.map((u) => (
              <tr key={u.id}>
                <td className="font-medium">
                  {u.name}
                  {u.id === me.id && <span className="ml-1.5 text-xs text-muted">(you)</span>}
                </td>
                <td className="text-muted">{u.email}</td>
                <td>
                  <RoleChip role={u.role} />
                </td>
                <td className="text-xs text-muted">
                  {u.lastLoginAt ? relativeTime(u.lastLoginAt) : 'never'}
                </td>
                <td className="text-xs text-muted">{dateTime(u.createdAt)}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
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
        <Table aria-label="Audit log">
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

/** Organisation and account settings, read-only; they change with the CLI. */
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
      <PageHeader
        title="Settings"
        description="Read-only. Each section shows the stampede command that changes it."
      />
      <Tabs.Root
        value={tab}
        onValueChange={(v) => void navigate({ search: { tab: v as SettingsTab } })}
      >
        <Tabs.List
          className="mb-5 flex flex-wrap gap-x-5 border-b border-line"
          aria-label="Settings sections"
        >
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
