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
  await page.getByRole('link', { name: 'checkout-stress' }).click();
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

test('the journey graph and the YAML editor edit the same scenario', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Scenarios');
  await page.getByRole('link', { name: 'checkout-stress' }).click();
  const graph = page.getByRole('group', { name: 'Journey graph' });
  const editor = page.locator('.monaco-editor .view-lines');
  await expect(editor).toContainText('name: home');

  // Graph → YAML: change a request in the side panel.
  await graph.getByLabel('GET home', { exact: true }).click();
  const panel = page.getByRole('complementary', { name: 'Selected step' });
  await panel.getByLabel('URL').fill('/landing');
  await panel.getByRole('button', { name: 'Apply' }).click();
  await expect(editor).toContainText('get: /landing');
  await expect(page.getByText('unsaved')).toBeVisible();

  // Drag a step below its next sibling to reorder it.
  const home = graph.getByLabel('GET home', { exact: true });
  const search = graph.getByLabel('GET search', { exact: true });
  const from = (await home.boundingBox())!;
  const to = (await search.boundingBox())!;
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(from.x + from.width / 2, to.y + to.height + 10, { steps: 12 });
  await page.mouse.up();
  await expect
    .poll(async () => {
      const h = await graph.getByLabel('GET home', { exact: true }).boundingBox();
      const s = await graph.getByLabel('GET search', { exact: true }).boundingBox();
      return h && s ? h.y > s.y : false;
    })
    .toBe(true);

  // YAML → graph: rename a step in the editor.
  await editor.getByText('search', { exact: true }).first().dblclick();
  await page.keyboard.type('find');
  await expect(graph.getByLabel('GET find', { exact: true })).toBeVisible();
});
