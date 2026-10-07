import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { renderApp, renderWithRouter } from '@/test/render';
import { installMockApi } from '@/test/server';
import { AIProvidersTab } from './AIProvidersTab';
import { IntegrationsTab } from './IntegrationsTab';
import { NotificationsTab } from './NotificationsTab';

describe('IntegrationsTab', () => {
  it('lists integrations without tokens and points to the CLI to add one', async () => {
    installMockApi();
    renderWithRouter(<IntegrationsTab />);
    expect(await screen.findByText('prod-prometheus')).toBeInTheDocument();
    expect(screen.getByText('http://prometheus.monitoring:9090')).toBeInTheDocument();
    expect(screen.getByText('stored')).toBeInTheDocument();
    expect(screen.getByText('stampede integrations create <kind>')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add integration|Delete/ })).toBeNull();
  });
});

describe('NotificationsTab', () => {
  it('lists channels and their delivery log, without creating or testing', async () => {
    installMockApi();
    const user = userEvent.setup();
    renderWithRouter(<NotificationsTab />);
    const row = (await screen.findByText('perf-alerts')).closest('tr')!;
    expect(screen.getByText('stampede notify channels create <name>')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add channel|Send test|Delete/ })).toBeNull();

    await user.click(within(row).getByRole('button', { name: 'Deliveries of perf-alerts' }));
    const log = await screen.findByRole('dialog', { name: 'Deliveries · perf-alerts' });
    await waitFor(() => expect(within(log).getAllByRole('row').length).toBeGreaterThan(1));
    expect(within(log).getByText('run.finished')).toBeInTheDocument();
  });
});

describe('AIProvidersTab', () => {
  it('lists providers without their keys and points to the CLI to change them', async () => {
    installMockApi();
    renderWithRouter(<AIProvidersTab />);
    const row = (await screen.findByText('default')).closest('tr')!;
    expect(within(row).getByText('stored')).toBeInTheDocument();
    expect(within(row).getByText('claude-sonnet-5-5')).toBeInTheDocument();
    expect(screen.getByText('stampede ai providers set <provider>')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Add provider|Replace|Delete/ })).toBeNull();
    expect(screen.queryByLabelText(/API key/)).not.toBeInTheDocument();
  });
});

describe('Settings', () => {
  it('shows who you are and how to sign the CLI in, with no password form', async () => {
    installMockApi();
    renderApp('/settings');
    expect(await screen.findByText('Who you are signed in as.')).toBeInTheDocument();
    expect(screen.getByText('priya@acme.dev')).toBeInTheDocument();
    expect(screen.getByText('stampede login')).toBeInTheDocument();
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument();
  });

  it('lists tokens and users read-only', async () => {
    installMockApi();
    const user = userEvent.setup();
    renderApp('/settings?tab=tokens');
    expect(await screen.findByRole('table', { name: 'API tokens' })).toBeInTheDocument();
    expect(screen.getByText('stampede tokens create <name>')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /New token|Revoke/ })).toBeNull();

    await user.click(screen.getByRole('tab', { name: 'Users' }));
    const users = await screen.findByRole('table', { name: 'Users' });
    expect(within(users).queryByRole('combobox')).not.toBeInTheDocument();
    expect(within(users).queryByRole('button')).not.toBeInTheDocument();
    expect(screen.getByText('stampede users create <email>')).toBeInTheDocument();
  });

  it('shows Integrations, Notifications and AI providers to admins only', async () => {
    const db = installMockApi();
    const viewer = db.users.find((u) => u.role === 'viewer' || u.role === 'editor')!;
    db.meId = viewer.id;
    renderApp('/settings?tab=integrations');
    expect(await screen.findByRole('tab', { name: 'Account' })).toHaveAttribute(
      'data-state',
      'active',
    );
    expect(screen.queryByRole('tab', { name: 'Integrations' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Notifications' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'AI providers' })).not.toBeInTheDocument();
  });
});
