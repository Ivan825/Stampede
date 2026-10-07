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
  await expect(page.getByText(/Read-only analysis/)).toBeVisible();

  // Sign out again.
  await page.getByRole('button', { name: /Account menu/ }).click();
  await page.getByRole('menuitem', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
});

test('first-time setup points to stampede setup instead of a form', async ({ page }) => {
  await page.goto('/?mock=setup');
  await expect(page.getByRole('heading', { name: 'Set up Stampede' })).toBeVisible();
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByText('stampede setup', { exact: true })).toBeVisible();
  await expect(page.getByRole('textbox')).toHaveCount(0);
  await page.getByRole('button', { name: /I have run it, continue/ }).click();
  await expect(page.getByText(/still needs setting up/)).toBeVisible();
});

test('browses runs and reads a finished run’s report and downloads', async ({ page }) => {
  await open(page, '/');
  // The overview points to the CLI instead of a New run button.
  await expect(page.getByText(/^stampede start --project \S+ --scenario/)).toBeVisible();
  await expect(page.getByRole('button', { name: /New run/ })).toHaveCount(0);

  await nav(page, 'Runs');
  await expect(page.getByRole('heading', { name: 'Runs' })).toBeVisible();
  await expect(page.getByRole('button', { name: /New run/ })).toHaveCount(0);
  await page
    .getByRole('row')
    .filter({ hasText: 'catalog-breakpoint' })
    .filter({ hasText: 'completed' })
    .first()
    .getByRole('link')
    .first()
    .click();
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/);
  await expect(page.getByRole('heading', { name: 'Journeys and steps' })).toBeVisible();
  const downloads = page.getByRole('group', { name: 'Download report' });
  for (const f of ['HTML', 'JSON', 'CSV', 'JUnit', 'Markdown']) {
    await expect(downloads.getByRole('link', { name: f, exact: true })).toBeVisible();
  }
  await expect(downloads.getByRole('button', { name: /PDF/ })).toBeVisible();
  await expect(page.getByText(/^stampede start --project/)).toBeVisible();
  // No safety controls on a finished run.
  await expect(page.getByRole('group', { name: 'Safety controls' })).toHaveCount(0);
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

test('AI jobs are read-only, with the CLI commands to generate and approve', async ({ page }) => {
  await open(page, '/');
  await nav(page, 'AI jobs');
  await expect(page.getByRole('heading', { name: 'AI jobs' })).toBeVisible();
  await expect(
    page.getByText('stampede ai jobs create --describe "<what users do>" --target <url>'),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: /Generate journeys/ })).toHaveCount(0);
  await page.getByRole('row').filter({ hasText: 'needs review' }).getByRole('link').click();
  await expect(page.getByRole('heading', { name: /^Job [0-9a-f]{8}/ })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Journey checkout' })).toBeVisible();
  await expect(page.getByLabel('Proposed scenario YAML')).toContainText('name: shop-generated');
  await expect(page.getByText(/^stampede ai jobs approve [0-9a-f-]{36}$/)).toBeVisible();
  await expect(page.getByRole('button', { name: /Approve/ })).toHaveCount(0);
});
