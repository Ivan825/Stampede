import { expect, test } from '@playwright/test';
import { nav, open } from './fixtures';

test('signs in', async ({ page }) => {
  await page.goto('/login?mock=signed-out');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await page.getByLabel('Email').fill('priya@acme.dev');
  await page.getByLabel('Password').fill('wrong password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await page.getByLabel('Password').fill('correct-horse-battery');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]{36}$/);
});

test('creates a project, target and scenario, runs it and reads the report', async ({ page }) => {
  await open(page, '/projects');
  await page.getByRole('button', { name: 'New project' }).click();
  const project = page.getByRole('dialog', { name: 'New project' });
  await project.getByLabel('Name').fill('E2E shop');
  await project.getByRole('button', { name: 'Create project' }).click();
  await expect(page.getByRole('heading', { name: 'E2E shop' })).toBeVisible();

  await nav(page, 'Targets');
  await page.getByRole('button', { name: 'New target' }).click();
  const target = page.getByRole('dialog', { name: 'New target' });
  await target.getByLabel('Name').fill('local');
  await target.getByLabel('Base URL').fill('http://localhost:8090');
  await target.getByRole('button', { name: 'Create target' }).click();
  await expect(page.getByText('http://localhost:8090', { exact: true })).toBeVisible();

  await nav(page, 'Scenarios');
  await page.getByRole('button', { name: 'New scenario' }).click();
  await expect(page.getByRole('heading', { name: 'New scenario' })).toBeVisible();
  // The template is valid; wait for the server's validation before saving.
  await page.getByRole('tab', { name: /Problems/ }).click();
  await expect(page.getByText('No problems found.')).toBeVisible();
  await page.getByRole('button', { name: 'Create scenario' }).click();
  await expect(page.getByRole('heading', { name: /my-scenario/ })).toBeVisible();

  await page.getByRole('button', { name: 'Run', exact: true }).click();
  const run = page.getByRole('dialog', { name: 'New run' });
  await expect(run.getByLabel('Target')).toHaveValue(/.+/);
  await run.getByRole('button', { name: /Load overrides/ }).click();
  await run.getByLabel('Duration').fill('6s');
  await run.getByRole('button', { name: 'Start run' }).click();

  // Live: charts, stats and the worker health grid.
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/);
  const health = page.getByRole('list', { name: 'Worker health' });
  await expect(health.getByRole('listitem').first()).toBeVisible();
  await expect(health.getByRole('meter').first()).toBeVisible();

  // The run finishes and the report replaces the live view.
  await expect(page.getByRole('heading', { name: 'Journeys and steps' })).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByText(/Every target held|At least one target was missed/)).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Workers', exact: true })).toBeVisible();
  await expect(page.getByRole('img', { name: /Throughput and users chart/ })).toBeVisible();
});

test('compares finished runs', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'Runs');
  await expect(page.getByText('Select finished runs to compare them.')).toBeVisible();
  const boxes = page.getByRole('checkbox', { name: /Select run/ });
  await boxes.first().waitFor();
  let picked = 0;
  for (let i = 0; picked < 2 && i < (await boxes.count()); i++) {
    const box = boxes.nth(i);
    if (await box.isEnabled()) {
      await box.check();
      picked++;
    }
  }
  await expect(page.getByRole('status').filter({ hasText: '2 runs selected' })).toBeVisible();
  await page.getByRole('button', { name: /Compare…/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Compare runs' });
  await dialog.getByRole('button', { name: 'Compare' }).click();
  await expect(page.getByRole('heading', { name: 'Compare runs' })).toBeVisible();
  await expect(page.getByLabel('Verdict')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Copy Markdown' })).toBeVisible();
});

test('generates journeys in the AI studio', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'AI studio');
  await expect(page.getByRole('heading', { name: 'AI studio' })).toBeVisible();
  await page.getByRole('button', { name: /Generate journeys/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Generate journeys' });
  await dialog.getByLabel('Description').fill('Shoppers browse the catalogue and sometimes buy.');
  const targetSelect = dialog.getByLabel('Target', { exact: true });
  const staging = await targetSelect
    .locator('option', { hasText: 'staging' })
    .getAttribute('value');
  expect(staging).toBeTruthy();
  await targetSelect.selectOption(staging ?? '');
  await dialog.getByRole('button', { name: 'Generate' }).click();
  await expect(page).toHaveURL(/\/ai\/[0-9a-f-]{36}$/);
  await expect(page.getByRole('status', { name: 'Progress' })).toBeVisible();
  // The mock pipeline finishes within a few seconds with a proposal.
  await expect(page.getByRole('region', { name: /^Journey / }).first()).toBeVisible({
    timeout: 30_000,
  });
});
