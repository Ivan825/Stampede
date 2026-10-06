import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { renderApp, renderWithRouter } from '@/test/render';
import { installMockApi } from '@/test/server';
import { IntegrationsTab } from './IntegrationsTab';
import { NotificationsTab } from './NotificationsTab';

describe('IntegrationsTab', () => {
  it('lists integrations without tokens and adds a traces link template', async () => {
    const db = installMockApi();
    const user = userEvent.setup();
    renderWithRouter(<IntegrationsTab />);
    expect(await screen.findByText('prod-prometheus')).toBeInTheDocument();
    expect(screen.getByText('http://prometheus.monitoring:9090')).toBeInTheDocument();
    expect(screen.getByText('stored')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Add integration/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Add integration' });
    await user.type(within(dialog).getByLabelText('Name'), 'tempo');
    await user.selectOptions(within(dialog).getByLabelText('Kind'), 'traces');
    // A template without {traceId} is refused before it is sent.
    await user.type(within(dialog).getByLabelText('URL'), 'https://tempo.example.com/trace/');
    await user.click(within(dialog).getByRole('button', { name: 'Add integration' }));
    expect(within(dialog).getByText('The template must contain {traceId}.')).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText('URL'), '{{traceId}');
    expect(within(dialog).queryByLabelText(/Bearer token/)).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Add integration' }));
    expect(await screen.findByText('tempo')).toBeInTheDocument();
    await waitFor(() => expect(db.integrations.map((i) => i.name)).toContain('tempo'));
    expect(db.integrations.find((i) => i.name === 'tempo')?.url).toBe(
      'https://tempo.example.com/trace/{traceId}',
    );
  });
});

describe('NotificationsTab', () => {
  it('creates a webhook channel and shows its signing secret once', async () => {
    const db = installMockApi();
    const user = userEvent.setup();
    renderWithRouter(<NotificationsTab />);
    expect(await screen.findByText('perf-alerts')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Add channel/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Add notification channel' });
    await user.type(within(dialog).getByLabelText('Name'), 'ops-hook');
    await user.selectOptions(within(dialog).getByLabelText('Kind'), 'webhook');
    await user.type(within(dialog).getByLabelText('URL'), 'https://hooks.example.com/stampede');
    await user.click(within(dialog).getByRole('checkbox', { name: /Run killed/ }));
    await user.click(within(dialog).getByRole('button', { name: 'Add channel' }));

    const done = await screen.findByRole('dialog', { name: 'Channel created' });
    expect(within(done).getByLabelText('Signing secret').textContent).toMatch(/^whsec_/);
    const created = db.channels.find((c) => c.name === 'ops-hook');
    expect(created?.events).toEqual(['run.finished', 'run.target_failed']);
    expect(created?.urlHint).toBe('https://hooks.example.com');
    await user.click(within(done).getByRole('button', { name: 'Done' }));
    expect(await screen.findByText('ops-hook')).toBeInTheDocument();
  });

  it('sends a test and shows it in the delivery log', async () => {
    installMockApi();
    const user = userEvent.setup();
    renderWithRouter(<NotificationsTab />);
    const row = (await screen.findByText('perf-alerts')).closest('tr')!;
    await user.click(within(row).getByRole('button', { name: /Send test/ }));
    expect(await screen.findByText(/Test delivered to perf-alerts/)).toBeInTheDocument();

    await user.click(within(row).getByRole('button', { name: 'Deliveries' }));
    const log = await screen.findByRole('dialog', { name: 'Deliveries · perf-alerts' });
    await waitFor(() => expect(within(log).getAllByRole('row')).toHaveLength(3));
    expect(within(log).getByText('test')).toBeInTheDocument();
    expect(within(log).getByText('run.finished')).toBeInTheDocument();
  });
});

describe('Settings tabs', () => {
  it('shows Integrations and Notifications to admins only', async () => {
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
  });
});
