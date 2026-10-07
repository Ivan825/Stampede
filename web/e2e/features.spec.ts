import { expect, test, type Page } from '@playwright/test';
import { nav, open } from './fixtures';

/** Opens a run of a scenario from the runs list. */
async function openRun(page: Page, scenario: string, status: string) {
  await nav(page, 'Runs');
  const row = page
    .getByRole('row')
    .filter({ hasText: scenario })
    .filter({ hasText: status })
    .first();
  await row.getByRole('link').first().click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/);
}

test('the report shows the load curve, breakpoint holds, workers and error examples', async ({
  page,
}) => {
  await open(page, '/');
  await openRun(page, 'catalog-breakpoint', 'completed');
  await expect(page.getByRole('heading', { name: 'Breakpoint', exact: true })).toBeVisible();
  await expect(page.getByRole('list', { name: 'Confirmation holds' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Throughput against load' })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Throughput against load chart' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Workers', exact: true })).toBeVisible();
  // gen-2 saturated for a while: the timeline charts shade it.
  await expect(
    page.getByRole('list', { name: 'Shaded windows' }).first().getByText('worker saturated'),
  ).toBeVisible();

  const show = page.getByRole('button', { name: /Show \d examples?/ }).first();
  await show.click();
  await expect(page.getByRole('region', { name: 'Example 1 request' })).toContainText(
    'Authorization: [redacted]',
  );
  await expect(page.getByRole('region', { name: 'Example 1 response' })).toBeVisible();
});

test('the live view shows each worker’s health', async ({ page }) => {
  await open(page, '/');
  await openRun(page, 'checkout-stress', 'running');
  const grid = page.getByRole('list', { name: 'Worker health' });
  await expect(grid.getByRole('listitem', { name: 'worker-eu-1' })).toContainText('healthy');
  await expect(grid.getByRole('listitem', { name: 'worker-eu-2' })).toContainText('saturated');
  await expect(grid.getByRole('meter', { name: 'worker-eu-1 CPU' })).toBeVisible();
});

test('the library lists packs and their files', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Library');
  await page.getByRole('list', { name: 'Packs' }).getByText('E-commerce, marketplaces').click();
  await expect(page.getByRole('heading', { name: 'E-commerce and marketplaces' })).toBeVisible();
  await page.getByRole('button', { name: 'journeys/shop-mix.yaml' }).click();
  await expect(page.getByLabel('journeys/shop-mix.yaml YAML')).toContainText(
    'name: ecommerce-shop-mix',
  );
  // Saving a pack file is done from the terminal.
  await expect(page.getByText('stampede pack install ecommerce')).toBeVisible();
  await expect(page.getByText(/^stampede push --project/)).toBeVisible();
  await expect(page.getByRole('button', { name: /Create scenario/ })).toHaveCount(0);
});

const spec = `openapi: 3.0.3
info: { title: Shop, version: "1" }
paths:
  /api/search:
    get: { summary: Search }
  /api/cart:
    post: { summary: Add to cart }
  /api/wishlist:
    get: { summary: Wish list }
`;

test('checks a scenario’s API coverage and drift', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Scenarios');
  // Scenario links only: the page before may still list runs of it.
  await page.locator('a[href*="/scenarios/"]', { hasText: 'checkout-stress' }).first().click();
  await page.getByRole('link', { name: /API coverage/ }).click();
  await expect(page.getByRole('heading', { name: 'API coverage and drift' })).toBeVisible();
  const coverage = page.getByRole('form', { name: 'Check coverage' });
  await coverage.getByLabel('OpenAPI document (YAML or JSON)').fill(spec);
  await coverage.getByRole('button', { name: 'Check coverage' }).click();
  const endpoints = page.getByRole('table', { name: 'Endpoints' });
  await expect(endpoints.getByRole('row').filter({ hasText: '/api/wishlist' })).toContainText(
    'no journey',
  );
  await expect(page.getByText('2 of 3')).toBeVisible();
  await expect(page.getByRole('table', { name: 'Requests that match no endpoint' })).toBeVisible();

  await page.getByRole('tab', { name: 'Drift' }).click();
  const drift = page.getByRole('form', { name: 'Check drift' });
  await drift.getByLabel('OpenAPI document (YAML or JSON)').first().fill(spec);
  await expect(drift.getByLabel('Dry run against')).toHaveCount(0);
  await drift.getByRole('button', { name: 'Check drift' }).click();
  await expect(page.getByText('The scenario drifted from the API.')).toBeVisible();
});

test('admins see SSO and limits settings', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Settings');
  await page.getByRole('tab', { name: 'SSO' }).click();
  await expect(page.getByText('https://acme.okta.com/oauth2/default')).toBeVisible();
  await page.getByRole('tab', { name: 'Limits' }).click();
  await expect(page.getByRole('table', { name: 'Server caps' })).toContainText('5,000/s');
  await expect(page.getByRole('table', { name: 'Target caps' })).toContainText('staging');
});

