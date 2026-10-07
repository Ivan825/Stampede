import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderApp } from '@/test/render';
import { installMockApi, server } from '@/test/server';

describe('SSO settings', () => {
  it('shows the OIDC configuration without secrets', async () => {
    installMockApi();
    renderApp('/settings?tab=sso');
    expect(await screen.findByRole('tab', { name: 'SSO' })).toHaveAttribute('data-state', 'active');
    expect(await screen.findByText('https://acme.okta.com/oauth2/default')).toBeInTheDocument();
    expect(screen.getByText('enabled')).toBeInTheDocument();
    expect(screen.getByText('Okta')).toBeInTheDocument();
    expect(screen.getByText('acme.dev')).toBeInTheDocument();
    expect(screen.getByText(/Creates an account with the role/)).toHaveTextContent('viewer');
    expect(screen.getByText('openid email profile')).toBeInTheDocument();
  });

  it('explains when SSO is off', async () => {
    installMockApi();
    server.use(
      http.get('*/api/v1/settings/sso', () =>
        HttpResponse.json({ enabled: false, passwordLogin: true, allowedDomains: [], scopes: [] }),
      ),
    );
    renderApp('/settings?tab=sso');
    expect(await screen.findByText(/Single sign-on is not configured/)).toBeInTheDocument();
    expect(screen.getByText('off')).toBeInTheDocument();
  });
});

describe('Limits settings', () => {
  it('shows server caps, the abort floor and each target’s effective caps', async () => {
    installMockApi();
    const user = userEvent.setup();
    renderApp('/settings');
    await user.click(await screen.findByRole('tab', { name: 'Limits' }));
    const caps = within(await screen.findByRole('table', { name: 'Server caps' }));
    const every = caps.getByText('Every run').closest('tr')!;
    expect(within(every).getByText('5,000/s')).toBeInTheDocument();
    expect(within(every).getByText('4h')).toBeInTheDocument();
    const unverified = caps.getByText('Unverified public targets').closest('tr')!;
    expect(within(unverified).getByText('50/s')).toBeInTheDocument();
    expect(within(unverified).getByText('10m')).toBeInTheDocument();
    expect(screen.getByText(/error rate stays at or above 90%/)).toHaveTextContent(
      'for 30s is stopped',
    );

    const targets = within(screen.getByRole('table', { name: 'Target caps' }));
    const staging = targets.getByText('https://staging.shop.acme.dev').closest('tr')!;
    expect(within(staging).getByText('verified')).toBeInTheDocument();
    expect(within(staging).getByText('2,000/s · 5,000 · 4h')).toBeInTheDocument();
    expect(within(staging).getByText('2,000/s')).toBeInTheDocument();
    const prod = targets.getByText('https://shop.acme.dev').closest('tr')!;
    expect(within(prod).getByText('unverified')).toBeInTheDocument();
    expect(within(prod).getByText('5/s')).toBeInTheDocument();
    const local = targets.getAllByText('http://localhost:8090')[0]!.closest('tr')!;
    expect(within(local).getByText('private')).toBeInTheDocument();
    expect(within(local).getByText('none')).toBeInTheDocument();
  });
});

describe('Organisation and project caps', () => {
  it('shows the organisation caps read-only, with the command that changes them', async () => {
    installMockApi();
    renderApp('/settings?tab=limits');
    const org = within(await screen.findByRole('table', { name: 'Organisation caps' }));
    const row = org.getByText('Every run in the organisation').closest('tr')!;
    expect(within(row).getByText('3,000')).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'Organisation caps' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save caps' })).not.toBeInTheDocument();
    expect(screen.getByText('stampede caps set')).toBeInTheDocument();
  });

  it('lists each project’s caps and dry-run gate', async () => {
    installMockApi();
    renderApp('/settings?tab=limits');
    const projects = within(await screen.findByRole('table', { name: 'Project caps' }));
    const store = projects.getByRole('link', { name: 'Storefront' }).closest('tr')!;
    expect(within(store).getByText('required')).toBeInTheDocument();
    expect(within(store).getByText('2h')).toBeInTheDocument();
    const pay = projects.getByRole('link', { name: 'Payments API' }).closest('tr')!;
    expect(within(pay).getByText('not required')).toBeInTheDocument();
    expect(within(pay).getAllByText('no cap')).toHaveLength(3);
    // Storefront's 2h cap is tighter than the staging target's own 4h.
    const targets = within(screen.getByRole('table', { name: 'Target caps' }));
    const staging = targets.getByText('https://staging.shop.acme.dev').closest('tr')!;
    expect(within(staging).getByText('2h')).toBeInTheDocument();
    expect(within(staging).getByText('3,000')).toBeInTheDocument();
  });
});

describe('Server settings tabs', () => {
  it('are for admins only', async () => {
    const db = installMockApi();
    const runner = db.users.find((u) => u.role === 'runner')!;
    db.meId = runner.id;
    renderApp('/settings?tab=limits');
    expect(await screen.findByRole('tab', { name: 'Account' })).toHaveAttribute(
      'data-state',
      'active',
    );
    expect(screen.queryByRole('tab', { name: 'SSO' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Limits' })).not.toBeInTheDocument();
  });
});
