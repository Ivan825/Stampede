import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { mockShopSpec } from '@/mocks/library';
import { renderApp } from '@/test/render';
import { installMockApi } from '@/test/server';

describe('Library', () => {
  it('lists packs and opens one with its scenario files', async () => {
    installMockApi();
    const user = userEvent.setup();
    const { router } = renderApp('/library');
    const packs = within(await screen.findByRole('list', { name: 'Packs' }));
    expect(packs.getByText('AI and LLM apps')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Library' })).toHaveAttribute('data-status', 'active');

    await user.click(packs.getByText('E-commerce, marketplaces'));
    expect(
      await screen.findByRole('heading', { name: 'E-commerce and marketplaces' }),
    ).toBeVisible();
    expect(router.state.location.pathname).toBe('/library/ecommerce');
    expect(screen.getByText('TARGET_URL')).toBeInTheDocument();
    expect(screen.getByText('stampede pack install ecommerce')).toBeInTheDocument();
    const rows = within(screen.getByRole('table', { name: 'Scenario files' })).getAllByRole('row');
    expect(within(rows[1]!).getByText('ecommerce-shop-mix')).toBeInTheDocument();
    expect(within(rows[1]!).getByText('checkout')).toBeInTheDocument();
    expect(within(rows[2]!).getByText('stress')).toBeInTheDocument();
    expect(within(rows[2]!).getByText('spike')).toBeInTheDocument();

    const toggle = screen.getByRole('button', { name: 'journeys/shop-mix.yaml' });
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByLabelText('journeys/shop-mix.yaml YAML').textContent).toContain(
      'name: ecommerce-shop-mix',
    );
  });

  it('creates a scenario from a pack file in a chosen project', async () => {
    const db = installMockApi();
    const user = userEvent.setup();
    const { router } = renderApp('/library/ecommerce');
    await user.click(await screen.findByRole('button', { name: 'journeys/shop-mix.yaml' }));
    const project = db.projects[1]!;
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Project for ecommerce-shop-mix' }),
      project.id,
    );
    await user.click(screen.getByRole('button', { name: /Create scenario/ }));
    await waitFor(() =>
      expect(db.scenarios.some((s) => s.name === 'ecommerce-shop-mix')).toBe(true),
    );
    const created = db.scenarios.find((s) => s.name === 'ecommerce-shop-mix')!;
    expect(created.projectId).toBe(project.id);
    await waitFor(() =>
      expect(router.state.location.pathname).toBe(
        `/projects/${project.id}/scenarios/${created.id}`,
      ),
    );
  });

  it('shows not found for an unknown pack', async () => {
    installMockApi();
    renderApp('/library/nope');
    expect(await screen.findByText('pack not found.')).toBeInTheDocument();
  });
});

describe('Scenario coverage and drift', () => {
  const setup = () => {
    const db = installMockApi();
    const sc = db.scenarios.find((s) => s.name === 'checkout-stress')!;
    return { db, sc, url: `/projects/${sc.projectId}/scenarios/${sc.id}/coverage` };
  };

  it('checks a pasted OpenAPI document', async () => {
    const { url } = setup();
    const user = userEvent.setup();
    renderApp(url);
    await screen.findByRole('heading', { name: 'API coverage and drift' });
    const form = within(screen.getByRole('form', { name: 'Check coverage' }));
    const doc = form.getByLabelText('OpenAPI document (YAML or JSON)');
    await user.click(doc);
    await user.paste(mockShopSpec);
    await user.click(form.getByRole('button', { name: 'Check coverage' }));

    const endpoints = within(await screen.findByRole('table', { name: 'Endpoints' }));
    const pay = endpoints.getByText('/api/checkout/pay').closest('tr')!;
    expect(within(pay).getByText('shopper')).toBeInTheDocument();
    const orders = endpoints.getByText('/api/orders').closest('tr')!;
    expect(within(orders).getByText('order-status')).toBeInTheDocument();
    const login = endpoints.getByText('/api/login').closest('tr')!;
    expect(within(login).getByText('no journey')).toBeInTheDocument();
    expect(screen.getByText('7 of 11')).toBeInTheDocument();
  });

  it('fetches the document from a target and reports drift with a dry run', async () => {
    const { db, sc, url } = setup();
    const user = userEvent.setup();
    renderApp(`${url}?tab=drift`);
    const form = within(await screen.findByRole('form', { name: 'Check drift' }));
    const current = within(form.getByRole('radiogroup', { name: 'Current API source' }));
    await user.click(current.getByLabelText('Fetch from a target'));
    const local = db.targets.find((t) => t.projectId === sc.projectId && t.name === 'local')!;
    await user.type(form.getByLabelText('Document URL'), `${local.baseURL}/openapi.json`);
    await user.click(form.getByLabelText('OpenAPI document (YAML or JSON)'));
    await user.paste(
      mockShopSpec.replace('/api/orders:', '/api/wishlist:\n    get: {}\n  /api/orders:'),
    );
    await user.selectOptions(form.getByLabelText('Dry run against'), local.id);
    await user.click(form.getByRole('button', { name: 'Check drift' }));

    expect(
      await screen.findByText('No drift: every request uses an endpoint of the current API.'),
    ).toBeInTheDocument();
    const change = within(screen.getByRole('list', { name: 'API change' }));
    expect(change.getByText('− GET /api/wishlist')).toBeInTheDocument();
    const dry = within(screen.getByRole('table', { name: 'Dry run' }));
    expect(dry.getByText('shopper')).toBeInTheDocument();
    expect(dry.queryByText('fails')).not.toBeInTheDocument();
  });

  it('loads the document from a file', async () => {
    const { url } = setup();
    const user = userEvent.setup();
    renderApp(url);
    const form = within(await screen.findByRole('form', { name: 'Check coverage' }));
    await user.upload(
      form.getByLabelText('The API file'),
      new File([mockShopSpec], 'openapi.yaml', { type: 'application/yaml' }),
    );
    await waitFor(() =>
      expect(form.getByLabelText('OpenAPI document (YAML or JSON)')).toHaveValue(mockShopSpec),
    );
    const big = new File(['x'], 'huge.json');
    Object.defineProperty(big, 'size', { value: 6 << 20 });
    await user.upload(form.getByLabelText('The API file'), big);
    expect(form.getByRole('alert')).toHaveTextContent('huge.json is larger than 5 MiB');
  });

  it('refuses a URL that is not on a target', async () => {
    const { url } = setup();
    const user = userEvent.setup();
    renderApp(url);
    const form = within(await screen.findByRole('form', { name: 'Check coverage' }));
    await user.click(form.getByLabelText('Fetch from a target'));
    await user.type(form.getByLabelText('Document URL'), 'https://elsewhere.example/openapi.json');
    await user.click(form.getByRole('button', { name: 'Check coverage' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'elsewhere.example is not the host of a target in this project',
    );
  });
});