test('settings are read-only: account, tokens, users, integrations, AI providers and audit', async ({
  page,
}) => {
  await open(page, '/');
  await nav(page, 'Settings');
  await expect(page.getByText('Who you are signed in as.')).toBeVisible();
  await expect(page.getByText('stampede login', { exact: true })).toBeVisible();
  await expect(page.getByLabel(/password/i)).toHaveCount(0);

  await page.getByRole('tab', { name: 'API tokens' }).click();
  await expect(page.getByRole('table', { name: 'API tokens' })).toBeVisible();
  await expect(page.getByRole('button', { name: /New token|Revoke/ })).toHaveCount(0);

  await page.getByRole('tab', { name: 'Users' }).click();
  await expect(page.getByRole('table', { name: 'Users' })).toContainText('Ana Lima');
  await expect(page.getByRole('table', { name: 'Users' }).getByRole('combobox')).toHaveCount(0);

  await page.getByRole('tab', { name: 'Integrations' }).click();
  await expect(page.getByRole('table', { name: 'Integrations' })).toContainText('prod-prometheus');
  await page.getByRole('tab', { name: 'Notifications' }).click();
  await expect(page.getByRole('table', { name: 'Notification channels' })).toContainText(
    'perf-alerts',
  );
  await page.getByRole('tab', { name: 'AI providers' }).click();
  await expect(page.getByRole('table', { name: 'AI providers' })).toContainText('stored');
  await page.getByRole('tab', { name: 'Audit log' }).click();
  await expect(page.getByRole('table', { name: 'Audit log' })).toBeVisible();

  // Nothing on these pages writes.
  await expect(page.getByRole('main').getByRole('textbox')).toHaveCount(0);
});

test('a scenario is shown read-only: highlighted YAML, journey graph, plan and history', async ({
  page,
}) => {
  await open(page, '/');
  await nav(page, 'Scenarios');
  await expect(page.getByText(/^stampede push --project \S+ <scenario.yaml>$/)).toBeVisible();
  await expect(page.getByRole('button', { name: /New scenario/ })).toHaveCount(0);
  // Scenario links only: the page before may still list runs of it.
  await page.locator('a[href*="/scenarios/"]', { hasText: 'checkout-stress' }).first().click();
  const yaml = page.getByLabel('checkout-stress YAML');
  await expect(yaml).toContainText('name: home');
  await expect(page.locator('.monaco-editor')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Save|Create scenario|Delete/ })).toHaveCount(0);

  const graph = page.getByRole('group', { name: 'Journey graph' });
  await graph.getByLabel('GET home', { exact: true }).click();
  const panel = page.getByRole('complementary', { name: 'Selected step' });
  await expect(panel).toContainText('HTTP request');
  await expect(panel.getByRole('textbox')).toHaveCount(0);
  await expect(panel.getByRole('button', { name: /Apply|Remove|Move/ })).toHaveCount(0);

  await page.getByRole('tab', { name: 'Plan' }).click();
  await expect(page.getByText('Peak load')).toBeVisible();
  await page.getByRole('tab', { name: 'History' }).click();
  await expect(page.getByText('latest')).toBeVisible();
});
